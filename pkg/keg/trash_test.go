package keg_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/tapper/internal/testkegrepo"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// trashingRepo is the memory repository with a trash, as the hub keeps one.
type trashingRepo struct {
	*testkegrepo.MemoryRepository
	mu    sync.Mutex
	trash map[keg.NodeId]trashedNode
	// floor is the highest id Next may not hand out, as the hub's
	// next_node_id counter only rises.
	floor int
}

func (r *trashingRepo) RaiseNodeCounter(_ context.Context, past keg.NodeId) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.floor = max(r.floor, past.ID)
	return nil
}

type trashedNode struct {
	entry  keg.TrashEntry
	bundle []byte
}

// Next never hands out a trashed id, as the hub's rising counter never does.
func (r *trashingRepo) Next(ctx context.Context) (keg.NodeId, error) {
	for {
		id, err := r.MemoryRepository.Next(ctx)
		if err != nil {
			return id, err
		}
		r.mu.Lock()
		_, trashed := r.trash[id]
		below := id.ID <= r.floor
		r.mu.Unlock()
		if !trashed && !below {
			return id, nil
		}
	}
}

func (r *trashingRepo) TrashNode(ctx context.Context, id keg.NodeId, in keg.TrashWrite) error {
	r.mu.Lock()
	_, taken := r.trash[id]
	r.mu.Unlock()
	if taken {
		return keg.ErrDestinationExists
	}
	if err := r.DeleteNode(ctx, id); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trash[id] = trashedNode{entry: keg.TrashEntry{ID: id, Title: in.Title, Reason: in.Reason, MergedInto: in.MergedInto, Size: int64(len(in.Bundle))}, bundle: in.Bundle}
	return nil
}

func (r *trashingRepo) ListTrash(context.Context) ([]keg.TrashEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]keg.TrashEntry, 0, len(r.trash))
	for _, item := range r.trash {
		out = append(out, item.entry)
	}
	return out, nil
}

func (r *trashingRepo) ReadTrash(_ context.Context, id keg.NodeId) (keg.TrashEntry, []byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.trash[id]
	if !ok {
		return keg.TrashEntry{}, nil, keg.ErrNotExist
	}
	return item.entry, item.bundle, nil
}

// expire sets when the trash would purge id's entry.
func (r *trashingRepo) expire(id keg.NodeId, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item := r.trash[id]
	item.entry.ExpiresAt = &at
	r.trash[id] = item
}

// plant puts an entry in the trash directly, as a stale or legacy entry is.
func (r *trashingRepo) plant(id keg.NodeId) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trash[id] = trashedNode{entry: keg.TrashEntry{ID: id, Reason: keg.TrashReasonManual}}
}

func (r *trashingRepo) TrashInfo(_ context.Context, id keg.NodeId) (keg.TrashEntry, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.trash[id]
	if !ok {
		return keg.TrashEntry{}, keg.ErrNotExist
	}
	return item.entry, nil
}

func (r *trashingRepo) MoveTrash(_ context.Context, from, to keg.NodeId) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	item, ok := r.trash[from]
	if !ok {
		return keg.ErrNotExist
	}
	if _, taken := r.trash[to]; taken {
		return keg.ErrDestinationExists
	}
	delete(r.trash, from)
	// The original id is kept through later displacements.
	if item.entry.DisplacedFrom == nil {
		old := from
		item.entry.DisplacedFrom = &old
	}
	item.entry.ID = to
	r.trash[to] = item
	return nil
}

func (r *trashingRepo) DeleteTrash(_ context.Context, id keg.NodeId) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.trash, id)
	return nil
}

func TestTrashKeepsRemovedNodesRestorable(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	repo := &trashingRepo{MemoryRepository: newTestMemoryRepo(fx.Runtime()), trash: map[keg.NodeId]trashedNode{}}
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	gone, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Gone\n\nKeep me.\n"), Meta: []byte("tags: [kept]\n")})
	require.NoError(t, err)
	_, err = k.AppendSnapshot(ctx, gone.ID, "before removal")
	require.NoError(t, err)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Linker\n\nSee [gone](../%d).\n", gone.ID.ID))})
	require.NoError(t, err)
	before, err := k.ReadNode(ctx, gone.ID)
	require.NoError(t, err)

	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: gone.ID, ExpectedHash: before.Hash()})
	require.NoError(t, err)
	exists, err := k.NodeExists(ctx, gone.ID)
	require.NoError(t, err)
	require.False(t, exists)
	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
	require.Equal(t, gone.ID, trash[0].ID)
	require.Equal(t, "Gone", trash[0].Title)
	require.Equal(t, keg.TrashReasonManual, trash[0].Reason)
	info, err := k.TrashInfo(ctx, gone.ID)
	require.NoError(t, err)
	require.Equal(t, "Gone", info.Title)
	_, err = k.TrashInfo(ctx, linker.ID)
	require.ErrorIs(t, err, keg.ErrNotExist, "a live node is not trashed")
	// The link is left alone so a restore is lossless.
	linking, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Contains(t, string(linking.Content), fmt.Sprintf("../%d)", gone.ID.ID))

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{gone.ID}))
	restored, err := k.ReadNode(ctx, gone.ID)
	require.NoError(t, err)
	require.Equal(t, before.Content, restored.Content)
	require.Equal(t, before.Meta, restored.Meta)
	snapshots, err := k.ListSnapshots(ctx, gone.ID)
	require.NoError(t, err)
	require.NotEmpty(t, snapshots)
	trash, err = k.ListTrash(ctx)
	require.NoError(t, err)
	require.Empty(t, trash)
	_, err = k.TrashInfo(ctx, gone.ID)
	require.ErrorIs(t, err, keg.ErrNotExist, "a restored node is no longer trashed")
	require.ErrorIs(t, k.RestoreTrashed(ctx, []keg.NodeId{gone.ID}), keg.ErrNotExist)

	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: gone.ID, ExpectedHash: restored.Hash()})
	require.NoError(t, err)
	purged, err := k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: []keg.NodeId{gone.ID}})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{gone.ID}, purged.Purged)
	require.Equal(t, []keg.NodeId{linker.ID}, purged.Rewritten)
	linking, err = k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Contains(t, string(linking.Content), "../0)")
	require.ErrorIs(t, k.RestoreTrashed(ctx, []keg.NodeId{gone.ID}), keg.ErrNotExist)
}

