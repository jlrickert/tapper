package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type catTestCase struct {
	name             string
	args             []string
	setupFixture     *string
	cwd              *string
	expectedInStdout []string
	expectedErr      string
	description      string
}

func TestCatCommand_TableDrivenErrorHandling(t *testing.T) {
	tests := []catTestCase{
		{
			name:         "cat_invalid_node_id",
			args:         []string{"cat", "invalid"},
			setupFixture: strPtr("joe"),
			expectedErr:  "invalid node ID",
			description:  "Error when node ID cannot be parsed",
		},
		{
			name:        "cat_missing_node_id",
			args:        []string{"cat"},
			expectedErr: "at least 1 arg",
			description: "Error when node ID is not provided",
		},
		{
			name:         "cat_nonexistent_alias",
			args:         []string{"cat", "0", "--keg", "nonexistent"},
			setupFixture: strPtr("joe"),
			expectedErr:  "keg not initialized",
			description:  "Error when the remote keg does not exist",
		},
		{
			name:         "cat_nonexistent_node",
			args:         []string{"cat", "12341234"},
			setupFixture: strPtr("joe"),
			expectedErr:  "node 12341234 not found",
			description:  "Error when node does not exist",
		},
		{
			name:         "cat_conflicting_output_flags",
			args:         []string{"cat", "0", "--meta-only", "--stats-only"},
			setupFixture: strPtr("joe"),
			expectedErr:  "only one output mode may be selected",
			description:  "Error when multiple output modes are selected",
		},
		{
			name:         "cat_conflicting_edit_and_output_flag",
			args:         []string{"cat", "0", "--edit", "--stats-only"},
			setupFixture: strPtr("joe"),
			expectedErr:  "only one output mode may be selected",
			description:  "Error when edit mode conflicts with output flags",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			if tt.cwd != nil {
				sb.Setwd(*tt.cwd)
			}

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			if tt.expectedErr != "" {
				require.Error(innerT, res.Err, "expected error - %s", tt.description)
				stderr := string(res.Stderr)
				require.Contains(innerT, stderr, tt.expectedErr,
					"error message should contain %q, got stderr: %s and err: %v", tt.expectedErr, stderr, res.Err)
			} else {
				require.NoError(innerT, res.Err, "cat command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}

				// Verify frontmatter structure: starts with ---, has ---, then content
				lines := strings.Split(stdout, "\n")
				require.Greater(innerT, len(lines), 2, "output should have multiple lines")
				require.Equal(innerT, "---", lines[0], "output should start with frontmatter delimiter")

				// Find the closing delimiter
				closingFound := false
				for i := 1; i < len(lines); i++ {
					if lines[i] == "---" {
						closingFound = true
						break
					}
				}
				require.True(innerT, closingFound, "output should have closing frontmatter delimiter")
			}
		})
	}
}

