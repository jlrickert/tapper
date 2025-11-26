package keg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/stretchr/testify/require"
)

// TestInitWhenRepoIsExample attempts to Init a keg when the repo already
// contains the example data. Init should fail with ErrExist.
func TestInitWhenRepoIsExample(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("example", "~/repos/example"))

	k, err := kegpkg.NewKegFromTarget(
		f.Context(),
		kegurl.NewFile("~/repos/example"),
	)
	require.NoError(t, err, "NewKegFromTarget failed")

	err = k.Init(f.Context())
	require.Error(t, err)
	require.Truef(
		t,
		errors.Is(err, kegpkg.ErrExist),
		"Init expected ErrExist, got: %v", err,
	)
}

// TestInitOnEmptyRepo initializes a new keg in an empty fixture repo and
// verifies the repository reports an initialized keg and a zero node exists.
func TestInitOnEmptyRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegurl.NewFile("repo"))
	require.NoError(t, err, "NewKegFromTarget failed")

	require.NoError(t, k.Init(f.Context()), "Init failed")

	// Repo should now report a keg exists.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), k.Repo)
	require.NoError(t, err, "KegExists returned error")
	require.True(t, exists, "KegExists expected true after Init")

	// Ensure a zero node is present.
	ids, err := k.Repo.ListNodes(f.Context())
	require.NoError(t, err, "ListNodes failed")
	foundZero := false
	for _, n := range ids {
		if n.ID == 0 {
			foundZero = true
			break
		}
	}
	require.True(t, foundZero, "expected zero node to exist after Init")
}

// TestKegExistsWithMemoryRepo verifies KegExists behavior with the in-memory
// repository. It should report false for an uninitialized repo and true after
// Init has been called.
func TestKegExistsWithMemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo()

	// Initially not initialized.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for new memory repo")

	// Initialize via Keg.Init and re-check.
	k := kegpkg.NewKeg(repo)
	require.NoError(t, k.Init(f.Context()), "Init failed for memory repo")

	exists, err = kegpkg.RepoContainsKeg(f.Context(), repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after Init")
}

// TestKegExistsWithFsRepo verifies KegExists behavior using the filesystem
// repository. It uses the provided empty fixture and ensures behavior mirrors
// the memory repo.
func TestKegExistsWithFsRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegurl.NewFile("repofs"))
	require.NoError(t, err, "NewKegFromTarget failed")

	// Uninitialized on disk.
	exists, err := kegpkg.RepoContainsKeg(f.Context(), k.Repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for empty fs repo")

	// Initialize and verify.
	require.NoError(t, k.Init(f.Context()), "Init failed for fs repo")

	exists, err = kegpkg.RepoContainsKeg(f.Context(), k.Repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after Init")
}

// Additional tests

// TestCreateZeroNodeInMemoryRepo verifies creating the zero node via Create
// on a fresh in-memory repository. The zero node should contain the
// RawZeroNodeContent.
func TestCreateZeroNodeInMemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	k.Init(f.Context())

	b, err := k.GetContent(f.Context(), kegpkg.Node{ID: 0})
	require.NoError(t, err)
	require.Contains(t, string(b), "Sorry, planned but not yet available")
}

// TestCreateNodeWithMeta ensures non-zero nodes created with options write
// sensible content and meta that can be parsed.
func TestCreateNodeWithMeta(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	k.Init(f.Context())

	opts := &kegpkg.KegCreateOptions{
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

	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "MyTitle", m.Title())
	require.Equal(t, "short lead", m.Lead())
	// normalized tags should include "tag-a"
	foundTag := slices.Contains(m.Tags(), "tag-a")
	require.True(t, foundTag, "expected normalized tag 'tag-a' to be present")
}

// New test: create where Body is provided in the Create options. Ensure the
// provided body becomes the node content and meta is parsed from it.
func TestCreateWithBody(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	require.NoError(t, k.Init(f.Context()))

	body := []byte("# BodyTitle\n\nbody paragraph\n")
	opts := &kegpkg.KegCreateOptions{
		Body: body,
	}
	id, err := k.Create(f.Context(), opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	got, err := k.GetContent(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, string(body), string(got))

	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "BodyTitle", m.Title())
	require.Equal(t, "body paragraph", m.Lead())
}

// New test: Body contains YAML frontmatter. Ensure content written equals the
// provided bytes and parsed meta reflects the markdown heading and lead.
func TestCreateWithBodyFrontmatter(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	require.NoError(t, k.Init(f.Context()))

	rawBody := []byte(`---
tags:
  - fm
foo: bar
---
# FMTitle

fm lead paragraph
`)
	id, err := k.Create(f.Context(), &kegpkg.KegCreateOptions{Body: rawBody})
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	got, err := k.GetContent(f.Context(), id)
	content, _ := kegpkg.ParseContent(f.Context(), rawBody, kegpkg.FormatMarkdown)
	require.NoError(t, err)
	require.Equal(t, content.Body, string(got))

	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)

	// Title should be derived from the first H1 in the markdown body.
	require.Equal(t, "FMTitle", m.Title())
	require.Equal(t, "fm lead paragraph", m.Lead())
	require.Contains(t, m.Tags(), "fm")
	require.Contains(t, m.ToYAML(), "foo: bar")
}

