package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerFileTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerListFiles(srv, tap, defaults)
	registerListImages(srv, tap, defaults)
	registerDeleteFile(srv, tap, defaults)
	registerDeleteImage(srv, tap, defaults)
	registerUploadFile(srv, tap, defaults)
	registerDownloadFile(srv, tap, defaults)
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
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	NodeID     string `json:"node_id" jsonschema:"node ID to attach the file to"`
	Filename   string `json:"filename" jsonschema:"filename for the attachment"`
	SourcePath string `json:"source_path" jsonschema:"absolute path to the source file"`
	Keg        string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerUploadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "upload_file",
		Description: "Upload a file attachment to a node from a local file path",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadFileInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.UploadFileOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			FilePath:         in.SourcePath,
			Name:             in.Filename,
		}
		name, err := tap.UploadFile(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("uploaded file %q to node %s", name, in.NodeID)), nil, nil
	})
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
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
	NodeID     string `json:"node_id" jsonschema:"node ID to attach the image to"`
	Filename   string `json:"filename" jsonschema:"image filename for the attachment"`
	SourcePath string `json:"source_path" jsonschema:"absolute path to the source image file"`
	Keg        string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerUploadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "upload_image",
		Description: "Upload an image attachment to a node from a local file path",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadImageInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.UploadImageOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
			NodeID:           in.NodeID,
			FilePath:         in.SourcePath,
			Name:             in.Filename,
		}
		name, err := tap.UploadImage(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("uploaded image %q to node %s", name, in.NodeID)), nil, nil
	})
}

// --- download_image ---

type downloadImageInput struct {
	NodeID   string `json:"node_id" jsonschema:"node ID containing the image"`
	Filename string `json:"filename" jsonschema:"image filename to download"`
	DestPath string `json:"dest_path" jsonschema:"absolute path to write the downloaded image"`
	Keg      string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
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
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
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
