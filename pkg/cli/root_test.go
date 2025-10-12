// Package cli_test contains command-level tests for the CLI wiring.
// Tests use a Fixture that provides a hermetic test environment:
// logger, env, clock, injectable stdin/stdout/stderr, and an in-memory repo.
package cli_test

// import (
// 	"bytes"
// 	"context"
// 	"os"
// 	"path/filepath"
// 	"testing"
//
// 	std "github.com/jlrickert/go-std/pkg"
// 	"github.com/jlrickert/tapper/pkg/cli"
// 	"github.com/spf13/cobra"
// 	"github.com/stretchr/testify/require"
// )
//
// // helper to run a command and capture errors
// func runCmd(t *testing.T, cmd *cobra.Command, ctx context.Context, in *bytes.Buffer, out *bytes.Buffer, errb *bytes.Buffer, args []string) error {
// 	t.Helper()
// 	cmd.SetContext(ctx)
// 	cmd.SetIn(in)
// 	cmd.SetOut(out)
// 	cmd.SetErr(errb)
// 	cmd.SetArgs(args)
// 	return cmd.Execute()
// }
//
// func TestDoCommand_command_level(t *testing.T) {
// 	require := require.New(t)
//
// 	f := NewFixture(t)
//
// 	// Build root command and inject the fixture repo into the command context.
// 	root := cli.NewRootCmd()
//
// 	// Set a context that carries our fixture logger/env/clock and also include
// 	// the repo value so the command uses the fixture's in-memory repo.
// 	ctx := context.WithValue(f.Ctx, struct{}{}, f.Repo)
//
// 	err := runCmd(t, root, ctx, f.InBuf, &f.OutBuf, &f.ErrBuf, []string{"do"})
// 	require.NoError(err)
//
// 	out := f.Out()
// 	require.NotEmpty(out, "expected stdout output")
//
// 	// ensure logs were captured by the fixture handler
// 	entries := FindEntriesOnHandler(f.Handler, "runner finished")
// 	require.NotEmpty(entries, "expected runner finished log entry")
// }
//
// func TestDoCommand_logfile_flag(t *testing.T) {
// 	require := require.New(t)
//
// 	f := NewFixture(t)
//
// 	root := cli.NewRootCmd()
// 	logPath := filepath.Join(f.Jail, "test.log")
//
// 	// Do not set the fixture context so PersistentPreRunE constructs a
// 	// file-based logger. Use a background context to avoid replacing the
// 	// test logger.
// 	ctx := context.Background()
//
// 	err := runCmd(t, root, ctx, f.InBuf, &f.OutBuf, &f.ErrBuf, []string{"--log-file", logPath, "do"})
// 	require.NoError(err)
//
// 	// Read the log file and assert it has content.
// 	b, err := os.ReadFile(logPath)
// 	require.NoError(err)
// 	require.NotEmpty(b, "expected log file to have content")
// }
//
// // --- small helpers used by these tests
//
// // FindEntriesOnHandler is a tiny helper to inspect the test handler entries.
// func FindEntriesOnHandler(h *std.TestHandler, wantMsg string) []std.LoggedEntry {
// 	return std.FindEntries(h, func(e std.LoggedEntry) bool {
// 		return e.Msg == wantMsg
// 	})
// }
//
// // cliKey mirrors the internal repoKey zero-size type used in the cli package.
// // We declare it here to be able to set a context value in tests that the cli
// // code will read. The actual cli implementation uses an unexported key type;
// // because tests live in a different package this is a pragmatic stand-in that
// // matches the intent of command-context repo injection in examples.
// type cliKey struct{}
