package keg_test

import (
	"context"
	"embed"
	iofs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/keg"
)

//go:embed data/**
var testdata embed.FS

// FixtureOption modifies a Fixture during construction.
type FixtureOption func(f *Fixture)

// Fixture bundles common test setup (ctx, env, logger, clock, services, tempdir, helpers).
type Fixture struct {
	t *testing.T

	ctx context.Context

	logger *std.TestHandler
	env    *std.TestEnv
	clock  *std.TestClock
	hasher *keg.MD5Hasher

	// optional runtime state
	// Jail is a temporary directory that acts as the root filesystem
	Jail string
}

// NewFixture constructs a Fixture and applies given options. It registers
// cleanup with t.Cleanup so callers don't need to call a cleanup func.
func NewFixture(t *testing.T, opts ...FixtureOption) *Fixture {
	t.Helper()

	jail := t.TempDir()
	lg, handler := std.NewTestLogger(t, std.ParseLevel("debug"))
	env := std.NewTestEnv(jail, filepath.Join("home", "testuser"), "testuser")
	clock := std.NewTestClock(time.Now())
	hasher := &keg.MD5Hasher{}

	// env.Setwd(jail)

	// populate common temp env vars
	tmp := filepath.Join(jail, "tmp")
	_ = env.Set("TMPDIR", tmp) // preferred on Unix/macOS
	_ = env.Set("TMP", tmp)
	_ = env.Set("TEMP", tmp)
	_ = env.Set("TEMPDIR", tmp)

	ctx := context.Background()
	ctx = std.WithLogger(ctx, lg)
	ctx = std.WithEnv(ctx, env)
	ctx = std.WithClock(ctx, clock)

	f := &Fixture{
		t:      t,
		ctx:    ctx,
		logger: handler,
		hasher: hasher,
		env:    env,
		clock:  clock,
		Jail:   jail,
	}

	// apply options
	for _, opt := range opts {
		opt(f)
	}

	// register cleanup (reserved for future teardown)
	t.Cleanup(func() { f.cleanup() })

	return f
}

// WithEnv sets a single env entry in the fixture's Env.
func WithEnv(key, val string) FixtureOption {
	return func(f *Fixture) {
		f.t.Helper()
		if f.env == nil {
			f.t.Fatalf("WithEnv: fixture Env is nil")
		}
		if err := f.env.Set(key, val); err != nil {
			f.t.Fatalf("WithEnv failed to set %s: %v", key, err)
		}
	}
}

// WithClock sets the test clock to the provided time.
func WithClock(t0 time.Time) FixtureOption {
	return func(f *Fixture) {
		f.t.Helper()
		if f.clock == nil {
			f.t.Fatalf("WithClock: fixture Clock is nil")
		}
		f.clock.Set(t0)
	}
}

// WithTempDir creates a t.TempDir and sets it on the fixture (and optionally HOME).
func WithTempDir(setAsHome bool) FixtureOption {
	return func(f *Fixture) {
		f.t.Helper()
		tmp := f.t.TempDir()
		f.Jail = tmp
		if setAsHome {
			if err := f.env.SetHome(tmp); err != nil {
				f.t.Fatalf("WithTempDir SetHome failed: %v", err)
			}
		}
	}
}

// WithEnvMap seeds multiple environment variables from a map.
func WithEnvMap(m map[string]string) FixtureOption {
	return func(f *Fixture) {
		f.t.Helper()
		for k, v := range m {
			if err := f.env.Set(k, v); err != nil {
				f.t.Fatalf("WithEnvMap set %s failed: %v", k, err)
			}
		}
	}
}

// fixture is a directory under pkg/keg/data. For example, empty, and example
// are valid values
// path is where the keg data should be copied over.
func WithFileKeg(fixture string, pathArg string) FixtureOption {
	return func(f *Fixture) {
		f.t.Helper()

		// Source is the embedded package data directory.
		src := path.Join("data", fixture)
		if _, err := iofs.Stat(testdata, src); err != nil {
			f.t.Fatalf("WithFileKeg: source %s not found: %v", src, err)
		}

		dst := filepath.Join(f.Jail, pathArg)
		if err := copyEmbedDir(testdata, src, dst); err != nil {
			f.t.Fatalf("WithFileKeg: copy %s -> %s failed: %v", src, dst, err)
		}
	}
}

// AbsPath returns an absolute path relative to TempDir if it's set; otherwise
// attempts to make the provided path absolute.
func (f *Fixture) AbsPath(rel string) string {
	f.t.Helper()
	if filepath.IsAbs(rel) || f.Jail == "" {
		abs, err := filepath.Abs(rel)
		if err != nil {
			f.t.Fatalf("AbsPath failed: %v", err)
		}
		return abs
	}
	return std.AbsPath(f.ctx, filepath.Join(f.Jail, rel))
}

func (f *Fixture) cleanup() {
	// reserved for future teardown (stop mocks, remove long-lived artifacts, etc.)
}

// DumpJailTree logs a tree of files rooted at the fixture's Jail.
//
// maxDepth limits recursion depth. maxDepth <= 0 means unlimited depth.
func (f *Fixture) DumpJailTree(maxDepth int) {
	f.t.Helper()
	if f.Jail == "" {
		f.t.Log("DumpJailTree: no jail set")
		return
	}

	f.t.Logf("Jail tree: %s", f.Jail)
	err := filepath.WalkDir(f.Jail, func(p string, d iofs.DirEntry, err error) error {
		if err != nil {
			f.t.Logf("  error: %v", err)
			return nil
		}
		rel, err := filepath.Rel(f.Jail, p)
		if err != nil {
			rel = p
		}
		// Normalize current dir
		if rel == "." {
			rel = "."
		}
		// Apply depth limit when requested
		if maxDepth > 0 && rel != "." {
			depth := strings.Count(rel, string(os.PathSeparator)) + 1
			if depth > maxDepth {
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
		}
		suffix := ""
		if d.IsDir() {
			suffix = "/"
		}
		f.t.Logf("  %s%s", rel, suffix)
		return nil
	})
	if err != nil {
		f.t.Logf("DumpJailTree walk error: %v", err)
	}
}

// copyEmbedDir recursively copies a directory tree from an embedded FS to dst.
func copyEmbedDir(fsys embed.FS, src, dst string) error {
	entries, err := iofs.ReadDir(fsys, src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	for _, e := range entries {
		s := path.Join(src, e.Name())
		d := filepath.Join(dst, e.Name())
		if e.IsDir() {
			if err := copyEmbedDir(fsys, s, d); err != nil {
				return err
			}
			continue
		}
		data, err := fsys.ReadFile(s)
		if err != nil {
			return err
		}
		if err := os.WriteFile(d, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}
