package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// orientResourceURITemplate is the URI shape for per-(host, tier) orient
// resources. The scheme is namespaced to tapper so it cannot collide with
// file:// or other well-known schemes.
const orientResourceURITemplate = "tapper://orient/%s/tier-%d"

// registerResourceTools wires the MCP Resources surface. For each
// registered integration adapter with a known orient surface, it
// registers one Resource per tier (OrientTierMin .. OrientTierMax). The
// handler shares buildOrientPayload with the orient tool, so
// resources/read returns bytes byte-equal to the tool's output at the
// matching (host, tier).
//
// The tap parameter is accepted for signature consistency with the other
// register*Tools helpers but is not needed at registration time.
func registerResourceTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	_ = tap
	for _, a := range integrations.DefaultAdapters() {
		host := a.Name()
		if _, ok := hostRenderedPath[host]; !ok {
			// An adapter is registered but has no orient surface yet.
			// Skip rather than panic so a new adapter can ship before
			// its orient plumbing lands.
			continue
		}
		for tier := OrientTierMin; tier <= OrientTierMax; tier++ {
			registerOrientResource(srv, defaults, host, tier)
		}
	}
}

func registerOrientResource(srv *sdkmcp.Server, defaults KegDefaults, host string, tier int) {
	uri := fmt.Sprintf(orientResourceURITemplate, host, tier)
	srv.AddResource(&sdkmcp.Resource{
		URI:         uri,
		Name:        fmt.Sprintf("tapper orient: %s tier %d", host, tier),
		Description: fmt.Sprintf("Tapper orientation payload for host %s at tier %d. Identical to the output of the orient tool invoked with host=%s tier=%d.", host, tier, host, tier),
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		payload, err := buildOrientPayload(host, defaults.Keg, "", tier)
		if err != nil {
			return nil, err
		}
		return &sdkmcp.ReadResourceResult{
			Contents: []*sdkmcp.ResourceContents{
				{
					URI:      uri,
					MIMEType: "text/markdown",
					Text:     payload,
				},
			},
		}, nil
	})
}
