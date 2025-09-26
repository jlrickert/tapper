package keg_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/keg"
)

// FixtureOption modifies a Fixture during construction.
type FixtureOption func(f *Fixture)

// Fixture bundles common test setup (ctx, env, logger, clock, services, tempdir, helpers).
type Fixture struct {
	t *testing.T

	ctx context.Context

	logger *std.TestHandler
	env    *std.MapEnv
	clock  *std.TestClock
	hasher *keg.MD5Hasher

	// optional runtime state
	tempDir string
}

// NewFixture constructs a Fixture and applies given options. It registers
// cleanup with t.Cleanup so callers don't need to call a cleanup func.
func NewFixture(t *testing.T, opts ...FixtureOption) *Fixture {
	t.Helper()

	tempDir := t.TempDir()
	lg, handler := std.NewTestLogger(t, std.ParseLevel("debug"))
	env := std.NewTestEnv(filepath.Join(tempDir, "home", "testuser"), "testuser")
	clock := std.NewTestClock(time.Now())
	hasher := &keg.MD5Hasher{}

	// populate common temp env vars
	tmp := filepath.Join(tempDir, "tmp")
	_ = env.Set("TMPDIR", tmp) // preferred on Unix/macOS
	_ = env.Set("TMP", tmp)
	_ = env.Set("TEMP", tmp)
	_ = env.Set("TEMPDIR", tmp)

	ctx := context.Background()
	ctx = std.WithLogger(ctx, lg)
	ctx = std.WithEnv(ctx, env)
	ctx = std.WithClock(ctx, clock)

	f := &Fixture{
		t:       t,
		ctx:     ctx,
		logger:  handler,
		hasher:  hasher,
		env:     env,
		clock:   clock,
		tempDir: tempDir,
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
		f.tempDir = tmp
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

// AbsPath returns an absolute path relative to TempDir if it's set; otherwise
// attempts to make the provided path absolute.
func (f *Fixture) AbsPath(rel string) string {
	f.t.Helper()
	if filepath.IsAbs(rel) || f.tempDir == "" {
		abs, err := filepath.Abs(rel)
		if err != nil {
			f.t.Fatalf("AbsPath failed: %v", err)
		}
		return abs
	}
	return std.AbsPath(f.ctx, filepath.Join(f.tempDir, rel))
}

func (f *Fixture) cleanup() {
	// reserved for future teardown (stop mocks, remove long-lived artifacts, etc.)
}