func TestRemoveWithoutTrashRewritesLinks(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	gone, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Gone\n")})
	require.NoError(t, err)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Linker\n\n[gone](../%d)\n", gone.ID.ID))})
	require.NoError(t, err)
	view, err := k.ReadNode(ctx, gone.ID)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: gone.ID, ExpectedHash: view.Hash()})
	require.NoError(t, err)
	linking, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Contains(t, string(linking.Content), "../0)")
	_, err = k.ListTrash(ctx)
	require.ErrorIs(t, err, keg.ErrNotSupported)
	_, err = k.TrashInfo(ctx, gone.ID)
	require.ErrorIs(t, err, keg.ErrNotSupported)
}

func TestMergeNodesFoldsADuplicateIntoTheTarget(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	repo := &trashingRepo{MemoryRepository: newTestMemoryRepo(fx.Runtime()), trash: map[keg.NodeId]trashedNode{}}
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	target, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Topic\n\nFirst take.\n")})
	require.NoError(t, err)
	dup, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Topic again\n\nSecond take.\n")})
	require.NoError(t, err)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Linker\n\n[one](../%d) and [two](../%d)\n", target.ID.ID, dup.ID.ID))})
	require.NoError(t, err)
	targetView, err := k.ReadNode(ctx, target.ID)
	require.NoError(t, err)
	dupView, err := k.ReadNode(ctx, dup.ID)
	require.NoError(t, err)

	merged := fmt.Sprintf("# Topic\n\nFirst take.\n\nSecond take, once [a duplicate](../%d).\n", dup.ID.ID)
	_, err = k.MergeNodes(ctx, keg.MergeNodesOptions{
		Source: dup.ID, ExpectedSourceHash: "stale",
		Target: keg.NodeUpdateOptions{ID: target.ID, Content: []byte(merged), HasContent: true, ExpectedContentHash: targetView.ContentHash()},
	})
	var conflict *keg.PreconditionConflictError
	require.ErrorAs(t, err, &conflict, "a stale source hash changes nothing")
	unchanged, err := k.ReadNode(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, targetView.Content, unchanged.Content)

	result, err := k.MergeNodes(ctx, keg.MergeNodesOptions{
		Source: dup.ID, ExpectedSourceHash: dupView.Hash(),
		Target: keg.NodeUpdateOptions{ID: target.ID, Content: []byte(merged), HasContent: true, ExpectedContentHash: targetView.ContentHash()},
	})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{linker.ID}, result.Rewritten)

	after, err := k.ReadNode(ctx, target.ID)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("# Topic\n\nFirst take.\n\nSecond take, once [a duplicate](../%d).\n", target.ID.ID), string(after.Content), "the target's own link to the source points at itself")
	require.Equal(t, after.ContentHash(), result.Target.ContentHash)
	linking, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("# Linker\n\n[one](../%d) and [two](../%d)\n", target.ID.ID, target.ID.ID), string(linking.Content))

	exists, err := k.NodeExists(ctx, dup.ID)
	require.NoError(t, err)
	require.False(t, exists)
	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
	require.Equal(t, keg.TrashReasonMerge, trash[0].Reason)
	require.NotNil(t, trash[0].MergedInto)
	require.Equal(t, target.ID, *trash[0].MergedInto)

	// Restoring the source brings it back; the links stay with the target.
	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{dup.ID}))
	restored, err := k.ReadNode(ctx, dup.ID)
	require.NoError(t, err)
	require.Contains(t, string(restored.Content), "Second take.")
	linking, err = k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.NotContains(t, string(linking.Content), fmt.Sprintf("../%d)", dup.ID.ID))
}

func TestMergeNodesRefusesWhatIsNotAMerge(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	a, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# A\n")})
	require.NoError(t, err)
	view, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	for name, opts := range map[string]keg.MergeNodesOptions{
		"into itself": {Source: a.ID, ExpectedSourceHash: view.Hash(), Target: keg.NodeUpdateOptions{ID: a.ID, Content: []byte("# A\n"), HasContent: true, ExpectedContentHash: view.ContentHash()}},
		"node 0":      {Source: keg.NodeId{ID: 0}, Target: keg.NodeUpdateOptions{ID: a.ID, Content: []byte("# A\n"), HasContent: true}},
		"no body":     {Source: keg.NodeId{ID: 0}, Target: keg.NodeUpdateOptions{ID: a.ID, Meta: []byte("tags: [x]\n"), HasMeta: true}},
	} {
		_, err := k.MergeNodes(ctx, opts)
		require.ErrorIs(t, err, keg.ErrInvalid, name)
	}
}

