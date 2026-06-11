package keg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

type externalMemoryRepo struct {
	*kegpkg.MemoryRepo
}

func (r *externalMemoryRepo) Name() string {
	return "external-memory"
}

// TestInitWhenRepoIsExample attempts to InitKeg a keg when the repo already
// contains the example data. InitKeg should fail with ErrExist.
func TestInitWhenRepoIsExample(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("example", "~/repos/example"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("~/repos/example"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	err = k.Init(f.Context())
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

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	require.NoError(t, k.Init(f.Context()), "InitKeg failed")

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

// TestKegExistsWithMemoryRepo verifies KegExists behavior with the in-memory
// repository. It should report false for an uninitialized repo and true after
// InitKeg has been called.
func TestKegExistsWithMemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())

	// Initially not initialized.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for new memory repo")

	// Initialize via Keg.InitKeg and re-check.
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()), "InitKeg failed for memory repo")

	exists, err = kegpkg.RepoContainsKeg(f.Context(), repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after InitKeg")
}

// TestKegExistsWithFsRepo verifies KegExists behavior using the filesystem
// repository. It uses the provided empty fixture and ensures behavior mirrors
// the memory repo.
func TestKegExistsWithFsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repofs"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	// Uninitialized on disk.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), k.(*kegpkg.LocalKeg).Repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for empty fs repo")

	// Initialize and verify.
	require.NoError(t, k.Init(f.Context()), "InitKeg failed for fs repo")

	exists, err = kegpkg.RepoContainsKeg(f.Context(), k.(*kegpkg.LocalKeg).Repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after InitKeg")
}

// Additional tests

// TestCreateZeroNodeInMemoryRepo verifies creating the zero node via Create
// on a fresh in-memory repository. The zero node should contain the
// RawZeroNodeContent.
func TestCreateZeroNodeInMemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	k.Init(f.Context())

	b, err := k.GetContent(f.Context(), kegpkg.NodeId{ID: 0})
	require.NoError(t, err)
	require.Contains(t, string(b), "Sorry, planned but not yet available")
}

