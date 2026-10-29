package tapper

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/jlrickert/tapper/pkg/keg"
)

// ListVideosOptions configures behavior for Tap.ListVideos.
type ListVideosOptions struct {
	KegTargetOptions
	NodeID string
}

// UploadVideoOptions configures behavior for Tap.UploadVideo. Data, when
// set, is uploaded in place of the file at FilePath.
type UploadVideoOptions struct {
	KegTargetOptions
	NodeID   string
	FilePath string
	Data     []byte
	Name     string
}

// ReadVideoOptions configures behavior for Tap.ReadVideo.
type ReadVideoOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
}

// DownloadVideoOptions configures behavior for Tap.DownloadVideo.
type DownloadVideoOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
	Dest   string
	// NoOverwrite creates a private new file and refuses existing destinations.
	NoOverwrite bool
}

// DeleteVideoOptions configures behavior for Tap.DeleteVideo.
type DeleteVideoOptions struct {
	KegTargetOptions
	NodeID string
	Name   string
}

// ListVideos returns the names of a node's videos.
func (t *Tap) ListVideos(ctx context.Context, opts ListVideosOptions) ([]string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return nil, err
	}
	var names []string
	req := keg.AttachmentListRequest{IDs: []int{id.ID}, Limit: 100}
	for {
		page, err := k.ListAttachments(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("unable to list videos: %w", err)
		}
		for _, a := range page.Results {
			if a.Kind == keg.AttachmentVideo {
				names = append(names, a.Name)
			}
		}
		if page.Cursor == "" {
			return names, nil
		}
		req.Cursor = page.Cursor
	}
}

// UploadVideo stores a video on a node and returns its stored name. Bytes
// that are not a supported video container (MP4, WebM, Ogg) are refused.
func (t *Tap) UploadVideo(ctx context.Context, opts UploadVideoOptions) (string, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return "", err
	}
	data := opts.Data
	if data == nil {
		data, err = t.Runtime.ReadFile(opts.FilePath)
		if err != nil {
			return "", fmt.Errorf("unable to read local file %q: %w", opts.FilePath, err)
		}
	}
	name := opts.Name
	if name == "" && opts.FilePath != "" {
		name = filepath.Base(opts.FilePath)
	}
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if err := keg.ValidateVideo(data); err != nil {
		return "", fmt.Errorf("unable to upload video: %w", err)
	}
	if err := k.WriteAttachment(ctx, id, keg.AttachmentVideo, name, data); err != nil {
		return "", fmt.Errorf("unable to upload video: %w", err)
	}
	return name, nil
}

// ReadVideo returns a node video's bytes and the MIME type its container
// identifies as.
func (t *Tap) ReadVideo(ctx context.Context, opts ReadVideoOptions) ([]byte, string, error) {
	return t.ReadAttachment(ctx, ReadAttachmentOptions{
		KegTargetOptions: opts.KegTargetOptions,
		NodeID:           opts.NodeID,
		Kind:             keg.AttachmentVideo,
		Name:             opts.Name,
	})
}

// DownloadVideo writes a node video to a local path, or to stdout when Dest
// is "-", and returns the destination path.
func (t *Tap) DownloadVideo(ctx context.Context, opts DownloadVideoOptions) (string, error) {
	data, _, err := t.ReadVideo(ctx, ReadVideoOptions{
		KegTargetOptions: opts.KegTargetOptions,
		NodeID:           opts.NodeID,
		Name:             opts.Name,
	})
	if err != nil {
		return "", err
	}
	dest := opts.Dest
	if dest == "-" {
		if _, err := t.Runtime.Stream().Out.Write(data); err != nil {
			return "", fmt.Errorf("unable to write to stdout: %w", err)
		}
		return "", nil
	}
	if dest == "" {
		cwd, cwdErr := t.Runtime.Getwd()
		if cwdErr != nil {
			return "", fmt.Errorf("unable to determine working directory: %w", cwdErr)
		}
		dest = filepath.Join(cwd, opts.Name)
	}
	if err := t.writeDownload(dest, data, opts.NoOverwrite); err != nil {
		return "", fmt.Errorf("unable to write video to %q: %w", dest, err)
	}
	return dest, nil
}

// DeleteVideo removes a video from a node.
func (t *Tap) DeleteVideo(ctx context.Context, opts DeleteVideoOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	k, id, err := t.resolveNodeArg(ctx, k, opts.NodeID)
	if err != nil {
		return err
	}
	if err := k.DeleteAttachment(ctx, id, keg.AttachmentVideo, opts.Name); err != nil {
		return fmt.Errorf("unable to delete video %q: %w", opts.Name, err)
	}
	return nil
}
