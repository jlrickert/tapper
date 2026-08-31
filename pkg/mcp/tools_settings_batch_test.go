package mcp_test

import (
	"context"
	"fmt"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func callKegSettings(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings",
		Arguments: args,
	})
	require.NoError(t, err)
	return res
}

func TestMCP_KegSettingsBatchValidationAndMinimalOutput(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	ok := callKegSettings(t, ctx, session, map[string]any{
		"kegs": []string{"@local/personal"},
	})
	require.False(t, ok.IsError, extractText(t, ok))
	require.Contains(t, extractText(t, ok), "keg: '@local/personal'")
	require.Contains(t, extractText(t, ok), "title: Personal KEG")

	overLimit := make([]string, 101)
	for i := range overLimit {
		overLimit[i] = fmt.Sprintf("@local/keg%d", i)
	}
	cases := []map[string]any{
		{"keg": "@local/personal", "kegs": []string{"@local/personal"}},
		{"kegs": []string{}},
		{"kegs": overLimit},
		{"kegs": []string{"personal"}},
		{"kegs": []string{"@local/personal", "@local/personal"}},
		{"kegs": []string{"@local/personal", "@local/private"}, "minimal": false},
	}
	for _, args := range cases {
		res := callKegSettings(t, ctx, session, args)
		require.Truef(t, res.IsError, "args=%v text=%s", args, extractText(t, res))
	}
}

func TestMCP_KegSettingsMinimalIncludesInstructions(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	expectedHash := readSettingsHash(t, session, ctx, "@local/personal")

	edit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "keg_settings_edit",
		Arguments: map[string]any{
			"keg":           "@local/personal",
			"expected_hash": expectedHash,
			"data":          "kegv: 2025-07\ntitle: Personal KEG\nsummary: Discovery\ninstructions: |\n  Targeted guidance.\n",
		},
	})
	require.NoError(t, err)
	require.False(t, edit.IsError, extractText(t, edit))
	callOrient(t, ctx, session)

	single := callKegSettings(t, ctx, session, map[string]any{"keg": "@local/personal"})
	require.False(t, single.IsError, extractText(t, single))
	require.Contains(t, extractText(t, single), "instructions:")
	require.Contains(t, extractText(t, single), "Targeted guidance.")

	batch := callKegSettings(t, ctx, session, map[string]any{"kegs": []string{"@local/personal"}})
	require.False(t, batch.IsError, extractText(t, batch))
	require.Contains(t, extractText(t, batch), "instructions:")
	require.Contains(t, extractText(t, batch), "Targeted guidance.")

	full := callKegSettings(t, ctx, session, map[string]any{
		"kegs":    []string{"@local/personal"},
		"minimal": false,
	})
	require.False(t, full.IsError, extractText(t, full))
	require.Contains(t, extractText(t, full), "kegv:")
	require.Contains(t, extractText(t, full), "title: Personal KEG")
}
