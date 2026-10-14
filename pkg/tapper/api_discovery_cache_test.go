package tapper_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func TestAPIDiscoveryCache_PersistsUnderCacheRoot(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	tap := newTestTap(t, sb)
	at := sb.Runtime().Clock().Now().UTC()

	cache := tapper.NewAPIDiscoveryCache(sb.Runtime(), tap.PathService)
	require.NotNil(t, cache)
	_, ok := cache.Get("rev https://hub.example.com")
	require.False(t, ok, "an absent file is a miss")

	cache.Put("rev https://hub.example.com", at)
	path := filepath.Join(tap.PathService.CacheRoot, "api-discovery.json")
	info, err := sb.Runtime().Stat(path, false)
	require.NoError(t, err)
	require.Equal(t, "-rw-------", info.Mode().Perm().String())

	// A new cache over the same file sees the entry, as the next invocation does.
	reopened := tapper.NewAPIDiscoveryCache(sb.Runtime(), tap.PathService)
	got, ok := reopened.Get("rev https://hub.example.com")
	require.True(t, ok)
	require.True(t, got.Equal(at), "got %v want %v", got, at)

	reopened.Delete("rev https://hub.example.com")
	_, ok = cache.Get("rev https://hub.example.com")
	require.False(t, ok)
}

func TestAPIDiscoveryCache_CorruptFileIsAMiss(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	tap := newTestTap(t, sb)
	path := filepath.Join(tap.PathService.CacheRoot, "api-discovery.json")
	require.NoError(t, sb.Runtime().Mkdir(tap.PathService.CacheRoot, 0o700, true))
	require.NoError(t, sb.Runtime().AtomicWriteFile(path, []byte("not json"), 0o600))

	cache := tapper.NewAPIDiscoveryCache(sb.Runtime(), tap.PathService)
	_, ok := cache.Get("rev https://hub.example.com")
	require.False(t, ok)
	cache.Put("rev https://hub.example.com", time.Unix(0, 0))
	_, ok = cache.Get("rev https://hub.example.com")
	require.True(t, ok, "a write replaces the corrupt file")
}
