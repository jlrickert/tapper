package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// registerFileTools publishes the attachment surface in one of two variants.
// Listing and deletion are identical either way; only the transfer tools
// differ, because they are the one place where "where do the bytes come from,
// and where do they go" depends on the transport.
//
// sharedFS says the server and its agent host see the same filesystem, which
// is true for stdio (`tap mcp`) and false for a hosted endpoint. The two
// variants use distinct input types so a path field is absent from the hosted
// schema rather than accepted and refused at call time: a hosted agent cannot
// name the server's disk because the vocabulary to do so is never published.
func registerFileTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults, sharedFS bool) {
	registerListFiles(srv, tap, defaults)
	registerListImages(srv, tap, defaults)
	registerDeleteFile(srv, tap, defaults)
	registerDeleteImage(srv, tap, defaults)
	if sharedFS {
		registerLocalUploadFile(srv, tap, defaults)
		registerLocalUploadImage(srv, tap, defaults)
		registerDownloadFile(srv, tap, defaults)
		registerLocalDownloadImage(srv, tap, defaults)
		return
	}
	registerUploadFile(srv, tap, defaults)
	registerUploadImage(srv, tap, defaults)
	registerDownloadImage(srv, tap, defaults)
}

// --- list_files ---

type listFilesInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID to list files for"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerListFiles(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_files",
		Description: "List file attachments for a node",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listFilesInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ListFilesOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		files, err := tap.ListFiles(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(files) == 0 {
			return textResult("no files"), nil, nil
		}
		return linesResult(files), nil, nil
	})
}

// --- list_images ---

type listImagesInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID to list images for"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerListImages(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "list_images",
		Description: "List image attachments for a node",
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listImagesInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ListImagesOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		}
		images, err := tap.ListImages(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(images) == 0 {
			return textResult("no images"), nil, nil
		}
		return linesResult(images), nil, nil
	})
}

// --- delete_file ---

type deleteFileInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID containing the file"`
	Name   string `json:"name" jsonschema:"filename to delete"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerDeleteFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "delete_file",
		Description: "Delete a file attachment from a node",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deleteFileInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.DeleteFileOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Name,
		}
		if err := tap.DeleteFile(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("deleted file %q from node %s", in.Name, in.NodeID)), nil, nil
	})
}

// --- delete_image ---

type deleteImageInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID containing the image"`
	Name   string `json:"name" jsonschema:"image filename to delete"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerDeleteImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "delete_image",
		Description: "Delete an image attachment from a node",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deleteImageInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.DeleteImageOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Name,
		}
		if err := tap.DeleteImage(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("deleted image %q from node %s", in.Name, in.NodeID)), nil, nil
	})
}

// --- upload_file ---

type uploadFileInput struct {
	NodeID     string               `json:"node_id" jsonschema:"node ID to attach the file to"`
	Filename   string               `json:"filename,omitempty" jsonschema:"filename for the attachment; derived from a data URI or resource URI if empty"`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"data: URI for the source file"`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"base64-encoded file bytes"`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"optional MIME type hint for raw file bytes"`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"embedded resource with uri, mime_type or mimeType, and blob or text"`
	Keg        string               `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

// localUploadFileInput is uploadFileInput plus the local-path source that only
// a shared-filesystem transport can honour. Keeping it a separate type is what
// keeps source_path out of the hosted schema.
type localUploadFileInput struct {
	NodeID     string               `json:"node_id" jsonschema:"node ID to attach the file to"`
	Filename   string               `json:"filename,omitempty" jsonschema:"filename for the attachment; derived from the source path, data URI, or resource URI if empty"`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"absolute path to the source file on the machine running the server"`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"file: or data: URI for the source file"`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"base64-encoded file bytes"`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"optional MIME type hint for raw file bytes"`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"embedded resource with uri, mime_type or mimeType, and blob or text"`
	Keg        string               `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerUploadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadFileTool(
		"Upload a file attachment to a node from raw bytes, a data URI, or an embedded resource",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadFileInput) (*sdkmcp.CallToolResult, any, error) {
		return handleFileUpload(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename, uploadSourceInput{
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, false)
	})
}

func registerLocalUploadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadFileTool(
		"Upload a file attachment to a node from a local path, raw bytes, a data URI, or an embedded resource",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in localUploadFileInput) (*sdkmcp.CallToolResult, any, error) {
		return handleFileUpload(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename, uploadSourceInput{
			SourcePath: in.SourcePath,
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, true)
	})
}

// linkHint tells the caller how to reference what it just uploaded. It lives on
// the tool rather than only in orientation because the moment of upload is when
// the reference is written, and a tool description is re-delivered with every
// tools/list — so it survives a context reset that discards orientation.
const (
	uploadFileLinkHint  = " Link it from the node body as [label](./assets/FILENAME) — the directory is plural."
	uploadImageLinkHint = " Link it from the node body as ![alt](./images/FILENAME) — the directory is plural."
)

func uploadFileTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:        "upload_file",
		Description: description + uploadFileLinkHint,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}
}

func handleFileUpload(ctx context.Context, tap *tapper.Tap, defaults KegDefaults, kegAlias, nodeID, filename string, src uploadSourceInput, sharedFS bool) (*sdkmcp.CallToolResult, any, error) {
	data, sourceName, err := resolveUploadSource(tap.Runtime, src, sharedFS)
	if err != nil {
		return errorResult(err), nil, nil
	}
	name, err := resolveUploadName(filename, sourceName)
	if err != nil {
		return errorResult(err), nil, nil
	}
	storedName, err := tap.UploadFile(ctx, tapper.UploadFileOptions{
		KegTargetOptions: resolveKegTarget(ctx, kegAlias, defaults),
		NodeID:           nodeID,
		Data:             data,
		Name:             name,
	})
	if err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(fmt.Sprintf("uploaded file %q to node %s", storedName, nodeID)), nil, nil
}

// resolveUploadName prefers the caller's explicit filename and falls back to
// one derived from the source. Byte sources such as data_base64 carry no name,
// so an explicit one is required there.
func resolveUploadName(explicit, derived string) (string, error) {
	name := strings.TrimSpace(explicit)
	if name == "" {
		name = derived
	}
	if name == "" {
		return "", fmt.Errorf("filename is required when the upload source has no filename")
	}
	return name, nil
}

// --- download_file ---

type downloadFileInput struct {
	NodeID   string `json:"node_id" jsonschema:"node ID containing the file"`
	Filename string `json:"filename" jsonschema:"filename to download"`
	DestPath string `json:"dest_path" jsonschema:"absolute path to write the downloaded file"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerDownloadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "download_file",
		Description: "Download a file attachment from a node to a local file path",
		Annotations: &sdkmcp.ToolAnnotations{
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in downloadFileInput) (*sdkmcp.CallToolResult, any, error) {
		if in.DestPath == "-" {
			return errorResult(fmt.Errorf("stdout mode is not supported over MCP")), nil, nil
		}
		opts := tapper.DownloadFileOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
			Dest:             in.DestPath,
		}
		dest, err := tap.DownloadFile(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("downloaded file %q to %s", in.Filename, dest)), nil, nil
	})
}

// --- upload_image ---

type uploadImageInput struct {
	NodeID     string               `json:"node_id" jsonschema:"node ID to attach the image to"`
	Filename   string               `json:"filename,omitempty" jsonschema:"image filename for the attachment; derived from a data URI or resource URI if empty"`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"data: URI for the source image"`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"base64-encoded image bytes"`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"optional MIME type hint for raw image bytes"`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"embedded resource with uri, mime_type or mimeType, and blob"`
	Keg        string               `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

// localUploadImageInput is uploadImageInput plus the local-path source. See
// localUploadFileInput for why this is a separate type.
type localUploadImageInput struct {
	NodeID     string               `json:"node_id" jsonschema:"node ID to attach the image to"`
	Filename   string               `json:"filename,omitempty" jsonschema:"image filename for the attachment; derived from the source path, data URI, or resource URI if empty"`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"absolute path to the source image on the machine running the server"`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"file: or data: URI for the source image"`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"base64-encoded image bytes"`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"optional MIME type hint for raw image bytes"`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"embedded resource with uri, mime_type or mimeType, and blob"`
	Keg        string               `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerUploadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadImageTool(
		"Upload an image attachment to a node from raw bytes, a data URI, or an embedded resource",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadImageInput) (*sdkmcp.CallToolResult, any, error) {
		return handleImageUpload(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename, uploadSourceInput{
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, false)
	})
}

func registerLocalUploadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadImageTool(
		"Upload an image attachment to a node from a local path, raw bytes, a data URI, or an embedded resource",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in localUploadImageInput) (*sdkmcp.CallToolResult, any, error) {
		return handleImageUpload(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename, uploadSourceInput{
			SourcePath: in.SourcePath,
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, true)
	})
}

func uploadImageTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:        "upload_image",
		Description: description + uploadImageLinkHint,
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}
}

func handleImageUpload(ctx context.Context, tap *tapper.Tap, defaults KegDefaults, kegAlias, nodeID, filename string, src uploadSourceInput, sharedFS bool) (*sdkmcp.CallToolResult, any, error) {
	data, sourceName, err := resolveUploadSource(tap.Runtime, src, sharedFS)
	if err != nil {
		return errorResult(err), nil, nil
	}
	name, err := resolveUploadName(filename, sourceName)
	if err != nil {
		return errorResult(err), nil, nil
	}
	storedName, err := tap.UploadImage(ctx, tapper.UploadImageOptions{
		KegTargetOptions: resolveKegTarget(ctx, kegAlias, defaults),
		NodeID:           nodeID,
		Data:             data,
		Name:             name,
	})
	if err != nil {
		return errorResult(err), nil, nil
	}
	return textResult(fmt.Sprintf("uploaded image %q to node %s", storedName, nodeID)), nil, nil
}

// --- download_image ---

type downloadImageInput struct {
	NodeID   string `json:"node_id" jsonschema:"node ID containing the image"`
	Filename string `json:"filename" jsonschema:"image filename to download"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

// localDownloadImageInput adds an optional destination path. Omitting it keeps
// the inline-content behaviour, so a shared-filesystem agent can either look at
// the image or save it without needing two different tools.
type localDownloadImageInput struct {
	NodeID   string `json:"node_id" jsonschema:"node ID containing the image"`
	Filename string `json:"filename" jsonschema:"image filename to download"`
	DestPath string `json:"dest_path,omitempty" jsonschema:"absolute path to write the image to; omit to receive the image as MCP content"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerDownloadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, downloadImageTool(
		"Return an image attachment from a node as MCP image content",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in downloadImageInput) (*sdkmcp.CallToolResult, any, error) {
		return readImageContent(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename)
	})
}

func registerLocalDownloadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, downloadImageTool(
		"Download an image attachment from a node, either to a local file path or as MCP image content",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in localDownloadImageInput) (*sdkmcp.CallToolResult, any, error) {
		dest := strings.TrimSpace(in.DestPath)
		if dest == "" {
			return readImageContent(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename)
		}
		if dest == "-" {
			return errorResult(fmt.Errorf("stdout mode is not supported over MCP; omit dest_path to receive image content")), nil, nil
		}
		written, err := tap.DownloadImage(ctx, tapper.DownloadImageOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
			Dest:             dest,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("downloaded image %q to %s", in.Filename, written)), nil, nil
	})
}

func downloadImageTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:        "download_image",
		Description: description,
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}
}

func readImageContent(ctx context.Context, tap *tapper.Tap, defaults KegDefaults, kegAlias, nodeID, filename string) (*sdkmcp.CallToolResult, any, error) {
	data, format, err := tap.ReadImage(ctx, tapper.ReadImageOptions{
		KegTargetOptions: resolveKegTarget(ctx, kegAlias, defaults),
		NodeID:           nodeID,
		Name:             filename,
	})
	if err != nil {
		return errorResult(err), nil, nil
	}
	mimeType := imageMIMEType(format)
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.ImageContent{Data: data, MIMEType: mimeType}},
		StructuredContent: map[string]any{
			"node_id": nodeID, "filename": filename,
			"mime_type": mimeType, "size": len(data),
		},
	}, nil, nil
}

func imageMIMEType(format string) string {
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		return "image/jpeg"
	case "png", "gif", "webp", "avif", "heic":
		return "image/" + strings.ToLower(format)
	default:
		return "application/octet-stream"
	}
}

