package mcp

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

const (
	// orientResourceURI is the host-independent MCP resource mirror of the
	// orient tool. The scheme is namespaced to tapper so it cannot collide
	// with file:// or other well-known schemes.
	orientResourceURI       = "tapper://orient"
	nodeResourceURITemplate = "tapper://node/{node_id}{?keg}"
)

// registerResourceTools wires the MCP Resources surface. The orient resource
// delegates to tap.Orient, so resources/read returns bytes byte-equal to a
// bare orient tool call.
func registerResourceTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
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

func registerOrientResource(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	srv.AddResource(&sdkmcp.Resource{
		URI:         orientResourceURI,
		Name:        "tapper orient",
		Description: "Tapper KEG system orientation payload. Identical to the output of the orient tool with no explicit arguments.",
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		var payload string
		if defaults.gate != nil {
			current, err := defaults.gate.refresh(ctx, sessionIDFromContext(ctx))
			if err != nil {
				return nil, err
			}
			payload = current.payload
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
