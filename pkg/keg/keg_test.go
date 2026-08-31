package keg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/internal/testkegrepo"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type externalMemoryRepo struct {
	*testkegrepo.MemoryRepository
}

func (r *externalMemoryRepo) Name() string {
	return "external-fs"
}

// TestInitWhenRepoExists verifies a second Init reports ErrExist.
func TestInitWhenRepoExists(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	k := kegpkg.NewLocalKeg(newTestMemoryRepo(f.Runtime()), f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	err := k.Init(f.Context())
	require.Error(t, err)
	require.Truef(
		t,
		errors.Is(err, kegpkg.ErrExist),
		"InitKeg expected ErrExist, got: %v", err,
	)
}

// TestInitOnEmptyRepo initializes a new keg in an empty fixture repo and
// verifies the repository reports an initialized keg and a zero node exists.
func TestInitOnEmptyRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	initNonStrictTestKeg(t, k, f.Context())

	cfg, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.Equal(t, f.Now().Format(time.RFC3339), cfg.Updated)

	// Repo should now report a keg exists.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), k.(*kegpkg.LocalKeg).Repo)
	require.NoError(t, err, "KegExists returned error")
	require.True(t, exists, "KegExists expected true after InitKeg")

	// Ensure a zero node is present.
	ids, err := k.(*kegpkg.LocalKeg).Repo.ListNodes(f.Context())
	require.NoError(t, err, "ListNodes failed")
	foundZero := false
	for _, n := range ids {
		if n.ID == 0 {
			foundZero = true
			break
		}
	}
	require.True(t, foundZero, "expected zero node to exist after InitKeg")
}

func TestKegExistsWithMemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repofs"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	// Uninitialized on disk.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), k.(*kegpkg.LocalKeg).Repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for empty fs repo")

	// Initialize and verify.
	initNonStrictTestKeg(t, k, f.Context())

	exists, err = kegpkg.RepoContainsKeg(f.Context(), k.(*kegpkg.LocalKeg).Repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after InitKeg")
}

// Additional tests

// TestCreateZeroNodeInMemoryRepository verifies creating the zero node via
// Create on a fresh repository.
func TestCreateZeroNodeInMemoryRepository(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	b, err := k.GetContent(f.Context(), kegpkg.NodeId{ID: 0})
	require.NoError(t, err)
	require.Contains(t, string(b), "Sorry, planned but not yet available")
}

// TestCreateNodeWithMeta ensures non-zero nodes created with options write
// sensible content and meta that can be parsed.
func TestCreateNodeWithMeta(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	opts := &kegpkg.CreateOptions{
		Title: "MyTitle",
		Lead:  "short lead",
		Tags:  []string{"TagA", "tag-a"},
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID.ID, "expected created node id to be 1")

	content, err := k.GetContent(f.Context(), id.ID)
	require.NoError(t, err)
	require.Contains(t, string(content), "# MyTitle")

	stats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, "short lead", stats.Lead())
	// normalized tags should include "tag-a"
	m, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	foundTag := slices.Contains(m.Tags(), "tag-a")
	require.True(t, foundTag, "expected normalized tag 'tag-a' to be present")
}

// New test: create where Body is provided in the Create options. Ensure the
// provided body becomes the node content and meta is parsed from it.
func TestCreateWithBody(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	body := []byte("# BodyTitle\n\nbody paragraph\n")
	opts := &kegpkg.CreateOptions{
		Body: body,
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID.ID, "expected created node id to be 1")

	got, err := k.GetContent(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, string(body), string(got))

	stats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, "BodyTitle", stats.Title())
	require.Equal(t, "body paragraph", stats.Lead())
}