func TestReplaceTextAcrossTheKeg(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	a, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Alpha\n\nUses Widget v1 and Widget v1.\n"), Meta: []byte("tags: [widget]\n")})
	require.NoError(t, err)
	b, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Beta\n\nWidget v1 here too.\n"), Meta: []byte("tags: [other]\n")})
	require.NoError(t, err)
	c, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Gamma\n\nNothing to see.\n")})
	require.NoError(t, err)
	read := func(id keg.NodeId) *keg.NodeView {
		view, err := k.ReadNode(ctx, id)
		require.NoError(t, err)
		return view
	}
	before := read(c.ID).Hash()

	dry, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "Widget v1", Replace: "Widget v2", DryRun: true})
	require.NoError(t, err)
	require.Len(t, dry.Nodes, 2)
	require.Equal(t, 3, dry.Nodes[0].Matches+dry.Nodes[1].Matches, "three matches over two nodes")
	require.Contains(t, string(read(a.ID).Content), "Widget v1", "a dry run writes nothing")

	done, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: `Widget v(\d)`, Replace: "Gadget v$1", Regex: true, Query: "widget"})
	require.NoError(t, err)
	require.Len(t, done.Nodes, 1, "the query keeps it to the tagged node")
	require.Equal(t, "# Alpha\n\nUses Gadget v1 and Gadget v1.\n", string(read(a.ID).Content))
	require.Equal(t, read(a.ID).ContentHash(), done.Nodes[0].ContentHash)
	require.Contains(t, string(read(b.ID).Content), "Widget v1")
	require.Equal(t, before, read(c.ID).Hash())

	broken, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "tags: [other]", Replace: "tags: [other", IncludeMeta: true})
	require.NoError(t, err)
	require.Len(t, broken.Nodes, 1)
	require.NotEmpty(t, broken.Nodes[0].Error, "metadata that would not parse is reported")
	require.Contains(t, string(read(b.ID).Meta), "other]", "and left alone")

	meta, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "widget", Replace: "gadget", IncludeMeta: true})
	require.NoError(t, err)
	require.Len(t, meta.Nodes, 1)
	require.Contains(t, string(read(a.ID).Meta), "gadget")

	_, err = k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "(", Regex: true})
	require.ErrorIs(t, err, keg.ErrInvalid)
	_, err = k.ReplaceText(ctx, keg.ReplaceTextOptions{})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

func newTrashingKeg(t *testing.T) (*keg.LocalKeg, *trashingRepo, context.Context) {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	repo := &trashingRepo{MemoryRepository: newTestMemoryRepo(fx.Runtime()), trash: map[keg.NodeId]trashedNode{}}
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	return k, repo, ctx
}

func TestTrashedIDIsReserved(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	a, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# A\n\nOriginal.\n")})
	require.NoError(t, err)
	b, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# B\n")})
	require.NoError(t, err)
	aView, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: a.ID, ExpectedHash: aView.Hash()})
	require.NoError(t, err)
	_, bundle, err := k.Repo.(keg.RepositoryTrash).ReadTrash(ctx, a.ID)
	require.NoError(t, err)

	bView, err := k.ReadNode(ctx, b.ID)
	require.NoError(t, err)
	_, err = k.Move(ctx, keg.NodeMoveOptions{Source: b.ID, Destination: a.ID, ExpectedHash: bView.Hash()})
	require.ErrorIs(t, err, keg.ErrDestinationExists, "a trashed id is not free to move onto")
	stillB, err := k.ReadNode(ctx, b.ID)
	require.NoError(t, err)
	require.Equal(t, bView.Content, stillB.Content)

	_, after, err := k.Repo.(keg.RepositoryTrash).ReadTrash(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, bundle, after, "the trashed copy is untouched")
	exists, err := k.NodeExists(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, exists)

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{a.ID}))
	restored, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, aView.Content, restored.Content)
}

// An archive always restores: a trashed node holding an archived id moves to
// a fresh id, keeps its content, and restores there with its links to itself
// following it, while links elsewhere stay with the archived node.
func TestArchiveRestoreDisplacesATrashedNode(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	a, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# A\n")})
	require.NoError(t, err)
	n := a.ID.ID
	archivedBody := fmt.Sprintf("# A\n\nArchived, see [me](../%d).\n", n)
	require.NoError(t, k.Repo.WriteContent(ctx, a.ID, []byte(archivedBody)))
	linkerBody := fmt.Sprintf("# Linker\n\n[a](../%d)\n", n)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(linkerBody)})
	require.NoError(t, err)
	archive, err := k.ExportNodes(ctx, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{a.ID}})
	require.NoError(t, err)
	archived, err := io.ReadAll(archive)
	require.NoError(t, err)
	require.NoError(t, archive.Close())

	trashedBody := fmt.Sprintf("# A\n\nLater, see [me](../%d).\n", n)
	require.NoError(t, k.Repo.WriteContent(ctx, a.ID, []byte(trashedBody)))
	view, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: a.ID, ExpectedHash: view.Hash()})
	require.NoError(t, err)

	_, err = k.ImportNodes(ctx, bytes.NewReader(archived), keg.ImportNodesOptions{})
	require.NoError(t, err, "an archive restores even when its id is in the trash")
	got, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, archivedBody, string(got.Content))

	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
	moved := trash[0].ID
	require.NotEqual(t, a.ID, moved)
	require.NotNil(t, trash[0].DisplacedFrom)
	require.Equal(t, a.ID, *trash[0].DisplacedFrom)

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{moved}))
	back, err := k.ReadNode(ctx, moved)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("# A\n\nLater, see [me](../%d).\n", moved.ID), string(back.Content), "the trashed content, its self-link following it")
	still, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, archivedBody, string(still.Content), "the archived node keeps its id")
	link, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Equal(t, linkerBody, string(link.Content), "links elsewhere stay with the archived node")

	// The archived node can itself be trashed as usual.
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: a.ID, ExpectedHash: still.Hash()})
	require.NoError(t, err)
	trash, err = k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
	require.Equal(t, a.ID, trash[0].ID)
	require.Nil(t, trash[0].DisplacedFrom)
}

