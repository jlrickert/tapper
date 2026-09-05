// Package parity_test provides table-driven tests that verify CLI commands and
// MCP tools produce equivalent results when calling the same underlying Tap API
// methods against the same sandboxed keg state.
//
// The tests exercise both surfaces through their real execution paths:
//   - CLI: pkg/cli.Run → Cobra → pkg/tapper.Tap
//   - MCP: in-memory MCP client → pkg/mcp.Server → pkg/tapper.Tap
//
// Both surfaces share the same sandbox, runtime, and Tap instance per test case.
package parity_test

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/testkegrepo"
	"github.com/jlrickert/tapper/pkg/cli"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

//go:embed all:data/**
var testdata embed.FS

// --- test harness ---

// parityEnv holds a sandboxed environment with both a CLI runner and an MCP
// session ready to use against the same keg state.
type parityEnv struct {
	t       *testing.T
	sb      *sandbox.Sandbox
	tap     *tapper.Tap
	session *sdkmcp.ClientSession
	ctx     context.Context
}

// newParityEnv creates a shared test environment that both CLI and MCP can
// operate on. The "testuser" fixture provides a minimal personal keg with two
// nodes (0: Personal Overview, 1: Hello World) that link to each other.
func newParityEnv(t *testing.T) *parityEnv {
	t.Helper()

	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, sandbox.WithFixture("testuser", "~"))

	rt := sb.Runtime()
	ctx := sb.Context()

	sharedKeg := newParityKeg(t, ctx, rt)
	tap, err := tapper.NewTap(tapper.TapOptions{
		Runtime: rt,
	})
	require.NoError(t, err)
	tap.KegResolver = func(_ context.Context, opts tapper.KegTargetOptions, _ tapper.FlightRole) (keg.Keg, error) {
		ref := strings.TrimSpace(opts.Keg)
		if ref == "" {
			ref = "personal"
		}
		if strings.HasPrefix(ref, "@") {
			_, ref, _ = strings.Cut(strings.TrimPrefix(ref, "@"), "/")
		}
		if ref != "personal" {
			return nil, fmt.Errorf("keg %q: %w", ref, keg.ErrNotExist)
		}
		return sharedKeg, nil
	}

	// Both surfaces use the same remote-targeted LocalKeg orchestration over a
	// concurrency-safe internal repository. No production resolver can select
	// this repository; it exists only in tests.
	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, mcp.ServerOptions{
		SharedFilesystem:    true,
		OrientationProvider: parityOrientationProvider{},
	})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("MCP server error: %v", err)
		}
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "parity-test",
		Version: "0.1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		session.Close()
	})

	return &parityEnv{
		t:       t,
		sb:      sb,
		tap:     tap,
		session: session,
		ctx:     ctx,
	}
}

type parityOrientationProvider struct{}

func (parityOrientationProvider) Load(context.Context) (*mcp.Orientation, error) {
	return newParityOrientation()
}

func (parityOrientationProvider) Render(context.Context, *tapper.Flight) (*mcp.Orientation, error) {
	return newParityOrientation()
}

func (parityOrientationProvider) Resolve(context.Context, string, string) (*mcp.Orientation, error) {
	return newParityOrientation()
}

func newParityOrientation() (*mcp.Orientation, error) {
	kegs := []tapper.OrientationKeg{{
		Ref: "@local/personal", Namespace: "local", Alias: "personal",
		Title: "Personal KEG", Role: "admin", Source: "test", FlightCap: "admin",
	}}
	orientation := &mcp.Orientation{
		Identity: "parity-test", Kegs: kegs, AggregateKegs: kegs, FullAccess: true,
		ReconnectInstructions: "start a new parity test session",
	}
	if err := mcp.FinalizeOrientation(orientation); err != nil {
		return nil, err
	}
	payload, err := tapper.BuildOrientationPayload(nil, "Parity test authority.", "", kegs, nil,
		&tapper.OrientationAuthority{FullAccess: true, Revision: orientation.Revision})
	if err != nil {
		return nil, err
	}
	orientation.Payload = payload
	return orientation, nil
}

