package mcp_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"testing"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

// These clients deliberately consume one wire representation only. Unlike
// legacy behavior helpers, text decoding never consults StructuredContent.
func wirePayload(t *testing.T, result *sdkmcp.CallToolResult, mode string) map[string]any {
	t.Helper()
	var raw []byte
	if mode == "text" {
		for _, block := range result.Content {
			if text, ok := block.(*sdkmcp.TextContent); ok {
				raw = append(raw, text.Text...)
			}
		}
	} else {
		var err error
		raw, err = json.Marshal(result.StructuredContent)
		require.NoError(t, err)
	}
	var out map[string]any
	require.NoError(t, json.Unmarshal(raw, &out))
	require.NotNil(t, out)
	return out
}

func TestPublicContractIndependentNodeWorkflow(t *testing.T) {
	for _, mode := range []string{"text", "structured"} {
		t.Run(mode, func(t *testing.T) {
			session, ctx := newTestSession(t)
			call := func(name string, args map[string]any, fail bool) map[string]any {
				result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
				require.NoError(t, err)
				require.Equal(t, fail, result.IsError, "%s: %#v", name, result)
				return wirePayload(t, result, mode)
			}
			call("orient", nil, false)
			settings := call("keg_settings", map[string]any{"minimal": false}, false)
			require.NotEmpty(t, settings["hash"])
			require.Contains(t, settings["data"], "title:")
			created := call("create", map[string]any{"nodes": []any{map[string]any{"key": "subject", "content": "# Wire subject\n\nBefore.", "meta": "tags: [wire-contract]"}}}, false)
			row := created["results"].([]any)[0].(map[string]any)
			id := row["node_id"].(string)
			call("node_snapshot", map[string]any{"nodes": []any{map[string]any{"node_id": id}}}, false)
			read := func() map[string]any {
				return call("cat", map[string]any{"node_ids": []string{id}}, false)["nodes"].([]any)[0].(map[string]any)
			}
			before := read()
			invalid := call("edit", map[string]any{"nodes": []any{map[string]any{"node_id": id, "content": "# Invalid"}}}, true)
			require.Contains(t, invalid["message"], "expected_hash")
			require.NotEmpty(t, invalid["action"])
			require.Equal(t, false, invalid["operationPerformed"])
			args := map[string]any{"nodes": []any{map[string]any{"node_id": id, "expected_hash": before["hash"], "content": "# Wire subject\n\nAfter."}}}
			edited := call("edit", args, false)
			after := read()
			require.Contains(t, after["content"], "After.")
			require.Equal(t, before["meta"], after["meta"])
			require.Equal(t, after["hash"], edited["results"].([]any)[0].(map[string]any)["hash"])
			conflict := call("edit", args, true)
			require.Equal(t, "CONFLICT", conflict["code"])
			require.Equal(t, false, conflict["operationPerformed"])
			require.Equal(t, after["hash"], conflict["currentHash"])
			require.NotEmpty(t, conflict["action"])
			require.Equal(t, after["content"], read()["content"])
			selected := call("cat", map[string]any{"query": "wire-contract", "meta_only": true}, false)
			require.Len(t, selected["nodes"], 1)
			seen := []string{}
			offset := any(0)
			for page := 0; page < 10; page++ {
				result := call("list", map[string]any{"query": "wire-contract", "id_only": true, "limit": 1, "offset": offset}, false)
				for _, line := range result["lines"].([]any) {
					seen = append(seen, line.(string))
				}
				offset = result["next_offset"]
				if offset == nil {
					break
				}
				require.Less(t, page, 9, "pagination must terminate")
			}
			require.Equal(t, []string{id}, seen)
			call("remove", map[string]any{"nodes": []any{map[string]any{"node_id": id, "expected_hash": read()["hash"]}}}, false)
		})
	}
}