// Purging a displaced entry rewrites links to its new id, never its old one,
// which belongs to the archived node.
func TestPurgingADisplacedEntryLeavesTheOldIDsLinks(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	a, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# A\n")})
	require.NoError(t, err)
	archive, err := k.ExportNodes(ctx, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{a.ID}})
	require.NoError(t, err)
	archived, err := io.ReadAll(archive)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	linkerBody := fmt.Sprintf("# Linker\n\n[a](../%d)\n", a.ID.ID)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(linkerBody)})
	require.NoError(t, err)
	view, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: a.ID, ExpectedHash: view.Hash()})
	require.NoError(t, err)
	_, err = k.ImportNodes(ctx, bytes.NewReader(archived), keg.ImportNodesOptions{})
	require.NoError(t, err)
	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
	result, err := k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: []keg.NodeId{trash[0].ID}})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{trash[0].ID}, result.Purged)
	require.Empty(t, result.Rewritten)
	got, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Equal(t, linkerBody, string(got.Content))
}

func TestRestoreTrashedRefusesDuplicateIDs(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	a, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# A\n")})
	require.NoError(t, err)
	view, err := k.ReadNode(ctx, a.ID)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: a.ID, ExpectedHash: view.Hash()})
	require.NoError(t, err)
	err = k.RestoreTrashed(ctx, []keg.NodeId{a.ID, a.ID})
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.ErrorContains(t, err, "listed twice")
	exists, err := k.NodeExists(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, exists, "nothing was restored")
	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
}

func TestMergedSourceRestoresAsItWas(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	target, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Target\n")})
	require.NoError(t, err)
	source, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Source\n")})
	require.NoError(t, err)
	selfLinking := fmt.Sprintf("# Source\n\nSee [myself](../%d).\n", source.ID.ID)
	require.NoError(t, k.Repo.WriteContent(ctx, source.ID, []byte(selfLinking)))
	third, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Third\n\n[src](../%d)\n", source.ID.ID))})
	require.NoError(t, err)
	sourceView, err := k.ReadNode(ctx, source.ID)
	require.NoError(t, err)
	targetView, err := k.ReadNode(ctx, target.ID)
	require.NoError(t, err)

	_, err = k.MergeNodes(ctx, keg.MergeNodesOptions{
		Source: source.ID, ExpectedSourceHash: sourceView.Hash(),
		Target: keg.NodeUpdateOptions{ID: target.ID, Content: []byte("# Target\n\nMerged.\n"), HasContent: true, ExpectedContentHash: targetView.ContentHash()},
	})
	require.NoError(t, err)
	thirdView, err := k.ReadNode(ctx, third.ID)
	require.NoError(t, err)
	require.Equal(t, fmt.Sprintf("# Third\n\n[src](../%d)\n", target.ID.ID), string(thirdView.Content))

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{source.ID}))
	restored, err := k.ReadNode(ctx, source.ID)
	require.NoError(t, err)
	require.Equal(t, selfLinking, string(restored.Content))
}

func TestRetargetLinks(t *testing.T) {
	t.Parallel()
	got := keg.RetargetLinks([]byte("[a](../3) [b](../30) [c](../3#x)\n"), keg.NodeId{ID: 3}, keg.NodeId{ID: 7})
	require.Equal(t, "[a](../7) [b](../30) [c](../7#x)\n", string(got))
}

func TestReplaceTextLeavesRefusedNodesAloneAndDryRunPredictsThem(t *testing.T) {
	t.Parallel()
	k, ctx := newStrictBatchKeg(t)
	created, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "typed", Schema: "task", Body: []byte("# Typed\n\nfoo here\n"), Meta: []byte("type: task\n")},
		{Key: "untyped", Schema: "task", Body: []byte("# Untyped\n\nfoo there\n"), Meta: []byte("type: task\n")},
		{Key: "breaks", Schema: "task", Body: []byte("# Breaks\n\nfoo again\n"), Meta: []byte("type: task\nnote: foo\n")},
	})
	require.NoError(t, err)
	typed, untyped, breaks := created[0].ID, created[1].ID, created[2].ID
	// An untyped node, as one written before the keg turned strict is.
	require.NoError(t, k.Repo.WriteMeta(ctx, untyped, []byte("tags: [foo]\n")))
	// Metadata that breaks the schema once "foo" in it is replaced.
	require.NoError(t, k.Repo.WriteMeta(ctx, breaks, []byte("type: foo\n")))
	before := map[keg.NodeId]*keg.NodeView{}
	for _, id := range []keg.NodeId{typed, untyped, breaks} {
		view, err := k.ReadNode(ctx, id)
		require.NoError(t, err)
		before[id] = view
	}
	opts := keg.ReplaceTextOptions{Find: "foo", Replace: "bar", IncludeMeta: true, DryRun: true}
	errorsOf := func(r keg.ReplaceTextResult) map[keg.NodeId]string {
		out := map[keg.NodeId]string{}
		for _, n := range r.Nodes {
			out[n.ID] = n.Error
		}
		return out
	}

	dry, err := k.ReplaceText(ctx, opts)
	require.NoError(t, err)
	opts.DryRun = false
	done, err := k.ReplaceText(ctx, opts)
	require.NoError(t, err, "a node that would be refused does not fail the replace")
	require.Equal(t, errorsOf(dry), errorsOf(done), "the dry run predicts the same refusals")

	for _, n := range done.Nodes {
		after, err := k.ReadNode(ctx, n.ID)
		require.NoError(t, err)
		if n.Error == "" {
			require.NotEmpty(t, n.Hash)
			require.Contains(t, string(after.Content), "bar")
			continue
		}
		require.Empty(t, n.Hash)
		require.Equal(t, before[n.ID].Content, after.Content, "node %s is left alone", n.ID.Path())
		require.Equal(t, before[n.ID].Meta, after.Meta)
	}
	errs := errorsOf(done)
	require.Empty(t, errs[typed])
	require.NotEmpty(t, errs[breaks], "metadata that breaks the schema is refused")
	require.NotContains(t, errs[breaks], "invalid argument")
}

