package tapper

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// ImportFromKegOptions controls how ImportFromKeg copies nodes from one live keg into another.
type ImportFromKegOptions struct {
	// Source is the source keg to copy nodes from.
	Source KegTargetOptions
	// Target is the destination keg; defaults to the resolved default keg.
	Target KegTargetOptions
	// NodeIDs lists the source node IDs to import. Values may be bare integers
	// ("5") or cross-keg references ("keg:pub/5"). All must resolve to Source.
	// When empty and TagQuery is also empty, all non-zero nodes are imported.
	NodeIDs []string
	// TagQuery is a boolean tag expression (same syntax as tap tags EXPR) that
	// selects additional source nodes; combined with NodeIDs as a union.
	TagQuery string
	// LeaveStubs writes a forwarding stub at each source node location after import.
	LeaveStubs bool
	// SkipZeroNode skips the source keg's node 0 (the index/root node).
	SkipZeroNode bool
}

// ImportedNode records the source → target ID mapping for one imported node.
type ImportedNode struct {
	SourceID keg.NodeId
	TargetID keg.NodeId
}

// kegArgRefRE matches a bare keg:ALIAS/N argument (full string).
var kegArgRefRE = regexp.MustCompile(`^keg:([a-zA-Z0-9][a-zA-Z0-9_-]*)/([0-9]+)$`)

