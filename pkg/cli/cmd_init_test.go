package cli_test

import (
	"path/filepath"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type initTestCase struct {
	name               string
	args               []string
	expectedAlias      string
	expectedLocation   string
	expectedStdout     []string
	expectConfigUpdate bool
	setupFixture       *string
	cwd                *string
	description        string
}

func TestInitCommand_TableDriven(t *testing.T) {
	tests := []initTestCase{
		{
			name: "local_keg_named_project_defaults_to_kegs_alias",
			args: []string{
				"init",
				"--project",
				"--keg", "power",
				"--creator", "me",
			},
			expectedAlias:    "power",
			expectedLocation: "~/kegs/power",
			expectedStdout: []string{
				"keg power created (file-backed)",
			},
			description: "When --project, destination should default to kegs/<alias> under project root",
		},
		{
			name: "local_keg_with_cwd_without_project",
			args: []string{
				"init",
				"--cwd",
				"--keg", "power",
				"--creator", "me",
			},
			expectedAlias:    "power",
			expectedLocation: "~/myproject/kegs/power",
			expectedStdout: []string{
				"keg power created (file-backed)",
			},
			cwd:         strPtr("~/myproject"),
			description: "When --cwd is set without --project, destination should still resolve as a local keg under the current working directory",
		},
		{
			name: "local_keg_with_path_without_project",
			args: []string{
				"init",
				"--path", ".",
				"--keg", "workspace",
				"--creator", "me",
			},
			expectedAlias:    "workspace",
			expectedLocation: "~/myproject",
			expectedStdout: []string{
				"keg workspace created (file-backed)",
			},
			cwd:         strPtr("~/myproject"),
			description: "When --path is set without --project, destination should resolve as a local keg at the explicit path",
		},
		{
			name: "local_keg_with_explicit_alias",
			args: []string{
				"init",
				"--project",
				"--keg", "myalias",
				"--creator", "me",
			},
			expectedAlias:    "myalias",
			expectedLocation: "~/kegs/myalias",
			description:      "When --project with explicit --keg, default destination should be kegs/<alias> under project root",
		},
		{
			name: "local_keg_infers_alias_from_cwd",
			args: []string{
				"init",
				"--project",
				"--creator", "me",
			},
			expectedAlias:    "myproject",
			expectedLocation: "~/myproject/kegs/myproject",
			cwd:              strPtr("~/myproject"),
			description:      "Project keg should infer alias from current working directory base when --keg not provided",
		},
		{
			name: "local_keg_project_explicit_alias",
			args: []string{
				"init",
				"--project",
				"--keg", "myalias",
				"--creator", "me",
			},
			expectedAlias:    "myalias",
			expectedLocation: "~/kegs/myalias",
			description:      "Project keg with explicit --project and --keg flags",
		},
		{
			name: "user_keg_defaults_to_user_type",
			args: []string{
				"init",
				"--keg", "public",
				"--creator", "testcreator",
			},
			expectedAlias:      "public",
			expectedLocation:   "~/kegs/@local/public",
			expectConfigUpdate: false,
			setupFixture:       strPtr("testuser"),
			description:        "When no destination flag is provided, default destination should be user",
		},
		{
			name: "user_keg_with_explicit_type",
			args: []string{
				"init",
				"--user",
				"--keg", "public",
				"--creator", "testcreator",
			},
			expectedAlias:      "public",
			expectedLocation:   "~/kegs/@local/public",
			expectConfigUpdate: false,
			setupFixture:       strPtr("testuser"),
			description:        "User keg with explicit --user flag",
		},
		{
			name: "user_keg_with_explicit_alias",
			args: []string{
				"init",
				"--keg", "myblog",
				"--creator", "me",
			},
			expectedAlias:      "myblog",
			expectedLocation:   "~/kegs/@local/myblog",
			expectConfigUpdate: false,
			setupFixture:       strPtr("testuser"),
			description:        "User keg should use --keg alias for directory name",
		},
		{
			name: "user_type_infers_alias_from_cwd",
			args: []string{
				"init",
				"--user",
				"--creator", "me",
			},
			expectedAlias:      "myproject",
			expectedLocation:   "~/kegs/@local/myproject",
			expectConfigUpdate: false,
			setupFixture:       strPtr("testuser"),
			cwd:                strPtr("/home/testuser/myproject"),
			description:        "When --keg is omitted with --user, alias should infer from current working directory base",
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

			require.NoError(innerT, res.Err, "init command should succeed - %s", tt.description)
			require.Contains(innerT, string(res.Stdout), "keg "+tt.expectedAlias+" created",
				"unexpected output: %q", string(res.Stdout))
			for _, fragment := range tt.expectedStdout {
				require.Contains(innerT, string(res.Stdout), fragment,
					"expected output to contain %q, got %q", fragment, string(res.Stdout))
			}
			require.NotContains(innerT, string(res.Stderr), "level=ERROR", "stderr should not contain errors")

			// Determine the base path for reading files (remove /dex/nodes.tsv from the location)
			var baseKegPath string
			if tt.setupFixture != nil {
				// User kegs land on the local hub at <basePath>/@local/{alias}
				baseKegPath = "~/kegs/@local/" + tt.expectedAlias
			} else {
				// Project kegs are at the repo root
				baseKegPath = ""
			}

			// Verify the created keg contains the example contents
			nodesPath := baseKegPath
			if nodesPath != "" {
				nodesPath = filepath.Join(baseKegPath, "/dex/nodes.tsv")
			} else {
				nodesPath = filepath.Join(tt.expectedLocation, "dex/nodes.tsv")
			}
			nodes := sb.MustReadFile(nodesPath)
			require.Contains(innerT, string(nodes), "0\t",
				"nodes index should contain zero node")

			readmePath := baseKegPath
			if readmePath != "" {
				readmePath += "/0/README.md"
			} else {
				readmePath = filepath.Join(tt.expectedLocation, "0/README.md")
			}
			readme := sb.MustReadFile(readmePath)
			require.Contains(innerT, string(readme),
				"Sorry, planned but not yet available",
				"zero node README should contain placeholder text")

			statsPath := baseKegPath
			if statsPath != "" {
				statsPath += "/0/stats.json"
			} else {
				statsPath = filepath.Join(tt.expectedLocation, "0/stats.json")
			}
			stats := sb.MustReadFile(statsPath)
			require.Contains(innerT, string(stats),
				`"title":"Sorry, planned but not yet available"`,
				"zero node stats should include the placeholder title")

			kegPath := baseKegPath
			if kegPath != "" {
				kegPath += "/keg"
			} else {
				kegPath = filepath.Join(tt.expectedLocation, "keg")
			}
			kegConfig := sb.MustReadFile(kegPath)
			require.Contains(innerT, string(kegConfig),
				"# yaml-language-server: $schema=https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/keg-config.json",
				"keg config should include schema modeline")

			// For user kegs, verify config was updated
			if tt.setupFixture != nil {
				userConfig := sb.MustReadFile("~/.config/tapper/config.yaml")

				if tt.expectConfigUpdate {
					require.Contains(innerT, string(userConfig), tt.expectedAlias+":",
						"user config should contain the new keg alias")
				} else {
					require.NotContains(innerT, string(userConfig), tt.expectedAlias+":",
						"user config should contain the new keg alias")
				}
			}
		})
	}
}