// TestCreateNodeWithMeta ensures non-zero nodes created with options write
// sensible content and meta that can be parsed.
func TestCreateNodeWithMeta(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	k.Init(f.Context())

	opts := &kegpkg.CreateOptions{
		Title: "MyTitle",
		Lead:  "short lead",
		Tags:  []string{"TagA", "tag-a"},
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	content, err := k.GetContent(f.Context(), id)
	require.NoError(t, err)
	require.Contains(t, string(content), "# MyTitle")

	stats, err := k.GetStats(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "short lead", stats.Lead())
	// normalized tags should include "tag-a"
	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	foundTag := slices.Contains(m.Tags(), "tag-a")
	require.True(t, foundTag, "expected normalized tag 'tag-a' to be present")
}

// New test: create where Body is provided in the Create options. Ensure the
// provided body becomes the node content and meta is parsed from it.
func TestCreateWithBody(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	body := []byte("# BodyTitle\n\nbody paragraph\n")
	opts := &kegpkg.CreateOptions{
		Body: body,
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	got, err := k.GetContent(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, string(body), string(got))

	stats, err := k.GetStats(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "BodyTitle", stats.Title())
	require.Equal(t, "body paragraph", stats.Lead())
}

// New test: Body contains YAML frontmatter. Ensure content written equals the
// provided bytes and parsed meta reflects the markdown heading and lead.
func TestCreateWithBodyFrontmatter(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

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
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	got, err := k.GetContent(f.Context(), id)
	content, _ := kegpkg.ParseContent(f.Runtime(), rawBody, kegpkg.FormatMarkdown)
	require.NoError(t, err)
	require.Equal(t, content.Body, string(got))

	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)

	// Title should be derived from the first H1 in the markdown body.
	stats, err := k.GetStats(f.Context(), id)
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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	k.Init(f.Context())

	// create zero and a second node
	_, err := k.Create(f.Context(), nil)
	require.NoError(t, err)

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Initial"})
	require.NoError(t, err)

	// change content to include a new lead paragraph
	newContent := []byte("# Initial\n\nupdated lead paragraph\n")
	require.NoError(t, k.SetContent(f.Context(), id, newContent))

	stats, err := k.GetStats(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "updated lead paragraph", stats.Lead())
}

// TestCreateAndUpdateNodesWithFsRepo uses the filesystem repo to create a
// node, ensures the dex contains the node, updates content, and validates
// meta and dex timestamps reflect the update.
func TestCreateAndUpdateNodesWithFsRepo(t *testing.T) {
	t.Parallel()
	// Use the empty fixture as a filesystem-backed repo.
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_fs"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repofs_fs"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	// Initialize on disk.
	require.NoError(t, k.Init(f.Context()), "InitKeg failed")

	// Create a new node with title and lead.
	opts := &kegpkg.CreateOptions{
		Title: "FSNode",
		Lead:  "lead fs",
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	// Dex should expose the node entry.
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	ref := dex.GetRef(f.Context(), id)
	require.NotNil(t, ref, "dex should contain created node")
	require.Equal(t, id.Path(), ref.ID)

	// Ensure zero node is present in dex as well.
	zeroRef := dex.GetRef(f.Context(), kegpkg.NodeId{ID: 0})
	require.NotNil(t, zeroRef, "dex should contain zero node")

	createdUpdated := ref.Updated

	// Advance clock so updated timestamp will differ after update.
	f.Advance(2 * time.Minute)
	// Update content to change the lead.
	newContent := []byte("# FSNode\n\nnew lead from fs\n")
	require.NoError(t, k.SetContent(f.Context(), id, newContent))

	// NodeMeta should reflect the new lead.
	stats, err := k.GetStats(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "new lead from fs", stats.Lead())

	// Re-acquire dex since SetContent invalidates the cached dex.
	dex, err = k.Dex(f.Context())
	require.NoError(t, err)

	// Dex entry should have a newer updated timestamp.
	ref2 := dex.GetRef(f.Context(), id)
	require.NotNil(t, ref2)
	require.True(t, ref2.Updated.After(createdUpdated),
		"expected dex updated timestamp to advance after content update")
}

// New test: create multiple nodes with tags and interlinks, and validate
// the generated indexes reflect tags, links, and backlinks.
func TestNodesWithTagsAndInterlinks(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	// Create node A with tags
	optsA := &kegpkg.CreateOptions{
		Title: "NodeA",
		Lead:  "lead a",
		Tags:  []string{"Alpha", "Shared"},
	}
	idA, err := k.Create(f.Context(), optsA)
	require.NoError(t, err)
	require.Equal(t, 1, idA.ID)

	// Create node B with tags
	optsB := &kegpkg.CreateOptions{
		Title: "NodeB",
		Lead:  "lead b",
		Tags:  []string{"Beta", "Shared"},
	}
	idB, err := k.Create(f.Context(), optsB)
	require.NoError(t, err)
	require.Equal(t, 2, idB.ID)

	// Update content so nodes link to each other using ../N links.
	contentA := []byte("# NodeA\n\nSee NodeB: [B](../2)\n")
	require.NoError(t, k.SetContent(f.Context(), idA, contentA))

	contentB := []byte("# NodeB\n\nSee NodeA: [A](../1)\n")
	require.NoError(t, k.SetContent(f.Context(), idB, contentB))

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
	outA, ok := dex.Links(f.Context(), idA)
	require.True(t, ok)
	require.Equal(t, 1, len(outA))
	require.Equal(t, idB.ID, outA[0].ID)

	inB, ok := dex.Backlinks(f.Context(), idB)
	require.True(t, ok)
	require.Equal(t, 1, len(inB))
	require.Equal(t, idA.ID, inB[0].ID)
}

// TestIndexFilesHaveExpectedData verifies the repository index artifacts that
// live under dex/ are present or handled correctly by the code that reads them.
// The example fixture contains `dex/nodes.tsv` and `dex/changes.md`. Tags and
// backlinks may be absent and should be treated as empty.
func TestIndexFilesHaveExpectedData(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("example", "~/repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("~/repo"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")

	// Load dex via NewDexFromRepo which reads the index artifacts.
	dex, err := kegpkg.NewDexFromRepo(f.Context(), k.(*kegpkg.LocalKeg).Repo)
	require.NoError(t, err, "NewDexFromRepo failed")

	// nodes.tsv should contain the zero node entry.
	zeroRef := dex.GetRef(f.Context(), kegpkg.NodeId{ID: 0})
	require.NotNil(t, zeroRef, "nodes.tsv should include zero node entry")

	// changes.md is expected to exist in the example fixture under dex/.
	changes, err := k.(*kegpkg.LocalKeg).Repo.GetIndex(f.Context(), "changes.md")
	require.NoError(t, err, "expected dex/changes.md to exist")
	require.Greater(t, len(changes), 0, "dex/changes.md should not be empty")

	// tags may be absent for the example fixture. If absent, Dex.TagList should
	// be empty. If present, ensure we can read it without error.
	if _, err := k.(*kegpkg.LocalKeg).Repo.GetIndex(f.Context(), "tags"); err != nil {
		require.True(t, errors.Is(err, kegpkg.ErrNotExist),
			"expected missing tags index to return ErrNotExist, got: %v", err)
		require.Empty(t, dex.TagList(f.Context()), "expected no tags when tags index is absent")
	} else {
		// tags file present, ensure parsed tag list is stable.
		require.GreaterOrEqual(t, len(dex.TagList(f.Context())), 0)
	}

	// backlinks may be absent. If absent, expect no backlinks for the zero node.
	if _, err := k.(*kegpkg.LocalKeg).Repo.GetIndex(f.Context(), "backlinks"); err != nil {
		require.True(t, errors.Is(err, kegpkg.ErrNotExist),
			"expected missing backlinks index to return ErrNotExist, got: %v", err)
		_, ok := dex.Backlinks(f.Context(), kegpkg.NodeId{ID: 0})
		require.False(t, ok, "expected no backlinks for zero when index is absent")
	} else {
		// backlinks file present, ensure parsing did not error earlier and that
		// the dex can return a backlinks mapping (possibly empty).
		_, _ = dex.Backlinks(f.Context(), kegpkg.NodeId{ID: 0})
	}
}

func TestIndex_PreservesUnknownConfigFields(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_config"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repofs_config"), f.Runtime())
	require.NoError(t, err, "NewKegFromTarget failed")
	require.NoError(t, k.Init(f.Context()), "InitKeg failed")

	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Config Field Preservation"})
	require.NoError(t, err)

	customConfig := []byte(`kegv: "2025-07"
updated: "2020-01-01T00:00:00Z"
title: "custom config"
summary: "contains unknown fields"
custom_block:
  keep_me: true
  nested:
    item: value
`)
	require.NoError(t, f.Runtime().WriteFile("repofs_config/keg", customConfig, 0o644))

	require.NoError(t, k.Index(f.Context(), kegpkg.IndexOptions{}))

	raw, err := f.Runtime().ReadFile("repofs_config/keg")
	require.NoError(t, err)
	out := string(raw)
	require.Contains(t, out, "custom_block:")
	require.Contains(t, out, "keep_me: true")
	require.Contains(t, out, "nested:")
	require.Contains(t, out, "item: value")

	cfg, err := k.(*kegpkg.LocalKeg).Repo.ReadConfig(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, "2020-01-01T00:00:00Z", cfg.Updated)
}

