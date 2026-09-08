package keg

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
)

// AttachmentKind keeps independent filename namespaces for original media.
type AttachmentKind string

const (
	AttachmentFile  AttachmentKind = "file"
	AttachmentImage AttachmentKind = "image"
	AttachmentVideo AttachmentKind = "video"
)

// Attachment identifies one original attachment, including its byte length.
type Attachment struct {
	ID   int            `json:"id"`
	Kind AttachmentKind `json:"kind"`
	Name string         `json:"name"`
	Size int64          `json:"size"`
}

// AttachmentListRequest selects 1–100 nodes and a bounded page of their attachments.
type AttachmentListRequest struct {
	IDs    []int  `json:"ids"`
	Cursor string `json:"cursor,omitempty"`
	Limit  int    `json:"limit,omitempty"`
}

// AttachmentPage is a deterministic page of kind-qualified attachments.
type AttachmentPage struct {
	Results []Attachment `json:"results"`
	Cursor  string       `json:"cursor,omitempty"`
}

// RepositoryVideos persists originals without transcoding or thumbnail generation.
type RepositoryVideos interface {
	// ListVideos returns original filenames sorted lexicographically.
	ListVideos(context.Context, NodeId) ([]string, error)
	// ReadVideo returns the original bytes for a filename.
	ReadVideo(context.Context, NodeId, string) ([]byte, error)
	// WriteVideo stores original bytes without renaming the attachment.
	WriteVideo(context.Context, NodeId, string, []byte) error
	// DeleteVideo removes the kind-qualified attachment.
	DeleteVideo(context.Context, NodeId, string) error
}

// RepositoryAttachmentMetadata lists kind-qualified metadata without loading payloads.
type RepositoryAttachmentMetadata interface {
	// ListAttachmentMetadata returns all attachment names, kinds, and sizes for a node.
	ListAttachmentMetadata(context.Context, NodeId) ([]Attachment, error)
}

type pageScopeKey struct{}

// WithPageScope binds opaque pagination cursors to the caller's authority scope.
func WithPageScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, pageScopeKey{}, scope)
}

type pageCursor struct {
	Version int    `json:"v"`
	Query   string `json:"q"`
	Offset  int    `json:"o"`
}

func pageWindow(ctx context.Context, cursor string, limit int, query any) (int, int, string, error) {
	if limit == 0 {
		limit = 25
	}
	if limit < 1 || limit > 100 {
		return 0, 0, "", ErrInvalid
	}
	scope, _ := ctx.Value(pageScopeKey{}).(string)
	raw, err := json.Marshal([]any{scope, query})
	if err != nil {
		return 0, 0, "", err
	}
	sum := sha256.Sum256(raw)
	key := fmt.Sprintf("%x", sum)
	offset := 0
	if cursor != "" {
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			return 0, 0, "", ErrInvalid
		}
		var c pageCursor
		if json.Unmarshal(data, &c) != nil || c.Version != 1 || c.Query != key || c.Offset < 0 {
			return 0, 0, "", ErrInvalid
		}
		offset = c.Offset
	}
	return offset, limit, key, nil
}
func nextPageCursor(key string, offset int) string {
	data, _ := json.Marshal(pageCursor{1, key, offset})
	return base64.RawURLEncoding.EncodeToString(data)
}

func validAttachmentKind(kind AttachmentKind) bool {
	return kind == AttachmentFile || kind == AttachmentImage || kind == AttachmentVideo
}
func (k *LocalKeg) attachmentNames(ctx context.Context, id NodeId, kind AttachmentKind) ([]string, error) {
	switch kind {
	case AttachmentFile:
		return k.listFiles(ctx, id)
	case AttachmentImage:
		return k.listImages(ctx, id)
	case AttachmentVideo:
		if repo, ok := k.Repo.(RepositoryVideos); ok {
			return repo.ListVideos(ctx, id)
		}
	}
	return nil, ErrNotSupported
}
func (k *LocalKeg) readAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string) ([]byte, error) {
	if !validAttachmentKind(kind) {
		return nil, ErrInvalid
	}
	if err := ValidateAssetName(name); err != nil {
		return nil, err
	}
	switch kind {
	case AttachmentFile:
		return k.readFile(ctx, id, name)
	case AttachmentImage:
		return k.readImage(ctx, id, name)
	case AttachmentVideo:
		if repo, ok := k.Repo.(RepositoryVideos); ok {
			return repo.ReadVideo(ctx, id, name)
		}
	}
	return nil, ErrNotSupported
}

// ReadAttachment returns original bytes from the selected kind's namespace.
func (k *LocalKeg) ReadAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string) ([]byte, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) ([]byte, error) { return k.readAttachment(ctx, id, kind, name) })
}

// VideoContentType identifies supported original video containers from their bytes.
// Unknown content is served as a download, never as an active browser document.
func VideoContentType(data []byte) string {
	switch typ := http.DetectContentType(data); typ {
	case "video/mp4", "video/webm", "video/ogg":
		return typ
	case "application/ogg":
		return "video/ogg"
	default:
		return "application/octet-stream"
	}
}

// ValidateVideo rejects content that cannot be served as a supported video container.
func ValidateVideo(data []byte) error {
	if VideoContentType(data) == "application/octet-stream" {
		return fmt.Errorf("unsupported video container: %w", ErrInvalid)
	}
	return nil
}

