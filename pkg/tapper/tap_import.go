package tapper

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

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

// kegLinkInTextRE matches keg:ALIAS/N links anywhere in content.
var kegLinkInTextRE = regexp.MustCompile(`keg:([a-zA-Z0-9][a-zA-Z0-9_-]*)/([0-9]+)`)

// relImportLinkRE matches ../N links in content (same pattern as importedNodeLinkRE in tap_archive.go).
var relImportLinkRE = regexp.MustCompile(`\.\./\s*([0-9]+)([[:space:]\)\]\}\>\.,;:!?'"#]|$)`)

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

	srcKeg, err := t.resolveKeg(ctx, opts.Source)
	if err != nil {
		return nil, fmt.Errorf("unable to open source keg: %w", err)
	}
	tgtKeg, err := t.resolveKeg(ctx, opts.Target)
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

	// Collect nodes from tag query and merge with explicit IDs.
	if opts.TagQuery != "" {
		tagIDs, err := collectImportNodesByTag(ctx, srcKeg, opts.TagQuery)
		if err != nil {
			return nil, fmt.Errorf("unable to query source keg by tag: %w", err)
		}
		srcIDs = unionImportNodeIDs(srcIDs, tagIDs)
	}

	// Default to all nodes when nothing is specified.
	if len(srcIDs) == 0 && opts.TagQuery == "" {
		all, err := srcKeg.Repo.ListNodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list source nodes: %w", err)
		}
		srcIDs = all
	}

	if opts.SkipZeroNode {
		srcIDs = filterZeroImportNode(srcIDs)
	}
	slices.SortFunc(srcIDs, func(a, b keg.NodeId) int { return a.Compare(b) })

	// Pass 1: allocate target IDs. Build the full mapping before writing anything.
	mapping := make(map[string]keg.NodeId, len(srcIDs)) // srcID numeric string → newID
	for _, srcID := range srcIDs {
		newID, err := tgtKeg.Repo.Next(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to allocate node ID for import of %s: %w", srcID.Path(), err)
		}
		mapping[srcID.Path()] = newID
	}

	// Pass 2: rewrite links and write each node to the target.
	for _, srcID := range srcIDs {
		newID := mapping[srcID.Path()]

		content, err := srcKeg.Repo.ReadContent(ctx, srcID)
		if err != nil {
			return nil, fmt.Errorf("unable to read content for node %s: %w", srcID.Path(), err)
		}
		meta, err := readOptionalNodeMeta(ctx, srcKeg.Repo, srcID)
		if err != nil {
			return nil, fmt.Errorf("unable to read meta for node %s: %w", srcID.Path(), err)
		}
		statsBytes, err := readOptionalNodeStats(ctx, srcKeg.Repo, srcID)
		if err != nil {
			return nil, fmt.Errorf("unable to read stats for node %s: %w", srcID.Path(), err)
		}
		statsObj, err := keg.ParseStats(ctx, statsBytes)
		if err != nil {
			return nil, fmt.Errorf("unable to parse stats for node %s: %w", srcID.Path(), err)
		}

		content = rewriteLiveImportLinks(content, srcAlias, tgtAlias, mapping)
		remapStatsLinks(statsObj, mapping)

		srcAssets, err := readImportedNodeAssets(ctx, srcKeg.Repo, srcID)
		if err != nil {
			return nil, fmt.Errorf("unable to read assets for node %s: %w", srcID.Path(), err)
		}

		var snapshots []importSnapshotPayload
		if snapRepo, ok := srcKeg.Repo.(keg.RepositorySnapshots); ok {
			snapshots, err = collectImportSnapshots(ctx, snapRepo, srcID, srcAlias, tgtAlias, mapping)
			if err != nil {
				return nil, fmt.Errorf("unable to read snapshots for node %s: %w", srcID.Path(), err)
			}
		}

		if err := tgtKeg.Repo.WriteContent(ctx, newID, content); err != nil {
			return nil, fmt.Errorf("unable to write content for imported node %s: %w", srcID.Path(), err)
		}
		if err := tgtKeg.Repo.WriteMeta(ctx, newID, meta); err != nil {
			return nil, fmt.Errorf("unable to write meta for imported node %s: %w", srcID.Path(), err)
		}
		if err := tgtKeg.Repo.WriteStats(ctx, newID, statsObj); err != nil {
			return nil, fmt.Errorf("unable to write stats for imported node %s: %w", srcID.Path(), err)
		}
		if err := restoreImportedNodeAssets(ctx, tgtKeg.Repo, newID, srcAssets); err != nil {
			return nil, fmt.Errorf("unable to write assets for imported node %s: %w", srcID.Path(), err)
		}
		if tgtSnapRepo, ok := tgtKeg.Repo.(keg.RepositorySnapshots); ok && len(snapshots) > 0 {
			if err := replayImportSnapshots(ctx, tgtSnapRepo, newID, snapshots); err != nil {
				return nil, fmt.Errorf("unable to replay snapshots for imported node %s: %w", srcID.Path(), err)
			}
		}
	}

	// Write forwarding stubs at source locations if requested.
	if opts.LeaveStubs && tgtAlias != "" {
		for _, srcID := range srcIDs {
			newID := mapping[srcID.Path()]
			statsBytes, _ := readOptionalNodeStats(ctx, srcKeg.Repo, srcID)
			statsObj, _ := keg.ParseStats(ctx, statsBytes)
			title := statsObj.Title()
			if title == "" {
				title = srcID.Path()
			}
			stub := fmt.Sprintf("# %s\n\nMoved to [keg:%s/%s](keg:%s/%s).\n",
				title, tgtAlias, newID.Path(), tgtAlias, newID.Path())
			if err := srcKeg.Repo.WriteContent(ctx, srcID, []byte(stub)); err != nil {
				return nil, fmt.Errorf("unable to write stub for source node %s: %w", srcID.Path(), err)
			}
		}
	}

	if err := rebuildDexFromRepo(ctx, tgtKeg); err != nil {
		return nil, err
	}
	if err := tgtKeg.UpdateConfig(ctx, func(cfg *keg.Config) {
		cfg.Updated = t.Runtime.Clock().Now().UTC().Format(time.RFC3339)
	}); err != nil {
		return nil, fmt.Errorf("unable to update target keg config after import: %w", err)
	}

	result := make([]ImportedNode, len(srcIDs))
	for i, srcID := range srcIDs {
		result[i] = ImportedNode{SourceID: srcID, TargetID: mapping[srcID.Path()]}
	}
	return result, nil
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

