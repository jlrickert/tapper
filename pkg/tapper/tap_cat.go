package tapper

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type CatOptions struct {
	// NodeIDs are the node identifiers to read (e.g., "0", "42").
	// Multiple IDs produce concatenated output separated by blank lines.
	NodeIDs []string

	// Query is an optional boolean expression (same syntax as tap tags) used
	// to select nodes. Mutually exclusive with NodeIDs.
	Query string

	KegTargetOptions

	// Edit opens the node in the editor instead of printing output.
	Edit bool

	// ContentOnly displays content only.
	ContentOnly bool

	// StatsOnly displays stats only.
	StatsOnly bool

	// MetaOnly displays metadata only.
	MetaOnly bool

	// Stream carries stdin piping information when editing.
	Stream *toolkit.Stream

	// LockToken is an optional cross-process lock token for edit operations.
	LockToken string
}

func (t *Tap) Cat(ctx context.Context, opts CatOptions) (string, error) {
	outputModes := 0
	if opts.Edit {
		outputModes++
	}
	if opts.ContentOnly {
		outputModes++
	}
	if opts.StatsOnly {
		outputModes++
	}
	if opts.MetaOnly {
		outputModes++
	}
	if outputModes > 1 {
		return "", fmt.Errorf("only one output mode may be selected: --edit, --content-only, --stats-only, --meta-only")
	}

	nodeIDs := opts.NodeIDs
	if opts.Query != "" {
		if len(nodeIDs) > 0 {
			return "", fmt.Errorf("cannot specify both node IDs and --query")
		}
	}

	if len(nodeIDs) == 0 && opts.Query == "" {
		return "", nil
	}

	if opts.Edit {
		if opts.Query != "" {
			return "", fmt.Errorf("--edit requires an explicit node ID")
		}
		if len(nodeIDs) > 1 {
			return "", fmt.Errorf("--edit can only be used with a single node")
		}
		return "", t.Edit(ctx, EditOptions{
			NodeID:           nodeIDs[0],
			KegTargetOptions: opts.KegTargetOptions,
			Stream:           opts.Stream,
			LockToken:        opts.LockToken,
		})
	}

	// Interactive TTY with a single node and no output-mode flags: delegate
	// to the edit flow so the user gets the full editor experience (live
	// sync, frontmatter editing) instead of a dump to stdout. The check
	// uses IsTTY (stdout is a terminal) as the primary signal; IsPiped
	// (stdin) is not checked because cat does not consume stdin.
	if len(nodeIDs) == 1 &&
		!opts.ContentOnly && !opts.StatsOnly && !opts.MetaOnly &&
		opts.Stream != nil && opts.Stream.IsTTY {
		return "", t.Edit(ctx, EditOptions{
			NodeID:           nodeIDs[0],
			KegTargetOptions: opts.KegTargetOptions,
			Stream:           opts.Stream,
			LockToken:        opts.LockToken,
		})
	}

	views, err := t.CatViews(ctx, opts)
	if err != nil {
		return "", err
	}
	return FormatCatViews(ctx, views, opts), nil
}

// FormatCatViews renders views the way Cat does. It is exported so a caller
// that already holds the views — one that needed CatViews for the per-node
// hashes — can produce the same text without reading the nodes a second time.
// Re-reading would also double every access touch.
func FormatCatViews(ctx context.Context, views []keg.NodeView, opts CatOptions) string {
	if len(views) == 0 {
		return ""
	}
	if len(views) == 1 {
		return strings.TrimRight(formatCatView(ctx, views[0], opts, false), "\n") + "\n"
	}

	// Multiple nodes: emit a YAML document stream where every document is
	// self-identifying via an injected "id:" field. The leading "---" of each
	// document serves as the visual separator; documents are joined with a
	// single blank line.
	//
	//   default        ---\nid: "N"\n<meta>\n---\n<content>
	//   --meta-only    ---\nid: "N"\n<meta yaml>
	//   --stats-only   ---\nid: "N"\n<stats yaml>
	//   --content-only ---\nid: "N"\n---\n<content>
	var buf strings.Builder
	for i, view := range views {
		if i > 0 {
			buf.WriteString("\n")
		}
		out := formatCatView(ctx, view, opts, true)
		buf.WriteString(strings.TrimRight(out, "\n"))
		buf.WriteString("\n")
	}
	return buf.String()
}