// WriteAttachment preserves the filename and storage layout of each media kind.
func (k *LocalKeg) WriteAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string, data []byte) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error {
		if !validAttachmentKind(kind) {
			return ErrInvalid
		}
		if err := ValidateAssetName(name); err != nil {
			return err
		}
		exists, err := k.nodeExistsWithContent(ctx, id)
		if err != nil {
			return err
		}
		if !exists {
			return ErrNotExist
		}
		switch kind {
		case AttachmentFile:
			return k.writeFile(ctx, id, name, data)
		case AttachmentImage:
			return k.writeImage(ctx, id, name, data)
		case AttachmentVideo:
			if repo, ok := k.Repo.(RepositoryVideos); ok {
				if err := ValidateVideo(data); err != nil {
					return err
				}
				return repo.WriteVideo(ctx, id, name, data)
			}
		}
		return ErrNotSupported
	})
}

// DeleteAttachment removes only the requested kind and filename.
func (k *LocalKeg) DeleteAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string) error {
	return k.withKegWrite(ctx, func(ctx context.Context) error {
		if !validAttachmentKind(kind) {
			return ErrInvalid
		}
		if err := ValidateAssetName(name); err != nil {
			return err
		}
		switch kind {
		case AttachmentFile:
			return k.deleteFile(ctx, id, name)
		case AttachmentImage:
			return k.deleteImage(ctx, id, name)
		case AttachmentVideo:
			if repo, ok := k.Repo.(RepositoryVideos); ok {
				return repo.DeleteVideo(ctx, id, name)
			}
		}
		return ErrNotSupported
	})
}

// ListAttachments lists originals with kind and byte length in a scoped page.
func (k *LocalKeg) ListAttachments(ctx context.Context, in AttachmentListRequest) (*AttachmentPage, error) {
	return withKegReadValue(ctx, k, func(ctx context.Context) (*AttachmentPage, error) {
		if err := validateMutationBatchSize(len(in.IDs)); err != nil {
			return nil, err
		}
		offset, limit, key, err := pageWindow(ctx, in.Cursor, in.Limit, in.IDs)
		if err != nil {
			return nil, err
		}
		ids := append([]int(nil), in.IDs...)
		sort.Ints(ids)
		all := []Attachment{}
		for i, id := range ids {
			if id < 0 || (i > 0 && ids[i-1] == id) {
				return nil, ErrInvalid
			}

			if repo, ok := k.Repo.(RepositoryAttachmentMetadata); ok {
				items, err := repo.ListAttachmentMetadata(ctx, NodeId{ID: id})
				if err != nil {
					return nil, err
				}
				sort.Slice(items, func(i, j int) bool {
					if items[i].Kind != items[j].Kind {
						return items[i].Kind < items[j].Kind
					}
					return items[i].Name < items[j].Name
				})
				all = append(all, items...)
				continue
			}
			for _, kind := range []AttachmentKind{AttachmentFile, AttachmentImage, AttachmentVideo} {
				if kind == AttachmentVideo {
					if _, ok := k.Repo.(RepositoryVideos); !ok {
						continue
					}
				}
				names, err := k.attachmentNames(ctx, NodeId{ID: id}, kind)
				if err != nil {
					return nil, err
				}
				sort.Strings(names)
				for _, name := range names {
					data, err := k.readAttachment(ctx, NodeId{ID: id}, kind, name)
					if err != nil {
						return nil, err
					}
					all = append(all, Attachment{ID: id, Kind: kind, Name: name, Size: int64(len(data))})
				}
			}
		}
		if offset > len(all) {
			offset = len(all)
		}
		end := min(len(all), offset+limit)
		out := &AttachmentPage{Results: all[offset:end]}
		if end < len(all) {
			out.Cursor = nextPageCursor(key, end)
		}
		return out, nil
	})
}
func attachmentPath(id NodeId, kind AttachmentKind, name string) string {
	return fmt.Sprintf("/nodes/%d/attachments/%s/%s", id.ID, url.PathEscape(string(kind)), url.PathEscape(name))
}
func (k *RemoteKeg) ListAttachments(ctx context.Context, in AttachmentListRequest) (*AttachmentPage, error) {
	var out AttachmentPage
	err := k.postJSON(ctx, "/nodes/attachments/list", "ListAttachments", in, &out, http.StatusOK)
	return &out, err
}
func (k *RemoteKeg) ReadAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string) ([]byte, error) {
	resp, err := k.do(ctx, http.MethodGet, attachmentPath(id, kind, name), nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, "ReadAttachment", http.StatusOK)
}
func (k *RemoteKeg) WriteAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string, data []byte) error {
	resp, err := k.do(ctx, http.MethodPut, attachmentPath(id, kind, name), bytes.NewReader(data), "application/octet-stream", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "WriteAttachment", http.StatusOK, http.StatusCreated, http.StatusNoContent)
	return err
}
func (k *RemoteKeg) DeleteAttachment(ctx context.Context, id NodeId, kind AttachmentKind, name string) error {
	resp, err := k.do(ctx, http.MethodDelete, attachmentPath(id, kind, name), nil, "", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "DeleteAttachment", http.StatusNoContent)
	return err
}
