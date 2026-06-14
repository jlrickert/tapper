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

func TestSettingsCommand_DisplaysKegMetadata(t *testing.T) {
	tests := []infoTestCase{
		{
			name:        "info_no_alias_error",
			args:        []string{"keg", "settings"},
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
				require.NoError(innerT, res.Err, "settings command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}
			}
		})
	}
}

func TestSettingsCommand_IntegrationWithInit(t *testing.T) {
	t.Run("config_after_init_displays_keg_metadata", func(innerT *testing.T) {
		innerT.Parallel()
		opts := []testutils.Option{
			testutils.WithFixture("testuser", "~"),
		}
		sb := NewSandbox(innerT, opts...)

		// First, initialize a user keg
		initCmd := NewProcess(innerT, false,
			"init",
			"--user",
			"--keg", "newstudy",
			"--creator", "test-user",
		)
		initRes := initCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, initRes.Err, "init should succeed")

		// Now display the keg config
		infoCmd := NewProcess(innerT, false, "keg", "settings", "--keg", "newstudy")
		infoRes := infoCmd.Run(sb.Context(), sb.Runtime())
		require.NoError(innerT, infoRes.Err, "settings should succeed after init")

		stdout := string(infoRes.Stdout)
		require.Contains(innerT, stdout, "kegv:", "output should contain keg version")
		require.Contains(innerT, stdout, "creator:", "output should contain creator field")
	})
}

func TestSettingsCommand_WithJoeFixture(t *testing.T) {
	tests := []infoTestCase{
		{
			name:             "info_with_explicit_alias",
			args:             []string{"keg", "settings", "--keg", "personal"},
			setupFixture:     strPtr("joe"),
			expectedInStdout: []string{"kegv:", "indexes:"},
			description:      "Display info for explicitly specified keg alias",
		},
		{
			name:         "info_with_nonexistent_alias",
			args:         []string{"keg", "settings", "--keg", "nonexistent"},
			setupFixture: strPtr("joe"),
			expectedErr:  "keg not initialized",
			description:  "Error when keg does not exist on disk",
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
				require.NoError(innerT, res.Err, "settings command should succeed - %s", tt.description)
				stdout := string(res.Stdout)

				for _, expected := range tt.expectedInStdout {
					require.Contains(innerT, stdout, expected,
						"expected output to contain %q, got:\n%s", expected, stdout)
				}
			}
		})
	}
}

func TestSettingsCommand_PreservesEntitiesAndCustomSections(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	custom := `kegv: 2025-07
title: Example
entities:
  client:
    id: 42
    summary: Example entity
tags:
  project: project summary
custom_block:
  enabled: true
`
	sb.MustWriteFile("~/kegs/@local/example/keg", []byte(custom), 0o644)

	infoCmd := NewProcess(t, false, "keg", "settings", "--keg", "example")
	infoRes := infoCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, infoRes.Err)

	stdout := string(infoRes.Stdout)
	require.Contains(t, stdout, "entities:")
	require.Contains(t, stdout, "client:")
	require.Contains(t, stdout, "custom_block:")
}
