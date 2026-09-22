package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

const (
	// orientResourceURI is the host-independent MCP resource mirror of the
	// orient tool. The scheme is namespaced to tapper so it cannot collide
	// with file:// or other well-known schemes.
	orientResourceURI       = "tapper://orient"
	nodeResourceURITemplate = "tapper://node/{node_id}{?keg}"
	// attachmentResourceURITemplate addresses one original attachment by the
	// same kind-qualified namespace the hub's attachment API uses.
	attachmentResourceURITemplate = "tapper://node/{node_id}/attachments/{kind}/{name}{?keg}"
	// maxAttachmentResourceBytes matches the hub's per-attachment upload cap,
	// so anything an upload accepted can be read back as a resource.
	maxAttachmentResourceBytes = 50 << 20
)

// registerResourceTools wires the MCP Resources surface. The orient resource
// delegates to the same read-only session view as orient, so resources/read
// returns bytes byte-equal to a bare orient tool call.
func registerResourceTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerAttachmentResource(srv, tap, defaults)
	registerNodeResource(srv, tap, defaults)
	registerOrientResource(srv, tap, defaults)
}

func registerNodeResource(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	srv.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: nodeResourceURITemplate,
		Name:        "tapper node content",
		Description: "Current markdown content for a Tapper node. Add ?keg= with a URL-escaped keg target to override the server default.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		ref, ok := parseNodeResourceURI(req.Params.URI)
		if !ok {
			return nil, fmt.Errorf("unsupported node resource URI %q", req.Params.URI)
		}
		payload, err := tap.Cat(ctx, tapper.CatOptions{
			NodeIDs:          []string{ref.nodeID},
			KegTargetOptions: resolveKegTarget(ctx, ref.keg, defaults),
			ContentOnly:      true,
		})
		if err != nil {
			return nil, err
		}
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{
				{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     payload,
				},
			},
		}, nil
	})
}

type nodeResourceRef struct {
	nodeID string
	keg    string
}

func parseNodeResourceURI(raw string) (nodeResourceRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "tapper" || u.Host != "node" {
		return nodeResourceRef{}, false
	}
	nodeID := strings.TrimPrefix(u.Path, "/")
	if nodeID == "" || strings.Contains(nodeID, "/") {
		return nodeResourceRef{}, false
	}
	if _, err := strconv.Atoi(nodeID); err != nil {
		return nodeResourceRef{}, false
	}
	return nodeResourceRef{nodeID: nodeID, keg: u.Query().Get("keg")}, true
}

func registerAttachmentResource(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	srv.AddResourceTemplate(&sdkmcp.ResourceTemplate{
		URITemplate: attachmentResourceURITemplate,
		Name:        "tapper node attachment",
		Description: "Original bytes of a node attachment. kind is image, file, or video; name is the percent-encoded filename. list_images and list_files return these URIs as resource links. Add ?keg= with a URL-escaped keg target to override the server default.",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		uri := req.Params.URI
		ref, ok := parseAttachmentResourceURI(uri)
		if !ok {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		data, mimeType, err := tap.ReadAttachment(ctx, tapper.ReadAttachmentOptions{
			KegTargetOptions: resolveKegTarget(ctx, ref.keg, defaults),
			NodeID:           ref.nodeID,
			Kind:             ref.kind,
			Name:             ref.name,
		})
		if errors.Is(err, keg.ErrNotExist) {
			return nil, sdkmcp.ResourceNotFoundError(uri)
		}
		if err != nil {
			return nil, err
		}
		if len(data) > maxAttachmentResourceBytes {
			return nil, fmt.Errorf("%s %q is %d bytes, over the %d byte resource limit", ref.kind, ref.name, len(data), maxAttachmentResourceBytes)
		}
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{{URI: uri, MIMEType: mimeType, Blob: data}},
		}, nil
	})
}

type attachmentResourceRef struct {
	nodeID string
	kind   keg.AttachmentKind
	name   string
	keg    string
}

func parseAttachmentResourceURI(raw string) (attachmentResourceRef, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "tapper" || u.Host != "node" {
		return attachmentResourceRef{}, false
	}
	parts := strings.Split(strings.TrimPrefix(u.EscapedPath(), "/"), "/")
	if len(parts) != 4 || parts[1] != "attachments" {
		return attachmentResourceRef{}, false
	}
	if _, err := strconv.Atoi(parts[0]); err != nil {
		return attachmentResourceRef{}, false
	}
	kind := keg.AttachmentKind(parts[2])
	switch kind {
	case keg.AttachmentImage, keg.AttachmentFile, keg.AttachmentVideo:
	default:
		return attachmentResourceRef{}, false
	}
	name, err := url.PathUnescape(parts[3])
	if err != nil || keg.ValidateAssetName(name) != nil {
		return attachmentResourceRef{}, false
	}
	return attachmentResourceRef{nodeID: parts[0], kind: kind, name: name, keg: u.Query().Get("keg")}, true
}

// attachmentResourceURI builds the canonical resource URI for an attachment.
// Every byte outside RFC 3986 unreserved is percent-encoded, which is what a
// level-1 URI template variable matches.
func attachmentResourceURI(nodeID string, kind keg.AttachmentKind, name, kegTarget string) string {
	uri := "tapper://node/" + nodeID + "/attachments/" + string(kind) + "/" + escapeURIComponent(name)
	if kegTarget != "" {
		uri += "?keg=" + escapeURIComponent(kegTarget)
	}
	return uri
}

func escapeURIComponent(s string) string {
	const hex = "0123456789ABCDEF"
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

func registerOrientResource(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	srv.AddResource(&sdkmcp.Resource{
		URI:         orientResourceURI,
		Name:        "tapper orient",
		Description: "Tapper KEG system orientation payload. Identical to the output of the orient tool with no explicit arguments.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		var payload string
		if defaults.gate != nil {
			payload = defaults.gate.payload(ctx)
		} else {
			var err error
			payload, err = tap.Orient(ctx, tapper.OrientOptions{
				KegTargetOptions: resolveKegTarget(ctx, "", defaults),
			})
			if err != nil {
				return nil, err
			}
		}
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{
				{
					URI:      orientResourceURI,
					MIMEType: "text/markdown",
					Text:     payload,
				},
			},
		}, nil
	})
}
