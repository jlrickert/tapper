package keg_test

import (
	"testing"

	kegpkg "github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// TestNewKegFromTargetHubURL verifies the SchemeHub branch composes the API
// base URL against the resolved HubURL when present, and falls back to the
// legacy "https://<hub>/..." form (hub name as host) when HubURL is unset.
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
			"https://atlas.foldwise.ai/api/v1/kegs/@jared/work",
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
			"https://atlas.foldwise.ai/api/v1/kegs/@jared/work",
			repo.BaseURL,
		)
	})

	t.Run("legacy fallback uses hub name as host", func(t *testing.T) {
		target := kegpkg.NewApi("knut", "alice", "blog")
		k, err := kegpkg.NewKegFromTarget(f.Context(), target, f.Runtime())
		require.NoError(t, err)
		repo, ok := k.Repo.(*kegpkg.ApiRepo)
		require.True(t, ok, "expected *ApiRepo, got %T", k.Repo)
		require.Equal(t,
			"https://knut/api/v1/kegs/@alice/blog",
			repo.BaseURL,
		)
	})
}