// New test: Body contains YAML frontmatter. Ensure content written equals the
// provided bytes and parsed meta reflects the markdown heading and lead.
func TestCreateWithBodyFrontmatter(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	rawBody := []byte(`---
tags:
  - fm
foo: bar
---
# FMTitle

fm lead paragraph
`)
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Body: rawBody})
	require.NoError(t, err)
	require.Equal(t, 1, id.ID.ID, "expected created node id to be 1")

	got, err := k.GetContent(f.Context(), id.ID)
	content, _ := kegpkg.ParseContent(f.Runtime(), rawBody, kegpkg.FormatMarkdown)
	require.NoError(t, err)
	require.Equal(t, content.Body, string(got))

	m, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)

	// Title should be derived from the first H1 in the markdown body.
	stats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, "FMTitle", stats.Title())
	require.Equal(t, "fm lead paragraph", stats.Lead())
	require.Contains(t, m.Tags(), "fm")
	require.Contains(t, m.ToYAML(), "foo: bar")
}

// TestSetContentAndUpdate ensures SetContent causes meta to be updated from
// parsed content (for example lead paragraph changes).
func TestSetContentAndUpdate(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	// create zero and a second node
	_, err := k.Create(f.Context(), nil)
	require.NoError(t, err)

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Initial"})
	require.NoError(t, err)

	// change content to include a new lead paragraph
	newContent := []byte("# Initial\n\nupdated lead paragraph\n")
	require.NoError(t, k.SetContent(f.Context(), id.ID, newContent))

	stats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, "updated lead paragraph", stats.Lead())
}

// TestCreateAndUpdateNodesWithMemoryRepository uses the filesystem repo to create a
// node, ensures the dex contains the node, updates content, and validates
// meta and dex timestamps reflect the update.
func TestCreateAndUpdateNodesWithMemoryRepository(t *testing.T) {
	t.Parallel()
	// Use the empty fixture as a filesystem-backed repo.
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_fs"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repofs_fs"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	// Initialize on disk.
	initNonStrictTestKeg(t, k, f.Context())

	// Create a new node with title and lead.
	opts := &kegpkg.CreateOptions{
		Title: "FSNode",
		Lead:  "lead fs",
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID.ID, "expected created node id to be 1")

	// Dex should expose the node entry.
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	ref := dex.GetRef(f.Context(), id.ID)
	require.NotNil(t, ref, "dex should contain created node")
	require.Equal(t, id.ID.Path(), ref.ID)

	// Ensure zero node is present in dex as well.
	zeroRef := dex.GetRef(f.Context(), kegpkg.NodeId{ID: 0})
	require.NotNil(t, zeroRef, "dex should contain zero node")

	createdUpdated := ref.Updated

	// Advance clock so updated timestamp will differ after update.
	f.Advance(2 * time.Minute)
	// Update content to change the lead.
	newContent := []byte("# FSNode\n\nnew lead from fs\n")
	require.NoError(t, k.SetContent(f.Context(), id.ID, newContent))

	// NodeMeta should reflect the new lead.
	stats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, "new lead from fs", stats.Lead())

	// Re-acquire dex since SetContent invalidates the cached dex.
	dex, err = k.Dex(f.Context())
	require.NoError(t, err)

	// Dex entry should have a newer updated timestamp.
	ref2 := dex.GetRef(f.Context(), id.ID)
	require.NotNil(t, ref2)
	require.True(t, ref2.Updated.After(createdUpdated),
		"expected dex updated timestamp to advance after content update")
}