func TestMove_RewritesLinksAndUpdatesDex(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id1, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	require.Equal(t, 1, id1.ID)

	id2, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)
	require.Equal(t, 2, id2.ID)

	// Add canonical and bare links to node 2.
	require.NoError(t, k.SetContent(f.Context(), id1, []byte("# One\n\nSee [two](../2).\nAlso ../2.\n")))

	require.NoError(t, errOnly(k.Move(f.Context(), kegpkg.NodeId{ID: 2}, kegpkg.NodeId{ID: 3})))

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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	_, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)
	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Three"})
	require.NoError(t, err)

	_, err = k.Move(f.Context(), kegpkg.NodeId{ID: 2}, kegpkg.NodeId{ID: 3})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrDestinationExists)
}

func TestRemove_DeletesNodeAndUpdatesDex(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id1, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "One"})
	require.NoError(t, err)
	id2, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Two"})
	require.NoError(t, err)

	require.NoError(t, k.SetContent(f.Context(), id1, []byte("# One\n\nSee [two](../2).\n")))

	require.NoError(t, errOnly(k.Remove(f.Context(), id2)))

	exists, err := k.Repo.HasNode(f.Context(), id2)
	require.NoError(t, err)
	require.False(t, exists, "node should be deleted from repository")

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	_, ok := dex.Links(f.Context(), id2)
	require.False(t, ok, "deleted node should be absent from links index")

	_, ok = dex.Backlinks(f.Context(), id2)
	require.False(t, ok, "deleted node should be absent from backlinks index")

	node1Links, ok := dex.Links(f.Context(), id1)
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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Doomed"})
	require.NoError(t, err)

	require.NoError(t, errOnly(k.Remove(f.Context(), id)))

	// Attempt to write content to the removed node should fail.
	err = k.SetContent(f.Context(), id, []byte("# Resurrected\n"))
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)
}

