package mcp

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// registerVideoTools publishes the video attachment tools. Like the file and
// image tools, only the transfers differ by transport: sharedFS adds local
// paths to the upload and a destination to the download.
func registerVideoTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults, sharedFS bool) {
	registerListVideos(srv, tap, defaults)
	registerDeleteVideo(srv, tap, defaults)
	if sharedFS {
		registerLocalUploadVideo(srv, tap, defaults)
		registerLocalDownloadVideo(srv, tap, defaults)
		return
	}
	registerUploadVideo(srv, tap, defaults)
	registerDownloadVideo(srv, tap, defaults)
}

// --- video_list ---

type listVideosInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to list videos for."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerListVideos(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "video_list",
		Description:  "Memory: List a node's video attachments.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in listVideosInput) (*sdkmcp.CallToolResult, any, error) {
		videos, err := tap.ListVideos(ctx, tapper.ListVideosOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(videos) == 0 {
			return textResult("no videos"), nil, nil
		}
		return attachmentLinesResult(in.NodeID, keg.AttachmentVideo, videos, in.Keg), nil, nil
	})
}

// --- video_delete ---

type deleteVideoInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the video."`
	Filename string `json:"filename" jsonschema:"Video filename to delete."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerDeleteVideo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "video_delete",
		Description:  "Memory: Delete a video attachment from a node.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deleteVideoInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.DeleteVideo(ctx, tapper.DeleteVideoOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
		}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("deleted video %q from node %s", in.Filename, in.NodeID)), nil, nil
	})
}

// --- video_upload ---

type uploadVideoInput struct {
	NodeID     string               `json:"node_id" jsonschema:"Node ID to attach the video to."`
	Filename   string               `json:"filename,omitempty" jsonschema:"Video filename for the attachment; derived from a data URI or resource URI if empty."`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"Source video as a data: URI."`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"Base64-encoded video bytes."`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"Embedded resource with uri, mime_type or mimeType, and blob."`
	Keg        string               `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// localUploadVideoInput is uploadVideoInput plus the local-path source; see
// localUploadFileInput for why it is a separate type.
type localUploadVideoInput struct {
	NodeID     string               `json:"node_id" jsonschema:"Node ID to attach the video to."`
	Filename   string               `json:"filename,omitempty" jsonschema:"Video filename for the attachment; derived from the source path, data URI, or resource URI if empty."`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"Regular video file within the server working directory."`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"Source video as a file: or data: URI."`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"Base64-encoded video bytes."`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"Embedded resource with uri, mime_type or mimeType, and blob."`
	Keg        string               `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// videoUploadOutput is video_upload's structured content.
type videoUploadOutput struct {
	NodeID   string `json:"node_id" jsonschema:"The node the video belongs to."`
	Filename string `json:"filename" jsonschema:"The stored filename."`
}

func registerUploadVideo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadVideoTool(
		"Memory: Upload a video attachment (MP4, WebM, or Ogg) to a node from raw bytes, a data URI, or an embedded resource."+uploadVideoLinkHint,
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in uploadVideoInput) (*sdkmcp.CallToolResult, any, error) {
		return handleVideoUpload(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename, uploadSourceInput{
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, false)
	})
}

func registerLocalUploadVideo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadVideoTool(
		"Memory: Upload a video attachment (MP4, WebM, or Ogg) to a node from a local path, raw bytes, a data URI, or an embedded resource."+uploadVideoLinkHint,
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in localUploadVideoInput) (*sdkmcp.CallToolResult, any, error) {
		return handleVideoUpload(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename, uploadSourceInput{
			SourcePath: in.SourcePath,
			SourceURI:  in.SourceURI,
			DataBase64: in.DataBase64,
			Resource:   in.Resource,
		}, true)
	})
}

func uploadVideoTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:         "video_upload",
		Description:  description,
		OutputSchema: outputSchema[videoUploadOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}
}

func handleVideoUpload(ctx context.Context, tap *tapper.Tap, defaults KegDefaults, kegAlias, nodeID, filename string, src uploadSourceInput, sharedFS bool) (*sdkmcp.CallToolResult, any, error) {
	data, sourceName, err := resolveUploadSource(tap.Runtime, src, sharedFS)
	if err != nil {
		return errorResult(err), nil, nil
	}
	name, err := resolveUploadName(filename, sourceName)
	if err != nil {
		return errorResult(err), nil, nil
	}
	storedName, err := tap.UploadVideo(ctx, tapper.UploadVideoOptions{
		KegTargetOptions: resolveKegTarget(ctx, kegAlias, defaults),
		NodeID:           nodeID,
		Data:             data,
		Name:             name,
	})
	if err != nil {
		return errorResult(err), nil, nil
	}
	result := textResult(fmt.Sprintf("uploaded video %q to node %s", storedName, nodeID))
	appendAttachmentResourceLink(result, nodeID, keg.AttachmentVideo, storedName, kegAlias)
	result.StructuredContent = videoUploadOutput{NodeID: nodeID, Filename: storedName}
	return result, nil, nil
}