// New test: create multiple nodes with tags and interlinks, and validate
// the generated indexes reflect tags, links, and backlinks.
func TestNodesWithTagsAndInterlinks(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	// Create node A with tags
	optsA := &kegpkg.CreateOptions{
		Title: "NodeA",
		Lead:  "lead a",
		Tags:  []string{"Alpha", "Shared"},
	}
	idA, err := k.Create(f.Context(), optsA)
	require.NoError(t, err)
	require.Equal(t, 1, idA.ID.ID)

	// Create node B with tags
	optsB := &kegpkg.CreateOptions{
		Title: "NodeB",
		Lead:  "lead b",
		Tags:  []string{"Beta", "Shared"},
	}
	idB, err := k.Create(f.Context(), optsB)
	require.NoError(t, err)
	require.Equal(t, 2, idB.ID.ID)

	// Update content so nodes link to each other using ../N links.
	contentA := []byte("# NodeA\n\nSee NodeB: [B](../2)\n")
	require.NoError(t, k.SetContent(f.Context(), idA.ID, contentA))

	contentB := []byte("# NodeB\n\nSee NodeA: [A](../1)\n")
	require.NoError(t, k.SetContent(f.Context(), idB.ID, contentB))

	// Load dex and verify in-memory indexes.
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	// Tag list should include normalized "shared".
	tags := dex.TagList(f.Context())
	require.Contains(t, tags, "shared")

	// Tags index file should exist and reference both nodes.
	tagsData, err := k.Repo.GetIndex(f.Context(), "tags")
	require.NoError(t, err)
	ts := string(tagsData)
	require.Contains(t, ts, "shared\t")
	require.Contains(t, ts, "1")
	require.Contains(t, ts, "2")

	// Links index should contain mutual links 1 -> 2 and 2 -> 1.
	linksData, err := k.Repo.GetIndex(f.Context(), "links")
	require.NoError(t, err)
	ls := string(linksData)
	require.Contains(t, ls, "1\t2")
	require.Contains(t, ls, "2\t1")

	// Backlinks index should show the inverse mappings.
	backlinksData, err := k.Repo.GetIndex(f.Context(), "backlinks")
	require.NoError(t, err)
	bs := string(backlinksData)
	require.Contains(t, bs, "2\t1")
	require.Contains(t, bs, "1\t2")

	// In-memory link lookups should reflect outgoing and incoming links.
	outA, ok := dex.Links(f.Context(), idA.ID)
	require.True(t, ok)
	require.Equal(t, 1, len(outA))
	require.Equal(t, idB.ID.ID, outA[0].ID)

	inB, ok := dex.Backlinks(f.Context(), idB.ID)
	require.True(t, ok)
	require.Equal(t, 1, len(inB))
	require.Equal(t, idA.ID.ID, inB[0].ID)
}

func TestMarkdownLinkCreatesBacklinkWhileBareKegProseDoesNot(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	one, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	two, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)
	three, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Three"})
	require.NoError(t, err)

	require.NoError(t, k.SetContent(f.Context(), one.ID, []byte(
		"# One\n\n[Two](../2) is a graph link. Bare keg:example/3 is prose.\n",
	)))
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	backlinks, ok := dex.Backlinks(f.Context(), two.ID)
	require.True(t, ok)
	require.Equal(t, []kegpkg.NodeId{one.ID}, backlinks)
	_, ok = dex.Backlinks(f.Context(), three.ID)
	require.False(t, ok, "bare keg: prose must not create a backlink")
}

func TestIndex_PreservesUnknownConfigFields(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_config"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repofs_config"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")
	initNonStrictTestKeg(t, k, f.Context())

	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Settings Field Preservation"})
	require.NoError(t, err)

	customSettings := []byte(`kegv: "2025-07"
updated: "2020-01-01T00:00:00Z"
title: "custom settings"
summary: "contains unknown fields"
custom_block:
  keep_me: true
  nested:
    item: value
`)
	require.NoError(t, f.Runtime().WriteFile("repofs_config/keg", customSettings, 0o644))

	require.NoError(t, k.Index(f.Context(), kegpkg.IndexOptions{}))

	raw, err := f.Runtime().ReadFile("repofs_config/keg")
	require.NoError(t, err)
	out := string(raw)
	require.Contains(t, out, "custom_block:")
	require.Contains(t, out, "keep_me: true")
	require.Contains(t, out, "nested:")
	require.Contains(t, out, "item: value")

	cfg, err := k.(*kegpkg.LocalKeg).Repo.ReadSettings(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, "2020-01-01T00:00:00Z", cfg.Updated)
}