func strPtr(s string) *string {
	return &s
}

func TestInitCommand_DestinationValidation(t *testing.T) {
	t.Run("project_and_user_flags_conflict", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT)

		h := NewProcess(innerT, false, "init", "--keg", "blog", "--project", "--user")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "cannot be combined with a local destination")
	})

	t.Run("cwd_conflicts_with_user_flag", func(innerT *testing.T) {
		innerT.Parallel()
		sb := NewSandbox(innerT)

		h := NewProcess(innerT, false, "init", "--keg", "blog", "--cwd", "--user")
		res := h.Run(sb.Context(), sb.Runtime())

		require.Error(innerT, res.Err)
		require.Contains(innerT, string(res.Stderr), "cannot be combined with a local destination")
	})
}

func TestInitCommand_FallsBackToPlatformDefaultWhenSearchPathsUnset(t *testing.T) {
	t.Parallel()

	sb := NewSandbox(t)

	h := NewProcess(t, false, "init", "--user", "--keg", "fresh", "--creator", "me")
	res := h.Run(sb.Context(), sb.Runtime())

	require.NoError(t, res.Err, "init should succeed without preconfigured kegSearchPaths")
	require.Contains(t, string(res.Stdout), "keg fresh created")

	keg := sb.MustReadFile("~/.local/share/tapper/kegs/@local/fresh/keg")
	require.Contains(t, string(keg), "$schema=",
		"keg config should have been written under platform default data dir")
}

