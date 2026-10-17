package mcp_test

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/mcp/mcptest"
	"github.com/jlrickert/tapper/pkg/tapapi"
)

// Every tool declares the shape of its structured result, and every result a
// test receives is checked against it: a client that validates results (as
// the MCP spec asks clients to) must never reject one of ours.

var (
	declaredOutputsOnce sync.Once
	declaredOutputs     map[string]*jsonschema.Resolved
	declaredOutputsErr  error
)

// fullToolListing lists every tool a server offers with all providers set,
// on a shared filesystem (where the local attachment variants register) or
// not.
func fullToolListing(t *testing.T, sharedFS bool) []*sdkmcp.Tool {
	t.Helper()
	session, ctx := newPlainTestSession(t, mcp.ServerOptions{
		AgentProvider:     &fakeAgents{},
		NamespaceProvider: &fakeNamespaces{},
		ExploreProvider:   &fakeExplore{},
		SharedFilesystem:  sharedFS,
	})
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, listed.Tools)
	return listed.Tools
}

// outputSchemas resolves each tool's declared outputSchema, by tool name.
// The shared-filesystem variants of a tool declare the same shape as the
// others, so one schema per name serves every server.
func outputSchemas(t *testing.T) map[string]*jsonschema.Resolved {
	t.Helper()
	declaredOutputsOnce.Do(func() {
		declaredOutputs = map[string]*jsonschema.Resolved{}
		for _, shared := range []bool{false, true} {
			for _, tool := range fullToolListing(t, shared) {
				if tool.OutputSchema == nil {
					continue
				}
				raw, err := json.Marshal(tool.OutputSchema)
				if err != nil {
					declaredOutputsErr = err
					return
				}
				var schema jsonschema.Schema
				if err := json.Unmarshal(raw, &schema); err != nil {
					declaredOutputsErr = err
					return
				}
				resolved, err := schema.Resolve(nil)
				if err != nil {
					declaredOutputsErr = err
					return
				}
				declaredOutputs[tool.Name] = resolved
			}
		}
	})
	require.NoError(t, declaredOutputsErr)
	return declaredOutputs
}

// validateOutput validates structured content as a client decodes it.
func validateOutput(schema *jsonschema.Resolved, structured any) error {
	raw, err := json.Marshal(structured)
	if err != nil {
		return err
	}
	var instance any
	if err := json.Unmarshal(raw, &instance); err != nil {
		return err
	}
	return schema.Validate(instance)
}

// newTestClient is the client every test session connects with: it checks
// each result, failed or not, as a strict client would.
func newTestClient(t *testing.T, name string, opts *sdkmcp.ClientOptions) *sdkmcp.Client {
	t.Helper()
	return mcptest.ValidatingClient(t, &sdkmcp.Implementation{Name: name, Version: "0.1"}, opts)
}

func TestMCP_EveryToolDeclaresAnOutputSchema(t *testing.T) {
	t.Parallel()
	for _, shared := range []bool{false, true} {
		for _, tool := range fullToolListing(t, shared) {
			require.NotNilf(t, tool.OutputSchema, "%s declares no outputSchema", tool.Name)
			raw, err := json.Marshal(tool.OutputSchema)
			require.NoError(t, err)
			var schema struct {
				Type       string                     `json:"type"`
				Properties map[string]json.RawMessage `json:"properties"`
				Additional json.RawMessage            `json:"additionalProperties"`
			}
			require.NoError(t, json.Unmarshal(raw, &schema))
			require.Equalf(t, "object", schema.Type, "%s: structured content is always an object", tool.Name)
			require.Containsf(t, schema.Properties, "message", "%s: every result carries its text as message", tool.Name)
			require.Emptyf(t, schema.Additional, "%s: a newer server's extra field must not fail an older client", tool.Name)
		}
	}
}

// outputFields lists an outputSchema's top-level properties.
func outputFields(t *testing.T, tool *sdkmcp.Tool) map[string]json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(tool.OutputSchema)
	require.NoError(t, err)
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &schema))
	return schema.Properties
}

// The read tools agents page through most declare the fields they page by,
// and what they actually return matches.
func TestMCP_ReadToolOutputsMatchTheirSchemas(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	byName := map[string]*sdkmcp.Tool{}
	for _, tool := range listed.Tools {
		byName[tool.Name] = tool
	}
	wantFields := map[string][]string{
		"node_list": {"lines", "offset", "limit", "next_offset", "has_more", "pagination"},
		"node_grep": {"lines", "next_offset", "has_more"},
		"node_read": {"nodes"},
	}
	for name, fields := range wantFields {
		props := outputFields(t, byName[name])
		for _, field := range fields {
			require.Containsf(t, props, field, "%s outputSchema", name)
		}
	}
	schemas := outputSchemas(t)
	for name, args := range map[string]map[string]any{
		"node_list": {"limit": 1},
		"node_grep": {"query": "Hello"},
		"node_read": {"node_ids": []string{"0", "1"}},
	} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		require.Falsef(t, res.IsError, "%s: %s", name, extractText(t, res))
		require.NoErrorf(t, validateOutput(schemas[name], res.StructuredContent), "%s result", name)
	}

	// The schema really does constrain: a result missing a required field
	// or carrying the wrong type fails it.
	require.Error(t, validateOutput(schemas["node_list"], map[string]any{"lines": "not a list"}))
	require.Error(t, validateOutput(schemas["node_read"], map[string]any{"nodes": []any{map[string]any{"node_id": "1"}}}))
}

