package mcp_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// Every tool tells the agent which job it does before anything else, and
// only the public directory reaches outside the caller's own memory.
func TestToolsDeclareTheirCategory(t *testing.T) {
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		AgentProvider:     &fakeAgents{},
		NamespaceProvider: &fakeNamespaces{},
		ExploreProvider:   &fakeExplore{},
	})
	listed, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, listed.Tools)

	for _, tool := range listed.Tools {
		category, ok := tapper.ToolCategoryOf(tool.Name)
		require.Truef(t, ok, "%s has no tool category", tool.Name)
		require.Truef(t, strings.HasPrefix(tool.Description, string(category)+": "),
			"%s description must open with %q: %s", tool.Name, string(category)+": ", tool.Description)

		openWorld := tool.Annotations != nil && tool.Annotations.OpenWorldHint != nil && *tool.Annotations.OpenWorldHint
		require.Equalf(t, category == tapper.ToolCategoryExplore, openWorld,
			"%s: only Explore tools reach outside the caller's memory", tool.Name)
		if strings.HasSuffix(tool.Name, "_search") {
			require.Equal(t, "node_search", tool.Name, "searching is for note content and the public directory; private resources are listed")
		}
	}
}
