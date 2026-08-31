package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/schemas"
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
			args:             []string{"config"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"defaultKeg:", "hubs:"},
			description:      "Display merged configuration from user config",
		},
		{
			name:         "config_with_project_flag",
			args:         []string{"config", "--project"},
			setupFixture: strPtr("joe"),
			expectedErr:  "no configuration available",
			description:  "Project config may not exist and should error gracefully",
		},
		{
			name:         "config_template_user_includes_new_keys",
			args:         []string{"config", "template", "user"},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"fallbackHub:",
				"defaultNamespace: pub",
				"hubs:",
			},
			description: "User template should include the fallback hub, per-hub namespace, and hubs map",
		},
		{
			name:         "config_template_project_includes_new_keys",
			args:         []string{"config", "template", "project"},
			setupFixture: strPtr("joe"),
			expectedInStdout: []string{
				"defaultKeg:",
				"defaultHub:",
				"defaultNamespace:",
			},
			description: "Project template should include the default hub/namespace keys",
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
				require.NoError(innerT, res.Err, "config command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}

				if strings.Contains(strings.Join(tt.args, " "), "template") {
					// The modeline resolves to the schema copy materialized
					// from this binary, not the URL published on main.
					require.True(innerT, strings.HasPrefix(stdout,
						schemas.ModelinePrefix+schemas.ModelineURI(sb.Runtime(), schemas.TapConfig)+"\n"),
						"template output should start with a modeline pointing at the local schema, got:\n%s", stdout)
				}

				// Verify it looks like YAML output
				require.True(innerT, strings.Contains(stdout, ":"),
					"output should contain YAML key-value pairs")
			}
		})
	}
}

func TestConfigCommand_IntegrationWithInit(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	res := NewProcess(t, false, "init").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), `unknown command "init"`)
}

func TestConfigCommand_ReadsExplicitConfigPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	const configPath = "/tmp/custom-tap-config.yaml"
	const raw = "fallbackKeg: custom\nunknownKey: keep-me\n"
	require.NoError(t, sb.Runtime().AtomicWriteFile(configPath, []byte(raw), 0o644))

	res := NewProcess(t, false, "-c", configPath, "config").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	require.Equal(t, raw, string(res.Stdout))
}

func TestConfigCommand_RejectsScopedFlagsWithExplicitConfigPath(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	const configPath = "/tmp/custom-tap-config.yaml"
	require.NoError(t, sb.Runtime().AtomicWriteFile(configPath, []byte("fallbackKeg: custom\n"), 0o644))

	tests := [][]string{
		{"-c", configPath, "config", "--user"},
		{"-c", configPath, "config", "--project"},
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

	res := NewProcess(t, false, "-c", "/tmp/custom-tap-config.yaml", "config", "template", "user").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "--config cannot be used with config template")
}

func TestConfigTemplateCommand_Completion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "config", "template", "").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "user")
	require.Contains(t, suggestions, "project")
}

func TestConfigCommand_ExplainFlagCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	comp := NewCompletionProcess(t, false, 0, "config", "--explain", "d").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	suggestions := parseCompletionSuggestions(string(comp.Stdout))
	require.Contains(t, suggestions, "defaultKeg")
	require.Contains(t, suggestions, "defaultHub")
	require.NotContains(t, suggestions, "logLevel")
}

func TestConfigCommand_ExplainFlag(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Setwd("/home/testuser"))

	res := NewProcess(t, false, "config", "--explain", "defaultKeg").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "defaultKeg =")
	require.Contains(t, stdout, "source:")
}

func TestConfigCommand_ExplainFlagWithEnvVar(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Setwd("/home/testuser"))
	require.NoError(t, sb.Runtime().Env().Set("TAP_DEFAULT_KEG", "envkeg"))

	res := NewProcess(t, false, "config", "--explain", "defaultKeg").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	require.Contains(t, stdout, "defaultKeg = envkeg")
	require.Contains(t, stdout, "source: env vars")
}

func TestConfigCommand_ProjectFlightPrecedenceMatchesOrient(t *testing.T) {
	t.Parallel()

	sb := NewRemoteKegListSandbox(t, remoteCompletionKegs())
	project := "/home/testuser/work/project"
	descendant := project + "/src/pkg"
	require.NoError(t, sb.Runtime().Mkdir(descendant, 0o755, true))
	require.NoError(t, sb.Setwd(descendant))
	userConfig := "/home/testuser/.config/tapper/config.yaml"
	remoteConfig, err := sb.Runtime().ReadFile(userConfig)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		userConfig, append([]byte("flight: +baseline\n"), remoteConfig...), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		project+"/.tapper/config.yaml", []byte("flight: +project\n"), 0o644))

	explained := NewProcess(t, false, "config", "--explain", "flight").Run(sb.Context(), sb.Runtime())
	require.NoError(t, explained.Err)
	require.Contains(t, string(explained.Stdout), "flight = +project")
	require.Contains(t, string(explained.Stdout), "source: project config")

	oriented := NewProcess(t, false, "orient").Run(sb.Context(), sb.Runtime())
	require.NoError(t, oriented.Err)
	require.Contains(t, string(oriented.Stdout), "+project")
	require.Contains(t, string(oriented.Stdout), "Project instructions")
	require.NotContains(t, string(oriented.Stdout), "Baseline instructions")

	require.NoError(t, sb.Runtime().Env().Set("TAP_FLIGHT", "+environment"))
	explained = NewProcess(t, false, "config", "--explain", "flight").Run(sb.Context(), sb.Runtime())
	require.NoError(t, explained.Err)
	require.Contains(t, string(explained.Stdout), "flight = +environment")
	require.Contains(t, string(explained.Stdout), "source: env vars")

	oriented = NewProcess(t, false, "orient").Run(sb.Context(), sb.Runtime())
	require.NoError(t, oriented.Err)
	require.Contains(t, string(oriented.Stdout), "+environment")
	require.Contains(t, string(oriented.Stdout), "Environment instructions")

	oriented = NewProcess(t, false, "--flight", "+explicit", "orient").Run(sb.Context(), sb.Runtime())
	require.NoError(t, oriented.Err)
	require.Contains(t, string(oriented.Stdout), "+explicit")
	require.Contains(t, string(oriented.Stdout), "Explicit instructions")
	require.NotContains(t, string(oriented.Stdout), "Environment instructions")
}

func TestConfigCommand_ShowSourcesFlag(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Setwd("/home/testuser"))

	res := NewProcess(t, false, "config", "--show-sources").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	stdout := string(res.Stdout)
	// Should contain all field names.
	require.Contains(t, stdout, "defaultKeg")
	require.Contains(t, stdout, "fallbackKeg")
	require.Contains(t, stdout, "logFile")
	require.Contains(t, stdout, "logLevel")
	require.Contains(t, stdout, "defaultHub")
	require.Contains(t, stdout, "fallbackHub")
	require.Contains(t, stdout, "defaultNamespace")
	require.Contains(t, stdout, "fallbackNamespace")
	// Should have source annotations in brackets.
	require.Contains(t, stdout, "[")
}

func TestConfigCommand_ExplainUnknownField(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser"))

	res := NewProcess(t, false, "config", "--explain", "nonexistent").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "unknown config field")
}