func TestReplaceTextEditsALegacyFrontmatterBody(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	legacy, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# T\n")})
	require.NoError(t, err)
	require.NoError(t, k.Repo.WriteContent(ctx, legacy.ID, []byte("---\na: 1\n---\n# T\n\nold word\n")))
	done, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "old word", Replace: "new word"})
	require.NoError(t, err)
	require.Len(t, done.Nodes, 1)
	require.Empty(t, done.Nodes[0].Error)
	got, err := k.ReadNode(ctx, legacy.ID)
	require.NoError(t, err)
	require.Equal(t, "---\na: 1\n---\n# T\n\nnew word\n", string(got.Content))
}

func TestPurgeLeavesLinksToALiveNodeAlone(t *testing.T) {
	t.Parallel()
	k, repo, ctx := newTrashingKeg(t)
	live, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Live\n")})
	require.NoError(t, err)
	linkerBody := fmt.Sprintf("# Linker\n\n[live](../%d)\n", live.ID.ID)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(linkerBody)})
	require.NoError(t, err)
	repo.plant(live.ID)

	result, err := k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: []keg.NodeId{live.ID}})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{live.ID}, result.Purged)
	require.Empty(t, result.Rewritten)
	got, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Equal(t, linkerBody, string(got.Content))
	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Empty(t, trash)
}

func TestPurgeTrashedReportsWhatItPurged(t *testing.T) {
	t.Parallel()
	k, repo, ctx := newTrashingKeg(t)
	remove := func(body string) keg.NodeId {
		created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(body)})
		require.NoError(t, err)
		view, err := k.ReadNode(ctx, created.ID)
		require.NoError(t, err)
		_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: created.ID, ExpectedHash: view.Hash()})
		require.NoError(t, err)
		return created.ID
	}
	a := remove("# A\n")
	b := remove("# B\n")
	linkerA, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# LA\n\n[a](../%d)\n", a.ID))})
	require.NoError(t, err)
	linkerB, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# LB\n\n[b](../%d)\n", b.ID))})
	require.NoError(t, err)

	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	repo.expire(a, now.Add(-time.Hour))
	repo.expire(b, now.Add(time.Hour))
	result, err := k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: []keg.NodeId{a, b}, ExpiredAt: &now})
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{a}, result.Purged, "only the expired entry goes")
	require.Equal(t, []keg.NodeId{linkerA.ID}, result.Rewritten)
	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{b}), "the unexpired one is still restorable")

	bView, err := k.ReadNode(ctx, b)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: b, ExpectedHash: bView.Hash()})
	require.NoError(t, err)
	c := remove("# C\n")
	linkerC, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# LC\n\n[c](../%d)\n", c.ID))})
	require.NoError(t, err)
	result, err = k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: []keg.NodeId{b, c, {ID: 999}}})
	require.NoError(t, err)
	require.ElementsMatch(t, []keg.NodeId{b, c}, result.Purged, "an id not in the trash is not reported purged")
	require.ElementsMatch(t, []keg.NodeId{linkerB.ID, linkerC.ID}, result.Rewritten)
	for _, id := range []keg.NodeId{linkerB.ID, linkerC.ID} {
		view, err := k.ReadNode(ctx, id)
		require.NoError(t, err)
		require.Contains(t, string(view.Content), "(../0)")
	}

	ids := make([]keg.NodeId, keg.MaxMutationBatchSize+1)
	for i := range ids {
		ids[i] = keg.NodeId{ID: 1000 + i}
	}
	_, err = k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: ids})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

// A replace is capped on every node it changes, refused ones included, and a
// refused node carries its error rather than its bodies.
func TestReplaceTextCapsRefusedNodesAndOmitsTheirBodies(t *testing.T) {
	t.Parallel()
	k, ctx := newStrictBatchKeg(t)
	var ids []keg.NodeId
	for len(ids) <= keg.MaxReplaceNodes {
		batch := make([]keg.NodeCreate, 0, keg.MaxMutationBatchSize)
		for i := 0; i < keg.MaxMutationBatchSize && len(ids)+len(batch) <= keg.MaxReplaceNodes; i++ {
			batch = append(batch, keg.NodeCreate{Key: fmt.Sprintf("n%d-%d", len(ids), i), Schema: "task", Body: []byte("# N\n\nfoo\n"), Meta: []byte("type: task\n")})
		}
		created, err := k.CreateNodes(ctx, batch)
		require.NoError(t, err)
		for _, c := range created {
			ids = append(ids, c.ID)
		}
	}
	// Untyped, as nodes written before the keg turned strict are: a strict
	// keg refuses to edit them without a schema.
	for _, id := range ids {
		require.NoError(t, k.Repo.WriteMeta(ctx, id, []byte("tags: [x]\n")))
	}
	_, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "foo", Replace: "bar", DryRun: true})
	require.ErrorIs(t, err, keg.ErrInvalid, "%d refused nodes still count against the cap", len(ids))

	small, smallCtx := newStrictBatchKeg(t)
	created, err := small.CreateNodes(smallCtx, []keg.NodeCreate{{Key: "a", Schema: "task", Body: []byte("# N\n\nfoo\n"), Meta: []byte("type: task\n")}})
	require.NoError(t, err)
	require.NoError(t, small.Repo.WriteMeta(smallCtx, created[0].ID, []byte("tags: [x]\n")))
	few, err := small.ReplaceText(smallCtx, keg.ReplaceTextOptions{Find: "foo", Replace: "bar", DryRun: true})
	require.NoError(t, err)
	require.Len(t, few.Nodes, 1)
	n := few.Nodes[0]
	require.NotEmpty(t, n.Error)
	require.Equal(t, 1, n.Matches)
	require.Empty(t, n.ContentBefore)
	require.Empty(t, n.ContentAfter)
	require.Empty(t, n.MetaBefore)
	require.Empty(t, n.MetaAfter)
}