func TestRemove_NotFound(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	_, err := k.Remove(f.Context(), kegpkg.NodeId{ID: 4242})
	require.Error(t, err)
	require.ErrorIs(t, err, kegpkg.ErrNotExist)
}

// TestSetMeta_PreservesLinksInDex verifies that calling SetMeta followed by
// SetContent with unchanged body does not lose link index entries.
// Reproduction test for bug 261 sub-issue 1.
func TestSetMeta_PreservesLinksInDex(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	// Create two nodes
	id1, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Source"})
	require.NoError(t, err)
	id2, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Target"})
	require.NoError(t, err)

	// Set content with a link from node 1 to node 2
	body := []byte("# Source\n\nSee [target](../2)\n")
	require.NoError(t, k.SetContent(f.Context(), id1, body))

	// Verify links exist
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)
	links, ok := dex.Links(f.Context(), id1)
	require.True(t, ok, "node 1 should have outgoing links after SetContent")
	require.Len(t, links, 1)
	require.Equal(t, id2.ID, links[0].ID)

	// Now simulate what tap edit does: SetMeta then SetContent with same body
	meta, err := k.GetMeta(f.Context(), id1)
	require.NoError(t, err)
	meta.SetTags([]string{"new-tag"})
	require.NoError(t, k.SetMeta(f.Context(), id1, meta))

	// SetContent with unchanged body -- should not lose links
	require.NoError(t, k.SetContent(f.Context(), id1, body))

	// Links should still be present
	links, ok = dex.Links(f.Context(), id1)
	require.True(t, ok, "links should survive SetMeta + SetContent no-op")
	require.Len(t, links, 1)
	require.Equal(t, id2.ID, links[0].ID)
}

// TestIndex_ContentOnlyNodeGetsIndexed verifies that a node with only
// README.md (no meta.yaml, no stats.json) is still included in the index
// after a rebuild.
func TestIndex_ContentOnlyNodeGetsIndexed(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

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

	repo := kegpkg.NewMemoryRepo(f.Runtime())
	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	// Create a node normally first, then corrupt its meta.
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Good Node"})
	require.NoError(t, err)

	// Overwrite meta with invalid YAML.
	require.NoError(t, repo.WriteMeta(f.Context(), id, []byte("{{{invalid yaml")))

	// Rebuild — the node should still appear.
	err = k.Index(f.Context(), kegpkg.IndexOptions{})
	// Index may return aggregated errors for the malformed meta, but the node
	// should still be in the dex.
	_ = err

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)

	ref := dex.GetRef(f.Context(), id)
	require.NotNil(t, ref, "node with malformed meta should still appear in index")
	require.Equal(t, "Good Node", ref.Title)
}

