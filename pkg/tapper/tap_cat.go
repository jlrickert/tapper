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

	// Tag is an optional tag expression (same syntax as tap tags) used to
	// select nodes. Mutually exclusive with NodeIDs.
	Tag string

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

	// Resolve node IDs from tag expression or direct args.
	nodeIDs := opts.NodeIDs
	if opts.Tag != "" {
		if len(nodeIDs) > 0 {
			return "", fmt.Errorf("cannot specify both node IDs and --tag")
		}
		tagIDs, err := t.Tags(ctx, TagsOptions{
			KegTargetOptions: opts.KegTargetOptions,
			Tag:              opts.Tag,
			IdOnly:           true,
		})
		if err != nil {
			return "", fmt.Errorf("unable to query by tag: %w", err)
		}
		nodeIDs = tagIDs
	}

	if len(nodeIDs) == 0 {
		return "", nil
	}

	if opts.Edit {
		if len(nodeIDs) > 1 {
			return "", fmt.Errorf("--edit can only be used with a single node")
		}
		return "", t.Edit(ctx, EditOptions{
			NodeID:           nodeIDs[0],
			KegTargetOptions: opts.KegTargetOptions,
			Stream:           opts.Stream,
		})
	}

	k, err := t.resolveKeg(ctx, opts.KegTargetOptions)
	if err != nil {
		return "", fmt.Errorf("unable to open keg: %w", err)
	}

	// Single node: return output as-is (preserve existing behaviour).
	if len(nodeIDs) == 1 {
		out, err := t.catSingleNode(ctx, k, nodeIDs[0], opts)
		if err != nil {
			return "", err
		}
		return strings.TrimRight(out, "\n") + "\n", nil
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
	for i, nodeID := range nodeIDs {
		if i > 0 {
			buf.WriteString("\n")
		}
		var out string
		out, err = t.catSingleNodeForStream(ctx, k, nodeID, opts)
		if err != nil {
			return "", err
		}
		buf.WriteString(strings.TrimRight(out, "\n"))
		buf.WriteString("\n")
	}
	return buf.String(), nil
}

// catSingleNode reads and formats a single node's content according to opts.
func (t *Tap) catSingleNode(ctx context.Context, k *keg.Keg, nodeID string, opts CatOptions) (string, error) {
	node, err := keg.ParseNode(nodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", nodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", nodeID, keg.ErrInvalid)
	}

	content, err := k.Repo.ReadContent(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found", node.Path())
		}
		return "", fmt.Errorf("unable to read node content: %w", err)
	}

	meta, err := k.Repo.ReadMeta(ctx, *node)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}

	if err := k.Touch(ctx, *node); err != nil {
		return "", fmt.Errorf("unable to update node access: %w", err)
	}

	if opts.ContentOnly {
		return string(content), nil
	}

	if opts.StatsOnly {
		stats, err := k.Repo.ReadStats(ctx, *node)
		if err != nil {
			if errors.Is(err, keg.ErrNotExist) {
				stats = &keg.NodeStats{}
			} else {
				return "", fmt.Errorf("unable to read node stats: %w", err)
			}
		}
		return formatStatsOnlyYAML(ctx, stats), nil
	}

	if opts.MetaOnly {
		return string(meta), nil
	}

	return formatFrontmatter(meta, content), nil
}

func formatFrontmatter(meta []byte, content []byte) string {
	metaText := strings.TrimRight(string(meta), "\n")
	return fmt.Sprintf("---\n%s\n---\n%s", metaText, string(content))
}

// formatFrontmatterWithID is like formatFrontmatter but prepends an `id` field.
func formatFrontmatterWithID(id string, meta []byte, content []byte) string {
	metaText := strings.TrimRight(string(meta), "\n")
	return fmt.Sprintf("---\nid: %q\n%s\n---\n%s", id, metaText, string(content))
}

// formatMetaWithID wraps a raw meta YAML block as a `---`-delimited document
// with an injected `id` field at the top.
func formatMetaWithID(id string, meta []byte) string {
	metaText := strings.TrimRight(string(meta), "\n")
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
func (t *Tap) catSingleNodeForStream(ctx context.Context, k *keg.Keg, nodeID string, opts CatOptions) (string, error) {
	node, err := keg.ParseNode(nodeID)
	if err != nil {
		return "", fmt.Errorf("invalid node ID %q: %w", nodeID, err)
	}
	if node == nil {
		return "", fmt.Errorf("invalid node ID %q: %w", nodeID, keg.ErrInvalid)
	}

	content, err := k.Repo.ReadContent(ctx, *node)
	if err != nil {
		if errors.Is(err, keg.ErrNotExist) {
			return "", fmt.Errorf("node %s not found", node.Path())
		}
		return "", fmt.Errorf("unable to read node content: %w", err)
	}

	meta, err := k.Repo.ReadMeta(ctx, *node)
	if err != nil && !errors.Is(err, keg.ErrNotExist) {
		return "", fmt.Errorf("unable to read node metadata: %w", err)
	}

	if err := k.Touch(ctx, *node); err != nil {
		return "", fmt.Errorf("unable to update node access: %w", err)
	}

	id := node.Path()

	if opts.ContentOnly {
		return formatContentWithID(id, content), nil
	}

	if opts.StatsOnly {
		stats, readErr := k.Repo.ReadStats(ctx, *node)
		if readErr != nil {
			if errors.Is(readErr, keg.ErrNotExist) {
				stats = &keg.NodeStats{}
			} else {
				return "", fmt.Errorf("unable to read node stats: %w", readErr)
			}
		}
		return formatStatsWithID(id, formatStatsOnlyYAML(ctx, stats)), nil
	}

	if opts.MetaOnly {
		return formatMetaWithID(id, meta), nil
	}

	return formatFrontmatterWithID(id, meta, content), nil
}

func formatStatsOnlyYAML(ctx context.Context, stats *keg.NodeStats) string {
	meta := keg.NewMeta(ctx, time.Time{})
	return strings.TrimRight(meta.ToYAMLWithStats(stats), "\n")
}
