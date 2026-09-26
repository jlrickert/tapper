package mcp_test

import (
	"context"
	"testing"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// fakeAgents is an in-memory AgentProvider keyed by ref.
type fakeAgents struct{ byRef map[string]tapper.HubAgent }

func (f *fakeAgents) ListAgents(_ context.Context, namespace string) ([]tapper.HubAgent, error) {
	var out []tapper.HubAgent
	for _, a := range f.byRef {
		if namespace == "" || a.Namespace == namespace {
			out = append(out, a)
		}
	}
	return out, nil
}
func (f *fakeAgents) GetAgent(_ context.Context, ref string) (*tapper.HubAgent, error) {
	a, ok := f.byRef[ref]
	if !ok {
		return nil, keg.ErrNotExist
	}
	return &a, nil
}
func (f *fakeAgents) CreateAgent(_ context.Context, opts tapper.CreateAgentOptions) (*tapper.HubAgent, error) {
	ns, name, err := tapper.ParseHubAgentRef(opts.Ref)
	if err != nil {
		return nil, err
	}
	a := tapper.HubAgent{Ref: opts.Ref, Namespace: ns, Name: name, Title: opts.Title, Model: opts.Model, Tools: opts.Tools, Instructions: opts.Instructions}
	f.byRef[opts.Ref] = a
	return &a, nil
}
func (f *fakeAgents) EditAgent(_ context.Context, opts tapper.EditAgentOptions) (*tapper.HubAgent, error) {
	a := f.byRef[opts.Ref]
	if opts.Title != nil {
		a.Title = *opts.Title
	}
	if opts.Tools != nil {
		a.Tools = *opts.Tools
	}
	f.byRef[opts.Ref] = a
	return &a, nil
}
func (f *fakeAgents) DeleteAgent(_ context.Context, ref string) error {
	delete(f.byRef, ref)
	return nil
}

type fakeNamespaces struct{ lastQuery string }

func (f *fakeNamespaces) ListNamespaces(context.Context) ([]tapper.HubNamespace, error) {
	return []tapper.HubNamespace{{Name: "me", Kind: "user", Role: "owner"}}, nil
}
func (f *fakeNamespaces) SearchNamespaces(_ context.Context, query string) (tapper.NamespaceSearchResult, error) {
	f.lastQuery = query
	return tapper.NamespaceSearchResult{Namespaces: []tapper.NamespaceMatch{{Name: "acme", Kind: "org", DisplayName: "Acme"}}}, nil
}

func callTool(t *testing.T, ctx context.Context, session *sdkmcp.ClientSession, name string, args map[string]any) *sdkmcp.CallToolResult {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	require.NoError(t, err)
	return res
}

// The agent tools read and change agents through the provider, and agent_edit
// keeps the fields it is not given.
func TestAgentTools_CRUD(t *testing.T) {
	agents := &fakeAgents{byRef: map[string]tapper.HubAgent{}}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{AgentProvider: agents, NamespaceProvider: &fakeNamespaces{}})

	res := callTool(t, ctx, session, "agent_create", map[string]any{
		"agent": "@me/writer", "title": "Writer", "model": "relay/ollama/qwen3", "tools": []string{"keg:read", "node_edit"},
	})
	require.False(t, res.IsError, "%v", res.Content)
	created := wirePayload(t, res, "structured")
	require.Equal(t, "@me/writer", created["ref"])
	require.Equal(t, []any{"keg:read", "node_edit"}, created["tools"])

	res = callTool(t, ctx, session, "agent_edit", map[string]any{"agent": "@me/writer", "title": "Scribe"})
	require.False(t, res.IsError)
	require.Equal(t, "Scribe", agents.byRef["@me/writer"].Title)
	require.Equal(t, []string{"keg:read", "node_edit"}, agents.byRef["@me/writer"].Tools, "omitted tools are kept")

	res = callTool(t, ctx, session, "agent_list", map[string]any{"namespace": "me"})
	require.False(t, res.IsError)
	listed := wirePayload(t, res, "structured")["agents"].([]any)
	require.Len(t, listed, 1)

	res = callTool(t, ctx, session, "agent_read", map[string]any{"agent": "@me/writer"})
	require.False(t, res.IsError)
	require.Equal(t, "relay/ollama/qwen3", wirePayload(t, res, "structured")["model"])

	res = callTool(t, ctx, session, "agent_delete", map[string]any{"agent": "@me/writer"})
	require.False(t, res.IsError)
	require.Empty(t, agents.byRef)
}

func TestNamespaceTools_ListAndSearch(t *testing.T) {
	namespaces := &fakeNamespaces{}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{AgentProvider: &fakeAgents{byRef: map[string]tapper.HubAgent{}}, NamespaceProvider: namespaces})

	res := callTool(t, ctx, session, "namespace_search", map[string]any{"query": "ac"})
	require.False(t, res.IsError)
	require.Equal(t, "ac", namespaces.lastQuery)
	found := wirePayload(t, res, "structured")["namespaces"].([]any)
	require.Equal(t, "acme", found[0].(map[string]any)["name"])

	res = callTool(t, ctx, session, "namespace_list", nil)
	require.False(t, res.IsError)
	require.Equal(t, "me", wirePayload(t, res, "structured")["namespaces"].([]any)[0].(map[string]any)["name"])
}
