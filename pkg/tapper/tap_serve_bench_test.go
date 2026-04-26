package tapper_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// newBenchHandler constructs a serve handler backed by a MemoryRepo keg with
// test nodes. It bypasses the sandbox (which requires *testing.T) and builds
// the runtime directly from b.TempDir().
func newBenchHandler(b *testing.B) http.Handler {
	b.Helper()

	jail := b.TempDir()
	home := "/home/benchuser"
	user := "benchuser"

	rt, err := toolkit.NewTestRuntime(
		jail, home, user,
		toolkit.WithRuntimeClock(clock.NewTestClock(time.Date(2025, 10, 15, 12, 30, 0, 0, time.UTC))),
		toolkit.WithRuntimeHasher(&toolkit.MD5Hasher{}),
	)
	require.NoError(b, err)

	root := "/home/benchuser/work"
	require.NoError(b, rt.Mkdir(root, 0o755, true))
	require.NoError(b, rt.Setwd(root))

	tap, err := tapper.NewTap(tapper.TapOptions{
		Root:    root,
		Runtime: rt,
	})
	require.NoError(b, err)

	// Write user config.
	userCfg := "fallbackKeg: bench\nkegSearchPaths:\n  - /home/benchuser/kegs\n"
	require.NoError(b, rt.Mkdir(tap.PathService.ConfigRoot, 0o755, true))
	require.NoError(b, rt.AtomicWriteFile(tap.PathService.UserConfig(), []byte(userCfg), 0o644))

	// Create and init keg.
	kegDir := "/home/benchuser/kegs/bench"
	require.NoError(b, rt.Mkdir(kegDir, 0o755, true))
	k, err := keg.NewKegFromTarget(b.Context(), keg.NewFile(kegDir), rt)
	require.NoError(b, err)
	require.NoError(b, k.Init(b.Context()))

	ctx := b.Context()

	// Create test nodes: 3 nodes with tags.
	_, err = tap.Create(ctx, tapper.CreateOptions{
		Title: "First Node",
		Lead:  "This is the first node.",
		Tags:  []string{"alpha", "beta"},
	})
	require.NoError(b, err)

	_, err = tap.Create(ctx, tapper.CreateOptions{
		Title: "Second Node",
		Lead:  "This is the second node.",
		Tags:  []string{"beta", "gamma"},
	})
	require.NoError(b, err)

	body := []byte("# Third Node\n\nSee [first](../1) for details.\n")
	_, err = tap.Create(ctx, tapper.CreateOptions{
		Stream: &toolkit.Stream{IsPiped: true, In: bytes.NewReader(body)},
		Tags:   []string{"alpha"},
	})
	require.NoError(b, err)

	handler, err := tap.NewServeHandler(ctx, tapper.ServeOptions{Title: "Bench KEG"})
	require.NoError(b, err)
	return handler
}

// BenchmarkServe_IndexPage benchmarks the landing page handler.
func BenchmarkServe_IndexPage(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	// Warm up: one request to load dex caches.
	resp, err := http.Get(srv.URL + "/")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_NodePage benchmarks a rendered node page.
func BenchmarkServe_NodePage(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/1/")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/1/")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_NodeRawReadme benchmarks the raw README.md endpoint.
func BenchmarkServe_NodeRawReadme(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/1/README.md")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/1/README.md")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_TagsIndex benchmarks the tags index page.
func BenchmarkServe_TagsIndex(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/tags/")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/tags/")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_TagPage benchmarks a single tag page.
func BenchmarkServe_TagPage(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/tags/beta/")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/tags/beta/")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_ChangesPage benchmarks the changes page.
func BenchmarkServe_ChangesPage(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/changes/")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/changes/")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_NodeMetaJSON benchmarks the meta.json endpoint.
func BenchmarkServe_NodeMetaJSON(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/1/meta.json")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/1/meta.json")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_NodeStatsJSON benchmarks the stats.json endpoint.
func BenchmarkServe_NodeStatsJSON(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/1/stats.json")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/1/stats.json")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}

// BenchmarkServe_NotFound benchmarks the 404 handler.
func BenchmarkServe_NotFound(b *testing.B) {
	handler := newBenchHandler(b)
	srv := httptest.NewServer(handler)
	b.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/9999/")
	require.NoError(b, err)
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	b.ResetTimer()
	for b.Loop() {
		resp, err := http.Get(srv.URL + "/9999/")
		if err != nil {
			b.Fatal(err)
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}
}
