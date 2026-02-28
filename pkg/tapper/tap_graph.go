package tapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// GraphOptions configures graph HTML generation for a resolved keg.
type GraphOptions struct {
	KegTargetOptions

	// BundleJS is the compiled browser renderer injected into the generated page.
	BundleJS []byte
}

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

const graphFallbackBundle = `(() => {
  const app = document.getElementById("app");
  if (!app) return;
  app.innerHTML = "<pre>Graph bundle is missing. Rebuild assets.</pre>";
})();`

// Graph renders a self-contained HTML page for the resolved keg graph.
func (t *Tap) Graph(ctx context.Context, opts GraphOptions) (string, error) {
	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}
	dex, err := k.Dex(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to read dex: %w", err)
	}

	payload := buildGraphPayload(ctx, t.Runtime, k, dex)
	bundle := opts.BundleJS
	if len(strings.TrimSpace(string(bundle))) == 0 {
		bundle = []byte(graphFallbackBundle)
	}

	out, err := renderGraphHTML(payload, bundle)
	if err != nil {
		return "", err
	}
	return out, nil
}

func buildGraphPayload(ctx context.Context, rt *toolkit.Runtime, k *keg.Keg, dex *keg.Dex) graphPayload {
	payload := graphPayload{
		Nodes: []graphNode{},
		Edges: []graphEdge{},
	}
	if dex == nil {
		return payload
	}

	tagsByNode := graphTagsByNode(ctx, dex)
	nodeByID := map[string]graphNode{}

	entries := dex.Nodes(ctx)
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}

		label := strings.TrimSpace(entry.Title)
		if label == "" {
			label = id
		}

		node := graphNode{
			ID:      id,
			Label:   label,
			Summary: "",
			Tags:    tagsByNode[id],
			URL:     "",
		}
		if parsed, err := keg.ParseNode(id); err == nil && parsed != nil {
			node.Summary = readNodeSummary(ctx, rt, k.Repo, *parsed)
		}
		nodeByID[id] = node
	}

	edgeSeen := map[string]struct{}{}
	for _, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		if id == "" {
			continue
		}
		src, err := keg.ParseNode(id)
		if err != nil || src == nil {
			continue
		}

		if links, ok := dex.Links(ctx, *src); ok {
			for _, dst := range links {
				addEdgeAndNode(&payload, edgeSeen, nodeByID, graphEdge{
					Source: src.Path(),
					Target: dst.Path(),
					Type:   "link",
				})
			}
		}

		if backlinks, ok := dex.Backlinks(ctx, *src); ok {
			for _, source := range backlinks {
				addEdgeAndNode(&payload, edgeSeen, nodeByID, graphEdge{
					Source: src.Path(),
					Target: source.Path(),
					Type:   "backlink",
				})
			}
		}
	}

	payload.Nodes = make([]graphNode, 0, len(nodeByID))
	for _, node := range nodeByID {
		payload.Nodes = append(payload.Nodes, node)
	}
	sortGraphNodes(payload.Nodes)
	sortGraphEdges(payload.Edges)
	return payload
}

func addEdgeAndNode(payload *graphPayload, seen map[string]struct{}, nodeByID map[string]graphNode, edge graphEdge) {
	if payload == nil {
		return
	}
	if edge.Source == "" || edge.Target == "" || edge.Type == "" {
		return
	}
	key := edge.Source + "\x00" + edge.Target + "\x00" + edge.Type
	if _, ok := seen[key]; !ok {
		seen[key] = struct{}{}
		payload.Edges = append(payload.Edges, edge)
	}
	if _, ok := nodeByID[edge.Source]; !ok {
		nodeByID[edge.Source] = graphNode{
			ID:      edge.Source,
			Label:   edge.Source,
			Summary: "",
			Tags:    nil,
			URL:     "",
		}
	}
	if _, ok := nodeByID[edge.Target]; !ok {
		nodeByID[edge.Target] = graphNode{
			ID:      edge.Target,
			Label:   edge.Target,
			Summary: "",
			Tags:    nil,
			URL:     "",
		}
	}
}

func readNodeSummary(ctx context.Context, rt *toolkit.Runtime, repo keg.Repository, id keg.NodeId) string {
	if repo == nil || rt == nil {
		return ""
	}
	if stats, err := repo.ReadStats(ctx, id); err == nil {
		if lead := compactWhitespace(stats.Lead()); lead != "" {
			return lead
		}
	} else if !errors.Is(err, keg.ErrNotExist) {
		return ""
	}

	raw, err := repo.ReadContent(ctx, id)
	if err != nil {
		return ""
	}
	content, err := keg.ParseContent(rt, raw, keg.FormatMarkdown)
	if err != nil || content == nil {
		return ""
	}
	return compactWhitespace(content.Lead)
}

