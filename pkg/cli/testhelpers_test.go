package cli_test

import (
	"bytes"
	"embed"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jlrickert/go-std/testutils"
	"github.com/jlrickert/tapper/pkg/app"
	"github.com/jlrickert/tapper/pkg/cli"
)

// testdata is an optional embedded data FS for fixtures. Previously an embed
// pattern attempted to include empty directories which caused an embed error.
var testdata embed.FS

func NewFixture(t *testing.T, opts ...testutils.FixtureOption) *testutils.Fixture {
	return testutils.NewFixture(t,
		&testutils.FixtureOptions{
			Data: testdata,
			Home: "/home/testuser",
			User: "testuser",
		}, opts...)
}

// Harness provides a thin wrapper around the tap CLI root command with test
// buffers for stdin/stdout/stderr.
type Harness struct {
	t *testing.T

	Cmd     *cobra.Command
	InBuf   *bytes.Buffer
	OutBuf  *bytes.Buffer
	ErrBuf  *bytes.Buffer
	Streams app.Streams
}

// NewHarness constructs a Harness using the provided fixture. The Cobra command
// inherits the fixture context so tests get the fixture logger, env, and clock.
func NewHarness(t *testing.T, fx *testutils.Fixture) *Harness {
	t.Helper()

	inBuf := &bytes.Buffer{}
	outBuf := &bytes.Buffer{}
	errBuf := &bytes.Buffer{}

	streams := app.Streams{
		In:  inBuf,
		Out: outBuf,
		Err: errBuf,
	}

	cmd := cli.NewRootCmd()
	cmd.SetContext(fx.Context())
	cmd.SetIn(streams.In)
	cmd.SetOut(streams.Out)
	cmd.SetErr(streams.Err)

	return &Harness{
		t:       t,
		Cmd:     cmd,
		InBuf:   inBuf,
		OutBuf:  outBuf,
		ErrBuf:  errBuf,
		Streams: streams,
	}
}

// Run executes the CLI with the provided arguments. It returns any execution
// error so tests can assert on exit paths.
func (h *Harness) Run(args ...string) error {
	h.t.Helper()
	h.Cmd.SetArgs(args)
	return h.Cmd.ExecuteContext(h.Cmd.Context())
}

// ResetIO clears the captured stdin/stdout/stderr buffers.
func (h *Harness) ResetIO() {
	h.t.Helper()
	h.InBuf.Reset()
	h.OutBuf.Reset()
	h.ErrBuf.Reset()
}

// WriteInput replaces the stdin buffer contents with the provided string.
func (h *Harness) WriteInput(s string) {
	h.t.Helper()
	h.InBuf.Reset()
	_, _ = h.InBuf.WriteString(s)
}

// Completion renders a shell completion script for the configured command and
// returns it as a string. Zsh is used for now. The pos and args parameters are
// accepted for future expansion but are currently unused.
func (h *Harness) Completion(pos int, words ...string) ([]string, cobra.ShellCompDirective, error) {
	h.t.Helper()
	h.ResetIO()

	// Cobra expects argv with command name first.
	line := append([]string{h.Cmd.Name()}, words...)

	// Insert an empty word at the cursor position to mimic the shell.
	if pos < 0 || pos > len(line) {
		return nil, cobra.ShellCompDirectiveError, fmt.Errorf("cursor %d out of range", pos)
	}
	line = append(line[:pos], append([]string{""}, line[pos:]...)...)

	args := append([]string{"__complete"}, line...)
	if err := h.Run(args...); err != nil {
		return nil, cobra.ShellCompDirectiveError, err
	}

	out := strings.TrimSuffix(h.OutBuf.String(), "\n")
	lines := strings.Split(out, "\n")
	if len(lines) == 0 {
		return nil, cobra.ShellCompDirectiveError, fmt.Errorf("no completion output")
	}

	dirLine := lines[len(lines)-1]
	if !strings.HasPrefix(dirLine, ":") {
		return nil, cobra.ShellCompDirectiveError, fmt.Errorf("missing directive line: %q", dirLine)
	}
	dirVal, err := strconv.Atoi(dirLine[1:])
	if err != nil {
		return nil, cobra.ShellCompDirectiveError, fmt.Errorf("bad directive %q: %w", dirLine, err)
	}

	var comps []string
	for _, l := range lines[:len(lines)-1] {
		// Candidates may include a tab-separated description; keep only the value.
		comps = append(comps, strings.SplitN(l, "\t", 2)[0])
	}
	return comps, cobra.ShellCompDirective(dirVal), nil
}