// TestSetContent_NoChangeSkipsDexAndConfig verifies that calling SetContent
// with identical content does not modify the dex or keg config timestamp.
func TestSetContent_NoChangeSkipsDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_noop"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_noop"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	body := []byte("# NoOp Node\n\nOriginal content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Body: body})
	require.NoError(t, err)

	// Record keg config updated timestamp after create.
	cfg1, err := k.Config(f.Context())
	require.NoError(t, err)
	updatedAfterCreate := cfg1.Updated

	// Advance clock so any new timestamp would differ.
	f.Advance(5 * time.Minute)

	// SetContent with identical bytes — should be a no-op.
	require.NoError(t, k.SetContent(f.Context(), id, body))

	// Config timestamp should not have changed.
	cfg2, err := k.Config(f.Context())
	require.NoError(t, err)
	require.Equal(t, updatedAfterCreate, cfg2.Updated,
		"keg config updated timestamp should not change when content is unchanged")
}

// TestSetMeta_NoChangeSkipsDexAndConfig verifies that calling SetMeta
// with identical metadata does not modify the dex or keg config timestamp.
func TestSetMeta_NoChangeSkipsDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_meta_noop"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_meta_noop"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Meta NoOp",
		Tags:  []string{"test"},
	})
	require.NoError(t, err)

	// Normalize on-disk meta format by doing one round-trip through
	// GetMeta/SetMeta. This ensures the on-disk bytes match the
	// ToYAML() output format used by SetMeta's comparison.
	meta, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id, meta))

	// Record keg config updated timestamp after normalization.
	cfg1, err := k.Config(f.Context())
	require.NoError(t, err)
	updatedAfterNormalize := cfg1.Updated

	// Advance clock so any new timestamp would differ.
	f.Advance(5 * time.Minute)

	// Read existing meta and set it back unchanged — this should be a no-op.
	meta, err = k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id, meta))

	// Config timestamp should not have changed.
	cfg2, err := k.Config(f.Context())
	require.NoError(t, err)
	require.Equal(t, updatedAfterNormalize, cfg2.Updated,
		"keg config updated timestamp should not change when meta is unchanged")
}

// TestSetMeta_WithChangeUpdatesDexAndConfig verifies that calling SetMeta
// with different metadata does update the dex and keg config timestamp.
func TestSetMeta_WithChangeUpdatesDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_meta_change"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_meta_change"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Meta Change",
		Tags:  []string{"old-tag"},
	})
	require.NoError(t, err)

	// Record keg config updated timestamp after create.
	cfg1, err := k.Config(f.Context())
	require.NoError(t, err)
	updatedAfterCreate := cfg1.Updated

	// Advance clock so the new timestamp will differ.
	f.Advance(5 * time.Minute)

	// Read existing meta, change tags, and set it back.
	meta, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	meta.SetTags([]string{"new-tag"})
	require.NoError(t, k.SetMeta(f.Context(), id, meta))

	// Config timestamp should have been updated.
	cfg2, err := k.Config(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, updatedAfterCreate, cfg2.Updated,
		"keg config updated timestamp should change when meta is modified")

	// Verify the tag actually changed in the dex.
	dex, err := k.Dex(f.Context())
	require.NoError(t, err)
	tags := dex.TagList(f.Context())
	require.Contains(t, tags, "new-tag")
}