func TestCatCommand_WithJoeFixture(t *testing.T) {
	tests := []catTestCase{
		{
			name: "cat_personal_keg_from_default_location",
			args: []string{
				"cat",
				"0",
			},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"---",
				"# Sorry, planned but not yet available",
			},
			description: "Display node 0 from default personal keg",
		},
		{
			name: "cat_default_keg_overrides_path_resolution",
			args: []string{
				"cat",
				"0",
			},
			setupFixture: strPtr("joe"),
			cwd:          strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{
				"---",
				"# Sorry, planned but not yet available",
			},
			description: "Display node 0 from default keg even when kegMap path matches",
		},
		{
			name: "cat_explicit_alias_overrides_path_resolution",
			args: []string{
				"cat",
				"0",
				"--keg", "example",
			},
			setupFixture: strPtr("joe"),
			cwd:          strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{
				"---",
				"- example",
				"---",
			},
			description: "Explicit alias overrides path-based keg resolution",
		},
		{
			name: "cat_personal_keg_explicit_alias",
			args: []string{
				"cat",
				"0",
				"--keg", "personal",
			},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"---",
				"# Sorry, planned but not yet available",
			},
			description: "Display node 0 from personal keg with explicit alias",
		},
		{
			name: "cat_content_only",
			args: []string{
				"cat",
				"0",
				"--content-only",
				"--keg", "personal",
			},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"# Sorry, planned but not yet available",
			},
			description: "Display only content when --content-only is provided",
		},
		{
			name: "cat_meta_only",
			args: []string{
				"cat",
				"0",
				"--meta-only",
				"--keg", "personal",
			},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"tags:",
				"- planned",
			},
			description: "Display only metadata when --meta-only is provided",
		},
		{
			name: "cat_stats_only",
			args: []string{
				"cat",
				"0",
				"--stats-only",
				"--keg", "personal",
			},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"hash:",
				"updated:",
			},
			description: "Display only stats when --stats-only is provided",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			if tt.cwd != nil {
				sb.Setwd(*tt.cwd)
			}

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			if tt.expectedErr != "" {
				require.Error(innerT, res.Err, "expected error - %s", tt.description)
				stderr := string(res.Stderr)
				require.Contains(innerT, stderr, tt.expectedErr,
					"error message should contain %q, got stderr: %s and err: %v", tt.expectedErr, stderr, res.Err)
			} else {
				require.NoError(innerT, res.Err, "cat command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}

				if !strings.Contains(strings.Join(tt.args, " "), "--content-only") &&
					!strings.Contains(strings.Join(tt.args, " "), "--meta-only") &&
					!strings.Contains(strings.Join(tt.args, " "), "--stats-only") {
					// Verify frontmatter structure
					require.Contains(innerT, stdout, "---", "output should contain frontmatter delimiter")
				}
			}
		})
	}
}

func TestCatCommand_IntegrationWithInit(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	res := NewProcess(t, false, "init").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), `unknown command "init"`)
}

func TestCatCommand_UserKeg(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	res := NewProcess(t, false, "cat", "0", "--keg", "public").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "keg not initialized")
}

func TestCatCommand_BumpsAccessedAndAccessCount(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	statsPath := "~/kegs/@local/personal/0/stats.json"
	oldAccessed := "2001-01-01T00:00:00Z"
	sb.MustWriteFile(statsPath, []byte(`{"accessed":"`+oldAccessed+`","access_count":7}`), 0o644)

	h := NewProcess(t, false, "cat", "0", "--keg", "personal", "--content-only")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "cat should succeed and bump access metadata")

	afterOne := fixtureStats(t, sb.Runtime(), "personal", "0")
	require.Equal(t, 8, afterOne.AccessCount(), "access count should increment on read")
	require.False(t, afterOne.Accessed().IsZero(), "accessed should be set")
	require.NotEqual(t, oldAccessed, afterOne.Accessed().Format(time.RFC3339), "accessed should be bumped")

	res = h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "second cat should also succeed")

	afterTwo := fixtureStats(t, sb.Runtime(), "personal", "0")
	require.Equal(t, 9, afterTwo.AccessCount(), "access count should increment on every read")
}

func TestCatCommand_DefaultFrontmatterDoesNotInjectStats(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	statsPath := "~/kegs/@local/personal/0/stats.json"
	sb.MustWriteFile(statsPath, []byte(`{"accessed":"2025-01-01T00:00:00Z","access_count":123}`), 0o644)

	res := NewProcess(t, false, "cat", "0", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.NotContains(t, out, "access_count:", "default frontmatter should come from meta only")
	require.NotContains(t, out, "accessed:", "default frontmatter should come from meta only")
}

func TestCatCommand_EditFlagEditsNode(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "cat-edit-node.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
---
tags:
  - edited-via-cat
summary: changed by cat edit
---
# Cat Edited

Body updated from cat --edit.
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "cat", "0", "--keg", "personal", "--edit").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)))

	meta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	content := fixtureContent(t, sb.Runtime(), "personal", "0")
	require.Contains(t, meta, "- edited-via-cat")
	require.Contains(t, meta, "summary: changed by cat edit")
	require.Contains(t, content, "# Cat Edited")
	require.Contains(t, content, "Body updated from cat --edit.")
}