// archiveOf builds a keg archive whose nodes sit at ids 1..len(bodies), each
// with its body, in a keg of its own.
func archiveOf(t *testing.T, bodies ...string) []byte {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	ids := make([]keg.NodeId, len(bodies))
	for i, body := range bodies {
		created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(body)})
		require.NoError(t, err)
		require.Equal(t, i+1, created.ID.ID)
		ids[i] = created.ID
	}
	archive, err := k.ExportNodes(ctx, keg.ExportNodesOptions{NodeIDs: ids})
	require.NoError(t, err)
	data, err := io.ReadAll(archive)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	return data
}

// archiveAt builds a keg archive holding one node at id with body.
func archiveAt(t *testing.T, id int, body string) []byte {
	t.Helper()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	var last keg.NodeId
	for last.ID < id {
		created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(body)})
		require.NoError(t, err)
		last = created.ID
	}
	archive, err := k.ExportNodes(ctx, keg.ExportNodesOptions{NodeIDs: []keg.NodeId{last}})
	require.NoError(t, err)
	data, err := io.ReadAll(archive)
	require.NoError(t, err)
	require.NoError(t, archive.Close())
	return data
}

func trashNode(t *testing.T, ctx context.Context, k *keg.LocalKeg, id keg.NodeId) {
	t.Helper()
	view, err := k.ReadNode(ctx, id)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: id, ExpectedHash: view.Hash()})
	require.NoError(t, err)
}

func onlyTrashEntry(t *testing.T, ctx context.Context, k *keg.LocalKeg) keg.TrashEntry {
	t.Helper()
	trash, err := k.ListTrash(ctx)
	require.NoError(t, err)
	require.Len(t, trash, 1)
	return trash[0]
}

// A trashed node displaced twice keeps its original id on record and
// restores at its current id: the live node at its original id is never
// touched.
func TestTwiceDisplacedEntryRestoresAtItsCurrentID(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	first, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Trashed\n\nOriginal.\n")})
	require.NoError(t, err)
	require.Equal(t, 1, first.ID.ID)
	trashNode(t, ctx, k, first.ID)

	_, err = k.ImportNodes(ctx, bytes.NewReader(archiveAt(t, 1, "# Archived one\n")), keg.ImportNodesOptions{})
	require.NoError(t, err)
	entry := onlyTrashEntry(t, ctx, k)
	require.Equal(t, first.ID, *entry.DisplacedFrom)

	_, err = k.ImportNodes(ctx, bytes.NewReader(archiveAt(t, entry.ID.ID, "# Archived other\n")), keg.ImportNodesOptions{})
	require.NoError(t, err)
	moved := onlyTrashEntry(t, ctx, k)
	require.NotEqual(t, entry.ID, moved.ID)
	require.Equal(t, first.ID, *moved.DisplacedFrom, "the original id is kept")

	live, err := k.ReadNode(ctx, first.ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{ID: first.ID, Content: []byte("# Archived one\n\nEdited.\n"), HasContent: true, ExpectedContentHash: live.ContentHash()}})
	require.NoError(t, err)

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{moved.ID}))
	back, err := k.ReadNode(ctx, moved.ID)
	require.NoError(t, err)
	require.Equal(t, "# Trashed\n\nOriginal.\n", string(back.Content))
	edited, err := k.ReadNode(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, "# Archived one\n\nEdited.\n", string(edited.Content), "the live node is untouched")
}

// An archive whose ids run past the counter: the trashed node is displaced
// to an id the archive does not use, and the imported nodes stay as
// imported.
func TestDisplacementSkipsTheArchivesOwnIDs(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	first, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Trashed\n")})
	require.NoError(t, err)
	_, err = k.Create(ctx, &keg.CreateOptions{Body: []byte("# Two\n")})
	require.NoError(t, err)
	trashNode(t, ctx, k, first.ID)

	_, err = k.ImportNodes(ctx, bytes.NewReader(archiveOf(t, "# A1\n", "# A2\n", "# A3\n", "# A4\n")), keg.ImportNodesOptions{})
	require.NoError(t, err)
	entry := onlyTrashEntry(t, ctx, k)
	require.Greater(t, entry.ID.ID, 4, "the entry is not given one of the archive's ids")
	require.Equal(t, first.ID, *entry.DisplacedFrom)

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{entry.ID}))
	back, err := k.ReadNode(ctx, entry.ID)
	require.NoError(t, err)
	require.Equal(t, "# Trashed\n", string(back.Content))
	for i, want := range []string{"# A1\n", "# A2\n", "# A3\n", "# A4\n"} {
		got, err := k.ReadNode(ctx, keg.NodeId{ID: i + 1})
		require.NoError(t, err)
		require.Equal(t, want, string(got.Content))
	}
}