// --- video_download ---

type downloadVideoInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the video."`
	Filename string `json:"filename" jsonschema:"Video filename to download."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// localDownloadVideoInput adds an optional destination: omit it to receive
// the video as content, as on a hosted server.
type localDownloadVideoInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the video."`
	Filename string `json:"filename" jsonschema:"Video filename to download."`
	DestPath string `json:"dest_path,omitempty" jsonschema:"New file path within the server working directory; existing files are refused; omit to receive the video as an embedded resource."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// videoContentOutput describes the video video_download returns. Saving to
// dest_path (shared-filesystem servers only) returns text alone, so every
// field is optional.
type videoContentOutput struct {
	NodeID   string `json:"node_id,omitempty" jsonschema:"The node the video belongs to."`
	Filename string `json:"filename,omitempty" jsonschema:"The video's filename."`
	MIMEType string `json:"mime_type,omitempty" jsonschema:"The video's MIME type."`
	Size     int    `json:"size,omitempty" jsonschema:"The video's size in bytes."`
}

func registerDownloadVideo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, downloadVideoTool(
		"Memory: Return a video attachment from a node as an embedded resource holding its bytes.",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in downloadVideoInput) (*sdkmcp.CallToolResult, any, error) {
		return readVideoContent(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename)
	})
}

func registerLocalDownloadVideo(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	tool := downloadVideoTool("Memory: Return a video attachment as an embedded resource, or save it to a new private file within the working directory; existing files are never overwritten.")
	tool.Annotations.ReadOnlyHint = false
	tool.Annotations.DestructiveHint = boolPtr(false)
	sdkmcp.AddTool(srv, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, in localDownloadVideoInput) (*sdkmcp.CallToolResult, any, error) {
		dest := strings.TrimSpace(in.DestPath)
		if dest == "" {
			return readVideoContent(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename)
		}
		if dest == "-" {
			return errorResult(fmt.Errorf("%w: stdout mode is not supported over MCP; omit dest_path to receive the video", keg.ErrInvalid)), nil, nil
		}
		dest, pathErr := confinedTransferPath(tap.Runtime, dest, true)
		if pathErr != nil {
			return errorResult(pathErr), nil, nil
		}
		written, err := tap.DownloadVideo(ctx, tapper.DownloadVideoOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
			Dest:             dest,
			NoOverwrite:      true,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("downloaded video %q to %s", in.Filename, written)), nil, nil
	})
}

func downloadVideoTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:         "video_download",
		Description:  description,
		OutputSchema: outputSchema[videoContentOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}
}

func readVideoContent(ctx context.Context, tap *tapper.Tap, defaults KegDefaults, kegAlias, nodeID, filename string) (*sdkmcp.CallToolResult, any, error) {
	data, mimeType, err := tap.ReadVideo(ctx, tapper.ReadVideoOptions{
		KegTargetOptions: resolveKegTarget(ctx, kegAlias, defaults),
		NodeID:           nodeID,
		Name:             filename,
	})
	if err != nil {
		return errorResult(err), nil, nil
	}
	if len(data) > maxAttachmentResourceBytes {
		return errorResult(fmt.Errorf("video %q is %d bytes, over the %d byte download limit: %w", filename, len(data), maxAttachmentResourceBytes, keg.ErrInvalid)), nil, nil
	}
	// A bare node id has a canonical resource URI; any other node argument
	// names the video alone.
	uri := escapeURIComponent(filename)
	if id := strings.TrimSpace(nodeID); id != "" {
		if _, err := strconv.Atoi(id); err == nil {
			uri = attachmentResourceURI(id, keg.AttachmentVideo, filename, kegAlias)
		}
	}
	return &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{URI: uri, MIMEType: mimeType, Blob: data}}},
		StructuredContent: videoContentOutput{NodeID: nodeID, Filename: filename, MIMEType: mimeType, Size: len(data)},
	}, nil, nil
}