func TestMCP_NodeSearchOutputMatchesItsSchema(t *testing.T) {
	t.Parallel()
	provider := &recordingNodeSearch{}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{NodeSearchProvider: provider})
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range listed.Tools {
		if tool.Name == "node_search" {
			props := outputFields(t, tool)
			for _, field := range []string{"hits", "next_cursor", "mode", "warnings", "pagination"} {
				require.Contains(t, props, field)
			}
		}
	}

	schema := outputSchemas(t)["node_search"]
	for _, resp := range []string{
		`{"hits":[{"ref":"keg:@acme/notes/12","keg":"@acme/notes","node_id":"12","title":"Festival","score":4.25,"fields":["title"],"sources":["keyword"],"snippet":"festival","highlights":[{"start":0,"end":8}]}],"next_cursor":"c2","terms":[{"term":"festival","lexemes":["festiv"],"typo":false}],"mode":"hybrid","warnings":["partial"]}`,
		`{"hits":[]}`,
	} {
		provider.resp = decodeSearchResponse(t, resp)
		res := callNodeSearch(t, session, ctx, map[string]any{"query": "festival"})
		require.False(t, res.IsError, extractText(t, res))
		require.NoError(t, validateOutput(schema, res.StructuredContent))
	}
}

func decodeSearchResponse(t *testing.T, raw string) *tapapi.SearchResponse {
	t.Helper()
	var resp tapapi.SearchResponse
	require.NoError(t, json.Unmarshal([]byte(raw), &resp))
	return &resp
}

// errorPayload decodes a failed result's public payload from its leading
// text block, the only place a failure carries it.
func errorPayload(t *testing.T, res *sdkmcp.CallToolResult) map[string]any {
	t.Helper()
	require.True(t, res.IsError, "want a failed result")
	require.NotEmpty(t, res.Content)
	text, ok := res.Content[0].(*sdkmcp.TextContent)
	require.Truef(t, ok, "first content block is %T, want text", res.Content[0])
	var payload map[string]any
	require.NoErrorf(t, json.Unmarshal([]byte(text.Text), &payload), "leading text is not a JSON object: %q", text.Text)
	return payload
}

// A tool's outputSchema describes its success. A failure that carried its
// error payload as structured content would fail that schema in a client
// that validates results, which then reports a JSON-RPC error instead of
// the tool's answer; so failures carry the payload as JSON text alone.
func TestMCP_ErrorResultsCarryNoStructuredContent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	call := func(name string, args map[string]any) *sdkmcp.CallToolResult {
		t.Helper()
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		return res
	}
	require.False(t, call("orient", nil).IsError)

	read := call("node_read", map[string]any{"node_ids": []string{"1"}})
	require.False(t, read.IsError, extractText(t, read))
	node := read.StructuredContent.(map[string]any)["nodes"].([]any)[0].(map[string]any)

	for _, tc := range []struct {
		name string
		tool string
		args map[string]any
		code string
	}{
		{"missing node", "node_read", map[string]any{"node_ids": []string{"999999"}}, "NOT_FOUND"},
		{"edit without hashes", "node_edit", map[string]any{"nodes": []any{map[string]any{"node_id": "1", "content": "# Changed"}}}, "PRECONDITION_REQUIRED"},
		{"stale hash", "node_edit", map[string]any{"nodes": []any{map[string]any{"node_id": "1", "expected_content_hash": "stale", "content": "# Changed"}}}, "CONFLICT"},
		{"invalid arguments", "node_list", map[string]any{"limit": "many"}, "INVALID_ARGUMENT"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := call(tc.tool, tc.args)
			require.True(t, res.IsError, "%s: want a failure", tc.tool)
			require.Nil(t, res.StructuredContent, "%s: a failure carries no structured content", tc.tool)
			payload := errorPayload(t, res)
			require.Equal(t, tc.code, payload["code"])
			require.NotEmpty(t, payload["message"])
			require.NotEmpty(t, payload["action"])
			require.Contains(t, payload, "operationPerformed")
		})
	}

	// Still current: the refused edits changed nothing.
	again := call("node_read", map[string]any{"node_ids": []string{"1"}})
	require.Equal(t, node["content_hash"], again.StructuredContent.(map[string]any)["nodes"].([]any)[0].(map[string]any)["content_hash"])
}

// The other side of the line: a success keeps its structured content, and its
// text is the same payload.
func TestMCP_SuccessResultsKeepStructuredContent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"orient", nil},
		{"node_read", map[string]any{"node_ids": []string{"1"}}},
		{"node_list", map[string]any{"limit": 1}},
	} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
		require.NoError(t, err)
		require.False(t, res.IsError, "%s: %s", tc.tool, extractText(t, res))
		require.NotNil(t, res.StructuredContent, tc.tool)
		structured, err := json.Marshal(res.StructuredContent)
		require.NoError(t, err)
		require.JSONEq(t, string(structured), res.Content[0].(*sdkmcp.TextContent).Text, tc.tool)
	}
}
