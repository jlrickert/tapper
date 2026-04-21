package tapper_test

import (
	"context"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/tapper"

	// Adapter registration drives IntegrateHosts; without this import
	// the registry is empty and the integration tests below would
	// cover nothing.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
)

func newIntegrateTap(t *testing.T) (*tapper.Tap, *sandbox.Sandbox) {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap, sb
}

func TestTap_Integrate_DryRunReturnsPathsWithoutWriting(t *testing.T) {
	t.Parallel()
	tap, sb := newIntegrateTap(t)

	targets, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{
		Host:   "codex",
		DryRun: true,
	})
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	// Every target must sit under ~/.codex and carry one of the
	// known filenames produced by the codex adapter.
	for _, p := range targets {
		require.True(t, strings.Contains(p, "/.codex/"), "unexpected target %q", p)
	}
	joined := strings.Join(targets, "\n")
	require.Contains(t, joined, "AGENTS.md")
	require.Contains(t, joined, "config-snippet.toml")
	require.Contains(t, joined, filepath.FromSlash("prompts/tapper-orient.md"))

	// No files were actually written.
	_, err = sb.ReadFile(".codex/AGENTS.md")
	require.Error(t, err)
}

func TestTap_Integrate_CodexWritesAndMatchesEmbedded(t *testing.T) {
	t.Parallel()
	tap, sb := newIntegrateTap(t)

	targets, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex"})
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	// Spot-check that AGENTS.md landed with the embedded bytes.
	got, err := sb.ReadFile(".codex/AGENTS.md")
	require.NoError(t, err)
	want, err := fs.ReadFile(integrations.IntegrationsFS, "rendered/codex/AGENTS.md")
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))

	// Prompt files landed too.
	got, err = sb.ReadFile(".codex/prompts/tapper-orient.md")
	require.NoError(t, err)
	require.Contains(t, string(got), "Orient to the current tapper keg")
}

func TestTap_Integrate_ClaudeWritesToPluginPath(t *testing.T) {
	t.Parallel()
	tap, sb := newIntegrateTap(t)

	targets, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude"})
	require.NoError(t, err)
	require.NotEmpty(t, targets)

	// Every claude target should live under ~/.claude/plugins/tapper.
	for _, p := range targets {
		require.Contains(t, p, filepath.FromSlash(".claude/plugins/tapper"), "unexpected target %q", p)
	}

	got, err := sb.ReadFile(".claude/plugins/tapper/.claude-plugin/plugin.json")
	require.NoError(t, err)
	require.Contains(t, string(got), `"name": "tapper"`)

	got, err = sb.ReadFile(".claude/plugins/tapper/skills/tapper/SKILL.md")
	require.NoError(t, err)
	require.Contains(t, string(got), "tapper")
}

func TestTap_Integrate_UnknownHostReturnsError(t *testing.T) {
	t.Parallel()
	tap, _ := newIntegrateTap(t)
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "not-a-host"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown host")
}

func TestTap_Integrate_TargetOverrideWritesToCustomPath(t *testing.T) {
	t.Parallel()
	tap, sb := newIntegrateTap(t)

	custom := "/home/testuser/custom-codex"
	targets, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{
		Host:   "codex",
		Target: custom,
	})
	require.NoError(t, err)
	for _, p := range targets {
		require.True(t, strings.HasPrefix(p, custom), "target %q did not honor override %q", p, custom)
	}
	_, err = sb.ReadFile("custom-codex/AGENTS.md")
	require.NoError(t, err)
}

func TestTap_IntegrateHosts_IsSortedAndContainsRegisteredAdapters(t *testing.T) {
	t.Parallel()
	hosts := tapper.IntegrateHosts()
	require.NotEmpty(t, hosts)
	require.Contains(t, hosts, "claude")
	require.Contains(t, hosts, "codex")

	sortedCopy := append([]string(nil), hosts...)
	sort.Strings(sortedCopy)
	require.Equal(t, sortedCopy, hosts, "IntegrateHosts must be sorted")
}
