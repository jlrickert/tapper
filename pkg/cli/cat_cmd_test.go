package cli_test

import (
	"encoding/json"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type catStatsJSON struct {
	Accessed    string `json:"accessed"`
	AccessCount int    `json:"access_count"`
}

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
			expectedErr: "accepts 1 arg",
			description: "Error when node ID is not provided",
		},
		{
			name:         "cat_nonexistent_alias",
			args:         []string{"cat", "0", "--keg", "nonexistent"},
			setupFixture: strPtr("joe"),
			expectedErr:  "keg alias not found",
			description:  "Error when keg alias does not exist",
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
				"title: Sorry, planned but not yet available",
				"---",
			},
			description: "Display node 0 from default personal keg",
		},
		{
			name: "cat_work_keg_from_work_directory",
			args: []string{
				"cat",
				"0",
			},
			setupFixture: strPtr("joe"),
			cwd:          strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{
				"---",
				"title:",
				"---",
			},
			description: "Display node 0 from work keg when in work directory (resolved via kegMap)",
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
				"title:",
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
				"title:",
				"---",
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
				"title: Sorry, planned but not yet available",
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
	t.Run("cat_node_after_init", func(innerT *testing.T) {
		innerT.Parallel()
		opts := []testutils.Option{
			testutils.WithFixture("testuser", "~"),
		}
		sb := NewSandbox(innerT, opts...)

		// First, initialize a user keg
		initCmd := NewProcess(innerT, false,
			"repo", "init",
			"newstudy",
			"--user",
			"--keg", "newstudy",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "init should succeed")
		require.Contains(innerT, string(initRes.Stdout), "keg newstudy created")

		// Now cat the node 0
		catCmd := NewProcess(innerT, false, "cat", "0", "--keg", "newstudy")
		catRes := catCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, catRes.Err, "cat should succeed")

		stdout := string(catRes.Stdout)
		require.Contains(innerT, stdout, "---", "output should contain frontmatter")
		require.Contains(innerT, stdout, "title:", "output should contain title in metadata")
		require.Contains(innerT, stdout, "Sorry, planned but not yet available", "output should contain content")
	})
}

func TestCatCommand_UserKeg(t *testing.T) {
	t.Run("cat_from_user_keg_with_alias", func(innerT *testing.T) {
		innerT.Parallel()
		opts := []testutils.Option{
			testutils.WithFixture("testuser", "~"),
		}
		sb := NewSandbox(innerT, opts...)

		// First, initialize a user keg
		initCmd := NewProcess(innerT, false,
			"repo", "init",
			"public",
			"--user",
			"--keg", "public",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "init should succeed")
		require.Contains(innerT, string(initRes.Stdout), "keg public created")

		// Now cat the node from that user keg
		catCmd := NewProcess(innerT, false, "cat", "0", "--keg", "public")
		catRes := catCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, catRes.Err, "cat should succeed")

		stdout := string(catRes.Stdout)
		require.Contains(innerT, stdout, "---", "output should contain frontmatter")
		require.Contains(innerT, stdout, "title: Sorry, planned but not yet available", "metadata should contain title")
		require.Contains(innerT, stdout, "Sorry, planned but not yet available", "content should be present")
	})
}

func TestCatCommand_BumpsAccessedAndAccessCount(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	statsPath := "~/kegs/personal/0/stats.json"
	oldAccessed := "2001-01-01T00:00:00Z"
	sb.MustWriteFile(statsPath, []byte(`{"accessed":"`+oldAccessed+`","access_count":7}`), 0o644)

	h := NewProcess(t, false, "cat", "0", "--keg", "personal", "--content-only")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "cat should succeed and bump access metadata")

	var afterOne catStatsJSON
	require.NoError(t, json.Unmarshal(sb.MustReadFile(statsPath), &afterOne))
	require.Equal(t, 8, afterOne.AccessCount, "access count should increment on read")
	require.NotEmpty(t, afterOne.Accessed, "accessed should be set")
	require.NotEqual(t, oldAccessed, afterOne.Accessed, "accessed should be bumped")

	res = h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "second cat should also succeed")

	var afterTwo catStatsJSON
	require.NoError(t, json.Unmarshal(sb.MustReadFile(statsPath), &afterTwo))
	require.Equal(t, 9, afterTwo.AccessCount, "access count should increment on every read")
}
