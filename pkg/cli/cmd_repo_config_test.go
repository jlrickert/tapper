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
			args:         []string{"repo", "config", "template", "user"},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"# yaml-language-server: $schema=https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/tap-config.json",
				"fallbackKeg:",
				"kegSearchPaths:",
			},
			description: "Template output should include new config keys",
		},
		{
			name:         "config_template_project_includes_new_keys",
			args:         []string{"repo", "config", "template", "project"},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"# yaml-language-server: $schema=https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/tap-config.json",
				"defaultKeg:",
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

				if strings.Contains(strings.Join(tt.args, " "), "template") {
					require.True(innerT, strings.HasPrefix(stdout, "# yaml-language-server: $schema="),
						"template output should start with yaml-language-server modeline")
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

func TestConfigCommand_ReadsExplicitConfigPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	const configPath = "/tmp/custom-tap-config.yaml"
	const raw = "fallbackKeg: custom\nunknownKey: keep-me\n"
	require.NoError(t, sb.Runtime().AtomicWriteFile(configPath, []byte(raw), 0o644))

	res := NewProcess(t, false, "-c", configPath, "repo", "config").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, raw, string(res.Stdout))
}

func TestConfigCommand_RejectsScopedFlagsWithExplicitConfigPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	const configPath = "/tmp/custom-tap-config.yaml"
	require.NoError(t, sb.Runtime().AtomicWriteFile(configPath, []byte("fallbackKeg: custom\n"), 0o644))

	tests := [][]string{
		{"-c", configPath, "repo", "config", "--user"},
		{"-c", configPath, "repo", "config", "--project"},
	}

	for _, args := range tests {
		res := NewProcess(t, false, args...).Run(sb.Context(), sb.Runtime())
		require.Error(t, res.Err)
		require.Contains(t, string(res.Stderr), "--config cannot be combined with --user or --project")
	}
}

func TestConfigTemplateCommand_RejectsExplicitConfigPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	res := NewProcess(t, false, "-c", "/tmp/custom-tap-config.yaml", "repo", "config", "template", "user").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "--config cannot be used with repo config template")
}

func TestConfigTemplateCommand_Completion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "repo", "config", "template", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "user")
	require.Contains(t, suggestions, "project")
}