// TestCatCommand_MultiNode_YAMLStream verifies that requesting multiple nodes
// in default (frontmatter) mode produces a YAML multi-document stream where
// each document has an injected "id:" field and no "=== N ===" decoration.
func TestCatCommand_MultiNode_YAMLStream(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Create a second node so we have two to cat.
	createRes := NewProcess(t, false, "create", "--keg", "personal", "--title", "Second node").Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create should succeed")

	res := NewProcess(t, false, "cat", "0", "1", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)

	// Must contain id fields for both nodes.
	require.Contains(t, out, `id: "0"`, "first document should have id: 0")
	require.Contains(t, out, `id: "1"`, "second document should have id: 1")

	// Must NOT contain the old === decoration.
	require.NotContains(t, out, "=== 0 ===", "should not use old header format")
	require.NotContains(t, out, "=== 1 ===", "should not use old header format")

	// The output must be a valid YAML stream: starts with "---".
	require.True(t, strings.HasPrefix(out, "---\n"), "output should start with YAML document-start marker")
}

// TestCatCommand_MultiNode_ContentOnly verifies that --content-only with
// multiple nodes injects the node ID as a tiny YAML frontmatter before each
// content block.
func TestCatCommand_MultiNode_ContentOnly(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Create a second node so we have two to cat.
	createRes := NewProcess(t, false, "create", "--keg", "personal", "--title", "Second node").Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create should succeed")

	res := NewProcess(t, false, "cat", "0", "1", "--keg", "personal", "--content-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)

	// Each document must carry its ID.
	require.Contains(t, out, `id: "0"`, "content-only multi-node should inject id for first node")
	require.Contains(t, out, `id: "1"`, "content-only multi-node should inject id for second node")

	// The output starts with a YAML document-start marker.
	require.True(t, strings.HasPrefix(out, "---\n"), "output should start with ---")
}

// TestCatCommand_MultiNode_MetaOnly verifies that --meta-only with multiple
// nodes injects the node ID into each YAML document.
func TestCatCommand_MultiNode_MetaOnly(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	createRes := NewProcess(t, false, "create", "--keg", "personal", "--title", "Second node").Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create should succeed")

	res := NewProcess(t, false, "cat", "0", "1", "--keg", "personal", "--meta-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)

	require.Contains(t, out, `id: "0"`, "meta-only multi-node should inject id for first node")
	require.Contains(t, out, `id: "1"`, "meta-only multi-node should inject id for second node")
	require.True(t, strings.HasPrefix(out, "---\n"), "output should start with ---")
}

// TestCatCommand_MultiNode_StatsOnly verifies that --stats-only with multiple
// nodes injects the node ID into each YAML document.
func TestCatCommand_MultiNode_StatsOnly(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	createRes := NewProcess(t, false, "create", "--keg", "personal", "--title", "Second node").Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create should succeed")

	res := NewProcess(t, false, "cat", "0", "1", "--keg", "personal", "--stats-only").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)

	require.Contains(t, out, `id: "0"`, "stats-only multi-node should inject id for first node")
	require.Contains(t, out, `id: "1"`, "stats-only multi-node should inject id for second node")
	require.True(t, strings.HasPrefix(out, "---\n"), "output should start with ---")
}

// TestCatCommand_SingleNode_NoIDField verifies that a single-node cat does NOT
// inject an "id:" field (backward-compatibility guarantee).
func TestCatCommand_SingleNode_NoIDField(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	res := NewProcess(t, false, "cat", "0", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.NotContains(t, out, `id: "0"`, "single-node output should not have injected id field")
}

// TestCatCommand_TTY_SingleNode_DelegatesToEdit verifies that when stdout is a
// TTY and a single node is requested with no output-mode flags, cat delegates
// to the edit flow (editor is opened, no content printed to stdout).
func TestCatCommand_TTY_SingleNode_DelegatesToEdit(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	// Editor script that writes known content so we can verify edit ran.
	scriptPath := filepath.Join(jail, "cat-tty-edit.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
---
tags:
  - tty-edited
---
# TTY Cat Edit
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	// isTTY=true: should delegate to editor, no stdout output.
	res := NewProcess(t, true, "cat", "0", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)),
		"interactive TTY cat should not print to stdout")

	// Verify editor was invoked by checking the node was modified.
	content := fixtureContent(t, sb.Runtime(), "personal", "0")
	require.Contains(t, content, "# TTY Cat Edit",
		"editor should have modified the node content")
	meta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	require.Contains(t, meta, "- tty-edited",
		"editor should have modified the node metadata")
}

// TestCatCommand_NonTTY_SingleNode_PrintsToStdout verifies that when stdout is
// not a TTY, cat prints content to stdout as usual.
func TestCatCommand_NonTTY_SingleNode_PrintsToStdout(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// isTTY=false: should print to stdout, not open editor.
	res := NewProcess(t, false, "cat", "0", "--keg", "personal").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, "---", "non-TTY cat should print frontmatter")
	require.Contains(t, out, "# Sorry, planned but not yet available",
		"non-TTY cat should print node content to stdout")
}

