package tapper_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestKegBackendLabel(t *testing.T) {
	t.Parallel()

	t.Run("nil_target_returns_empty", func(t *testing.T) {
		t.Parallel()
		require.Equal(t, "", tapper.KegBackendLabel(nil))
	})

	t.Run("file_target_collapses_to_file_backed", func(t *testing.T) {
		t.Parallel()
		target := keg.NewFile("/home/testuser/Documents/kegs/notes")
		require.Equal(t, "file-backed", tapper.KegBackendLabel(&target))
	})

	t.Run("hub_target_renders_canonical_shorthand", func(t *testing.T) {
		t.Parallel()
		target := keg.NewApi("knut", "alice", "blog")
		require.Equal(t, "knut:@alice/blog", tapper.KegBackendLabel(&target))
	})

	t.Run("memory_target_collapses_to_in_memory", func(t *testing.T) {
		t.Parallel()
		target := keg.NewMemory("scratch")
		require.Equal(t, "in-memory", tapper.KegBackendLabel(&target))
	})

	t.Run("http_target_returns_scheme_only", func(t *testing.T) {
		t.Parallel()
		target, err := keg.Parse("https://example.com/kegs/blog")
		require.NoError(t, err)
		require.Equal(t, "https", tapper.KegBackendLabel(target))
	})
}
