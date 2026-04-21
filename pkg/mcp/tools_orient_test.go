package mcp_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/integrations"

	// Register the Claude adapter so orient and resources exercise the
	// live integration surface instead of an empty registry.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
)

// orientCall is a thin helper that invokes the orient tool and returns
// the text payload. It fails the test on transport error or tool-side
// error so callers can treat the returned string as authoritative.
func orientCall(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, args map[string]any) string {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "orient",
		Arguments: args,
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "orient returned error: %s", text)
	return text
}

func TestMCP_OrientTool_Tier0IsBoundedAndHostless(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	text := orientCall(t, session, ctx, map[string]any{"tier": 0})

	// Tier 0 must name the tier, state the purpose, and carry the rules
	// summary. It must not include canonical sections reserved for
	// higher tiers or host-specific bytes.
	require.Contains(t, text, "tier 0")
	require.Contains(t, text, "Tapper is a CLI and MCP server")
	require.Contains(t, text, "Active keg:")
	require.Contains(t, text, "Rules:")

	require.NotContains(t, text, "## Linking conventions")
	require.NotContains(t, text, "## Snapshot policy")
	require.NotContains(t, text, "## Host:")

	// Bounded: canonical tier-0 content stays under ~2 KB. The spec calls
	// for ~300 tokens; at roughly 4 characters per token the budget is
	// ~1200 chars with headroom for the active-keg line.
	require.Less(t, len(text), 2048, "tier-0 payload should be bounded; got %d bytes", len(text))
}

func TestMCP_OrientTool_Tier1AddsLinkingAndSnapshot(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	text := orientCall(t, session, ctx, map[string]any{"tier": 1})

	// Tier 1 adds linking + snapshot on top of tier 0. The canonical
	// files land verbatim, so their H1s are observable.
	require.Contains(t, text, "Rules:")
	require.Contains(t, text, "# Linking conventions")
	require.Contains(t, text, "# Snapshot policy")
	require.NotContains(t, text, "## Host:")
}

func TestMCP_OrientTool_Tier1WithKegIncludesManifestPlaceholder(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	text := orientCall(t, session, ctx, map[string]any{
		"tier": 1,
		"keg":  "notes",
	})

	require.Contains(t, text, "Active keg: `notes`")
	require.Contains(t, text, "Entity-kind manifest for `notes`")
	require.Contains(t, text, "not yet populated")
}

func TestMCP_OrientTool_Tier2ClaudeIncludesSKILLBytes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	text := orientCall(t, session, ctx, map[string]any{
		"tier": 2,
		"host": "claude",
	})

	// The rendered SKILL.md is appended under a "## Host: claude"
	// heading at tier 2. Every byte of the SKILL.md body must appear
	// in the payload.
	wantBytes, err := fs.ReadFile(integrations.IntegrationsFS, "rendered/claude/skills/tapper/SKILL.md")
	require.NoError(t, err)
	want := strings.TrimRight(string(wantBytes), "\n")
	require.Contains(t, text, "## Host: claude")
	require.Contains(t, text, want)
}

func TestMCP_OrientTool_UnknownHostReturnsError(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "orient",
		Arguments: map[string]any{
			"tier": 2,
			"host": "not-a-host",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "unknown host should surface as a tool error")
	require.Contains(t, extractText(t, res), "unknown host")
}

func TestMCP_OrientTool_TierClampsToMax(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Tier 99 should clamp to 2 and return the tier-2 payload. The
	// heading reflects the clamped value.
	text := orientCall(t, session, ctx, map[string]any{"tier": 99})
	require.Contains(t, text, "tier 2")
}

func TestMCP_Resources_ListPerHostTier(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListResources(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Resources, "expected at least one orient resource")

	uris := make(map[string]struct{}, len(res.Resources))
	for _, r := range res.Resources {
		uris[r.URI] = struct{}{}
	}

	// Every registered adapter with an orient surface should expose one
	// resource per tier. Claude is always registered in test; Codex
	// arrives later and will expand this set without a code change.
	require.Contains(t, uris, "tapper://orient/claude/tier-0")
	require.Contains(t, uris, "tapper://orient/claude/tier-1")
	require.Contains(t, uris, "tapper://orient/claude/tier-2")
}

func TestMCP_Resources_ReadMatchesToolBytes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// resources/read at (claude, tier 2) must equal the orient tool's
	// output at the same (host, tier) — byte-for-byte. That equivalence
	// is what lets hosts treat Resources as a cache-friendly mirror of
	// the tool surface.
	toolText := orientCall(t, session, ctx, map[string]any{
		"tier": 2,
		"host": "claude",
	})

	readRes, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "tapper://orient/claude/tier-2",
	})
	require.NoError(t, err)
	require.Len(t, readRes.Contents, 1)
	require.Equal(t, "tapper://orient/claude/tier-2", readRes.Contents[0].URI)
	require.Equal(t, "text/markdown", readRes.Contents[0].MIMEType)
	require.Equal(t, toolText, readRes.Contents[0].Text)
}

func TestMCP_Resources_ReadTier0HasNoHostBytes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	readRes, err := session.ReadResource(ctx, &sdkmcp.ReadResourceParams{
		URI: "tapper://orient/claude/tier-0",
	})
	require.NoError(t, err)
	require.Len(t, readRes.Contents, 1)

	text := readRes.Contents[0].Text
	require.Contains(t, text, "tier 0")
	require.NotContains(t, text, "## Host:")
}