func graphTagsByNode(ctx context.Context, dex *keg.Dex) map[string][]string {
	out := map[string][]string{}
	if dex == nil {
		return out
	}

	tags := dex.TagList(ctx)
	sort.Strings(tags)
	for _, tag := range tags {
		nodes, ok := dex.TagNodes(ctx, tag)
		if !ok {
			continue
		}
		for _, node := range nodes {
			key := node.Path()
			if key == "" {
				continue
			}
			out[key] = append(out[key], tag)
		}
	}

	for id, tags := range out {
		if len(tags) <= 1 {
			continue
		}
		sort.Strings(tags)
		dedup := tags[:0]
		for _, tag := range tags {
			if len(dedup) == 0 || dedup[len(dedup)-1] != tag {
				dedup = append(dedup, tag)
			}
		}
		out[id] = dedup
	}

	return out
}

func compactWhitespace(raw string) string {
	parts := strings.Fields(strings.TrimSpace(raw))
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func sortGraphNodes(nodes []graphNode) {
	sort.SliceStable(nodes, func(i, j int) bool {
		return compareNodePath(nodes[i].ID, nodes[j].ID) < 0
	})
}

func sortGraphEdges(edges []graphEdge) {
	sort.SliceStable(edges, func(i, j int) bool {
		if cmp := compareNodePath(edges[i].Source, edges[j].Source); cmp != 0 {
			return cmp < 0
		}
		if cmp := compareNodePath(edges[i].Target, edges[j].Target); cmp != 0 {
			return cmp < 0
		}
		return edges[i].Type < edges[j].Type
	})
}

func compareNodePath(a, b string) int {
	na, ea := keg.ParseNode(a)
	nb, eb := keg.ParseNode(b)

	switch {
	case ea == nil && na != nil && eb == nil && nb != nil:
		return na.Compare(*nb)
	case ea == nil && na != nil:
		return -1
	case eb == nil && nb != nil:
		return 1
	}

	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func renderGraphHTML(payload graphPayload, bundle []byte) (string, error) {
	graphJSON, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("unable to marshal graph payload: %w", err)
	}

	escapedBundle := strings.ReplaceAll(string(bundle), "</script>", "<\\/script>")
	escapedBundle = strings.ReplaceAll(escapedBundle, "</SCRIPT>", "<\\/SCRIPT>")

	out := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>KEG Graph</title>
  <style>
    :root {
      color-scheme: light;
      font-family: "Iosevka Aile", "SF Mono", Menlo, Monaco, Consolas, monospace;
    }
    html, body {
      margin: 0;
      padding: 0;
      width: 100%%;
      height: 100%%;
      overflow: hidden;
      background: radial-gradient(circle at top left, #f7fbff 0%%, #eef4ff 38%%, #f8f6f1 100%%);
      color: #1f2937;
    }
    #app {
      width: 100%%;
      height: 100%%;
    }
    #panel {
      position: fixed;
      right: 16px;
      top: 16px;
      width: min(420px, calc(100vw - 32px));
      max-height: calc(100vh - 32px);
      overflow: auto;
      box-sizing: border-box;
      padding: 14px 16px;
      border-radius: 12px;
      border: 1px solid #d0d7e2;
      background: rgba(255, 255, 255, 0.95);
      box-shadow: 0 8px 30px rgba(33, 55, 82, 0.12);
      backdrop-filter: blur(4px);
      line-height: 1.45;
    }
    #panel.hidden {
      display: none;
    }
    #panel h2 {
      margin: 0 0 8px 0;
      font-size: 1rem;
      letter-spacing: 0.01em;
    }
    #panel p {
      margin: 0 0 10px 0;
      white-space: pre-wrap;
    }
    #panel .meta {
      font-size: 0.84rem;
      color: #4b5563;
    }
    #panel code {
      font-size: 0.83rem;
      background: #eef2f8;
      padding: 2px 6px;
      border-radius: 6px;
    }
  </style>
</head>
<body>
  <div id="app"></div>
  <aside id="panel" class="hidden"></aside>
  <script>
    window.__KEG__ = %s;
  </script>
  <script>
%s
  </script>
</body>
</html>`, string(graphJSON), escapedBundle)

	return out, nil
}