// A trash restore never overwrites a live node: with a node at the entry's
// id, it is refused and nothing changes.
func TestRestoreOntoALiveNodeIsRefused(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	gone, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Gone\n")})
	require.NoError(t, err)
	trashNode(t, ctx, k, gone.ID)
	// A node at the trashed id, as legacy data or a direct write leaves one.
	require.NoError(t, k.Repo.WriteContent(ctx, gone.ID, []byte("# Squatter\n")))
	require.NoError(t, k.Repo.WriteMeta(ctx, gone.ID, []byte("{}\n")))

	err = k.RestoreTrashed(ctx, []keg.NodeId{gone.ID})
	require.ErrorIs(t, err, keg.ErrDestinationExists)
	got, err := k.ReadNode(ctx, gone.ID)
	require.NoError(t, err)
	require.Equal(t, "# Squatter\n", string(got.Content))
	require.Equal(t, gone.ID, onlyTrashEntry(t, ctx, k).ID)
}

// A replace measures what it would build before building it: a node that
// would grow too large, or has too many matches, is left alone with an
// error, and a replace that would grow every node together too large fails
// before writing any.
func TestReplaceTextBoundsWhatItBuilds(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	read := func(id keg.NodeId) string {
		view, err := k.ReadNode(ctx, id)
		require.NoError(t, err)
		return string(view.Content)
	}

	small, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Small\n\n" + strings.Repeat("word ", 40) + "\n")})
	require.NoError(t, err)
	wide := strings.Repeat("W", 64<<10)
	for _, dryRun := range []bool{true, false} {
		// Matching the empty string matches at every position.
		got, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "x*", Replace: wide, Regex: true, DryRun: dryRun})
		require.NoError(t, err)
		require.NotEmpty(t, got.Nodes)
		for _, node := range got.Nodes {
			require.Contains(t, node.Error, "MiB", "node %s", node.ID.Path())
			require.Empty(t, node.ContentAfter)
		}
		require.NotContains(t, read(small.ID), "WWW", "the oversized note is left alone")
	}

	many, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Many\n\n" + strings.Repeat("q", keg.MaxReplaceNodeMatches+1) + "\n")})
	require.NoError(t, err)
	got, err := k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "q", Replace: "r"})
	require.NoError(t, err)
	require.Len(t, got.Nodes, 1)
	require.Contains(t, got.Nodes[0].Error, "matches")
	require.Contains(t, read(many.ID), "qqq")

	perNode := keg.MaxReplaceNodeBytes / 2
	var ids []keg.NodeId
	for written := 0; written <= keg.MaxReplaceTotalBytes; written += perNode {
		created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Big\n\nZZ\n")})
		require.NoError(t, err)
		ids = append(ids, created.ID)
	}
	_, err = k.ReplaceText(ctx, keg.ReplaceTextOptions{Find: "ZZ", Replace: strings.Repeat("z", perNode)})
	require.ErrorIs(t, err, keg.ErrInvalid)
	for _, id := range ids {
		require.Contains(t, read(id), "ZZ", "a replace too large in all writes nothing")
	}
}

// readCountingRepo is a trash that counts reads of each node's content.
type readCountingRepo struct {
	*trashingRepo
	countMu sync.Mutex
	reads   map[keg.NodeId]int
}

func (r *readCountingRepo) ReadContent(ctx context.Context, id keg.NodeId) ([]byte, error) {
	r.countMu.Lock()
	r.reads[id]++
	r.countMu.Unlock()
	return r.trashingRepo.ReadContent(ctx, id)
}

func (r *readCountingRepo) resetReads() {
	r.countMu.Lock()
	defer r.countMu.Unlock()
	r.reads = map[keg.NodeId]int{}
}

func (r *readCountingRepo) readsOf(id keg.NodeId) int {
	r.countMu.Lock()
	defer r.countMu.Unlock()
	return r.reads[id]
}

// Restoring a batch from the trash costs the nodes restored, not the whole
// keg: no other node's content is read, yet the indexes come out as a full
// rebuild would leave them, with the links other nodes hold to the restored
// ones back in place.
func TestRestoreTrashedReadsOnlyWhatItRestores(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	repo := &readCountingRepo{trashingRepo: &trashingRepo{MemoryRepository: newTestMemoryRepo(fx.Runtime()), trash: map[keg.NodeId]trashedNode{}}, reads: map[keg.NodeId]int{}}
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	var bystanders []keg.NodeId
	for i := range 20 {
		created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Bystander %d\n", i))})
		require.NoError(t, err)
		bystanders = append(bystanders, created.ID)
	}
	first, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# First\n")})
	require.NoError(t, err)
	second, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Second\n\nSee [first](../%d).\n", first.ID.ID))})
	require.NoError(t, err)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Linker\n\n[first](../%d) and [second](../%d)\n", first.ID.ID, second.ID.ID))})
	require.NoError(t, err)
	trashNode(t, ctx, k, first.ID)
	trashNode(t, ctx, k, second.ID)

	repo.resetReads()
	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{first.ID, second.ID}))
	for _, id := range bystanders {
		require.Zero(t, repo.readsOf(id), "restoring read bystander %s", id.Path())
	}

	dex, err := k.Dex(ctx)
	require.NoError(t, err)
	backlinks, _ := dex.Backlinks(ctx, first.ID)
	require.ElementsMatch(t, []keg.NodeId{second.ID, linker.ID}, backlinks)
	backlinks, _ = dex.Backlinks(ctx, second.ID)
	require.ElementsMatch(t, []keg.NodeId{linker.ID}, backlinks)

	restored := map[string][]byte{}
	for _, name := range []string{"links", "backlinks", "nodes.tsv", "tags"} {
		data, err := k.ReadIndex(ctx, name)
		require.NoError(t, err)
		restored[name] = data
	}
	require.NoError(t, k.Index(ctx, keg.IndexOptions{}))
	for name, data := range restored {
		rebuilt, err := k.ReadIndex(ctx, name)
		require.NoError(t, err)
		require.Equal(t, string(rebuilt), string(data), "index %s after a restore differs from a rebuild", name)
	}
}