type uploadResourceInput struct {
	URI          string `json:"uri,omitempty" jsonschema:"resource URI"`
	MIMEType     string `json:"mime_type,omitempty" jsonschema:"resource MIME type"`
	MIMETypeSpec string `json:"mimeType,omitempty" jsonschema:"resource MIME type using MCP field casing"`
	Blob         string `json:"blob,omitempty" jsonschema:"base64-encoded binary resource bytes"`
	Text         string `json:"text,omitempty" jsonschema:"text resource contents for file uploads"`
}

type uploadSourceInput struct {
	SourcePath string
	SourceURI  string
	DataBase64 string
	Resource   *uploadResourceInput
}

func resolveUploadSource(rt *toolkit.Runtime, in uploadSourceInput, allowLocalSources bool) ([]byte, string, error) {
	provided := 0
	if strings.TrimSpace(in.SourcePath) != "" {
		provided++
	}
	if strings.TrimSpace(in.SourceURI) != "" {
		provided++
	}
	if strings.TrimSpace(in.DataBase64) != "" {
		provided++
	}
	if in.Resource != nil {
		provided++
	}
	if provided != 1 {
		return nil, "", fmt.Errorf("provide exactly one upload source")
	}

	switch {
	case strings.TrimSpace(in.SourcePath) != "":
		return readLocalUploadSource(rt, strings.TrimSpace(in.SourcePath), allowLocalSources)
	case strings.TrimSpace(in.SourceURI) != "":
		return readUploadURI(rt, strings.TrimSpace(in.SourceURI), allowLocalSources)
	case strings.TrimSpace(in.DataBase64) != "":
		data, err := decodeUploadBase64(strings.TrimSpace(in.DataBase64))
		return data, "", err
	default:
		return readUploadResource(rt, in.Resource, allowLocalSources)
	}
}

func readUploadResource(rt *toolkit.Runtime, resource *uploadResourceInput, allowLocalSources bool) ([]byte, string, error) {
	if resource == nil {
		return nil, "", fmt.Errorf("embedded resource is empty")
	}
	name := uploadNameFromURI(resource.URI)
	if strings.TrimSpace(resource.Blob) != "" {
		data, err := decodeUploadBase64(strings.TrimSpace(resource.Blob))
		return data, name, err
	}
	if resource.Text != "" {
		return []byte(resource.Text), name, nil
	}
	if strings.TrimSpace(resource.URI) == "" {
		return nil, "", fmt.Errorf("embedded resource must include blob, text, or uri")
	}
	return readUploadURI(rt, strings.TrimSpace(resource.URI), allowLocalSources)
}

func readUploadURI(rt *toolkit.Runtime, raw string, allowLocalSources bool) ([]byte, string, error) {
	if strings.HasPrefix(raw, "data:") {
		data, err := decodeDataURI(raw)
		return data, "", err
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, "", fmt.Errorf("invalid source_uri: %w", err)
	}
	if u.Scheme == "file" {
		if u.Host != "" && u.Host != "localhost" {
			return nil, "", fmt.Errorf("file URI host %q is not supported", u.Host)
		}
		return readLocalUploadSource(rt, u.Path, allowLocalSources)
	}
	return nil, "", fmt.Errorf("unsupported source_uri scheme %q", u.Scheme)
}

func readLocalUploadSource(rt *toolkit.Runtime, path string, allowLocalSources bool) ([]byte, string, error) {
	if !allowLocalSources {
		return nil, "", fmt.Errorf("local file sources are not available on this MCP surface; use data_base64 or an embedded resource blob")
	}
	data, err := rt.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("unable to read local file %q: %w", path, err)
	}
	return data, filepath.Base(path), nil
}

func decodeDataURI(raw string) ([]byte, error) {
	prefix, payload, ok := strings.Cut(raw, ",")
	if !ok {
		return nil, fmt.Errorf("invalid data URI")
	}
	if strings.Contains(strings.ToLower(prefix), ";base64") {
		return decodeUploadBase64(payload)
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid data URI payload: %w", err)
	}
	return []byte(decoded), nil
}

func decodeUploadBase64(raw string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(raw)
	if err == nil {
		return data, nil
	}
	if data, rawErr := base64.RawStdEncoding.DecodeString(raw); rawErr == nil {
		return data, nil
	}
	return nil, fmt.Errorf("invalid base64 upload data: %w", err)
}

func uploadNameFromURI(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if u.Scheme == "data" {
		return ""
	}
	name := filepath.Base(u.Path)
	if name == "." || name == "/" {
		return ""
	}
	return name
}
