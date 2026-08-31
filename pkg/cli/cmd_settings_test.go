package cli_test

import (
	"context"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
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
			name:        "info_not_bootstrapped",
			args:        []string{"keg", "settings"},
			expectedErr: "tap bootstrap",
			description: "Error when tapper is not bootstrapped and no alias specified",
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
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	res := NewProcess(t, false, "init").Run(sb.Context(), sb.Runtime())
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), `unknown command "init"`)
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
	opened := fixtureKeg(t, sb.Runtime(), "example")
	current, err := opened.Settings(context.Background())
	require.NoError(t, err)
	require.NoError(t, opened.SetSettings(context.Background(), []byte(custom), keg.SettingsWriteOptions{
		ExpectedHash: current.Hash(),
	}))

	infoCmd := NewProcess(t, false, "keg", "settings", "--keg", "example")
	infoRes := infoCmd.Run(sb.Context(), sb.Runtime())
	require.NoError(t, infoRes.Err)

	stdout := string(infoRes.Stdout)
	require.Contains(t, stdout, "entities:")
	require.Contains(t, stdout, "client:")
	require.Contains(t, stdout, "custom_block:")
}