func TestMove_RewritesLinksAndUpdatesDex(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id1, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	require.Equal(t, 1, id1.ID.ID)

	id2, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)
	require.Equal(t, 2, id2.ID.ID)

	// Add canonical and bare links to node 2.
	require.NoError(t, k.SetContent(f.Context(), id1.ID, []byte("# One\n\nSee [two](../2).\nAlso ../2.\n")))

	require.NoError(t, errOnly(k.Move(f.Context(), moveOptions(t, f.Context(), k, kegpkg.NodeId{ID: 2}, kegpkg.NodeId{ID: 3}))))

	exists, err := k.Repo.HasNode(f.Context(), kegpkg.NodeId{ID: 2})
	require.NoError(t, err)
	require.False(t, exists, "source node should be moved away")

	exists, err = k.Repo.HasNode(f.Context(), kegpkg.NodeId{ID: 3})
	require.NoError(t, err)
	require.True(t, exists, "destination node should exist")

	updatedContent, err := k.GetContent(f.Context(), kegpkg.NodeId{ID: 1})
	require.NoError(t, err)
	require.Contains(t, string(updatedContent), "[two](../3)")
	require.Contains(t, string(updatedContent), "../3.")
	require.NotContains(t, string(updatedContent), "../2")

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	links, ok := dex.Links(f.Context(), kegpkg.NodeId{ID: 1})
	require.True(t, ok, "node 1 should have outgoing links")
	require.Len(t, links, 1)
	require.Equal(t, 3, links[0].ID)

	backlinks, ok := dex.Backlinks(f.Context(), kegpkg.NodeId{ID: 3})
	require.True(t, ok, "node 3 should have backlinks")
	require.Len(t, backlinks, 1)
	require.Equal(t, 1, backlinks[0].ID)
}

func TestMove_DestinationExists(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	_, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)
	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Three"})
	require.NoError(t, err)

	_, err = k.Move(f.Context(), moveOptions(t, f.Context(), k, kegpkg.NodeId{ID: 2}, kegpkg.NodeId{ID: 3}))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrDestinationExists)
}

func TestRemove_DeletesNodeAndUpdatesDex(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id1, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	id2, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)

	require.NoError(t, k.SetContent(f.Context(), id1.ID, []byte("# One\n\nSee [two](../2).\n")))

	require.NoError(t, errOnly(k.Remove(f.Context(), removeOptions(t, f.Context(), k, id2.ID))))

	exists, err := k.Repo.HasNode(f.Context(), id2.ID)
	require.NoError(t, err)
	require.False(t, exists, "node should be deleted from repository")

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	_, ok := dex.Links(f.Context(), id2.ID)
	require.False(t, ok, "deleted node should be absent from links index")

	_, ok = dex.Backlinks(f.Context(), id2.ID)
	require.False(t, ok, "deleted node should be absent from backlinks index")

	node1Links, ok := dex.Links(f.Context(), id1.ID)
	if ok {
		require.Len(t, node1Links, 0, "links to deleted node should be removed")
	}
}

// TestSetContent_OnRemovedNode verifies that SetContent fails with ErrNotExist
// when the node has been removed, preventing resurrection of deleted nodes.
// Reproduction test for bug 325/326.
func TestSetContent_OnRemovedNode(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Doomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), removeOptions(t, f.Context(), k, id.ID))))

	// Attempt to write content to the removed node should fail.
	err = k.SetContent(f.Context(), id.ID, []byte("# Resurrected\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)
}

func TestRemove_NotFound(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	_, err := k.Remove(f.Context(), kegpkg.NodeRemoveOptions{ID: kegpkg.NodeId{ID: 4242}, ExpectedHash: "missing"})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)
}

// TestSetMeta_PreservesLinksInDex verifies that calling SetMeta followed by
// SetContent with unchanged body does not lose link index entries.
// Reproduction test for bug 261 sub-issue 1.
func TestSetMeta_PreservesLinksInDex(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	// Create two nodes
	id1, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Source"})
	require.NoError(t, err)
	id2, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Target"})
	require.NoError(t, err)

	// Set content with a link from node 1 to node 2
	body := []byte("# Source\n\nSee [target](../2)\n")
	require.NoError(t, k.SetContent(f.Context(), id1.ID, body))

	// Verify links exist
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)
	links, ok := dex.Links(f.Context(), id1.ID)
	require.True(t, ok, "node 1 should have outgoing links after SetContent")
	require.Len(t, links, 1)
	require.Equal(t, id2.ID.ID, links[0].ID)

	// Now simulate what tap edit does: SetMeta then SetContent with same body
	meta, err := k.GetMeta(f.Context(), id1.ID)
	require.NoError(t, err)
	meta.SetTags([]string{"new-tag"})
	require.NoError(t, k.SetMeta(f.Context(), id1.ID, meta))

	// SetContent with unchanged body -- should not lose links
	require.NoError(t, k.SetContent(f.Context(), id1.ID, body))

	// Links should still be present
	links, ok = dex.Links(f.Context(), id1.ID)
	require.True(t, ok, "links should survive SetMeta + SetContent no-op")
	require.Len(t, links, 1)
	require.Equal(t, id2.ID.ID, links[0].ID)
}

