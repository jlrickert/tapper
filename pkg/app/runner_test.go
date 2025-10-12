package app_test

// Unit tests for Runner that do not use Cobra. Verifies IO, logging, and
// repository side-effects.
// import (
// 	"bytes"
// 	"context"
// 	"strings"
// 	"testing"
// 	"time"
//
// 	std "github.com/jlrickert/go-std/pkg"
// 	"github.com/jlrickert/tapper/pkg/app"
// 	"github.com/jlrickert/tapper/pkg/keg"
// 	"github.com/stretchr/testify/require"
// )

// func TestRunner_Run_unit(t *testing.T) {
// 	t.Parallel()
//
// 	jail := t.TempDir()
// 	lg, handler := std.NewTestLogger(t, std.ParseLevel("debug"))
// 	env := std.NewTestEnv(jail, jail, "testuser")
// 	clock := std.NewTestClock(time.Now())
//
// 	ctx := context.Background()
// 	ctx = std.WithLogger(ctx, lg)
// 	ctx = std.WithEnv(ctx, env)
// 	ctx = std.WithClock(ctx, clock)
//
// 	repo := keg.NewMemoryRepo()
// 	runner := app.Runner{Root: jail}
//
// 	in := bytes.NewBufferString("hello\n")
// 	var out bytes.Buffer
// 	var errb bytes.Buffer
//
// 	streams := &app.Streams{In: in, Out: &out, Err: &errb}
//
// 	require.NoError(t, runner.Run(ctx, streams, []string{"echo"}))
//
// 	require.Contains(t, out.String(), "echo: hello")
//
// 	// verify that a log entry was recorded
// 	found := std.FindEntries(handler, func(e std.LoggedEntry) bool {
// 		return strings.Contains(e.Msg, "runner.run")
// 	})
// 	require.NotEmpty(t, found, "expected log entry for runner.run")
//
// 	// verify repo has nodes
// 	nodes, err := repo.ListNodes(context.Background())
// 	require.NoError(t, err)
// 	require.NotEmpty(t, nodes, "expected at least one node in repo")
// }
