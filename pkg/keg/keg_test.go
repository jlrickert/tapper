package keg_test

import (
	"errors"
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/stretchr/testify/require"
)

// TestInitWhenRepoIsExample attempts to Init a keg when the repo already
// contains the example data. Init should fail with ErrExist.
func TestInitWhenRepoIsExample(t *testing.T) {
	t.SkipNow()
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
	t.SkipNow()
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
	t.Skip()
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