func newParityKeg(t *testing.T, ctx context.Context, rt *toolkit.Runtime) keg.Keg {
	t.Helper()
	repo := testkegrepo.NewMemoryRepository(rt)
	base := "/home/testuser/kegs/@local/personal"
	settings, err := rt.ReadFile(filepath.Join(base, "keg"))
	require.NoError(t, err)
	require.NoError(t, repo.WriteSettingsDocument(ctx, settings))
	for _, rawID := range []string{"0", "1"} {
		id, err := keg.ParseNode(rawID)
		require.NoError(t, err)
		require.NotNil(t, id)
		nodeDir := filepath.Join(base, rawID)
		content, err := rt.ReadFile(filepath.Join(nodeDir, keg.MarkdownContentFilename))
		require.NoError(t, err)
		require.NoError(t, repo.WriteContent(ctx, *id, content))
		meta, err := rt.ReadFile(filepath.Join(nodeDir, "meta.yaml"))
		require.NoError(t, err)
		require.NoError(t, repo.WriteMeta(ctx, *id, meta))
		rawStats, err := rt.ReadFile(filepath.Join(nodeDir, "stats.json"))
		require.NoError(t, err)
		stats, err := keg.ParseStats(ctx, rawStats)
		require.NoError(t, err)
		require.NoError(t, repo.WriteStats(ctx, *id, stats))
	}
	dexDir := filepath.Join(base, "dex")
	entries, err := rt.ReadDir(dexDir)
	require.NoError(t, err)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		raw, err := rt.ReadFile(filepath.Join(dexDir, entry.Name()))
		require.NoError(t, err)
		require.NoError(t, repo.WriteIndex(ctx, entry.Name(), raw))
	}
	local := keg.NewLocalKeg(repo, rt)
	target := keg.NewApi("fixture", "local", "personal", keg.WithHubURL("https://fixture.invalid"))
	local.SetTarget(&target)
	return local
}

// runCLI executes a CLI command and returns stdout as a string.
// runCLICreate creates a node through the CLI by piping content on stdin,
// which is the only way to give a new node a title now that --title/--lead/
// --tags/--attrs are gone. meta, when non-empty, rides as YAML frontmatter.
func (e *parityEnv) runCLICreate(title, meta string, args ...string) (string, error) {
	e.t.Helper()
	content := "# " + title + "\n"
	if meta != "" {
		content = "---\n" + meta + "---\n" + content
	}
	return e.runCLIWithStdin(content, append([]string{"create"}, args...)...)
}

func (e *parityEnv) runCLI(args ...string) (string, error) {
	return e.runCLIWithStdin("", args...)
}