// TestSetContent_WithChangeUpdatesDexAndConfig verifies that calling SetContent
// with different content does update the dex and keg config timestamp.
func TestSetContent_WithChangeUpdatesDexAndConfig(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_content_change"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_content_change"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	body := []byte("# Change Node\n\nOriginal content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Body: body})
	require.NoError(t, err)

	// Record keg config updated timestamp after create.
	cfg1, err := k.Config(f.Context())
	require.NoError(t, err)
	updatedAfterCreate := cfg1.Updated

	// Advance clock so the new timestamp will differ.
	f.Advance(5 * time.Minute)

	// SetContent with different bytes — should update dex and config.
	newBody := []byte("# Change Node\n\nUpdated content.\n")
	require.NoError(t, k.SetContent(f.Context(), id, newBody))

	// Config timestamp should have been updated.
	cfg2, err := k.Config(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, updatedAfterCreate, cfg2.Updated,
		"keg config updated timestamp should change when content is modified")

	// Verify content was actually written.
	got, err := k.GetContent(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, string(newBody), string(got))
}

// TestEditNoChange_SimulatesSaveWithoutChanges simulates the tap edit
// flow where SetMeta and SetContent are called with unchanged data.
// After the first normalization round-trip, neither the dex files nor the
// keg config should be modified on a second save-without-changes.
func TestEditNoChange_SimulatesSaveWithoutChanges(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_edit_noop"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_edit_noop"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	body := []byte("# Edit NoOp\n\nSome content.\n")
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Body: body,
		Tags: []string{"edit-test"},
	})
	require.NoError(t, err)

	// First round-trip normalizes the on-disk meta format from Create's
	// struct serialization to ParseMeta/ToYAML tree serialization.
	meta, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id, meta))
	require.NoError(t, k.SetContent(f.Context(), id, body))

	// Record keg config updated timestamp after normalization.
	cfg1, err := k.Config(f.Context())
	require.NoError(t, err)
	updatedAfterNormalize := cfg1.Updated

	// Advance clock so any new write would produce a different timestamp.
	f.Advance(10 * time.Minute)

	// Simulate tap edit save-without-changes: SetMeta then SetContent
	// with identical data (this is what applyEditedNodeRaw does).
	meta, err = k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.NoError(t, k.SetMeta(f.Context(), id, meta))
	require.NoError(t, k.SetContent(f.Context(), id, body))

	// Config timestamp should not have changed.
	cfg2, err := k.Config(f.Context())
	require.NoError(t, err)
	require.Equal(t, updatedAfterNormalize, cfg2.Updated,
		"keg config should not change when editing saves without modifications")
}

// TestCreateAlwaysTriggersUpdate verifies that Create always updates dex
// and config, regardless of content.
func TestCreateAlwaysTriggersUpdate(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo_create_always"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repo_create_always"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	cfg1, err := k.Config(f.Context())
	require.NoError(t, err)
	updatedAfterInit := cfg1.Updated

	f.Advance(5 * time.Minute)

	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{Title: "New Node"})
	require.NoError(t, err)

	cfg2, err := k.Config(f.Context())
	require.NoError(t, err)
	require.NotEqual(t, updatedAfterInit, cfg2.Updated,
		"keg config should always update after Create")
}

// TestDexFresh_ReloadsAfterExternalModification verifies that DexFresh
// detects when the on-disk dex has been modified by an external process and
// reloads it. This is the core mechanism that makes the serve handler show
// fresh data without a server restart.
func TestDexFresh_ReloadsAfterExternalModification(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_dexfresh"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repofs_dexfresh"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	// Create a node so the dex has content.
	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Original Node",
		Lead:  "original lead",
		Tags:  []string{"alpha"},
	})
	require.NoError(t, err)

	// Load the dex via DexFresh and verify initial state.
	dex1, err := k.Dex(f.Context())
	require.NoError(t, err)
	ref1 := dex1.GetRef(f.Context(), id)
	require.NotNil(t, ref1)
	require.Equal(t, "Original Node", ref1.Title)

	// Simulate an external process creating a second node by directly
	// using a second Keg instance pointing at the same repo. This writes
	// new dex files to disk, changing the mtime.
	f.Advance(2 * time.Minute)
	k2, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repofs_dexfresh"), f.Runtime())
	require.NoError(t, err)
	_, err = k2.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "External Node",
		Lead:  "added externally",
		Tags:  []string{"beta"},
	})
	require.NoError(t, err)

	// The original keg instance's cached dex is now stale. DexFresh should
	// detect the mtime change and reload.
	dex2, err := k.Dex(f.Context())
	require.NoError(t, err)

	// Verify the externally-added node appears.
	extRef := dex2.GetRef(f.Context(), kegpkg.NodeId{ID: 2})
	require.NotNil(t, extRef, "DexFresh should reload and include the externally-added node")
	require.Equal(t, "External Node", extRef.Title)

	// The original node should still be present.
	origRef := dex2.GetRef(f.Context(), id)
	require.NotNil(t, origRef)
	require.Equal(t, "Original Node", origRef.Title)

	// Verify tag index also refreshed.
	tagList := dex2.TagList(f.Context())
	require.Contains(t, tagList, "alpha")
	require.Contains(t, tagList, "beta")
}