func TestInitCommand_RejectsInvalidAlias(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		alias string
	}{
		{"uppercase", "Blog"},
		{"space", "my blog"},
		{"slash", "kegs/blog"},
		{"dot", "blog.keg"},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(innerT *testing.T) {
			innerT.Parallel()
			sb := NewSandbox(innerT, testutils.WithFixture("testuser", "~"))

			h := NewProcess(innerT, false, "init", "--user", "--keg", c.alias)
			res := h.Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err)
			require.Contains(innerT, string(res.Stderr), "invalid keg alias")
		})
	}
}

// TestInitCommand_InteractivePrompt covers the TTY-gated prompt path:
// when stdin is a TTY and no destination flags are supplied, tap init
// asks for alias / location / title / creator. We pipe scripted answers
// via RunWithIO and assert that the resulting keg uses the alias from
// the prompt (not cwd basename) and lands in the user destination.
func TestInitCommand_InteractivePrompt(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	answers := strings.Join([]string{
		"diary",      // alias
		"user",       // location
		"My Diary",   // title
		"me@example", // creator
		"",           // trailing newline buffer
	}, "\n")

	h := NewProcess(t, true, "init")
	res := h.RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(answers))
	require.NoError(t, res.Err, "interactive init should succeed: stderr=%q", string(res.Stderr))
	require.Contains(t, string(res.Stdout), "keg diary created (file-backed)")

	keg := sb.MustReadFile("~/.local/share/tapper/kegs/@local/diary/keg")
	require.Contains(t, string(keg), "$schema=", "interactive init should have written the platform-default user keg")
	require.Contains(t, string(keg), "title: My Diary")
	require.Contains(t, string(keg), "creator: me@example")
}

// TestInitCommand_NonInteractiveFlagSkipsPrompt confirms that
// --non-interactive bypasses the TTY prompt even when stdin is a TTY,
// so scripted invocations on attended terminals can rely on flag
// defaults without piping answers.
func TestInitCommand_NonInteractiveFlagSkipsPrompt(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)

	h := NewProcess(t, true, "init", "--non-interactive", "--keg", "ci", "--user", "--creator", "ci-bot")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "init --non-interactive on TTY should succeed without piped stdin")
	require.Contains(t, string(res.Stdout), "keg ci created (file-backed)")
	require.NotContains(t, string(res.Stderr), "keg alias [")
}

// TestInitCommand_NonTTYSkipsPrompt confirms that bare `tap init`
// without a TTY (CI, MCP, pipes) does not block waiting for prompt
// answers — it falls back to alias inference from cwd and the platform
// default user destination. This is the behavior MCP relies on.
func TestInitCommand_NonTTYSkipsPrompt(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/scratch"))

	h := NewProcess(t, false, "init")
	res := h.Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err, "non-TTY bare init should succeed via cwd-inferred alias")
	require.Contains(t, string(res.Stdout), "keg scratch created (file-backed)")
}