func TestPublicContractFlightManifest(t *testing.T) {
	backend := newPerCallFlightBackend()
	for _, populated := range []bool{false, true} {
		t.Run(fmt.Sprint(populated), func(t *testing.T) {
			f := &tapper.Flight{Name: "@team/+inspect", Source: "test", ManifestHash: "read-precondition", FlightManifest: tapper.FlightManifest{Visibility: "private"}}
			if populated {
				f.Title = "Title"
				f.Description = "Description"
				f.Instructions = "Complete\nInstructions"
				f.Capabilities = []tapper.FlightCapability{tapper.FlightCapability("manage_flights")}
				f.Cover = []tapper.FlightCover{{Namespace: "team", Keg: "notes", Role: tapper.FlightRoleViewer, Depth: 3}}
				f.Subflights = []string{"@team/+child"}
			}
			backend.flights[f.Name] = f
			session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{FlightProvider: backend})
			result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "flight_show", Arguments: map[string]any{"name": f.Name}})
			require.NoError(t, err)
			require.False(t, result.IsError)
			text := wirePayload(t, result, "text")
			structured := wirePayload(t, result, "structured")
			require.Equal(t, text, structured)
			for _, mode := range []string{"text", "structured"} {
				out := wirePayload(t, result, mode)
				for _, key := range []string{"name", "hash", "title", "description", "source", "visibility", "capabilities", "cover", "subflights", "instructions"} {
					require.Contains(t, out, key)
					require.NotNil(t, out[key])
				}
				require.Equal(t, f.ManifestHash, out["hash"])
				require.Equal(t, f.Instructions, out["instructions"])
				require.NotContains(t, out, "effective_cover")
				require.NotContains(t, out, "allowedKegs")
				if populated {
					cover := out["cover"].([]any)[0].(map[string]any)
					require.Equal(t, "viewer", cover["role"])
					require.Equal(t, float64(3), cover["depth"])
				} else {
					require.Empty(t, out["cover"])
					require.Empty(t, out["subflights"])
					require.Empty(t, out["capabilities"])
				}
			}
		})
	}
}

func TestPublicContractGuideInventory(t *testing.T) {
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{SharedFilesystem: true})
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	inventory := map[string]bool{}
	for _, tool := range listed.Tools {
		inventory[tool.Name] = true
		require.NotEmpty(t, tool.Description)
		require.NotNil(t, tool.InputSchema)
	}
	referenced := map[string]bool{}
	for _, topic := range []string{"operating", "authoring", "linking", "snapshots", "tools", "troubleshooting"} {
		result, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "guide", Arguments: map[string]any{"topic": topic}})
		require.NoError(t, err)
		require.False(t, result.IsError)
		text := wirePayload(t, result, "text")
		require.Equal(t, text, wirePayload(t, result, "structured"))
		message := text["message"].(string)
		require.NotEmpty(t, message)
		for _, match := range regexp.MustCompile(`mcp__tapper__([a-z_]+)`).FindAllStringSubmatch(message, -1) {
			require.True(t, inventory[match[1]], "guide %s references unavailable %s", topic, match[1])
			referenced[match[1]] = true
		}
		if topic == "tools" {
			require.NotContains(t, message, "or `full_access`")
			require.Contains(t, message, "full_access\nis rejected")
		}
	}
	for name := range inventory {
		require.True(t, referenced[name], "registered tool %s missing from guide", name)
	}
	invalid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "guide", Arguments: map[string]any{"topic": "missing"}})
	require.NoError(t, err)
	require.True(t, invalid.IsError)
	require.Contains(t, wirePayload(t, invalid, "structured")["message"], "operating")
}

func TestPublicContractSearchTruncation(t *testing.T) {
	rows := []tapper.OrientationKeg{}
	for i := 0; i < 51; i++ {
		rows = append(rows, tapper.OrientationKeg{Ref: fmt.Sprintf("@team/notes%d", i), Title: "Match"})
	}
	out := mcp.SearchIdentityKegsResult(rows, "Match")
	require.Len(t, out.Kegs, 50)
	require.True(t, out.Truncated)
	require.False(t, out.Partial)
	require.False(t, mcp.SearchIdentityKegsResult(rows[:50], "Match").Truncated)
}
