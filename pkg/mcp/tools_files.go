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