// TestCatCommand_TTY_OutputModeFlags_OverrideTTY verifies that output-mode
// flags (--content-only, --stats-only, --meta-only) force stdout output even
// when the terminal is interactive.
func TestCatCommand_TTY_OutputModeFlags_OverrideTTY(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		flag     string
		expected string
	}{
		{
			name:     "content-only overrides TTY",
			flag:     "--content-only",
			expected: "# Sorry, planned but not yet available",
		},
		{
			name:     "meta-only overrides TTY",
			flag:     "--meta-only",
			expected: "tags:",
		},
		{
			name:     "stats-only overrides TTY",
			flag:     "--stats-only",
			expected: "hash:",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			sb := NewSandbox(innerT, testutils.WithFixture("joe", "~"))

			// isTTY=true but output-mode flag set: should print to stdout.
			res := NewProcess(innerT, true, "cat", "0", "--keg", "personal", tt.flag).
				Run(sb.Context(), sb.Runtime())
			require.NoError(innerT, res.Err)

			out := string(res.Stdout)
			require.Contains(innerT, out, tt.expected,
				"output-mode flag should force stdout output even with TTY")
		})
	}
}

// TestCatCommand_TTY_MultipleNodes_PrintsToStdout verifies that requesting
// multiple nodes always prints to stdout even on an interactive terminal.
func TestCatCommand_TTY_MultipleNodes_PrintsToStdout(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	// Create a second node so we have two to cat.
	createRes := NewProcess(t, false, "create", "--keg", "personal", "--title", "Second node").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, createRes.Err, "create should succeed")

	// isTTY=true with multiple nodes: should still print to stdout.
	res := NewProcess(t, true, "cat", "0", "1", "--keg", "personal").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := string(res.Stdout)
	require.Contains(t, out, `id: "0"`, "multi-node output should have id for first node")
	require.Contains(t, out, `id: "1"`, "multi-node output should have id for second node")
}

// TestCatCommand_QueryFlag verifies that --query selects nodes by a boolean
// expression.
func TestCatCommand_QueryFlag(t *testing.T) {
	t.Parallel()

	t.Run("query_flag_selects_nodes", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT, testutils.WithFixture("joe", "~"))

		res := NewProcess(innerT, false, "cat", "--query", "planned", "--keg", "personal", "--content-only").
			Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, res.Err, "--query should select nodes by expression")
		require.Contains(innerT, string(res.Stdout), "Sorry, planned but not yet available",
			"--query should return content for matching nodes")
	})
}

// TestCatCommand_TTY_BumpsAccessCount verifies that interactive TTY cat
// increments access_count via the edit flow's Touch call.
func TestCatCommand_TTY_BumpsAccessCount(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	// Editor that immediately exits without changes.
	scriptPath := filepath.Join(jail, "cat-tty-noop.sh")
	script := "#!/bin/sh\ntrue\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	statsPath := "~/kegs/@local/personal/0/stats.json"
	sb.MustWriteFile(statsPath, []byte(`{"accessed":"2001-01-01T00:00:00Z","access_count":5}`), 0o644)

	res := NewProcess(t, true, "cat", "0", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	stats := fixtureStats(t, sb.Runtime(), "personal", "0")
	require.Equal(t, 6, stats.AccessCount(),
		"interactive TTY cat should bump access_count")
}