// parseImportNodeIDs converts raw node ID strings to NodeId values.
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

// collectImportNodesByTag evaluates a boolean tag expression against the source keg's dex.
func collectImportNodesByTag(ctx context.Context, k *keg.Keg, query string) ([]keg.NodeId, error) {
	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to load source dex: %w", err)
	}
	expr, err := parseTagExpression(query)
	if err != nil {
		return nil, fmt.Errorf("invalid tag expression: %w", err)
	}
	indexEntries := dex.Nodes(ctx)
	universe := make(map[string]struct{}, len(indexEntries)*2)
	for _, entry := range indexEntries {
		universe[entry.ID] = struct{}{}
		node, parseErr := keg.ParseNode(entry.ID)
		if parseErr == nil && node != nil {
			universe[node.Path()] = struct{}{}
		}
	}
	matchedPaths := evaluateTagExpression(expr, universe, func(tagName string) map[string]struct{} {
		nodes, ok := dex.TagNodes(ctx, tagName)
		if !ok {
			return map[string]struct{}{}
		}
		return setFromNodeIDs(nodes)
	})
	ids := make([]keg.NodeId, 0, len(matchedPaths))
	seen := make(map[int]struct{}, len(matchedPaths))
	for path := range matchedPaths {
		n, err := keg.ParseNode(path)
		if err != nil || n == nil {
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
func kegsAreSame(a, b *keg.Keg) bool {
	if a == b {
		return true
	}
	if a.Target == nil || b.Target == nil {
		return false
	}
	return strings.EqualFold(a.Target.String(), b.Target.String())
}

// rewriteLiveImportLinks rewrites links in content according to the six rules:
//
//  1. ../N (imported)          → ../NEW_ID
//  2. ../N (not imported)      → keg:srcAlias/N  (only when srcAlias is known)
//  3. keg:tgtAlias/N           → ../N            (only when tgtAlias is known)
//  4. keg:srcAlias/N (imported)→ ../NEW_ID       (only when srcAlias is known)
//  5. keg:srcAlias/N (other)   → unchanged
//  6. keg:otherAlias/N         → unchanged
//
// Two sequential passes are used: relative links first, then cross-keg links.
// This ordering prevents pass-2 output from being re-processed by pass-1.
func rewriteLiveImportLinks(raw []byte, srcAlias, tgtAlias string, mapping map[string]keg.NodeId) []byte {
	if len(raw) == 0 {
		return raw
	}
	s := string(raw)

	// Pass 1: ../N links.
	s = relImportLinkRE.ReplaceAllStringFunc(s, func(match string) string {
		parts := relImportLinkRE.FindStringSubmatch(match)
		if len(parts) != 3 {
			return match
		}
		nodeNum, suffix := parts[1], parts[2]
		if dst, ok := mapping[nodeNum]; ok {
			return "../" + dst.Path() + suffix
		}
		if srcAlias != "" {
			return "keg:" + srcAlias + "/" + nodeNum + suffix
		}
		return match
	})

	// Pass 2: keg:ALIAS/N links.
	if srcAlias != "" || tgtAlias != "" {
		s = kegLinkInTextRE.ReplaceAllStringFunc(s, func(match string) string {
			parts := kegLinkInTextRE.FindStringSubmatch(match)
			if len(parts) != 3 {
				return match
			}
			alias, nodeNum := parts[1], parts[2]
			if tgtAlias != "" && alias == tgtAlias {
				return "../" + nodeNum
			}
			if srcAlias != "" && alias == srcAlias {
				if dst, ok := mapping[nodeNum]; ok {
					return "../" + dst.Path()
				}
				return match
			}
			return match
		})
	}

	if s == string(raw) {
		return raw
	}
	return []byte(s)
}

// importSnapshotPayload holds a fully-materialized snapshot ready to be
// replayed onto the target node.
type importSnapshotPayload struct {
	snap    keg.Snapshot
	content []byte
	meta    []byte
	stats   *keg.NodeStats
}

// collectImportSnapshots reads all snapshots for a source node and rewrites
// their content links using the same rules as rewriteLiveImportLinks.
func collectImportSnapshots(
	ctx context.Context,
	snapRepo keg.RepositorySnapshots,
	srcID keg.NodeId,
	srcAlias, tgtAlias string,
	mapping map[string]keg.NodeId,
) ([]importSnapshotPayload, error) {
	history, err := snapRepo.ListSnapshots(ctx, srcID)
	if err != nil {
		return nil, err
	}
	payloads := make([]importSnapshotPayload, 0, len(history))
	for _, snap := range history {
		_, snapContent, snapMeta, snapStats, err := snapRepo.GetSnapshot(
			ctx, srcID, snap.ID, keg.SnapshotReadOptions{ResolveContent: true},
		)
		if err != nil {
			return nil, fmt.Errorf("unable to load snapshot %d: %w", snap.ID, err)
		}
		snapContent = rewriteLiveImportLinks(snapContent, srcAlias, tgtAlias, mapping)
		remapStatsLinks(snapStats, mapping)
		payloads = append(payloads, importSnapshotPayload{
			snap:    snap,
			content: snapContent,
			meta:    snapMeta,
			stats:   snapStats,
		})
	}
	return payloads, nil
}

// replayImportSnapshots appends snapshot payloads onto a target node in
// chronological order, preserving original CreatedAt timestamps and messages.
func replayImportSnapshots(
	ctx context.Context,
	snapRepo keg.RepositorySnapshots,
	newID keg.NodeId,
	payloads []importSnapshotPayload,
) error {
	var expectedParent keg.RevisionID
	for _, p := range payloads {
		imported, err := snapRepo.AppendSnapshot(ctx, newID, keg.SnapshotWrite{
			ExpectedParent: expectedParent,
			Message:        p.snap.Message,
			CreatedAt:      p.snap.CreatedAt,
			Meta:           p.meta,
			Stats:          p.stats,
			Content: keg.SnapshotContentWrite{
				Kind: keg.SnapshotContentKindFull,
				Base: expectedParent,
				Data: p.content,
			},
		})
		if err != nil {
			return err
		}
		expectedParent = imported.ID
	}
	return nil
}