// A restored node's links to a node purged while it sat in the trash point
// at node 0, as purging pointed the live nodes' links; links to a live node
// or to another trashed node are kept.
func TestRestoreTrashedPointsLinksToPurgedNodesAtZero(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	live, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Live\n")})
	require.NoError(t, err)
	purged, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Purged\n")})
	require.NoError(t, err)
	sleeping, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Sleeping\n")})
	require.NoError(t, err)
	body := fmt.Sprintf("# Back\n\n[live](../%d) [purged](../%d) [sleeping](../%d)\n", live.ID.ID, purged.ID.ID, sleeping.ID.ID)
	back, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(body)})
	require.NoError(t, err)

	trashNode(t, ctx, k, back.ID)
	trashNode(t, ctx, k, purged.ID)
	trashNode(t, ctx, k, sleeping.ID)
	_, err = k.PurgeTrashed(ctx, keg.PurgeTrashOptions{IDs: []keg.NodeId{purged.ID}})
	require.NoError(t, err)

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{back.ID}))
	view, err := k.ReadNode(ctx, back.ID)
	require.NoError(t, err)
	want := fmt.Sprintf("# Back\n\n[live](../%d) [purged](../0) [sleeping](../%d)\n", live.ID.ID, sleeping.ID.ID)
	require.Equal(t, want, string(view.Content))
}

// limitedTrashRepo is a trash that holds bundles up to limit bytes.
type limitedTrashRepo struct {
	*trashingRepo
	limit int
}

func (r *limitedTrashRepo) TrashBundleLimit() int { return r.limit }

// A node too large for the trash is refused with a clear error and stays
// where it is; the rest of a removal batch is untouched.
func TestTrashRefusesANodeTooLargeForIt(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	repo := &limitedTrashRepo{trashingRepo: &trashingRepo{MemoryRepository: newTestMemoryRepo(fx.Runtime()), trash: map[keg.NodeId]trashedNode{}}, limit: 64 << 10}
	k := keg.NewLocalKeg(repo, fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)

	small, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Small\n")})
	require.NoError(t, err)
	big, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Big\n")})
	require.NoError(t, err)
	// Random bytes do not compress, so the bundle passes the limit.
	payload := make([]byte, 256<<10)
	_, err = rand.Read(payload)
	require.NoError(t, err)
	require.NoError(t, k.WriteFile(ctx, big.ID, "blob.bin", payload))

	view, err := k.ReadNode(ctx, big.ID)
	require.NoError(t, err)
	_, err = k.Remove(ctx, keg.NodeRemoveOptions{ID: big.ID, ExpectedHash: view.Hash()})
	require.ErrorIs(t, err, keg.ErrTrashBundleTooLarge)
	require.ErrorIs(t, err, keg.ErrInvalid)
	require.Contains(t, err.Error(), "cannot go to the trash")
	exists, err := k.NodeExists(ctx, big.ID)
	require.NoError(t, err)
	require.True(t, exists, "a node refused by the trash stays live")

	result, err := k.TrashNodes(ctx, []keg.NodeId{small.ID, big.ID}, keg.TrashReasonArchiveRestore)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{small.ID}, result.Trashed)
	require.Equal(t, []keg.NodeId{big.ID}, result.TooLarge)
	entry := onlyTrashEntry(t, ctx, k)
	require.Equal(t, small.ID, entry.ID)
	require.Equal(t, keg.TrashReasonArchiveRestore, entry.Reason)
}

// TrashNodes trashes live nodes for a reason of its own, skips ids with no
// node, refuses node 0 and a merge reason, and leaves links alone.
func TestTrashNodesKeepsNodesRestorable(t *testing.T) {
	t.Parallel()
	k, _, ctx := newTrashingKeg(t)
	gone, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Gone\n")})
	require.NoError(t, err)
	linker, err := k.Create(ctx, &keg.CreateOptions{Body: []byte(fmt.Sprintf("# Linker\n\n[gone](../%d)\n", gone.ID.ID))})
	require.NoError(t, err)

	_, err = k.TrashNodes(ctx, []keg.NodeId{gone.ID}, keg.TrashReasonMerge)
	require.ErrorIs(t, err, keg.ErrInvalid)
	_, err = k.TrashNodes(ctx, []keg.NodeId{{ID: 0}}, keg.TrashReasonArchiveRestore)
	require.ErrorIs(t, err, keg.ErrInvalid)

	result, err := k.TrashNodes(ctx, []keg.NodeId{gone.ID, {ID: 9999}}, keg.TrashReasonArchiveRestore)
	require.NoError(t, err)
	require.Equal(t, []keg.NodeId{gone.ID}, result.Trashed)
	exists, err := k.NodeExists(ctx, gone.ID)
	require.NoError(t, err)
	require.False(t, exists)
	linking, err := k.ReadNode(ctx, linker.ID)
	require.NoError(t, err)
	require.Contains(t, string(linking.Content), fmt.Sprintf("../%d)", gone.ID.ID))
	dex, err := k.Dex(ctx)
	require.NoError(t, err)
	require.Nil(t, dex.GetRef(ctx, gone.ID), "a trashed node leaves the dex")

	require.NoError(t, k.RestoreTrashed(ctx, []keg.NodeId{gone.ID}))
	exists, err = k.NodeExists(ctx, gone.ID)
	require.NoError(t, err)
	require.True(t, exists)
}