func (e *parityEnv) runCLIWithStdin(stdin string, args ...string) (string, error) {
	e.t.Helper()
	proc := sandbox.NewProcess(func(ctx context.Context, rt *toolkit.Runtime) (int, error) {
		ctx = cli.WithTestDepsHook(ctx, func(deps *cli.Deps) {
			deps.TapFactory = func(tapper.TapOptions) (*tapper.Tap, error) { return e.tap, nil }
		})
		return cli.Run(ctx, rt, args)
	}, false) // isTTY=false to get stdout output, not editor
	if stdin != "" {
		proc.SetStdin(strings.NewReader(stdin))
	}
	result := proc.Run(e.ctx, e.sb.Runtime())
	if result.Err != nil {
		return strings.TrimSpace(string(result.Stdout)), result.Err
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// runMCP calls an MCP tool and returns the text content as a string.
func (e *parityEnv) runMCP(toolName string, args map[string]any) (string, error) {
	e.t.Helper()
	res, err := e.session.CallTool(e.ctx, &sdkmcp.CallToolParams{
		Name:      toolName,
		Arguments: args,
	})
	if err != nil {
		return "", err
	}
	text := extractText(e.t, res)
	if res.IsError {
		return text, &mcpError{msg: text}
	}
	return strings.TrimSpace(text), nil
}

func (e *parityEnv) nodeHash(nodeID string) string {
	e.t.Helper()
	hash, err := e.tap.NodeHash(e.ctx, tapper.KegTargetOptions{}, nodeID)
	require.NoError(e.t, err)
	require.NotEmpty(e.t, hash)
	return hash
}

type mcpError struct {
	msg string
}

func (e *mcpError) Error() string { return e.msg }

func extractText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// containsLine checks whether needle appears as an exact line in text.
func containsLine(text, needle string) bool {
	for _, line := range strings.Split(text, "\n") {
		if strings.TrimSpace(line) == needle {
			return true
		}
	}
	return false
}

// --- comparison helpers ---

// normalizeLines splits text into trimmed non-empty lines for structural
// comparison, ignoring differences in trailing whitespace and blank lines.
func normalizeLines(s string) []string {
	raw := strings.Split(s, "\n")
	var out []string
	for _, line := range raw {
		trimmed := strings.TrimRight(line, " \t")
		if trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

// --- ParityTestCase ---

// ParityTestCase defines a single parity test: given the same keg state, CLI
// and MCP should produce equivalent results.
type ParityTestCase struct {
	Name string

	// CLI invocation.
	CLIArgs []string

	// MCP invocation.
	MCPTool  string
	MCPInput map[string]any

	// Compare defines how to compare CLI and MCP output. If nil, the default
	// comparison is used (exact line-for-line match after normalization).
	Compare func(t *testing.T, cliOut, mcpOut string)

	// SkipCLI skips the CLI side (for MCP-only tools).
	SkipCLI bool

	// SkipMCP skips the MCP side (for CLI-only commands).
	SkipMCP bool

	// WantErr indicates both surfaces should error.
	WantErr bool

	// WantErrContains checks that both error messages contain this substring.
	WantErrContains string
}

func defaultCompare(t *testing.T, cliOut, mcpOut string) {
	t.Helper()
	cliLines := normalizeLines(cliOut)
	mcpLines := normalizeLines(mcpOut)
	require.Equal(t, cliLines, mcpLines,
		"CLI and MCP output diverged.\nCLI:\n%s\n\nMCP:\n%s", cliOut, mcpOut)
}

func runParityTests(t *testing.T, cases []ParityTestCase) {
	t.Helper()
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			t.Parallel()
			env := newParityEnv(t)

			var cliOut string
			var cliErr error
			if !tc.SkipCLI {
				cliOut, cliErr = env.runCLI(tc.CLIArgs...)
			}

			var mcpOut string
			var mcpErr error
			if !tc.SkipMCP {
				mcpOut, mcpErr = env.runMCP(tc.MCPTool, tc.MCPInput)
			}

			if tc.WantErr {
				if !tc.SkipCLI {
					require.Error(t, cliErr, "CLI should have returned an error")
				}
				if !tc.SkipMCP {
					require.Error(t, mcpErr, "MCP should have returned an error")
				}
				if tc.WantErrContains != "" {
					if !tc.SkipCLI {
						require.Contains(t, cliErr.Error(), tc.WantErrContains,
							"CLI error message mismatch")
					}
					if !tc.SkipMCP {
						require.Contains(t, mcpErr.Error(), tc.WantErrContains,
							"MCP error message mismatch")
					}
				}
				return
			}

			if !tc.SkipCLI {
				require.NoError(t, cliErr, "CLI failed: %s", cliOut)
			}
			if !tc.SkipMCP {
				require.NoError(t, mcpErr, "MCP failed: %s", mcpOut)
			}

			// Only compare when both sides ran.
			if !tc.SkipCLI && !tc.SkipMCP {
				cmp := tc.Compare
				if cmp == nil {
					cmp = defaultCompare
				}
				cmp(t, cliOut, mcpOut)
			}
		})
	}
}
