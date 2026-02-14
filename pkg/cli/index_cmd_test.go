package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type indexTestCase struct {
	name             string
	args             []string
	setupFixture     *string
	cwd              *string
	expectedInStdout []string
	expectedErr      string
	description      string
}

func TestIndexCommand_TableDrivenErrorHandling(t *testing.T) {
	tests := []indexTestCase{
		{
			name:         "index_nonexistent_alias",
			args:         []string{"index", "--alias", "nonexistent"},
			setupFixture: strPtr("joe"),
			expectedErr:  "keg alias not found",
			description:  "Error when keg alias does not exist",
		},
		{
			name:        "index_no_keg_configured",
			args:        []string{"index"},
			expectedErr: "no keg configured",
			description: "Error when no keg is configured",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.SandboxOption
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			if tt.cwd != nil {
				sb.Setwd(*tt.cwd)
			}

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err, "expected error - %s", tt.description)
			stderr := string(res.Stderr)
			require.Contains(innerT, stderr, tt.expectedErr,
				"error message should contain %q, got stderr: %s and err: %v", tt.expectedErr, stderr, res.Err)
		})
	}
}

func TestIndexCommand_WithJoeFixture(t *testing.T) {
	tests := []indexTestCase{
		{
			name:             "index_personal_keg_from_default_location",
			args:             []string{"index"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Rebuild indices for default personal keg",
		},
		{
			name:             "index_work_keg_from_work_directory",
			args:             []string{"index"},
			setupFixture:     strPtr("joe"),
			cwd:              strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Rebuild indices for work keg when in work directory",
		},
		{
			name:             "index_explicit_alias_overrides_path_resolution",
			args:             []string{"index", "--alias", "example"},
			setupFixture:     strPtr("joe"),
			cwd:              strPtr("~/repos/work/spy-things"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Explicit alias overrides path-based keg resolution",
		},
		{
			name:             "index_personal_keg_explicit_alias",
			args:             []string{"index", "--alias", "personal"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"Indices rebuilt"},
			description:      "Rebuild indices for personal keg with explicit alias",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.SandboxOption
			if tt.setupFixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.setupFixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			if tt.cwd != nil {
				sb.Setwd(*tt.cwd)
			}

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			require.NoError(innerT, res.Err, "index command should succeed - %s", tt.description)
			stdout := string(res.Stdout)

			for _, expected := range tt.expectedInStdout {
				require.Contains(innerT, stdout, expected,
					"expected output to contain %q, got:\n%s", expected, stdout)
			}
		})
	}
}

func TestIndexCommand_IntegrationWithInit(t *testing.T) {
	t.Run("index_after_init", func(innerT *testing.T) {
		innerT.Parallel()
		opts := []testutils.SandboxOption{
			testutils.WithFixture("testuser", "~"),
		}
		sb := NewSandbox(innerT, opts...)

		// First, initialize a user keg
		initCmd := NewProcess(innerT, false,
			"init",
			"newstudy",
			"--type", "user",
			"--alias", "newstudy",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "init should succeed")
		require.Contains(innerT, string(initRes.Stdout), "keg newstudy created")

		// Now rebuild indices
		indexCmd := NewProcess(innerT, false, "index", "--alias", "newstudy")
		indexRes := indexCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, indexRes.Err, "index should succeed")

		stdout := string(indexRes.Stdout)
		require.Contains(innerT, stdout, "Indices rebuilt", "output should indicate successful rebuild")
	})
}
