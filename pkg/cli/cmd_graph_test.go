package cli_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type graphPayload struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
}

type graphNode struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
	URL     string   `json:"url"`
}

type graphEdge struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Type   string `json:"type"`
}

func TestGraphCommand_NoConfiguredKegErrors(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	res := NewProcess(t, false, "graph").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "no keg configured")
}

func TestGraphCommand_OutputsSelfContainedHTML(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	createOne := `---
tags:
  - alpha
---
# Alpha Node

Alpha lead paragraph.
`
	res := NewProcess(t, true, "create", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(createOne))
	require.NoError(t, res.Err)
	idOne := strings.TrimSpace(string(res.Stdout))
	require.NotEmpty(t, idOne)

	createTwo := fmt.Sprintf(`---
tags:
  - beta
---
# Beta Node

Beta lead paragraph with [alpha](../%s).
`, idOne)
	res = NewProcess(t, true, "create", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(createTwo))
	require.NoError(t, res.Err)
	idTwo := strings.TrimSpace(string(res.Stdout))
	require.NotEmpty(t, idTwo)

	res = NewProcess(t, false, "reindex", "--alias", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	res = NewProcess(t, false, "graph", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	html := string(res.Stdout)
	require.Contains(t, html, "<!DOCTYPE html>")
	require.Contains(t, html, "window.__KEG__ = ")

	payload := mustGraphPayload(t, html)
	require.NotEmpty(t, payload.Nodes)
	require.NotEmpty(t, payload.Edges)

	require.True(t, containsGraphNode(payload.Nodes, graphNode{
		ID:      idOne,
		Label:   "Alpha Node",
		Summary: "Alpha lead paragraph.",
	}), "expected node 1 to include title and lead paragraph")
	require.True(t, containsGraphEdge(payload.Edges, graphEdge{
		Source: idTwo,
		Target: idOne,
		Type:   "link",
	}), "expected forward link edge 2 -> 1")
	require.True(t, containsGraphEdge(payload.Edges, graphEdge{
		Source: idOne,
		Target: idTwo,
		Type:   "backlink",
	}), "expected backlink edge 1 -> 2")
}

func TestGraphCommand_WritesOutputFile(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	res := NewProcess(t, false, "graph", "--keg", "personal", "--output", "~/graph.html").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "graph written to")

	raw := sb.MustReadFile("~/graph.html")
	out := string(raw)
	require.Contains(t, out, "<!DOCTYPE html>")
	require.Contains(t, out, "window.__KEG__ = ")
}

func TestKegV2GraphCommand_WorksOnProjectKeg(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	sb.Setwd("~")

	initRes := NewProcess(t, false,
		"repo", "init", ".", "--project", "--cwd", "--keg", "project", "--creator", "test-user",
	).Run(sb.Context(), sb.Runtime())
	require.NoError(t, initRes.Err)

	res := NewKegV2Process(t, false, "graph").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Contains(t, string(res.Stdout), "<!DOCTYPE html>")
	require.Contains(t, string(res.Stdout), "window.__KEG__ = ")
}

func mustGraphPayload(t *testing.T, html string) graphPayload {
	t.Helper()

	re := regexp.MustCompile(`(?s)window\.__KEG__\s*=\s*(\{.*\});\s*</script>`)
	matches := re.FindStringSubmatch(html)
	require.Len(t, matches, 2, "expected embedded graph JSON in HTML")

	var payload graphPayload
	require.NoError(t, json.Unmarshal([]byte(matches[1]), &payload))
	return payload
}

func containsGraphNode(nodes []graphNode, want graphNode) bool {
	for _, node := range nodes {
		if node.ID != want.ID {
			continue
		}
		if node.Label != want.Label {
			continue
		}
		if node.Summary != want.Summary {
			continue
		}
		return true
	}
	return false
}

func containsGraphEdge(edges []graphEdge, want graphEdge) bool {
	for _, edge := range edges {
		if edge.Source == want.Source && edge.Target == want.Target && edge.Type == want.Type {
			return true
		}
	}
	return false
}
