package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/keg" // for memory repo in examples
)

// Fixture bundles common test setup for CLI tests.
type Fixture struct {
	t *testing.T

	Ctx     context.Context
	Handler *std.TestHandler // captured log handler
	Logger  *std.TestHandler // same as Handler; keep for readability
	Env     *std.TestEnv
	Clock   *std.TestClock

	OutBuf bytes.Buffer
	ErrBuf bytes.Buffer
	InBuf  *bytes.Buffer

	Jail string
	Repo keg.KegRepository
}

// NewFixture constructs a Fixture and registers t.Cleanup automatically.
func NewFixture(t *testing.T) *Fixture {
	t.Helper()

	jail := t.TempDir()
	lg, handler := std.NewTestLogger(t, std.ParseLevel("debug"))
	env := std.NewTestEnv(jail, filepath.Join("home", "testuser"), "testuser")
	clock := std.NewTestClock(time.Now())

	// populate common temp env vars; fail test on error to avoid silent setup problems
	tmp := filepath.Join(jail, "tmp")
	if err := env.Set("TMPDIR", tmp); err != nil {
		t.Fatalf("failed to set TMPDIR: %v", err)
	}
	if err := env.Set("TMP", tmp); err != nil {
		t.Fatalf("failed to set TMP: %v", err)
	}
	if err := env.Set("TEMP", tmp); err != nil {
		t.Fatalf("failed to set TEMP: %v", err)
	}
	if err := env.Set("TEMPDIR", tmp); err != nil {
		t.Fatalf("failed to set TEMPDIR: %v", err)
	}
	env.Setwd(jail)

	ctx := t.Context()
	ctx = std.WithLogger(ctx, lg)
	ctx = std.WithEnv(ctx, env)
	ctx = std.WithClock(ctx, clock)

	f := &Fixture{
		t:       t,
		Ctx:     ctx,
		Handler: handler,
		Logger:  handler,
		Env:     env,
		Clock:   clock,
		Jail:    jail,
		InBuf:   &bytes.Buffer{},
		Repo:    keg.NewMemoryRepo(),
	}

	// default Out/Err point to buffers
	f.OutBuf = bytes.Buffer{}
	f.ErrBuf = bytes.Buffer{}

	t.Cleanup(func() {
		f.cleanup()
	})

	return f
}

// Context returns the fixture context.
//
// Use this to call cmd.SetContext(f.Context()) before executing Cobra commands.
func (f *Fixture) Context() context.Context {
	f.t.Helper()
	return f.Ctx
}

// SetCmdIO wires cmd's stdin/stdout/stderr to the fixture buffers.
func (f *Fixture) SetCmdIO(cmd interface {
	SetIn(any)
	SetOut(any)
	SetErr(any)
}) {
	f.t.Helper()
	cmd.SetIn(f.InBuf)
	cmd.SetOut(&f.OutBuf)
	cmd.SetErr(&f.ErrBuf)
}

// Out returns the captured stdout as a string.
func (f *Fixture) Out() string {
	f.t.Helper()
	return f.OutBuf.String()
}

// Err returns the captured stderr as a string.
func (f *Fixture) Err() string {
	f.t.Helper()
	return f.ErrBuf.String()
}

// WriteIn writes s to the fixture stdin buffer (overwrites previous contents).
func (f *Fixture) WriteIn(s string) {
	f.t.Helper()
	f.InBuf.Reset()
	_, _ = f.InBuf.WriteString(s)
}

// cleanup reserved for future teardown (stop mocks, remove long-lived artifacts, etc.)
func (f *Fixture) cleanup() {
	// no-op for now
}