// CatViews returns the node views Cat renders, in caller order. It exists so
// callers needing structured per-node state — an agent reading the content
// hash it must echo back on its next write — do not have to parse Cat's
// formatted output. The editor and TTY delegation in Cat is deliberately
// absent: those paths are interactive and produce no views.
func (t *Tap) CatViews(ctx context.Context, opts CatOptions) ([]keg.NodeView, error) {
	nodeIDs := opts.NodeIDs
	if opts.Query != "" && len(nodeIDs) > 0 {
		return nil, fmt.Errorf("cannot specify both node IDs and --query")
	}
	if len(nodeIDs) == 0 && opts.Query == "" {
		return nil, nil
	}

	base, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	if opts.Query != "" {
		views, queryErr := base.ReadNodes(ctx, keg.ReadNodesOptions{Query: opts.Query, Touch: true})
		if queryErr != nil {
			return nil, fmt.Errorf("unable to query nodes: %w", queryErr)
		}
		return views, nil
	}

	// Group by resolved keg so a cross-keg id list still costs one read per
	// keg, then scatter each batch back to its caller-order position.
	type group struct {
		k         keg.Keg
		ids       []keg.NodeId
		positions []int
	}
	groups := map[string]*group{}
	order := []string{}
	views := make([]keg.NodeView, len(nodeIDs))
	for pos, raw := range nodeIDs {
		resolved, id, resolveErr := t.resolveNodeArg(ctx, base, raw)
		if resolveErr != nil {
			return nil, resolveErr
		}
		key := describeKeg(resolved)
		g := groups[key]
		if g == nil {
			g = &group{k: resolved}
			groups[key] = g
			order = append(order, key)
		}
		g.ids = append(g.ids, id)
		g.positions = append(g.positions, pos)
	}
	for _, key := range order {
		g := groups[key]
		batch, batchErr := g.k.ReadNodes(ctx, keg.ReadNodesOptions{NodeIDs: g.ids, Touch: true})
		if batchErr != nil {
			return nil, fmt.Errorf("unable to read nodes in %s: %w", key, batchErr)
		}
		for i, view := range batch {
			views[g.positions[i]] = view
		}
	}
	return views, nil
}

func formatCatView(ctx context.Context, view keg.NodeView, opts CatOptions, withID bool) string {
	id := view.ID.Path()
	if opts.ContentOnly {
		if withID {
			return formatContentWithID(id, view.Content)
		}
		return string(view.Content)
	}
	if opts.StatsOnly {
		stats := formatStatsOnlyYAML(ctx, view.Stats)
		if withID {
			return formatStatsWithID(id, stats)
		}
		return stats
	}
	if opts.MetaOnly {
		if withID {
			return formatMetaWithID(ctx, id, view.Meta)
		}
		return normalizeMetaYAML(ctx, view.Meta)
	}
	if withID {
		return formatFrontmatterWithID(ctx, id, view.Meta, view.Content)
	}
	return formatFrontmatter(ctx, view.Meta, view.Content)
}

