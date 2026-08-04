package cli

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRootConfiguredFlightDoesNotBecomeExplicitDependency(t *testing.T) {
	t.Parallel()
	sb := newTestSandbox(t)
	require.NoError(t, sb.Setwd("/home/testuser/project/child"))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/.config/tapper/config.yaml",
		[]byte("flight: +baseline\nfallbackNamespace: local\nhubs:\n  home:\n    kind: local\n    basePath: /home/testuser/kegs\n"), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/project/.tapper/config.yaml",
		[]byte("flight: +project\n"), 0o644))
	require.NoError(t, sb.Runtime().AtomicWriteFile(
		"/home/testuser/kegs/flights.d/project.yaml",
		[]byte("title: Project\ninstructions: Project instructions\n"), 0o644))

	deps := &Deps{Profile: TapProfile(), Runtime: sb.Runtime()}
	cmd := NewRootCmd(deps)
	cmd.SetArgs([]string{"orient"})
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)

	require.NoError(t, cmd.ExecuteContext(sb.Context()), stderr.String())
	require.Empty(t, deps.KegTargetOptions.Flight,
		"configured selection must not occupy the explicit --flight dependency")
	require.Contains(t, stdout.String(), "+project")
}
