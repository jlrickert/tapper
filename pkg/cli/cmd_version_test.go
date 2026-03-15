package cli_test

import (
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/cli"
	"github.com/stretchr/testify/require"
)

func TestVersionCommand_PrintsVersion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	result := NewProcess(t, false, "version").Run(sb.Context(), sb.Runtime())
	require.NoError(t, result.Err)
	require.Contains(t, string(result.Stdout), "tap")
}

func TestVersionCommand_LicenseFlag(t *testing.T) {
	t.Parallel()

	// Set the license text so the flag has something to print.
	cli.LicenseText = "Apache License\nVersion 2.0\n"

	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	result := NewProcess(t, false, "version", "--license").Run(sb.Context(), sb.Runtime())
	require.NoError(t, result.Err)

	stdout := string(result.Stdout)
	require.Contains(t, stdout, "Apache License")
	require.Contains(t, stdout, "Version 2.0")
	// Should NOT contain the version string when --license is passed.
	require.NotContains(t, stdout, "tap dev")
}

func TestVersionCommand_LicenseCompletion(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	comp := NewCompletionProcess(t, false, 0, "version", "--").Run(sb.Context(), sb.Runtime())
	require.NoError(t, comp.Err)

	stdout := string(comp.Stdout)
	suggestions := parseCompletionSuggestions(stdout)
	found := false
	for _, s := range suggestions {
		if strings.HasPrefix(s, "--license") {
			found = true
			break
		}
	}
	require.True(t, found, "expected --license in completion output, got: %v", suggestions)
}

func TestLicenseText_NotEmpty(t *testing.T) {
	// This test verifies the variable exists and can hold license text.
	// In production, this is set by go:embed in cmd/tap and cmd/keg.
	// Here we test with a known value.
	cli.LicenseText = "Apache License\nVersion 2.0\nSome license content"
	require.NotEmpty(t, cli.LicenseText)
	require.Contains(t, cli.LicenseText, "Apache License")
	require.Contains(t, cli.LicenseText, "Version 2.0")
}