// catSingleNode reads and formats a single node's content according to opts.
func (t *Tap) catSingleNode(ctx context.Context, k keg.Keg, nodeID string, opts CatOptions) (string, error) {
	// A cross-keg ref (keg:<alias>/<id> or keg:@<ns>/<keg>/<id>) redirects to
	// its owning keg; a bare id stays on the passed-in current keg.
	k, node, err := t.resolveNodeArg(ctx, k, nodeID)
	if err != nil {
		return "", err
	}

	view, err := k.ReadNode(ctx, node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found in %s: %w", node.Path(), describeKeg(k), err)
		}
		return "", fmt.Errorf("unable to read node %s in %s: %w", node.Path(), describeKeg(k), err)
	}
	content, meta := view.Content, view.Meta

	if err := k.Touch(ctx, node); err != nil {
		return "", fmt.Errorf("unable to update node access: %w", err)
	}

	if opts.ContentOnly {
		return string(content), nil
	}

	if opts.StatsOnly {
		return formatStatsOnlyYAML(ctx, view.Stats), nil
	}

	if opts.MetaOnly {
		return normalizeMetaYAML(ctx, meta), nil
	}

	return formatFrontmatter(ctx, meta, content), nil
}

// formatFrontmatter renders meta as canonical YAML frontmatter ahead of the
// content body. Raw repository bytes may be JSON (hub kegs store meta as
// JSONB), so the meta always passes through normalizeMetaYAML.
func formatFrontmatter(ctx context.Context, meta []byte, content []byte) string {
	metaText := normalizeMetaYAML(ctx, meta)
	return fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content))
}

// formatFrontmatterWithID is like formatFrontmatter but prepends an `id` field.
func formatFrontmatterWithID(ctx context.Context, id string, meta []byte, content []byte) string {
	metaText := normalizeMetaYAML(ctx, meta)
	return fmt.Sprintf("---\nid: %q\n%s\n---\n%s", id, metaText, string(content))
}

// formatMetaWithID wraps a meta block as a `---`-delimited document with an
// injected `id` field at the top, normalizing the meta to YAML first.
func formatMetaWithID(ctx context.Context, id string, meta []byte) string {
	metaText := normalizeMetaYAML(ctx, meta)
	return fmt.Sprintf("---\nid: %q\n%s", id, metaText)
}

// formatStatsWithID wraps a pre-rendered stats YAML string as a
// `---`-delimited document with an injected `id` field at the top.
func formatStatsWithID(id string, stats string) string {
	statsText := strings.TrimRight(stats, "\n")
	return fmt.Sprintf("---\nid: %q\n%s", id, statsText)
}

// formatContentWithID prefixes a content block with a tiny YAML frontmatter
// containing only the node `id`, then closes the frontmatter before the body.
func formatContentWithID(id string, content []byte) string {
	return fmt.Sprintf("---\nid: %q\n---\n%s", id, string(content))
}

// catSingleNodeForStream reads and formats a single node for multi-document
// stream output. It injects the node ID into every output mode so each
// document is self-identifying.
func (t *Tap) catSingleNodeForStream(ctx context.Context, k keg.Keg, nodeID string, opts CatOptions) (string, error) {
	// Each streamed node resolves independently, so a multi-node cat may mix
	// bare ids (current keg) with cross-keg refs that redirect elsewhere.
	k, node, err := t.resolveNodeArg(ctx, k, nodeID)
	if err != nil {
		return "", err
	}

	view, err := k.ReadNode(ctx, node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found in %s: %w", node.Path(), describeKeg(k), err)
		}
		return "", fmt.Errorf("unable to read node %s in %s: %w", node.Path(), describeKeg(k), err)
	}
	content, meta := view.Content, view.Meta

	if err := k.Touch(ctx, node); err != nil {
		return "", fmt.Errorf("unable to update node access: %w", err)
	}

	id := node.Path()

	if opts.ContentOnly {
		return formatContentWithID(id, content), nil
	}

	if opts.StatsOnly {
		return formatStatsWithID(id, formatStatsOnlyYAML(ctx, view.Stats)), nil
	}

	if opts.MetaOnly {
		return formatMetaWithID(ctx, id, meta), nil
	}

	return formatFrontmatterWithID(ctx, id, meta, content), nil
}

func formatStatsOnlyYAML(ctx context.Context, stats *keg.NodeStats) string {
	meta := keg.NewMeta(ctx, time.Time{})
	return strings.TrimRight(meta.ToYAMLWithStats(stats), "\n")
}
