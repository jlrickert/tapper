package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type configTestCase struct {
	name             string
	args             []string
	setupFixture     *string
	expectedInStdout []string
	expectedErr      string
	description      string
}

func TestConfigCommand_DisplaysMergedConfig(t *testing.T) {
	tests := []configTestCase{
		{
			name:             "config_displays_merged_config",
			args:             []string{"repo", "config"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"defaultKeg:", "kegs:"},
			description:      "Display merged configuration from user config",
		},
		{
			name:         "config_with_project_flag",
			args:         []string{"repo", "config", "--project"},
			setupFixture: strPtr("joe"),
			expectedErr:  "no configuration available",
			description:  "Project config may not exist and should error gracefully",
		},
		{
			name:         "config_template_user_includes_new_keys",
			args:         []string{"repo", "config", "--template"},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"defaultKeg:",
				"fallbackKeg:",
				"kegSearchPaths:",
			},
			description: "Template output should include new config keys",
		},
		{
			name:         "config_template_project_includes_new_keys",
			args:         []string{"repo", "config", "--template", "--project"},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"defaultKeg:",
				"fallbackKeg:",
				"kegSearchPaths:",
			},
			description: "Project template output should include new config keys",
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

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			if tt.expectedErr != "" {
				require.Error(innerT, res.Err, "expected error - %s", tt.description)
				stderr := string(res.Stderr)
				require.Contains(innerT, stderr, tt.expectedErr,
					"error message should contain %q, got stderr: %s", tt.expectedErr, stderr)
			} else {
				require.NoError(innerT, res.Err, "repo config command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}

				// Verify it looks like YAML output
				require.True(innerT, strings.Contains(stdout, ":"),
					"output should contain YAML key-value pairs")
			}
		})
	}
}

func TestConfigCommand_IntegrationWithInit(t *testing.T) {
	t.Run("config_after_init", func(innerT *testing.T) {
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

		// Now display the repo config
		configCmd := NewProcess(innerT, false, "repo", "config")
		configRes := configCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, configRes.Err, "repo config should succeed after init")

		stdout := string(configRes.Stdout)
		require.Contains(innerT, stdout, "kegs:", "output should contain kegs section")
		require.Contains(innerT, stdout, "newstudy", "output should contain the new keg alias")
	})
}