// TestIndex_ContentOnlyNodeGetsIndexed verifies that a node with only
// README.md (no meta.yaml, no stats.json) is still included in the index
// after a rebuild.
func TestIndex_ContentOnlyNodeGetsIndexed(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	// Write content directly without meta or stats.
	bareID := kegpkg.NodeId{ID: 42}
	require.NoError(t, repo.WriteContent(f.Context(), bareID, []byte("# Bare Node\n\nNo meta or stats.\n")))

	// Rebuild index — should not skip this node.
	err := k.Index(f.Context(), kegpkg.IndexOptions{})
	require.NoError(t, err)

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	ref := dex.GetRef(f.Context(), bareID)
	require.NotNil(t, ref, "content-only node should appear in the index")
	require.Equal(t, "42", ref.ID)
	require.Equal(t, "Bare Node", ref.Title)
}

// TestIndex_MalformedMetaNodeGetsIndexed verifies that a node whose meta.yaml
// is malformed YAML still gets indexed (the content is used for title/lead).
func TestIndex_MalformedMetaNodeGetsIndexed(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	// Create a node normally first, then corrupt its meta.
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Good Node"})
	require.NoError(t, err)

	// Overwrite meta with invalid YAML.
	require.NoError(t, repo.WriteMeta(f.Context(), id.ID, []byte("{{{invalid yaml")))

	// Rebuild — the node should still appear.
	err = k.Index(f.Context(), kegpkg.IndexOptions{})
	// Index may return aggregated errors for the malformed meta, but the node
	// should still be in the dex.
	_ = err

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	ref := dex.GetRef(f.Context(), id.ID)
	require.NotNil(t, ref, "node with malformed meta should still appear in index")
	require.Equal(t, "Good Node", ref.Title)
}

// TestSetContent_NoChangeSkipsDexAndConfig verifies that calling SetContent
// with identical content does not modify the dex or keg settings timestamp.
func TestSetContent_NoChangeSkipsDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_noop"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_noop"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	body := []byte("# NoOp Node\n\nOriginal content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Body: body})
	require.NoError(t, err)

	// Record keg settings updated timestamp after create.
	cfg1, err := k.Settings(f.Context())
	require.NoError(t, err)
	updatedAfterCreate := cfg1.Updated

	// Advance clock so any new timestamp would differ.
	f.Advance(5 * time.Minute)

	// SetContent with identical bytes — should be a no-op.
	require.NoError(t, k.SetContent(f.Context(), id.ID, body))

	// Settings timestamp should not have changed.
	cfg2, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.Equal(t, updatedAfterCreate, cfg2.Updated,
		"keg settings updated timestamp should not change when content is unchanged")
}

// TestSetMeta_NoChangeSkipsDexAndConfig verifies that calling SetMeta
// with identical metadata does not modify the dex or keg settings timestamp.
func TestSetMeta_NoChangeSkipsDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_meta_noop"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_meta_noop"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Meta NoOp",
		Tags:  []string{"test"},
	})
	require.NoError(t, err)

	// Normalize on-disk meta format by doing one round-trip through
	// GetMeta/SetMeta. This ensures the on-disk bytes match the
	// ToYAML() output format used by SetMeta's comparison.
	meta, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id.ID, meta))

	// Record keg settings updated timestamp after normalization.
	cfg1, err := k.Settings(f.Context())
	require.NoError(t, err)
	updatedAfterNormalize := cfg1.Updated

	// Advance clock so any new timestamp would differ.
	f.Advance(5 * time.Minute)

	// Read existing meta and set it back unchanged — this should be a no-op.
	meta, err = k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id.ID, meta))

	// Settings timestamp should not have changed.
	cfg2, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.Equal(t, updatedAfterNormalize, cfg2.Updated,
		"keg settings updated timestamp should not change when meta is unchanged")
}

