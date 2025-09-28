package cmd_test

import (
	"bytes"
	"log/slog"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/internal"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/keg/cmd"
	"github.com/jlrickert/tapper/pkg/log"
	"github.com/jlrickert/tapper/pkg/tapper"
)

//
// // NewTestDeps returns a CmdDeps pre-wired for tests along with the underlying
// // buffers (stdin/out/err) and the in-memory repository. Pass t from your test
// // so we can call t.Helper() and optionally add cleanup hooks later.
// func NewTestDeps(t *testing.T) (*cmd.CmdDeps, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer, keg.KegRepository) {
// 	t.Helper()
//
// 	repo := keg.NewMemoryRepo()
// 	k := keg.NewKeg(repo, nil)
//
// 	in := &bytes.Buffer{}  // use in.WriteString(...) to provide stdin content
// 	out := &bytes.Buffer{} // capture stdout
// 	errOut := &bytes.Buffer{}
//
// 	lg, _, _ := log.NewLogger(log.LoggerConfig{
// 		Out:  errOut,
// 		JSON: true,
// 	})
//
// 	deps := &cmd.CmdDeps{
// 		Keg:    k,
// 		Logger: lg,
// 		In:     in,
// 		Out:    out,
// 		Err:    errOut,
// 	}
//
// 	return deps, in, out, errOut, repo
// }
//
// // NewTestDepsWithRepo returns a CmdDeps pre-wired for tests that use the provided
// // repository. It returns the CmdDeps plus the underlying buffers (stdin/out/err)
// // and the same repo interface passed in. Pass t from your test so we can call
// // t.Helper() and optionally add cleanup hooks later.
// func NewTestDepsWithKeg(t *testing.T, keg keg.Keg) (*cmd.CmdDeps, *bytes.Buffer, *bytes.Buffer, *bytes.Buffer) {
// 	t.Helper()
//
// 	k := keg.NewKeg(repo, nil)
//
// 	in := &bytes.Buffer{}  // use in.WriteString(...) to provide stdin content
// 	out := &bytes.Buffer{} // capture stdout
// 	errOut := &bytes.Buffer{}
//
// 	deps := &cmd.CmdDeps{
// 		KegResolver: func(root string, cfg *tapper.UserConfig) (tapper.KegTarget, error) {
// 			return tapper.KegTarget{Value: "memory", Source: ""}
// 		},
// 		In:  in,
// 		Out: out,
// 		Err: errOut,
// 	}
//
// 	return deps, in, out, errOut, repo
// }

type TestFixture struct {
	t    *testing.T

	in  *bytes.Buffer
	out *bytes.Buffer
	err *bytes.Buffer

	logger *std.TestHandler
	env    *std.MapEnv
	clock  *std.TestClock
	hasher *tap.MD5Hasher

	Repos map[string]keg.KegRepository
}

// // NewTestFixtureWithRepo constructs a TestFixture wired to the provided repo.
// // Use NewTestFixture(t) as a convenience for the in-memory repo.
// func NewTestFixtureWithRepo(t *testing.T, repo keg.KegRepository) *TestFixture {
// 	t.Helper()
//
// 	// Create standard test deps wired to repo. This helper (NewTestDepsWithRepo)
// 	// can be the underlying implementation you already have.
// 	deps, in, out, errOut, repoIface := NewTestDepsWithRepo(t, repo)
//
// 	return &TestFixture{
// 		T:    t,
// 		Deps: deps,
// 		in:   in,
// 		out:  out,
// 		err:  errOut,
// 		Repo: repoIface,
// 	}
// }

// NewTestFixture returns a fixture backed by an in-memory repo (fast unit tests).
func NewTestFixture(t *testing.T) *TestFixture {
	t.Helper()

	projectDir := t.TempDir()

	repos := make(map[string]keg.KegRepository, 1)
	repos[projectDir] = &keg.MemoryRepo{}

	in := &bytes.Buffer{}
	out := &bytes.Buffer{}
	err := &bytes.Buffer{}

	lg, _, _ := log.NewLogger(log.LoggerConfig{
		JSON:  true,
		Out:   err,
		Level: slog.LevelDebug,
	})

	clock := internal.NewFixedClock(time.Time{})

	deps := &cmd.CmdDeps{
		Project: projectDir,

		In:  in,
		Out: out,
		Err: err,
		LocalConfig: &tapper.LocalConfig{
			Updated: clock.Now().String(),
		},
		UserConfig: &tapper.UserConfig{
			Mappings:[]tapper.Mapping{
				tapper.Mapping{
					Name:     "memory",
					Match:    tapper.MappingMatch{},
					Keg:      tapper.KegTarget{},
					Priority: 0,
				},
			},
			Aliases: map[string]tapper.KegTarget{
				"memory": &tapper.KegTarget{

				},
			},
			DefaultKeg: &tapper.KegTarget{Source: "", Value:},
		},

		Clock:  internal.NewFixedClock(time.Time{}),
		Logger: lg,
	}

	// mem := keg.NewMemoryRepo()
	return &TestFixture{
		T:    t,
		Deps: deps,

		in:  in,
		out: out,
		err: err,

		Repos: repos,
	}
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
	f.in.Reset()
	f.out.Reset()
	f.err.Reset()
}

// SetInput writes a string to stdin (convenience).
func (f *TestFixture) SetInput(s string) {
	f.T.Helper()
	f.in.Reset()
	f.in.WriteString(s)
}

// Stdout returns stdout as a string.
func (f *TestFixture) Stdout() string {
	return f.out.String()
}

// Stderr returns stderr as a string.
func (f *TestFixture) Stderr() string {
	return f.err.String()
}

func (f *TestFixture) Keg() *keg.Keg {
	return f.Deps.Keg
}

func (f *TestFixture) Repo() keg.KegRepository {
	return f.Deps.Keg.Repo
}
