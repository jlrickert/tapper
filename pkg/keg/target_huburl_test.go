package keg_test

import (
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// TestNewKegFromTargetHubURL verifies the keg-scheme branch composes the API
// base URL against the resolved HubURL, and errors when HubURL is unset (a keg
// reference that was never resolved against a hub).
func TestNewKegFromTargetHubURL(t *testing.T) {
	t.Parallel()
	f := NewSandbox(t)

	t.Run("resolved HubURL is honored", func(t *testing.T) {
		target := kegpkg.NewApi("atlas", "jared", "work",
			kegpkg.WithHubURL("https://atlas.foldwise.ai"))
		k, err := kegpkg.NewKegFromTarget(f.Context(), target, f.Runtime())
		require.NoError(t, err)
		repo, ok := k.Repo.(*kegpkg.ApiRepo)
		require.True(t, ok, "expected *ApiRepo, got %T", k.Repo)
		require.Equal(t,
			"https://atlas.foldwise.ai/api/v1/@jared/kegs/@work",
			repo.BaseURL,
		)
	})

	t.Run("trailing slash on HubURL is trimmed", func(t *testing.T) {
		target := kegpkg.NewApi("atlas", "jared", "work",
			kegpkg.WithHubURL("https://atlas.foldwise.ai/"))
		k, err := kegpkg.NewKegFromTarget(f.Context(), target, f.Runtime())
		require.NoError(t, err)
		repo, ok := k.Repo.(*kegpkg.ApiRepo)
		require.True(t, ok, "expected *ApiRepo, got %T", k.Repo)
		require.Equal(t,
			"https://atlas.foldwise.ai/api/v1/@jared/kegs/@work",
			repo.BaseURL,
		)
	})

	t.Run("missing HubURL is an error", func(t *testing.T) {
		// No WithHubURL: a keg reference that never went through hub resolution
		// has no host to compose an API URL against.
		target := kegpkg.NewApi("knut", "alice", "blog")
		_, err := kegpkg.NewKegFromTarget(f.Context(), target, f.Runtime())
		require.Error(t, err)
		require.Contains(t, err.Error(), "no resolved hub url")
	})
}
