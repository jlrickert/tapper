package parity_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	// Register integration adapters so IntegrateHosts() is populated and
	// the MCP orient/integrate surfaces know which hosts to expose.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
)

// TestParity_IntegrationOperations verifies that `tap integrate` and
// `tap orient` produce equivalent results on CLI and MCP. Because both
// surfaces delegate to Tap.Integrate / Tap.Orient, the bytes they
// produce must match on every input shape the public API supports.
func TestParity_IntegrationOperations(t *testing.T) {
	t.Parallel()

	cases := []ParityTestCase{
		{
			Name:     "orient/shared_payload",
			CLIArgs:  []string{"orient"},
			MCPTool:  "orient",
			MCPInput: map[string]any{},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// The shared payload is hostless and tierless; both
				// surfaces must include the same structural markers.
				require.Contains(t, cliOut, "# KEG System")
				require.Contains(t, mcpOut, "# KEG System")
				require.Contains(t, cliOut, "Rules:")
				require.Contains(t, mcpOut, "Rules:")
				require.NotContains(t, cliOut, "## Host:")
				require.NotContains(t, mcpOut, "## Host:")
				require.NotContains(t, strings.ToLower(cliOut), "tier 0")
				require.NotContains(t, strings.ToLower(mcpOut), "tier 0")
				// Line-by-line equivalence (ignoring blanks and
				// trailing whitespace) rules out silent divergence in
				// the payload the two surfaces produce.
				require.Equal(t, normalizeLines(cliOut), normalizeLines(mcpOut),
					"CLI and MCP orient payloads diverged")
			},
		},
		{
			Name:     "integrate/codex_dry_run_reports_target_paths",
			CLIArgs:  []string{"integrate", "codex", "--dry-run"},
			MCPTool:  "integrate",
			MCPInput: map[string]any{"host": "codex", "dry_run": true},
			Compare: func(t *testing.T, cliOut, mcpOut string) {
				t.Helper()
				// Both surfaces must name the standard codex targets
				// even though the underlying home path differs between
				// test runs — a substring check is enough.
				require.Contains(t, cliOut, "AGENTS.md")
				require.Contains(t, mcpOut, "AGENTS.md")
				require.Contains(t, cliOut, "prompts")
				require.Contains(t, mcpOut, "prompts")
				require.Contains(t, strings.ToLower(cliOut), "would write")
				require.Contains(t, strings.ToLower(mcpOut), "would write")
				// Both surfaces should report the same number of
				// target paths (one bullet per file under the rendered
				// codex tree).
				require.Equal(t, countPathLines(cliOut), countPathLines(mcpOut),
					"CLI and MCP integrate reported different path counts.\nCLI:\n%s\n\nMCP:\n%s", cliOut, mcpOut)
			},
		},
		{
			Name:            "integrate/unknown_host_errors_on_both",
			CLIArgs:         []string{"integrate", "nonesuch", "--dry-run"},
			MCPTool:         "integrate",
			MCPInput:        map[string]any{"host": "nonesuch", "dry_run": true},
			WantErr:         true,
			WantErrContains: "unknown host",
		},
	}

	runParityTests(t, cases)
}

// countPathLines returns the number of lines in text that begin with
// two leading spaces — matching the bullet format both surfaces emit
// for integrate target paths.
func countPathLines(text string) int {
	n := 0
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "  ") && strings.TrimSpace(line) != "" {
			n++
		}
	}
	return n
}