// TestSetMeta_WithChangeUpdatesDexAndConfig verifies that calling SetMeta
// with different metadata does update the dex and keg settings timestamp.
func TestSetMeta_WithChangeUpdatesDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_meta_change"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_meta_change"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Meta Change",
		Tags:  []string{"old-tag"},
	})
	require.NoError(t, err)

	// Record keg settings updated timestamp after create.
	cfg1, err := k.Settings(f.Context())
	require.NoError(t, err)
	updatedAfterCreate := cfg1.Updated

	// Advance clock so the new timestamp will differ.
	f.Advance(5 * time.Minute)
	expectedUpdated := f.Now().Format(time.RFC3339)

	// Read existing meta, change tags, and set it back.
	meta, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	meta.SetTags([]string{"new-tag"})
	require.NoError(t, k.SetMeta(f.Context(), id.ID, meta))

	// Settings timestamp should have been updated.
	cfg2, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, updatedAfterCreate, cfg2.Updated,
		"keg settings updated timestamp should change when meta is modified")
	require.Equal(t, expectedUpdated, cfg2.Updated,
		"keg settings updated timestamp should use the captured metadata update time")

	// Verify the tag actually changed in the dex.
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)
	tags := dex.TagList(f.Context())
	require.Contains(t, tags, "new-tag")
}

func TestSetMetaAndUpdateMetaRefreshCachedSourceHash(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)
	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	body := []byte("# Meta Hash\n\nOriginal content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Body: body})
	require.NoError(t, err)
	initialStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)

	f.Advance(5 * time.Minute)
	meta, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	require.NoError(t, meta.Set(f.Context(), "status", "ready"))
	require.NoError(t, k.SetMeta(f.Context(), id.ID, meta))
	setStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.NotEqual(t, initialStats.Hash(), setStats.Hash())

	f.Advance(5 * time.Minute)
	expectedUpdateMetaConfig := f.Now().Format(time.RFC3339)
	require.NoError(t, k.UpdateMeta(f.Context(), id.ID, func(meta *kegpkg.NodeMeta) {
		_ = meta.Set(f.Context(), "reviewed", true)
	}))
	updatedStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.NotEqual(t, setStats.Hash(), updatedStats.Hash())
	cfg, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.Equal(t, expectedUpdateMetaConfig, cfg.Updated,
		"keg settings updated timestamp should use the captured UpdateMeta time")

	content, err := k.GetContent(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, body, content)
}

func TestIndexRefreshesStatsForOutOfBandMetadataChange(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)
	repo := newTestMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Out Of Band Meta"})
	require.NoError(t, err)
	initialStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)

	meta, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	require.NoError(t, meta.Set(f.Context(), "status", "ready"))
	f.Advance(5 * time.Minute)
	require.NoError(t, repo.WriteMeta(f.Context(), id.ID, []byte(meta.ToYAML())))

	staleStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, initialStats.Hash(), staleStats.Hash())

	require.NoError(t, k.Index(f.Context(), kegpkg.IndexOptions{}))
	refreshedStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.NotEqual(t, initialStats.Hash(), refreshedStats.Hash())
	require.Equal(t, f.Now(), refreshedStats.Updated())

	refreshedHash := refreshedStats.Hash()
	refreshedUpdated := refreshedStats.Updated()
	f.Advance(5 * time.Minute)
	require.NoError(t, k.Index(f.Context(), kegpkg.IndexOptions{}))
	againStats, err := k.GetStats(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, refreshedHash, againStats.Hash())
	require.Equal(t, refreshedUpdated, againStats.Updated())
}

