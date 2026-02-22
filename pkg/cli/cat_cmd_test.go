package cli_test

import (
	"strings"
	"testing"

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

				// Verify frontmatter structure
				require.Contains(innerT, stdout, "---", "output should contain frontmatter delimiter")
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
			"--type", "user",
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
			"--type", "user",
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
