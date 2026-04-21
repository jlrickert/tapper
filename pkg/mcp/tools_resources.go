package mcp

import (
	"context"
	"fmt"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// orientResourceURITemplate is the URI shape for per-(host, tier)
// orient resources. The scheme is namespaced to tapper so it cannot
// collide with file:// or other well-known schemes.
const orientResourceURITemplate = "tapper://orient/%s/tier-%d"

// registerResourceTools wires the MCP Resources surface. For each
// registered integration adapter whose host also has a configured
// orient surface (per tapper.OrientableHosts), it registers one
// Resource per tier in [OrientTierMin, OrientTierMax]. The handler
// delegates to tap.Orient, so resources/read returns bytes byte-equal
// to the orient tool at the matching (host, tier).
func registerResourceTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	orientable := make(map[string]bool)
	for _, h := range tapper.OrientableHosts() {
		orientable[h] = true
	}
	for _, a := range integrations.DefaultAdapters() {
		if !orientable[a.Name()] {
			// A registered adapter without an orient surface is not an
			// error; skip it silently so new adapters can ship before
			// their orient plumbing lands.
			continue
		}
		for tier := tapper.OrientTierMin; tier <= tapper.OrientTierMax; tier++ {
			registerOrientResource(srv, tap, defaults, a.Name(), tier)
		}
	}
}

func registerOrientResource(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults, host string, tier int) {
	uri := fmt.Sprintf(orientResourceURITemplate, host, tier)
	srv.AddResource(&sdkmcp.Resource{
		URI:         uri,
		Name:        fmt.Sprintf("tapper orient: %s tier %d", host, tier),
		Description: fmt.Sprintf("Tapper orientation payload for host %s at tier %d. Identical to the output of the orient tool invoked with host=%s tier=%d.", host, tier, host, tier),
		MIMEType:    "text/markdown",
	}, func(ctx context.Context, req *sdkmcp.ReadResourceRequest) (*sdkmcp.ReadResourceResult, error) {
		payload, err := tap.Orient(ctx, tapper.OrientOptions{
			KegTargetOptions: defaults.KegTargetOptions,
			Host:             host,
			Tier:             tier,
		})
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
