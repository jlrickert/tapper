package tap_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/tap"
)

// FixtureOption modifies a Fixture during construction.
type FixtureOption func(f *Fixture)

// Fixture bundles common test setup (ctx, env, logger, clock, tempdir, helpers).
type Fixture struct {
	t *testing.T

	ctx    context.Context
	logger *std.TestHandler
	env    *std.MapEnv
	clock  *std.TestClock

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

	tmp := filepath.Join(tempDir, "tmp")
	env.Set("TMPDIR", tmp) // preferred on Unix/macOS
	env.Set("TMP", tmp)
	env.Set("TEMP", tmp)
	env.Set("TEMPDIR", tmp)

	ctx := context.Background()
	ctx = std.WithLogger(ctx, lg)
	ctx = std.WithEnv(ctx, env)
	ctx = std.WithClock(ctx, clock)

	f := &Fixture{
		t:       t,
		ctx:     ctx,
		logger:  handler,
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

func (f *Fixture) Context() context.Context {
	return f.ctx
}

// Helper wrapper: WriteLocalConfigFile writes LocalConfig into projectPath/.tapper/local.yaml
// and fails the test on error.
func (f *Fixture) WriteLocalConfigFile(projectPath string, lf *tap.LocalConfig) {
	f.t.Helper()
	if err := lf.WriteLocalFile(f.ctx, projectPath); err != nil {
		f.t.Fatalf("WriteLocalConfigFile failed: %v", err)
	}
}

// Helper wrapper: WriteUserConfigFile writes a UserConfig to path and fails on error.
func (f *Fixture) WriteUserConfigFile(path string, uc *tap.UserConfig) {
	f.t.Helper()
	if err := uc.WriteUserConfig(f.ctx, path); err != nil {
		f.t.Fatalf("WriteUserConfigFile failed: %v", err)
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
