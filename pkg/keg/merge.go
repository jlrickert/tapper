package keg

import (
	"context"
	"fmt"
)

// MergeNodesOptions folds Source into Target: Target is rewritten with the
// merged body, every link to Source is pointed at Target, and Source is
// removed (to the trash, where the keg keeps one).
type MergeNodesOptions struct {
	// Source is the node merged away, guarded by its combined hash.
	Source             NodeId `json:"source"`
	ExpectedSourceHash string `json:"expected_source_hash"`
	// Target is the surviving node's update. A merge is a rewrite, so it
	// must carry content or a content patch; meta is optional, for combining
	// tags and fields. Its part tokens guard it as for any update.
	Target NodeUpdateOptions `json:"target"`
}

// MergeNodesResult is the surviving node's tokens after the merge (links in
// it may have been repointed too), and the other nodes whose links moved.
type MergeNodesResult struct {
	Target    NodeUpdateResult `json:"target"`
	Rewritten []NodeId         `json:"rewritten"`
}

// MergeNodes folds opts.Source into opts.Target as one write: either all of
// it happens or none of it does.
func (k *LocalKeg) MergeNodes(ctx context.Context, opts MergeNodesOptions) (MergeNodesResult, error) {
	return withKegAtomicWriteValue(ctx, k, func(ctx context.Context) (MergeNodesResult, error) { return k.mergeNodes(ctx, opts) })
}

func (k *LocalKeg) mergeNodes(ctx context.Context, opts MergeNodesOptions) (MergeNodesResult, error) {
	source, target := opts.Source, opts.Target.ID
	if !source.Valid() || !target.Valid() {
		return MergeNodesResult{}, fmt.Errorf("merge needs a source and a target node: %w", ErrInvalid)
	}
	if source.ID == 0 || target.ID == 0 {
		return MergeNodesResult{}, fmt.Errorf("node 0 cannot be merged: %w", ErrInvalid)
	}
	if source.Equals(target) {
		return MergeNodesResult{}, fmt.Errorf("a node cannot be merged into itself: %w", ErrInvalid)
	}
	if !opts.Target.HasContent && !opts.Target.HasContentPatch {
		return MergeNodesResult{}, fmt.Errorf("a merge rewrites the target: send its merged content or a content patch: %w", ErrInvalid)
	}
	current, err := k.ReadNode(ctx, source)
	if err != nil {
		return MergeNodesResult{}, fmt.Errorf("merge source %s: %w", source.Path(), err)
	}
	if err := checkExpectedHash("node "+source.Path(), opts.ExpectedSourceHash, current.Hash(), nodeRecoveryContent(current)); err != nil {
		return MergeNodesResult{}, err
	}
	results, err := k.updateNodes(ctx, []NodeUpdateOptions{opts.Target})
	if err != nil {
		return MergeNodesResult{}, err
	}
	// The source's own links to itself stay as they are, so a restored
	// source comes back exactly as it was trashed.
	rewritten, err := k.retargetLinks(ctx, source, target, source)
	if err != nil {
		return MergeNodesResult{}, err
	}
	if _, err := k.removeUnchecked(ctx, source, trashInfo{reason: TrashReasonMerge, mergedInto: &target}); err != nil {
		return MergeNodesResult{}, err
	}
	// The target's own links to the source were repointed too, so read its
	// tokens back rather than trusting the update's.
	merged, err := k.ReadNode(ctx, target)
	if err != nil {
		return MergeNodesResult{}, err
	}
	result := MergeNodesResult{
		Target: NodeUpdateResult{ID: target, Validation: results[0].Validation, Hash: merged.Hash(), ContentHash: merged.ContentHash(), MetaHash: merged.MetaHash()},
	}
	for _, id := range rewritten {
		if !id.Equals(target) && !id.Equals(source) {
			result.Rewritten = append(result.Rewritten, id)
		}
	}
	return result, nil
}
