package cli_test

import (
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

type infoTestCase struct {
	name             string
	args             []string
	setupFixture     *string
	expectedInStdout []string
	expectedErr      string
	description      string
}

func TestInfoCommand_DisplaysKegMetadata(t *testing.T) {
	tests := []infoTestCase{
		{
			name:        "info_no_alias_error",
			args:        []string{"info"},
			expectedErr: "no keg configured",
			description: "Error when no keg is configured and no alias specified",
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
				require.NoError(innerT, res.Err, "info command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}
			}
		})
	}
}

func TestInfoCommand_IntegrationWithInit(t *testing.T) {
	t.Run("info_after_init_displays_keg_metadata", func(innerT *testing.T) {
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

		// Now display the keg info
		infoCmd := NewProcess(innerT, false, "info", "--keg", "newstudy")
		infoRes := infoCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, infoRes.Err, "info should succeed after init")

		stdout := string(infoRes.Stdout)
		require.Contains(innerT, stdout, "kegv:", "output should contain keg version")
		require.Contains(innerT, stdout, "creator:", "output should contain creator field")
	})
}

func TestInfoCommand_WithJoeFixture(t *testing.T) {
	tests := []infoTestCase{
		{
			name:             "info_with_explicit_alias",
			args:             []string{"info", "--keg", "personal"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"kegv:"},
			description:      "Display info for explicitly specified keg alias",
		},
		{
			name:         "info_with_nonexistent_alias",
			args:         []string{"info", "--keg", "nonexistent"},
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

			h := NewProcess(innerT, false, tt.args...)
			res := h.Run(sb.Context(), sb.Runtime())

			if tt.expectedErr != "" {
				require.Error(innerT, res.Err, "expected error - %s", tt.description)
				stderr := string(res.Stderr)
				require.Contains(innerT, stderr, tt.expectedErr,
					"error message should contain %q, got stderr: %s", tt.expectedErr, stderr)
			} else {
				require.NoError(innerT, res.Err, "info command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}
			}
		})
	}
}
