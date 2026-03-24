package tapper_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestKegService_QueryResolver_FavoriteIndex verifies that kegs resolved
// through KegService have the query resolver injected so that key=value
// attribute predicates (e.g. "favorite=true") work in config-driven custom
// indexes. This is the integration test for the resolver wiring fix.
func TestKegService_QueryResolver_FavoriteIndex(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))
	root := "/home/testuser/work"
	require.NoError(t, fx.Runtime().Mkdir(root, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write user config pointing to keg search path.
	userCfg := `fallbackKeg: test
kegs: {}
defaultRegistry: ""
kegSearchPaths:
  - /home/testuser/kegs
`
	require.NoError(t, fx.Runtime().Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	// Create and init the keg directly (Init writes a proper config file).
	kegDir := "/home/testuser/kegs/test"
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))
	initKeg, err := keg.NewKegFromTarget(fx.Context(), kegurl.NewFile(kegDir), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, initKeg.Init(fx.Context()))

	// Add the "favorite" custom index to the keg config.
	require.NoError(t, initKeg.UpdateConfig(fx.Context(), func(cfg *keg.Config) {
		cfg.Indexes = append(cfg.Indexes, keg.IndexEntry{
			File:    "dex/favorite",
			Summary: "favorite nodes",
			Query:   "favorite=true",
		})
	}))

	ctx := context.Background()

	// Create nodes through the Tap API, which resolves via KegService
	// and triggers injectDexOpts.
	favID, err := tap.Create(ctx, tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "test"},
		Title:            "My Favorite Node",
		Attrs:            map[string]string{"favorite": "true"},
	})
	require.NoError(t, err)

	_, err = tap.Create(ctx, tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "test"},
		Title:            "Regular Node",
	})
	require.NoError(t, err)

	yesID, err := tap.Create(ctx, tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "test"},
		Title:            "Also Favorite",
		Attrs:            map[string]string{"favorite": "yes"},
	})
	require.NoError(t, err)

	// Rebuild all indexes. This goes through Tap -> KegService -> Keg.Index,
	// which should now have the query resolver injected.
	_, err = tap.Index(ctx, tapper.IndexOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "test"},
	})
	require.NoError(t, err)

	// Read the custom "favorite" index.
	content, err := tap.IndexCat(ctx, tapper.IndexCatOptions{
		KegTargetOptions: tapper.KegTargetOptions{Keg: "test"},
		Name:             "favorite",
	})
	require.NoError(t, err, "favorite index should exist after reindex")
	require.NotEmpty(t, content, "favorite index should not be empty")

	// The node with favorite=true should be present.
	require.Contains(t, content, "My Favorite Node",
		"node with favorite=true should appear in the favorite index")
	require.Contains(t, content, favID.String(),
		"favorite node ID should appear in the favorite index")

	// The node without favorite attr should NOT be present.
	require.NotContains(t, content, "Regular Node",
		"node without favorite attr should not appear in the favorite index")

	// The node with favorite=yes should NOT match favorite=true query.
	require.NotContains(t, content, "Also Favorite",
		"node with favorite=yes should not match favorite=true query")
	_ = yesID // used above in assertions
}

// TestKegService_QueryResolver_ProjectKeg verifies the resolver is injected
// for project-local kegs resolved via --project / --path.
func TestKegService_QueryResolver_ProjectKeg(t *testing.T) {
	t.Parallel()

	fx := NewSandbox(t, sandbox.WithFixture("example", "/home/testuser"))

	// Set up a project-local keg at <root>/kegs/<project>/
	root := "/home/testuser/myproject"
	kegDir := filepath.Join(root, "kegs", "myproject")
	require.NoError(t, fx.Runtime().Mkdir(kegDir, 0o755, true))
	require.NoError(t, fx.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: fx.Runtime(),
	})
	require.NoError(t, err)

	// Write minimal user config (no keg aliases -- project resolution only).
	require.NoError(t, fx.Runtime().Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(
		tap.PathService.UserConfig(),
		[]byte("kegs: {}\ndefaultRegistry: \"\"\n"),
		0o644,
	))

	// Init the keg.
	initKeg, err := keg.NewKegFromTarget(fx.Context(), kegurl.NewFile(kegDir), fx.Runtime())
	require.NoError(t, err)
	require.NoError(t, initKeg.Init(fx.Context()))

	// Add "pinned" custom index.
	require.NoError(t, initKeg.UpdateConfig(fx.Context(), func(cfg *keg.Config) {
		cfg.Indexes = append(cfg.Indexes, keg.IndexEntry{
			File:    "dex/pinned",
			Summary: "pinned notes",
			Query:   "pinned=yes",
		})
	}))

	ctx := context.Background()

	// Create nodes through Tap with explicit path.
	_, err = tap.Create(ctx, tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Path: kegDir},
		Title:            "Pinned Note",
		Attrs:            map[string]string{"pinned": "yes"},
	})
	require.NoError(t, err)

	_, err = tap.Create(ctx, tapper.CreateOptions{
		KegTargetOptions: tapper.KegTargetOptions{Path: kegDir},
		Title:            "Unpinned Note",
	})
	require.NoError(t, err)

	// Rebuild indexes.
	_, err = tap.Index(ctx, tapper.IndexOptions{
		KegTargetOptions: tapper.KegTargetOptions{Path: kegDir},
	})
	require.NoError(t, err)

	// Verify the custom index.
	content, err := tap.IndexCat(ctx, tapper.IndexCatOptions{
		KegTargetOptions: tapper.KegTargetOptions{Path: kegDir},
		Name:             "pinned",
	})
	require.NoError(t, err, "pinned index should exist")
	require.Contains(t, content, "Pinned Note")
	require.NotContains(t, content, "Unpinned Note")
}
