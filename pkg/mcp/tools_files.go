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

type fileToolOptions struct {
	AllowLocalSources bool
	IncludeDownloads  bool
}

func registerFileTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults, opts fileToolOptions) {
	registerListFiles(srv, tap, defaults)
	registerListImages(srv, tap, defaults)
	registerDeleteFile(srv, tap, defaults)
	registerDeleteImage(srv, tap, defaults)
	registerUploadFile(srv, tap, defaults, opts.AllowLocalSources)
	registerUploadImage(srv, tap, defaults, opts.AllowLocalSources)
	if opts.IncludeDownloads {
		registerDownloadFile(srv, tap, defaults)
		registerDownloadImage(srv, tap, defaults)
	}
}

// --- list_files ---

type listFilesInput struct {
	NodeID string `json:"node_id" jsonschema:"node ID to list files for"`
	Keg    string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Flight string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
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
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
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
	Flight string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
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
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
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
	Flight string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
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
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
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
	Flight string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
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
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
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
	Filename   string               `json:"filename,omitempty" jsonschema:"filename for the attachment; derived from source path or resource URI if empty"`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"absolute path to the source file (stdio/local only)"`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"file:// URI (stdio/local only) or data: URI for the source file"`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"base64-encoded file bytes"`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"optional MIME type hint for raw file bytes"`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"embedded resource with uri, mime_type or mimeType, and blob or text"`
	Keg        string               `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Flight     string               `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
}

func registerUploadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults, allowLocalSources bool) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "upload_file",
		Description: "Upload a file attachment to a node from a local path, raw bytes, data URI, or embedded resource",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadFileInput) (*sdkmcp.CallToolResult, any, error) {
		data, sourceName, err := resolveUploadSource(tap.Runtime, uploadSourceInput{
			SourcePath: in.SourcePath,
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, allowLocalSources)
		if err != nil {
			return errorResult(err), nil, nil
		}
		name := strings.TrimSpace(in.Filename)
		if name == "" {
			name = sourceName
		}
		if name == "" {
			return errorResult(fmt.Errorf("filename is required when the upload source has no filename")), nil, nil
		}
		opts := tapper.UploadFileOptions{
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
			NodeID:           in.NodeID,
			Data:             data,
			Name:             name,
		}
		storedName, err := tap.UploadFile(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("uploaded file %q to node %s", storedName, in.NodeID)), nil, nil
	})
}

// --- download_file ---

type downloadFileInput struct {
	NodeID   string `json:"node_id" jsonschema:"node ID containing the file"`
	Filename string `json:"filename" jsonschema:"filename to download"`
	DestPath string `json:"dest_path" jsonschema:"absolute path to write the downloaded file"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Flight   string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
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
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
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
	Filename   string               `json:"filename,omitempty" jsonschema:"image filename for the attachment; derived from source path or resource URI if empty"`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"absolute path to the source image file (stdio/local only)"`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"file:// URI (stdio/local only) or data: URI for the source image"`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"base64-encoded image bytes"`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"optional MIME type hint for raw image bytes"`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"embedded resource with uri, mime_type or mimeType, and blob"`
	Keg        string               `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Flight     string               `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
}

func registerUploadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults, allowLocalSources bool) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "upload_image",
		Description: "Upload an image attachment to a node from a local path, raw bytes, data URI, or embedded resource",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadImageInput) (*sdkmcp.CallToolResult, any, error) {
		data, sourceName, err := resolveUploadSource(tap.Runtime, uploadSourceInput{
			SourcePath: in.SourcePath,
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, allowLocalSources)
		if err != nil {
			return errorResult(err), nil, nil
		}
		name := strings.TrimSpace(in.Filename)
		if name == "" {
			name = sourceName
		}
		if name == "" {
			return errorResult(fmt.Errorf("filename is required when the upload source has no filename")), nil, nil
		}
		opts := tapper.UploadImageOptions{
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
			NodeID:           in.NodeID,
			Data:             data,
			Name:             name,
		}
		storedName, err := tap.UploadImage(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("uploaded image %q to node %s", storedName, in.NodeID)), nil, nil
	})
}

// --- download_image ---

type downloadImageInput struct {
	NodeID   string `json:"node_id" jsonschema:"node ID containing the image"`
	Filename string `json:"filename" jsonschema:"image filename to download"`
	DestPath string `json:"dest_path" jsonschema:"absolute path to write the downloaded image"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
	Flight   string `json:"flight,omitempty" jsonschema:"flight ref to cap available kegs (uses server default if empty)"`
}

func registerDownloadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "download_image",
		Description: "Download an image attachment from a node to a local file path",
		Annotations: &sdkmcp.ToolAnnotations{
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in downloadImageInput) (*sdkmcp.CallToolResult, any, error) {
		if in.DestPath == "-" {
			return errorResult(fmt.Errorf("stdout mode is not supported over MCP")), nil, nil
		}
		opts := tapper.DownloadImageOptions{
			KegTargetOptions: resolveKegTargetWithFlight(in.Keg, in.Flight, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
			Dest:             in.DestPath,
		}
		dest, err := tap.DownloadImage(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("downloaded image %q to %s", in.Filename, dest)), nil, nil
	})
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
