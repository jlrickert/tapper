package cli_test

import (
	"path/filepath"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type initTestCase struct {
	name               string
	args               []string
	expectedAlias      string
	expectedLocation   string
	expectConfigUpdate bool
	setupFixture       *string
	cwd                *string
	description        string
}

func TestInitCommand_TableDriven(t *testing.T) {
	tests := []initTestCase{
		{
			name: "local_keg_with_dot_infers_type",
			args: []string{
				"init",
				".",
				"--alias", "myalias",
				"--creator", "me",
			},
			expectedAlias:    "myalias",
			expectedLocation: "~/",
			description:      "When first argument is '.', type should be inferred as local without --type flag",
		},
		{
			name: "local_keg_with_dot_infers_alias",
			args: []string{
				"init",
				".",
				"--creator", "me",
			},
			expectedAlias:    "myproject",
			expectedLocation: "~/myproject",
			cwd:              strPtr("~/myproject"),
			description:      "Project keg with '.' should infer alias from current working directory base when not provided",
		},
		{
			name: "local_keg_with_dot_explicit_type",
			args: []string{
				"init",
				".",
				"--type", "local",
				"--alias", "myalias",
				"--creator", "me",
			},
			expectedAlias:    "myalias",
			expectedLocation: "~/",
			description:      "Project keg with explicit --type local flag",
		},
		{
			name: "user_keg_defaults_to_user_type",
			args: []string{
				"init",
				"public",
				"--alias", "public",
				"--creator", "testcreator",
			},
			expectedAlias:      "public",
			expectedLocation:   "~/.local/share/tapper/kegs/public",
			expectConfigUpdate: true,
			setupFixture:       strPtr("testuser"),
			description:        "When type is omitted and first argument is not '.', default type should be user",
		},
		{
			name: "user_keg_with_explicit_type",
			args: []string{
				"init",
				"public",
				"--type", "user",
				"--alias", "public",
				"--creator", "testcreator",
			},
			expectedAlias:      "public",
			expectedLocation:   "~/.local/share/tapper/kegs/public",
			expectConfigUpdate: true,
			setupFixture:       strPtr("testuser"),
			description:        "User keg with explicit --type user flag",
		},
		{
			name: "user_keg_infers_alias",
			args: []string{
				"init",
				"myblog",
				"--creator", "me",
			},
			expectedAlias:      "myblog",
			expectedLocation:   "~/.local/share/tapper/kegs/myblog",
			expectConfigUpdate: true,
			setupFixture:       strPtr("testuser"),
			description:        "User keg should infer alias from name when not provided",
		},
		{
			name: "dot_with_user_type_infers_alias",
			args: []string{
				"init",
				".",
				"--type", "user",
				"--creator", "me",
			},
			expectedAlias:      "myproject",
			expectedLocation:   "~/.local/share/tapper/kegs/myproject",
			expectConfigUpdate: true,
			setupFixture:       strPtr("testuser"),
			cwd:                strPtr("/home/testuser/myproject"),
			description:        "When name is '.' with type user, alias should infer from current working directory base",
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
			require.Equal(innerT, "", string(res.Stderr), "stderr should be empty")

			// Determine the base path for reading files (remove /dex/nodes.tsv from the location)
			var baseKegPath string
			if tt.setupFixture != nil {
				// User kegs are at .local/share/tapper/kegs/{alias}
				baseKegPath = "~/.local/share/tapper/kegs/" + tt.expectedAlias
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
				readmePath = "0/README.md"
			}
			readme := sb.MustReadFile(readmePath)
			require.Contains(innerT, string(readme),
				"Sorry, planned but not yet available",
				"zero node README should contain placeholder text")

			metaPath := baseKegPath
			if metaPath != "" {
				metaPath += "/0/meta.yaml"
			} else {
				metaPath = "0/meta.yaml"
			}
			meta := sb.MustReadFile(metaPath)
			require.Contains(innerT, string(meta),
				"title: Sorry, planned but not yet available",
				"zero node meta should include the placeholder title")

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