// TestSetContent_WithChangeUpdatesDexAndConfig verifies that calling SetContent
// with different content does update the dex and keg settings timestamp.
func TestSetContent_WithChangeUpdatesDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_content_change"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_content_change"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	body := []byte("# Change Node\n\nOriginal content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Body: body})
	require.NoError(t, err)

	// Record keg settings updated timestamp after create.
	cfg1, err := k.Settings(f.Context())
	require.NoError(t, err)
	updatedAfterCreate := cfg1.Updated

	// Advance clock so the new timestamp will differ.
	f.Advance(5 * time.Minute)
	expectedUpdated := f.Now().Format(time.RFC3339)

	// SetContent with different bytes — should update dex and settings.
	newBody := []byte("# Change Node\n\nUpdated content.\n")
	require.NoError(t, k.SetContent(f.Context(), id.ID, newBody))

	// Settings timestamp should have been updated.
	cfg2, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, updatedAfterCreate, cfg2.Updated,
		"keg settings updated timestamp should change when content is modified")
	require.Equal(t, expectedUpdated, cfg2.Updated,
		"keg settings updated timestamp should use the helper's current clock time")

	// Verify content was actually written.
	got, err := k.GetContent(f.Context(), id.ID)
	require.NoError(t, err)
	require.Equal(t, string(newBody), string(got))
}

// TestEditNoChange_SimulatesSaveWithoutChanges simulates the tap edit
// flow where SetMeta and SetContent are called with unchanged data.
// After the first normalization round-trip, neither the dex files nor the
// keg settings should be modified on a second save-without-changes.
func TestEditNoChange_SimulatesSaveWithoutChanges(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_edit_noop"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_edit_noop"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	body := []byte("# Edit NoOp\n\nSome content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Body: body,
		Tags: []string{"edit-test"},
	})
	require.NoError(t, err)

	// First round-trip normalizes the on-disk meta format from Create's
	// struct serialization to ParseMeta/ToYAML tree serialization.
	meta, err := k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id.ID, meta))
	require.NoError(t, k.SetContent(f.Context(), id.ID, body))

	// Record keg settings updated timestamp after normalization.
	cfg1, err := k.Settings(f.Context())
	require.NoError(t, err)
	updatedAfterNormalize := cfg1.Updated

	// Advance clock so any new write would produce a different timestamp.
	f.Advance(10 * time.Minute)

	// Simulate tap edit save-without-changes: SetMeta then SetContent
	// with identical data (this is what applyEditedNodeRaw does).
	meta, err = k.GetMeta(f.Context(), id.ID)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id.ID, meta))
	require.NoError(t, k.SetContent(f.Context(), id.ID, body))

	// Settings timestamp should not have changed.
	cfg2, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.Equal(t, updatedAfterNormalize, cfg2.Updated,
		"keg settings should not change when editing saves without modifications")
}

// TestCreateAlwaysTriggersUpdate verifies that Create always updates dex
// and settings, regardless of content.
func TestCreateAlwaysTriggersUpdate(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_create_always"))

	k, err := newMemoryKegFromTarget(f.Context(), memoryTarget("repo_create_always"), f.Runtime())
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	cfg1, err := k.Settings(f.Context())
	require.NoError(t, err)
	updatedAfterInit := cfg1.Updated

	f.Advance(5 * time.Minute)
	expectedUpdated := f.Now().Format(time.RFC3339)

	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "New Node"})
	require.NoError(t, err)

	cfg2, err := k.Settings(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, updatedAfterInit, cfg2.Updated,
		"keg settings should always update after Create")
	require.Equal(t, expectedUpdated, cfg2.Updated,
		"keg settings updated timestamp should use the captured create time")
}

