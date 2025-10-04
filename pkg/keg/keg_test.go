package keg_test

import (
	"errors"
	"slices"
	"testing"
	"time"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/stretchr/testify/require"
)

// TestInitWhenRepoIsExample attempts to Init a keg when the repo already
// contains the example data. Init should fail with ErrExist.
func TestInitWhenRepoIsExample(t *testing.T) {
	t.Parallel()
	f := NewFixture(t, WithFileKeg("example", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.ctx, kegurl.NewFile("repo"))
	require.NoError(t, err, "NewKegFromTarget failed")

	err = k.Init(f.ctx)
	require.Error(t, err)
	require.Truef(t, errors.Is(err, kegpkg.ErrExist),
		"Init expected ErrExist, got: %v", err)
}

// TestInitOnEmptyRepo initializes a new keg in an empty fixture repo and
// verifies the repository reports an initialized keg and a zero node exists.
func TestInitOnEmptyRepo(t *testing.T) {
	t.Parallel()
	f := NewFixture(t, WithFileKeg("empty", "repo"))

	k, err := kegpkg.NewKegFromTarget(f.ctx, kegurl.NewFile("repo"))
	require.NoError(t, err, "NewKegFromTarget failed")

	require.NoError(t, k.Init(f.ctx), "Init failed")

	// Repo should now report a keg exists.
	exists, err := kegpkg.IsKegInitiated(f.ctx, k.Repo)
	require.NoError(t, err, "KegExists returned error")
	require.True(t, exists, "KegExists expected true after Init")

	// Ensure a zero node is present.
	ids, err := k.Repo.ListNodes(f.ctx)
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
	f := NewFixture(t)

	repo := kegpkg.NewMemoryRepo()

	// Initially not initialized.
	exists, err := kegpkg.IsKegInitiated(f.ctx, repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for new memory repo")

	// Initialize via Keg.Init and re-check.
	k := kegpkg.NewKeg(repo)
	require.NoError(t, k.Init(f.ctx), "Init failed for memory repo")

	exists, err = kegpkg.IsKegInitiated(f.ctx, repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after Init")
}

// TestKegExistsWithFsRepo verifies KegExists behavior using the filesystem
// repository. It uses the provided empty fixture and ensures behavior mirrors
// the memory repo.
func TestKegExistsWithFsRepo(t *testing.T) {
	t.Parallel()
	f := NewFixture(t, WithFileKeg("empty", "repofs"))

	k, err := kegpkg.NewKegFromTarget(f.ctx, kegurl.NewFile("repofs"))
	require.NoError(t, err, "NewKegFromTarget failed")

	// Uninitialized on disk.
	exists, err := kegpkg.IsKegInitiated(f.ctx, k.Repo)
	require.NoError(t, err)
	require.False(t, exists, "expected KegExists false for empty fs repo")

	// Initialize and verify.
	require.NoError(t, k.Init(f.ctx), "Init failed for fs repo")

	exists, err = kegpkg.IsKegInitiated(f.ctx, k.Repo)
	require.NoError(t, err)
	require.True(t, exists, "expected KegExists true after Init")
}

// Additional tests

// TestCreateZeroNodeInMemoryRepo verifies creating the zero node via Create
// on a fresh in-memory repository. The zero node should contain the
// RawZeroNodeContent.
func TestCreateZeroNodeInMemoryRepo(t *testing.T) {
	t.Parallel()
	f := NewFixture(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	k.Init(f.Context())

	b, err := k.GetContent(f.ctx, kegpkg.Node{ID: 0})
	require.NoError(t, err)
	require.Contains(t, string(b), "Sorry, planned but not yet available")
}

// TestCreateNodeWithMeta ensures non-zero nodes created with options write
// sensible content and meta that can be parsed.
func TestCreateNodeWithMeta(t *testing.T) {
	t.Parallel()
	f := NewFixture(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	k.Init(f.Context())

	opts := &kegpkg.KegCreateOptions{
		Title: "MyTitle",
		Lead:  "short lead",
		Tags:  []string{"TagA", "tag-a"},
	}
	id, err := k.Create(f.ctx, opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	content, err := k.GetContent(f.ctx, id)
	require.NoError(t, err)
	require.Contains(t, string(content), "# MyTitle")

	m, err := k.GetMeta(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, "MyTitle", m.Title())
	require.Equal(t, "short lead", m.Lead())
	// normalized tags should include "tag-a"
	foundTag := slices.Contains(m.Tags(), "tag-a")
	require.True(t, foundTag, "expected normalized tag 'tag-a' to be present")
}

// TestSetContentAndUpdate ensures SetContent causes meta to be updated from
// parsed content (for example lead paragraph changes).
func TestSetContentAndUpdate(t *testing.T) {
	t.Parallel()
	f := NewFixture(t)

	repo := kegpkg.NewMemoryRepo()
	k := kegpkg.NewKeg(repo)
	k.Init(f.Context())

	// create zero and a second node
	_, err := k.Create(f.ctx, nil)
	require.NoError(t, err)

	id, err := k.Create(f.ctx, &kegpkg.KegCreateOptions{Title: "Initial"})
	require.NoError(t, err)

	// change content to include a new lead paragraph
	newContent := []byte("# Initial\n\nupdated lead paragraph\n")
	require.NoError(t, k.SetContent(f.ctx, id, newContent))

	m, err := k.GetMeta(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, "updated lead paragraph", m.Lead())
}

// TestCreateAndUpdateNodesWithFsRepo uses the filesystem repo to create a
// node, ensures the dex contains the node, updates content, and validates
// meta and dex timestamps reflect the update.
func TestCreateAndUpdateNodesWithFsRepo(t *testing.T) {
	t.Parallel()
	// Use the empty fixture as a filesystem-backed repo.
	f := NewFixture(t, WithFileKeg("empty", "repofs_fs"))

	k, err := kegpkg.NewKegFromTarget(f.ctx, kegurl.NewFile("repofs_fs"))
	require.NoError(t, err, "NewKegFromTarget failed")

	// Initialize on disk.
	require.NoError(t, k.Init(f.ctx), "Init failed")

	// Create a new node with title and lead.
	opts := &kegpkg.KegCreateOptions{
		Title: "FSNode",
		Lead:  "lead fs",
	}
	id, err := k.Create(f.ctx, opts)
	require.NoError(t, err)
	require.Equal(t, 1, id.ID, "expected created node id to be 1")

	// Dex should expose the node entry.
	dex, err := k.Dex(f.ctx)
	require.NoError(t, err)

	ref := dex.GetRef(f.ctx, id)
	require.NotNil(t, ref, "dex should contain created node")
	require.Equal(t, id.Path(), ref.ID)

	// Ensure zero node is present in dex as well.
	zeroRef := dex.GetRef(f.ctx, kegpkg.Node{ID: 0})
	require.NotNil(t, zeroRef, "dex should contain zero node")

	createdUpdated := ref.Updated

	// Advance clock so updated timestamp will differ after update.
	f.clock.Advance(2 * time.Minute)
	// Update content to change the lead.
	newContent := []byte("# FSNode\n\nnew lead from fs\n")
	require.NoError(t, k.SetContent(f.ctx, id, newContent))

	// Meta should reflect the new lead.
	m, err := k.GetMeta(f.ctx, id)
	require.NoError(t, err)
	require.Equal(t, "new lead from fs", m.Lead())

	// Dex entry should have a newer updated timestamp.
	ref2 := dex.GetRef(f.ctx, id)
	require.NotNil(t, ref2)
	require.True(t, ref2.Updated.After(createdUpdated),
		"expected dex updated timestamp to advance after content update")
}