// TestDexFresh_ReturnsCachedWhenUnchanged verifies that DexFresh returns
// the same cached dex when no external modification has occurred, avoiding
// unnecessary reloads.
func TestDexFresh_ReturnsCachedWhenUnchanged(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_dexcache"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegpkg.NewFile("repofs_dexcache"), f.Runtime())
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	_, err = k.Create(f.Context(), &kegpkg.CreateOptions{
		Title: "Cached Node",
	})
	require.NoError(t, err)

	// Load dex twice without any external changes.
	dex1, err := k.Dex(f.Context())
	require.NoError(t, err)
	dex2, err := k.Dex(f.Context())
	require.NoError(t, err)

	// Both should return the same pointer (no reload occurred).
	require.Same(t, dex1, dex2, "DexFresh should return cached dex when mtime unchanged")
}

func TestDexFresh_ReloadsForExternalRepoImplementations(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)
	repo := &externalMemoryRepo{MemoryRepo: kegpkg.NewMemoryRepo(f.Runtime())}

	k := kegpkg.NewLocalKeg(repo, f.Runtime())
	require.NoError(t, k.Init(f.Context()))

	_, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Original Node"})
	require.NoError(t, err)

	dex1, err := k.Dex(f.Context())
	require.NoError(t, err)

	externalKeg := kegpkg.NewLocalKeg(repo, f.Runtime())
	externalID, err := externalKeg.Create(f.Context(), &kegpkg.CreateOptions{Title: "External Node"})
	require.NoError(t, err)

	require.Nil(t, dex1.GetRef(f.Context(), externalID), "primed dex should not mutate behind the caller")

	dex2, err := k.Dex(f.Context())
	require.NoError(t, err)
	require.NotSame(t, dex1, dex2, "external repo DexFresh should reload instead of returning the cached dex")

	extRef := dex2.GetRef(f.Context(), externalID)
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

	k, err := kegpkg.NewKegFromTarget(
		f.Context(),
		kegpkg.NewFile("repo", withKegName("example")),
		f.Runtime(),
	)
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))
	require.Equal(t, "example", k.Target().KegName, "KegName must be set to reproduce the bug")

	id, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Node 2"})
	require.NoError(t, err)

	// SetContent is the edit path that previously tainted the dex entry.
	require.NoError(t, k.SetContent(f.Context(), id, []byte("# Node 2\n\nedited body\n")))

	dex, err := k.Dex(f.Context())
	require.NoError(t, err)
	for _, e := range dex.Nodes(f.Context()) {
		require.NotContains(t, e.ID, "keg:", "local node id must be bare, got %q", e.ID)
	}

	// The edited node resolves under its bare id, and there is no stale
	// keg:-prefixed duplicate row left behind.
	require.NotNil(t, dex.GetRef(f.Context(), id), "edited node should be indexed under its bare id")
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

	k, err := kegpkg.NewKegFromTarget(
		f.Context(),
		kegpkg.NewFile("repo", withKegName("example")),
		f.Runtime(),
	)
	require.NoError(t, err)
	require.NoError(t, k.Init(f.Context()))

	target, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Target"})
	require.NoError(t, err)
	referrer, err := k.Create(f.Context(), &kegpkg.CreateOptions{Title: "Referrer"})
	require.NoError(t, err)

	// Referrer links to target via a canonical relative node link.
	require.NoError(t, k.SetContent(f.Context(), referrer,
		[]byte("# Referrer\n\nsee [target](../"+target.Path()+")\n")))

	// Move the target; this rewrites referrer's link and re-indexes it.
	require.NoError(t, errOnly(k.Move(f.Context(), target, kegpkg.NodeId{ID: target.ID + 10})))

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