func TestDexFresh_ReloadsForExternalRepoImplementations(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)
	repo := &externalMemoryRepo{MemoryRepository: newTestMemoryRepo(f.Runtime())}

	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	initNonStrictTestKeg(t, k, f.Context())

	_, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Original Node"})
	require.NoError(t, err)

	dex1, err := k.Dex(f.Context())
	require.NoError(t, err)

	externalKeg := kegpkg.NewLocalKeg(repo, f.Runtime())
	externalID, err := externalKeg.Create(f.Context(), &kegpkg.CreateOptions{Title: "External Node"})
	require.NoError(t, err)

	require.Nil(t, dex1.GetRef(f.Context(), externalID.ID), "primed dex should not mutate behind the caller")

	dex2, err := k.Dex(f.Context())
	require.NoError(t, err)
	require.NotSame(t, dex1, dex2, "external repo DexFresh should reload instead of returning the cached dex")

	extRef := dex2.GetRef(f.Context(), externalID.ID)
	require.NotNil(t, extRef, "DexFresh should reload and include the externally added node")
	require.Equal(t, "External Node", extRef.Title)
}

// withKegName sets Target.KegName on a file target. The bug being guarded
// against only fires when KegName is populated (production namespace
// resolution sets it); plain NewFile leaves it empty, which is why no existing
// file-based test reproduced the prefix leak.
func withKegName(name string) kegpkg.TargetOption {
	return func(t *kegpkg.Target) { t.KegName = name }
}

// TestSetContent_LocalNodeIDStaysBare guards the regression where the
// single-node index write path stamped the keg name onto a local node id,
// producing "keg:<name>/<id>" entries in dex/nodes.tsv instead of a bare id.
// SetContent reaches the formerly buggy Keg.Node() via indexNodeLocked.
func TestSetContent_LocalNodeIDStaysBare(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(
		f.Context(),
		memoryTarget("repo", withKegName("example")),
		f.Runtime(),
	)
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())
	require.Equal(t, "example", k.Target().KegName, "KegName must be set to reproduce the bug")

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Node 2"})
	require.NoError(t, err)

	// SetContent is the edit path that previously tainted the dex entry.
	require.NoError(t, k.SetContent(f.Context(), id.ID, []byte("# Node 2\n\nedited body\n")))

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)
	for _, e := range dex.Nodes(f.Context()) {
		require.NotContains(t, e.ID, "keg:", "local node id must be bare, got %q", e.ID)
	}

	// The edited node resolves under its bare id, and there is no stale
	// keg:-prefixed duplicate row left behind.
	require.NotNil(t, dex.GetRef(f.Context(), id.ID), "edited node should be indexed under its bare id")
	raw, err := k.(*kegpkg.LocalKeg).Repo.GetIndex(f.Context(), "nodes.tsv")
	require.NoError(t, err)
	require.NotContains(t, string(raw), "keg:", "nodes.tsv must not contain keg: prefixes")
}

// TestMove_LocalNodeIDStaysBare covers the third taint vector: Move rewrites
// the in-content links of every node that referenced the moved node and
// re-indexes them through setContentNoDex -> indexNodeLocked. Both the rewritten
// node and the link/backlink sub-indexes must stay free of keg: prefixes.
func TestMove_LocalNodeIDStaysBare(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := newMemoryKegFromTarget(
		f.Context(),
		memoryTarget("repo", withKegName("example")),
		f.Runtime(),
	)
	require.NoError(t, err)
	initNonStrictTestKeg(t, k, f.Context())

	target, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Target"})
	require.NoError(t, err)
	referrer, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Referrer"})
	require.NoError(t, err)

	// Referrer links to target via a canonical relative node link.
	require.NoError(t, k.SetContent(f.Context(), referrer.ID,
		[]byte("# Referrer\n\nsee [target](../"+target.ID.Path()+")\n")))

	// Move the target; this rewrites referrer's link and re-indexes it.
	require.NoError(t, errOnly(k.Move(f.Context(), moveOptions(t, f.Context(), k, target.ID, kegpkg.NodeId{ID: target.ID.ID + 10}))))

	for _, name := range []string{"nodes.tsv", "links", "backlinks"} {
		raw, err := k.(*kegpkg.LocalKeg).Repo.GetIndex(f.Context(), name)
		require.NoError(t, err)
		require.NotContains(t, string(raw), "keg:", "%s must not contain keg: prefixes", name)
	}
}

// errOnly discards the first result of a two-value call (e.g. Move/Remove
// rewritten-node lists) so tests can assert only on the error.
func errOnly[T any](_ T, err error) error {
	return err
}
