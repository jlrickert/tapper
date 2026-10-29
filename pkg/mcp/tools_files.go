package mcp

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
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
	registerHostedDownloadFile(srv, tap, defaults)
	registerDownloadImage(srv, tap, defaults)
}

// --- file_list ---

type listFilesInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to list files for."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerListFiles(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "file_list",
		Description:  "Memory: List a node's file attachments.",
		OutputSchema: textOutputSchema(),
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
		return attachmentLinesResult(in.NodeID, keg.AttachmentFile, files, in.Keg), nil, nil
	})
}

// --- image_list ---

type listImagesInput struct {
	NodeID string `json:"node_id" jsonschema:"Node ID to list images for."`
	Keg    string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerListImages(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "image_list",
		Description:  "Memory: List a node's image attachments.",
		OutputSchema: textOutputSchema(),
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
		return attachmentLinesResult(in.NodeID, keg.AttachmentImage, images, in.Keg), nil, nil
	})
}

// attachmentLinesResult lists attachment names as text, followed by one
// resource link per name so a client can read each through resources/read.
func attachmentLinesResult(nodeID string, kind keg.AttachmentKind, names []string, kegTarget string) *sdkmcp.CallToolResult {
	result := linesResult(names)
	for _, name := range names {
		appendAttachmentResourceLink(result, nodeID, kind, name, kegTarget)
	}
	return result
}

// appendAttachmentResourceLink adds a resource link for the attachment. Node
// arguments that are not a bare numeric id (a qualified ref, say) have no
// canonical resource URI, so they get the text result alone.
func appendAttachmentResourceLink(result *sdkmcp.CallToolResult, nodeID string, kind keg.AttachmentKind, name, kegTarget string) {
	nodeID = strings.TrimSpace(nodeID)
	if _, err := strconv.Atoi(nodeID); err != nil {
		return
	}
	result.Content = append(result.Content, &sdkmcp.ResourceLink{
		URI:  attachmentResourceURI(nodeID, kind, name, kegTarget),
		Name: name,
	})
}

// --- file_delete ---

type deleteFileInput struct {
	NodeID   string  `json:"node_id" jsonschema:"Node ID containing the file."`
	Filename *string `json:"filename,omitempty" jsonschema:"Filename to delete; required unless name is supplied."`
	Name     *string `json:"name,omitempty" jsonschema:"Compatibility alias for filename; must match when both are supplied."`
	Keg      string  `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerDeleteFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "file_delete",
		Description:  "Memory: Delete a file attachment from a node.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deleteFileInput) (*sdkmcp.CallToolResult, any, error) {
		name, err := deletionFilename(in.Filename, in.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		opts := tapper.DeleteFileOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             name,
		}
		if err := tap.DeleteFile(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("deleted file %q from node %s", name, in.NodeID)), nil, nil
	})
}

// --- image_delete ---

type deleteImageInput struct {
	NodeID   string  `json:"node_id" jsonschema:"Node ID containing the image."`
	Filename *string `json:"filename,omitempty" jsonschema:"Filename to delete; required unless name is supplied."`
	Name     *string `json:"name,omitempty" jsonschema:"Compatibility alias for filename; must match when both are supplied."`
	Keg      string  `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerDeleteImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "image_delete",
		Description:  "Memory: Delete an image attachment from a node.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in deleteImageInput) (*sdkmcp.CallToolResult, any, error) {
		name, err := deletionFilename(in.Filename, in.Name)
		if err != nil {
			return errorResult(err), nil, nil
		}
		opts := tapper.DeleteImageOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             name,
		}
		if err := tap.DeleteImage(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("deleted image %q from node %s", name, in.NodeID)), nil, nil
	})
}

// --- file_upload ---