// ImportFromKeg copies nodes from a source keg into the target keg. Each node
// is assigned a fresh ID via targetRepo.Next() and all links in the copied
// content are rewritten according to the six rules described in the plan.
func (t *Tap) ImportFromKeg(ctx context.Context, opts ImportFromKegOptions) ([]ImportedNode, error) {
	// Extract the source alias from any keg:ALIAS/N positional args and
	// validate consistency with opts.Source.Keg.
	srcAlias, bareIDs, err := resolveImportSourceAlias(opts.NodeIDs, opts.Source.Keg)
	if err != nil {
		return nil, err
	}
	opts.Source.Keg = srcAlias

	sourceRole := FlightRoleViewer
	if opts.LeaveStubs {
		sourceRole = FlightRoleEditor
	}
	srcKeg, err := t.resolveKegForRole(ctx, opts.Source, sourceRole)
	if err != nil {
		return nil, fmt.Errorf("unable to open source keg: %w", err)
	}
	tgtKeg, err := t.resolveKegForRole(ctx, opts.Target, FlightRoleEditor)
	if err != nil {
		return nil, fmt.Errorf("unable to open target keg: %w", err)
	}
	if kegsAreSame(srcKeg, tgtKeg) {
		return nil, fmt.Errorf("source and target keg are the same: %w", keg.ErrInvalid)
	}

	tgtAlias := opts.Target.Keg

	// Parse bare node IDs.
	srcIDs, err := parseImportNodeIDs(bareIDs)
	if err != nil {
		return nil, err
	}

	// Stream source nodes through a keg-archive and land them on fresh target
	// ids. The archive import rewrites links between imported nodes and
	// retargets cross-keg references via the source/target aliases.
	rc, err := srcKeg.ExportNodes(ctx, keg.ExportNodesOptions{
		NodeIDs:            srcIDs,
		Query:              opts.TagQuery,
		SkipZeroNode:       opts.SkipZeroNode,
		WithHistory:        true,
		HistoryIfSupported: true,
		WithAssets:         true,
		Source:             srcAlias,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to export source nodes: %w", err)
	}
	defer rc.Close()

	imported, err := tgtKeg.ImportNodes(ctx, rc, keg.ImportNodesOptions{
		AssignNewIDs:       true,
		HistoryIfSupported: true,
		SourceAlias:        srcAlias,
		TargetAlias:        tgtAlias,
	})
	if err != nil {
		return nil, fmt.Errorf("unable to import nodes: %w", err)
	}

	// Write forwarding stubs at source locations if requested.
	if opts.LeaveStubs && tgtAlias != "" {
		updates := make([]keg.NodeUpdateOptions, 0, len(imported))
		for _, node := range imported {
			srcID, parseErr := keg.ParseNode(node.SourceID)
			if parseErr != nil || srcID == nil {
				continue
			}
			view, readErr := srcKeg.ReadNode(ctx, *srcID)
			if readErr != nil {
				return nil, fmt.Errorf("unable to read source node %s before writing forwarding stub: %w", srcID.Path(), readErr)
			}
			title := ""
			if view.Stats != nil {
				title = strings.TrimSpace(view.Stats.Title())
			}
			if title == "" {
				if parsed, parseContentErr := keg.ParseContent(t.Runtime, view.Content, keg.MarkdownContentFilename); parseContentErr == nil {
					title = strings.TrimSpace(parsed.Title)
				}
			}
			if title == "" {
				title = srcID.Path()
			}
			target := "keg:" + tgtAlias
			body := fmt.Sprintf("# %s\n\nMoved to [%s/%s](%s/%s).\n", title, target, node.ID.Path(), target, node.ID.Path())
			updates = append(updates, keg.NodeUpdateOptions{ID: *srcID, Content: []byte(body), HasContent: true, ExpectedHash: node.SourceHash})
		}
		_, err := srcKeg.UpdateNodes(keg.WithValidationMode(ctx, keg.ValidationModeOff), updates)
		if err != nil {
			return nil, fmt.Errorf("unable to write forwarding stubs: %w", err)
		}
	}

	result := make([]ImportedNode, 0, len(imported))
	for _, node := range imported {
		srcID, parseErr := keg.ParseNode(node.SourceID)
		if parseErr == nil && srcID != nil {
			result = append(result, ImportedNode{SourceID: *srcID, TargetID: node.ID})
		}
	}
	return result, nil
}

// kegSupportsSnapshots probes snapshot support by listing revisions for the
// zero node and checking for ErrNotSupported.
func kegSupportsSnapshots(ctx context.Context, k keg.Keg) bool {
	_, err := k.ListSnapshots(ctx, keg.NodeId{ID: 0})
	return !errors.Is(err, keg.ErrNotSupported)
}

// resolveImportSourceAlias extracts the source keg alias from keg:ALIAS/N
// positional arguments, validates consistency with fromFlag, and returns the
// resolved alias and a slice of bare numeric ID strings.
func resolveImportSourceAlias(rawIDs []string, fromFlag string) (string, []string, error) {
	bareIDs := make([]string, 0, len(rawIDs))
	found := ""
	for _, raw := range rawIDs {
		if m := kegArgRefRE.FindStringSubmatch(raw); m != nil {
			alias, numStr := m[1], m[2]
			if fromFlag != "" && alias != fromFlag {
				return "", nil, fmt.Errorf("node reference %q has alias %q but --from is %q: %w",
					raw, alias, fromFlag, keg.ErrInvalid)
			}
			if found != "" && alias != found {
				return "", nil, fmt.Errorf("conflicting source keg aliases %q and %q in arguments: %w",
					found, alias, keg.ErrInvalid)
			}
			found = alias
			bareIDs = append(bareIDs, numStr)
		} else {
			bareIDs = append(bareIDs, raw)
		}
	}
	alias := fromFlag
	if alias == "" {
		alias = found
	}
	return alias, bareIDs, nil
}

// parseImportNodeIDs converts raw node ID strings to NodeId values. These ids
// are scoped to the import's source keg, which import selects through its own
// --from alias / "alias/id" syntax, so they are deliberately parsed as bare ids
// rather than routed through resolveNodeArg (whose keg: redirect would collide
// with import's own keg-selection mechanism).
func parseImportNodeIDs(rawIDs []string) ([]keg.NodeId, error) {
	ids := make([]keg.NodeId, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := parseNodeID(raw)
		if err != nil {
			return nil, fmt.Errorf("invalid node ID %q: %w", raw, err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// collectImportNodesByTag evaluates a boolean query expression (supporting both
// tag names and key=value attribute predicates) against the source keg's dex.
func collectImportNodesByTag(ctx context.Context, k keg.Keg, query string) ([]keg.NodeId, error) {
	entries, err := k.Query(ctx, keg.QueryOptions{Expr: query})
	if err != nil {
		return nil, fmt.Errorf("invalid query expression: %w", err)
	}
	ids := make([]keg.NodeId, 0, len(entries))
	seen := make(map[int]struct{}, len(entries))
	for _, entry := range entries {
		n, parseErr := keg.ParseNode(entry.ID)
		if parseErr != nil || n == nil {
			continue
		}
		if _, ok := seen[n.ID]; ok {
			continue
		}
		seen[n.ID] = struct{}{}
		ids = append(ids, *n)
	}
	return ids, nil
}

// unionImportNodeIDs merges two slices, deduplicating by numeric ID.
func unionImportNodeIDs(a, b []keg.NodeId) []keg.NodeId {
	seen := make(map[string]struct{}, len(a)+len(b))
	out := make([]keg.NodeId, 0, len(a)+len(b))
	for _, id := range append(a, b...) {
		key := id.Path()
		if _, ok := seen[key]; !ok {
			seen[key] = struct{}{}
			out = append(out, id)
		}
	}
	return out
}

// filterZeroImportNode removes node 0 from the list.
func filterZeroImportNode(ids []keg.NodeId) []keg.NodeId {
	out := make([]keg.NodeId, 0, len(ids))
	for _, id := range ids {
		if id.ID != 0 || id.Code != "" {
			out = append(out, id)
		}
	}
	return out
}

// kegsAreSame reports whether two kegs refer to the same underlying storage.
func kegsAreSame(a, b keg.Keg) bool {
	if a == b {
		return true
	}
	if a.Target() == nil || b.Target() == nil {
		return false
	}
	return strings.EqualFold(a.Target().String(), b.Target().String())
}