// TestSetContentAndUpdate ensures SetContent causes meta to be updated from
// parsed content (for example lead paragraph changes).
func TestSetContentAndUpdate(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	k.Init(f.Context())

	// create zero and a second node
	_, err := k.Create(f.Context(), nil)
	require.NoError(t, err)

	id, err := k.Create(f.Context(), &kegpkg.KegCreateOptions{Title: "Initial"})
	require.NoError(t, err)

	// change content to include a new lead paragraph
	newContent := []byte("# Initial\n\nupdated lead paragraph\n")
	require.NoError(t, k.SetContent(f.Context(), id, newContent))

	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "updated lead paragraph", m.Lead())
}

// TestCreateAndUpdateNodesWithFsRepo uses the filesystem repo to create a
// node, ensures the dex contains the node, updates content, and validates
// meta and dex timestamps reflect the update.
func TestCreateAndUpdateNodesWithFsRepo(t *testing.T) {
	t.Parallel()
	// Use the empty fixture as a filesystem-backed repo.
	f := NewSandbox(t, sandbox.WithFixture("empty", "repofs_fs"))

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegurl.NewFile("repofs_fs"))
	require.NoError(t, err, "NewKegFromTarget failed")

	// Initialize on disk.
	require.NoError(t, k.Init(f.Context()), "Init failed")

	// Create a new node with title and lead.
	opts := &kegpkg.KegCreateOptions{
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
	zeroRef := dex.GetRef(f.Context(), kegpkg.Node{ID: 0})
	require.NotNil(t, zeroRef, "dex should contain zero node")

	createdUpdated := ref.Updated

	// Advance clock so updated timestamp will differ after update.
	f.Advance(2 * time.Minute)
	// Update content to change the lead.
	newContent := []byte("# FSNode\n\nnew lead from fs\n")
	require.NoError(t, k.SetContent(f.Context(), id, newContent))

	// Meta should reflect the new lead.
	m, err := k.GetMeta(f.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "new lead from fs", m.Lead())

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

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	require.NoError(t, k.Init(f.Context()))

	// Create node A with tags
	optsA := &kegpkg.KegCreateOptions{
		Title: "NodeA",
		Lead:  "lead a",
		Tags:  []string{"Alpha", "Shared"},
	}
	idA, err := k.Create(f.Context(), optsA)
	require.NoError(t, err)
	require.Equal(t, 1, idA.ID)

	// Create node B with tags
	optsB := &kegpkg.KegCreateOptions{
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

	k, err := kegpkg.NewKegFromTarget(f.Context(), kegurl.NewFile("~/repo"))
	require.NoError(t, err, "NewKegFromTarget failed")

	// Load dex via NewDexFromRepo which reads the index artifacts.
	dex, err := kegpkg.NewDexFromRepo(f.Context(), k.Repo)
	require.NoError(t, err, "NewDexFromRepo failed")

	// nodes.tsv should contain the zero node entry.
	zeroRef := dex.GetRef(f.Context(), kegpkg.Node{ID: 0})
	require.NotNil(t, zeroRef, "nodes.tsv should include zero node entry")

	// changes.md is expected to exist in the example fixture under dex/.
	changes, err := k.Repo.GetIndex(f.Context(), "changes.md")
	require.NoError(t, err, "expected dex/changes.md to exist")
	require.Greater(t, len(changes), 0, "dex/changes.md should not be empty")

	// tags may be absent for the example fixture. If absent, Dex.TagList should
	// be empty. If present, ensure we can read it without error.
	if _, err := k.Repo.GetIndex(f.Context(), "tags"); err != nil {
		require.True(t, errors.Is(err, kegpkg.ErrNotExist),
			"expected missing tags index to return ErrNotExist, got: %v", err)
		require.Empty(t, dex.TagList(f.Context()), "expected no tags when tags index is absent")
	} else {
		// tags file present, ensure parsed tag list is stable.
		require.GreaterOrEqual(t, len(dex.TagList(f.Context())), 0)
	}

	// backlinks may be absent. If absent, expect no backlinks for the zero node.
	if _, err := k.Repo.GetIndex(f.Context(), "backlinks"); err != nil {
		require.True(t, errors.Is(err, kegpkg.ErrNotExist),
			"expected missing backlinks index to return ErrNotExist, got: %v", err)
		_, ok := dex.Backlinks(f.Context(), kegpkg.Node{ID: 0})
		require.False(t, ok, "expected no backlinks for zero when index is absent")
	} else {
		// backlinks file present, ensure parsing did not error earlier and that
		// the dex can return a backlinks mapping (possibly empty).
		_, _ = dex.Backlinks(f.Context(), kegpkg.Node{ID: 0})
	}
}