type uploadFileInput struct {
	NodeID     string               `json:"node_id" jsonschema:"Node ID to attach the file to."`
	Filename   string               `json:"filename,omitempty" jsonschema:"Filename for the attachment; derived from a data URI or resource URI if empty."`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"Source file as a data: URI."`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"Base64-encoded file bytes."`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"Optional MIME type hint for raw file bytes."`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"Embedded resource with uri, mime_type or mimeType, and blob or text."`
	Keg        string               `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// localUploadFileInput is uploadFileInput plus the local-path source that only
// a shared-filesystem transport can honour. Keeping it a separate type is what
// keeps source_path out of the hosted schema.
type localUploadFileInput struct {
	NodeID     string               `json:"node_id" jsonschema:"Node ID to attach the file to."`
	Filename   string               `json:"filename,omitempty" jsonschema:"Filename for the attachment; derived from the source path, data URI, or resource URI if empty."`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"Regular file within the server working directory."`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"Source file as a file: or data: URI."`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"Base64-encoded file bytes."`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"Optional MIME type hint for raw file bytes."`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"Embedded resource with uri, mime_type or mimeType, and blob or text."`
	Keg        string               `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerUploadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadFileTool(
		"Memory: Upload a file attachment to a node from raw bytes, a data URI, or an embedded resource.",
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
		"Memory: Upload a file attachment to a node from a local path, raw bytes, a data URI, or an embedded resource.",
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
	uploadVideoLinkHint = " Embed it in the node body as ![caption](./videos/FILENAME) to play it on the node's page — the directory is plural."
)

func uploadFileTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:         "file_upload",
		Description:  description + uploadFileLinkHint,
		OutputSchema: outputSchema[uploadOutput](),
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
	result := textResult(fmt.Sprintf("uploaded file %q to node %s", storedName, nodeID))
	appendAttachmentResourceLink(result, nodeID, keg.AttachmentFile, storedName, kegAlias)
	result.StructuredContent = uploadOutput{NodeID: nodeID, Filename: storedName, Link: "./assets/" + storedName}
	return result, nil, nil
}

// uploadOutput is file_upload's and image_upload's structured content.
type uploadOutput struct {
	NodeID   string `json:"node_id" jsonschema:"The node the attachment belongs to."`
	Filename string `json:"filename" jsonschema:"The stored filename, which may differ from the one sent."`
	Link     string `json:"link" jsonschema:"The relative link to write in the node body."`
}

// imageContentOutput describes the image image_download returns as MCP image
// content. Saving to dest_path (shared-filesystem servers only) returns text
// alone, so every field is optional.
type imageContentOutput struct {
	NodeID   string `json:"node_id,omitempty" jsonschema:"The node the image belongs to."`
	Filename string `json:"filename,omitempty" jsonschema:"The image's filename."`
	MIMEType string `json:"mime_type,omitempty" jsonschema:"The image's MIME type."`
	Size     int    `json:"size,omitempty" jsonschema:"The image's size in bytes."`
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
		return "", fmt.Errorf("filename is required when the upload source has no filename: %w", keg.ErrInvalid)
	}
	return name, nil
}

// --- file_download ---

type downloadFileInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the file."`
	Filename string `json:"filename" jsonschema:"Filename to download."`
	DestPath string `json:"dest_path" jsonschema:"New file path within the server working directory; existing files are refused."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// hostedDownloadFileInput is file_download without a destination: a hosted
// server shares no filesystem with its agent, so the bytes come back in the
// result instead, as image_download returns an image.
type hostedDownloadFileInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the file."`
	Filename string `json:"filename" jsonschema:"Filename to download."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// fileContentOutput describes the file the hosted file_download returns as an
// embedded resource.
type fileContentOutput struct {
	NodeID   string `json:"node_id" jsonschema:"The node the file belongs to."`
	Filename string `json:"filename" jsonschema:"The file's name."`
	MIMEType string `json:"mime_type" jsonschema:"The file's MIME type."`
	Size     int    `json:"size" jsonschema:"The file's size in bytes."`
}

func registerHostedDownloadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "file_download",
		Description:  "Memory: Return a file attachment from a node as an embedded resource holding its bytes, to read a PDF, document, or text file.",
		OutputSchema: outputSchema[fileContentOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in hostedDownloadFileInput) (*sdkmcp.CallToolResult, any, error) {
		data, mimeType, err := tap.ReadAttachment(ctx, tapper.ReadAttachmentOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Kind:             keg.AttachmentFile,
			Name:             in.Filename,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(data) > maxAttachmentResourceBytes {
			return errorResult(fmt.Errorf("file %q is %d bytes, over the %d byte download limit: %w", in.Filename, len(data), maxAttachmentResourceBytes, keg.ErrInvalid)), nil, nil
		}
		// A bare node id has a canonical resource URI; any other node
		// argument names the file alone.
		uri := escapeURIComponent(in.Filename)
		if nodeID := strings.TrimSpace(in.NodeID); nodeID != "" {
			if _, err := strconv.Atoi(nodeID); err == nil {
				uri = attachmentResourceURI(nodeID, keg.AttachmentFile, in.Filename, in.Keg)
			}
		}
		return &sdkmcp.CallToolResult{
			Content:           []sdkmcp.Content{&sdkmcp.EmbeddedResource{Resource: &sdkmcp.ResourceContents{URI: uri, MIMEType: mimeType, Blob: data}}},
			StructuredContent: fileContentOutput{NodeID: in.NodeID, Filename: in.Filename, MIMEType: mimeType, Size: len(data)},
		}, nil, nil
	})
}

func registerDownloadFile(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "file_download",
		Description:  "Memory: Download a file attachment from a node to a new local file.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in downloadFileInput) (*sdkmcp.CallToolResult, any, error) {
		if in.DestPath == "-" {
			return errorResult(fmt.Errorf("%w: stdout mode is not supported over MCP", keg.ErrInvalid)), nil, nil
		}
		destPath, pathErr := confinedTransferPath(tap.Runtime, in.DestPath, true)
		if pathErr != nil {
			return errorResult(pathErr), nil, nil
		}
		opts := tapper.DownloadFileOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
			Dest:             destPath,
			NoOverwrite:      true,
		}
		dest, err := tap.DownloadFile(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("downloaded file %q to %s", in.Filename, dest)), nil, nil
	})
}

// --- image_upload ---

type uploadImageInput struct {
	NodeID     string               `json:"node_id" jsonschema:"Node ID to attach the image to."`
	Filename   string               `json:"filename,omitempty" jsonschema:"Image filename for the attachment; derived from a data URI or resource URI if empty."`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"Source image as a data: URI."`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"Base64-encoded image bytes."`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"Optional MIME type hint for raw image bytes."`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"Embedded resource with uri, mime_type or mimeType, and blob."`
	Keg        string               `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// localUploadImageInput is uploadImageInput plus the local-path source. See
// localUploadFileInput for why this is a separate type.
type localUploadImageInput struct {
	NodeID     string               `json:"node_id" jsonschema:"Node ID to attach the image to."`
	Filename   string               `json:"filename,omitempty" jsonschema:"Image filename for the attachment; derived from the source path, data URI, or resource URI if empty."`
	SourcePath string               `json:"source_path,omitempty" jsonschema:"Regular image file within the server working directory."`
	SourceURI  string               `json:"source_uri,omitempty" jsonschema:"Source image as a file: or data: URI."`
	DataBase64 string               `json:"data_base64,omitempty" jsonschema:"Base64-encoded image bytes."`
	MIMEType   string               `json:"mime_type,omitempty" jsonschema:"Optional MIME type hint for raw image bytes."`
	Resource   *uploadResourceInput `json:"resource,omitempty" jsonschema:"Embedded resource with uri, mime_type or mimeType, and blob."`
	Keg        string               `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerUploadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, uploadImageTool(
		"Memory: Upload an image attachment to a node from raw bytes, a data URI, or an embedded resource.",
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
		"Memory: Upload an image attachment to a node from a local path, raw bytes, a data URI, or an embedded resource.",
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
		Name:         "image_upload",
		Description:  description + uploadImageLinkHint,
		OutputSchema: outputSchema[uploadOutput](),
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
	result := textResult(fmt.Sprintf("uploaded image %q to node %s", storedName, nodeID))
	appendAttachmentResourceLink(result, nodeID, keg.AttachmentImage, storedName, kegAlias)
	result.StructuredContent = uploadOutput{NodeID: nodeID, Filename: storedName, Link: "./images/" + storedName}
	return result, nil, nil
}

// --- image_download ---

type downloadImageInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the image."`
	Filename string `json:"filename" jsonschema:"Image filename to download."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// localDownloadImageInput adds an optional destination path. Omitting it keeps
// the inline-content behaviour, so a shared-filesystem agent can either look at
// the image or save it without needing two different tools.
type localDownloadImageInput struct {
	NodeID   string `json:"node_id" jsonschema:"Node ID containing the image."`
	Filename string `json:"filename" jsonschema:"Image filename to download."`
	DestPath string `json:"dest_path,omitempty" jsonschema:"New file path within the server working directory; existing files are refused; omit to receive the image as MCP content."`
	Keg      string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerDownloadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, downloadImageTool(
		"Memory: Return an image attachment from a node as MCP image content.",
	), func(ctx context.Context, req *sdkmcp.CallToolRequest, in downloadImageInput) (*sdkmcp.CallToolResult, any, error) {
		return readImageContent(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename)
	})
}

func registerLocalDownloadImage(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	tool := downloadImageTool("Memory: Return an image attachment as MCP image content, or save it to a new private file within the working directory; existing files are never overwritten.")
	tool.Annotations.ReadOnlyHint = false
	tool.Annotations.DestructiveHint = boolPtr(false)
	sdkmcp.AddTool(srv, tool, func(ctx context.Context, req *sdkmcp.CallToolRequest, in localDownloadImageInput) (*sdkmcp.CallToolResult, any, error) {
		dest := strings.TrimSpace(in.DestPath)
		if dest == "" {
			return readImageContent(ctx, tap, defaults, in.Keg, in.NodeID, in.Filename)
		}
		if dest == "-" {
			return errorResult(fmt.Errorf("%w: stdout mode is not supported over MCP; omit dest_path to receive image content", keg.ErrInvalid)), nil, nil
		}
		dest, pathErr := confinedTransferPath(tap.Runtime, dest, true)
		if pathErr != nil {
			return errorResult(pathErr), nil, nil
		}
		written, err := tap.DownloadImage(ctx, tapper.DownloadImageOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeID:           in.NodeID,
			Name:             in.Filename,
			Dest:             dest,
			NoOverwrite:      true,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("downloaded image %q to %s", in.Filename, written)), nil, nil
	})
}

func downloadImageTool(description string) *sdkmcp.Tool {
	return &sdkmcp.Tool{
		Name:         "image_download",
		Description:  description,
		OutputSchema: outputSchema[imageContentOutput](),
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
	mimeType := keg.ImageContentType(format)
	return &sdkmcp.CallToolResult{
		Content:           []sdkmcp.Content{&sdkmcp.ImageContent{Data: data, MIMEType: mimeType}},
		StructuredContent: imageContentOutput{NodeID: nodeID, Filename: filename, MIMEType: mimeType, Size: len(data)},
	}, nil, nil
}

type uploadResourceInput struct {
	URI          string `json:"uri,omitempty" jsonschema:"Resource URI."`
	MIMEType     string `json:"mime_type,omitempty" jsonschema:"Resource MIME type."`
	MIMETypeSpec string `json:"mimeType,omitempty" jsonschema:"Resource MIME type using MCP field casing."`
	Blob         string `json:"blob,omitempty" jsonschema:"Base64-encoded binary resource bytes."`
	Text         string `json:"text,omitempty" jsonschema:"Text resource contents for file uploads."`
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
		return nil, "", fmt.Errorf("provide exactly one upload source: %w", keg.ErrInvalid)
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
		return nil, "", fmt.Errorf("embedded resource is empty: %w", keg.ErrInvalid)
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
		return nil, "", fmt.Errorf("embedded resource must include blob, text, or uri: %w", keg.ErrInvalid)
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
		return nil, "", fmt.Errorf("invalid source_uri: %w: %w", err, keg.ErrInvalid)
	}
	if u.Scheme == "file" {
		if u.Host != "" && u.Host != "localhost" {
			return nil, "", fmt.Errorf("file URI host %q is not supported: %w", u.Host, keg.ErrInvalid)
		}
		return readLocalUploadSource(rt, u.Path, allowLocalSources)
	}
	return nil, "", fmt.Errorf("unsupported source_uri scheme %q: %w", u.Scheme, keg.ErrInvalid)
}

func readLocalUploadSource(rt *toolkit.Runtime, path string, allowLocalSources bool) ([]byte, string, error) {
	if !allowLocalSources {
		return nil, "", fmt.Errorf("local file sources are not available on this MCP surface; use data_base64 or an embedded resource blob: %w", keg.ErrInvalid)
	}
	path, err := confinedTransferPath(rt, path, false)
	if err != nil {
		return nil, "", fmt.Errorf("unable to read local file: %w", err)
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
		return nil, fmt.Errorf("invalid data URI: %w", keg.ErrInvalid)
	}
	if strings.Contains(strings.ToLower(prefix), ";base64") {
		return decodeUploadBase64(payload)
	}
	decoded, err := url.PathUnescape(payload)
	if err != nil {
		return nil, fmt.Errorf("invalid data URI payload: %w: %w", err, keg.ErrInvalid)
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
	return nil, fmt.Errorf("invalid base64 upload data: %w: %w", err, keg.ErrInvalid)
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

func deletionFilename(filename, alias *string) (string, error) {
	if filename != nil && alias != nil && *filename != *alias {
		return "", fmt.Errorf("%w: filename and name must match", keg.ErrInvalid)
	}
	if filename == nil {
		filename = alias
	}
	if filename == nil || strings.TrimSpace(*filename) == "" {
		return "", fmt.Errorf("%w: filename is required", keg.ErrInvalid)
	}
	return *filename, nil
}

// Resolve symlinks and environment expansion before checking the workspace
// boundary. Downloads additionally use O_EXCL, so a final symlink cannot be
// used to overwrite an existing destination.
func confinedTransferPath(rt *toolkit.Runtime, raw string, writing bool) (string, error) {
	if strings.TrimSpace(raw) == "" || raw == "-" {
		return "", fmt.Errorf("an explicit workspace file path is required")
	}
	root, err := rt.ResolvePath(".", true)
	if err != nil {
		return "", err
	}
	path, err := rt.ResolvePath(raw, false)
	// Runtime filesystem calls expand variables again. A resolved path must
	// be literal so a nested variable cannot change it after confinement.
	if strings.ContainsAny(root+path, "$") {
		return "", fmt.Errorf("file transfer paths must resolve to literal paths")
	}
	if err == nil {
		if writing {
			var parent string
			parent, err = rt.ResolvePath(filepath.Dir(path), true)
			path = filepath.Join(parent, filepath.Base(path))
		} else {
			path, err = rt.ResolvePath(path, true)
		}
	}
	if err != nil {
		return "", err
	}
	if strings.Contains(path, "$") {
		return "", fmt.Errorf("file transfer paths must resolve to literal paths")
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("file transfers must stay within the server working directory")
	}
	if writing {
		if _, err := rt.Stat(path, false); err == nil {
			return "", fmt.Errorf("destination already exists")
		} else if !os.IsNotExist(err) {
			return "", err
		}
	} else {
		info, err := rt.Stat(path, true)
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("source must be a regular file")
		}
	}
	return path, nil
}
