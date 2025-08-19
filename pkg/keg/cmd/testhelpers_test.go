package cmd_test

import (
	"bytes"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/keg/cmd"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewTestDeps returns a CmdDeps pre-wired for tests along with the underlying
// buffers (stdin/out/err) and the in-memory repository. Pass t from your test
// so we can call t.Helper() and optionally add cleanup hooks later.
func NewTestDeps(t *testing.T) (*cmd.CmdDeps, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer, keg.KegRepository) {
	t.Helper()

	repo := keg.NewMemoryRepo()
	k := keg.NewKeg(repo, nil)

	in := &bytes.Buffer{}  // use in.WriteString(...) to provide stdin content
	out := &bytes.Buffer{} // capture stdout
	errOut := &bytes.Buffer{}

	deps := &cmd.CmdDeps{
		Keg: k,
		In:  in,
		Out: out,
		Err: errOut,
	}

	return deps, in, out, errOut, repo
}

// NewTestDepsWithRepo returns a CmdDeps pre-wired for tests that use the provided
// repository. It returns the CmdDeps plus the underlying buffers (stdin/out/err)
// and the same repo interface passed in. Pass t from your test so we can call
// t.Helper() and optionally add cleanup hooks later.
func NewTestDepsWithRepo(t *testing.T, repo keg.KegRepository) (*cmd.CmdDeps, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer, keg.KegRepository) {
	t.Helper()

	k := keg.NewKeg(repo, nil)

	in := &bytes.Buffer{}  // use in.WriteString(...) to provide stdin content
	out := &bytes.Buffer{} // capture stdout
	errOut := &bytes.Buffer{}

	deps := &cmd.CmdDeps{
		Keg: k,
		In:  in,
		Out: out,
		Err: errOut,
	}

	return deps, in, out, errOut, repo
}

type TestFixture struct {
	T    *testing.T
	Deps *cmd.CmdDeps

	UserConfig  tapper.UserConfig
	LocalConfig tapper.LocalConfig

	In  *bytes.Buffer
	Out *bytes.Buffer
	Err *bytes.Buffer

	Repo keg.KegRepository
}

// NewTestFixtureWithRepo constructs a TestFixture wired to the provided repo.
// Use NewTestFixture(t) as a convenience for the in-memory repo.
func NewTestFixtureWithRepo(t *testing.T, repo keg.KegRepository) *TestFixture {
	t.Helper()

	// Create standard test deps wired to repo. This helper (NewTestDepsWithRepo)
	// can be the underlying implementation you already have.
	deps, in, out, errOut, repoIface := NewTestDepsWithRepo(t, repo)

	return &TestFixture{
		T:    t,
		Deps: deps,
		In:   in,
		Out:  out,
		Err:  errOut,
		Repo: repoIface,
	}
}

// NewTestFixture returns a fixture backed by an in-memory repo (fast unit tests).
func NewTestFixture(t *testing.T) *TestFixture {
	t.Helper()
	mem := keg.NewMemoryRepo()
	return NewTestFixtureWithRepo(t, mem)
}

// Run executes the CLI command using the fixture's CmdDeps.
func (f *TestFixture) Run(args []string) error {
	f.T.Helper()
	return cmd.RunWithDeps(f.T.Context(), args, f.Deps)
}

// RunOrFail executes and fails the test on error.
func (f *TestFixture) RunOrFail(args []string) {
	f.T.Helper()
	if err := f.Run(args); err != nil {
		f.T.Fatalf("command %v failed: %v\nstderr: %s\nstdout: %s", args, err, f.Stderr(), f.Stdout())
	}
}

// Reset clears captured output (use between runs).
func (f *TestFixture) Reset() {
	f.T.Helper()
	f.In.Reset()
	f.Out.Reset()
	f.Err.Reset()
}

// SetInput writes a string to stdin (convenience).
func (f *TestFixture) SetInput(s string) {
	f.T.Helper()
	f.In.Reset()
	f.In.WriteString(s)
}

// Stdout returns stdout as a string.
func (f *TestFixture) Stdout() string {
	return f.Out.String()
}

// Stderr returns stderr as a string.
func (f *TestFixture) Stderr() string {
	return f.Err.String()
}
