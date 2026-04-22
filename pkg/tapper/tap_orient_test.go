package tapper_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/integrations"
	"github.com/jlrickert/tapper/pkg/tapper"

	// The orient payload reads rendered host bytes from
	// integrations.IntegrationsFS. The test below asserts that the
	// Claude SKILL.md appears at tier 2; the adapter's init() must run
	// so the embedded rendered tree is usable in isolation.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
)

func newOrientTap(t *testing.T) *tapper.Tap {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: "/home/testuser",
		User: "testuser",
	})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap
}

func TestTap_Orient_Tier0IsBoundedAndHostless(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: 0})
	require.NoError(t, err)
	require.Contains(t, payload, "tier 0")
	require.Contains(t, payload, "Rules:")
	require.NotContains(t, payload, "## Host:")
	require.NotContains(t, payload, "## Linking conventions")
	require.Less(t, len(payload), 2048, "tier-0 payload should stay bounded")
}

func TestTap_Orient_Tier2ClaudeIncludesSKILLBytes(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 2,
		Host: "claude",
	})
	require.NoError(t, err)
	want, err := fs.ReadFile(integrations.IntegrationsFS, "rendered/claude/skills/tapper/SKILL.md")
	require.NoError(t, err)
	require.Contains(t, payload, "## Host: claude")
	require.Contains(t, payload, strings.TrimRight(string(want), "\n"))
}

func TestTap_Orient_UnknownHostReturnsError(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	_, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 2,
		Host: "not-a-host",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown host")
}

func TestTap_Orient_TierClampsToBounds(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)

	high, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: 99})
	require.NoError(t, err)
	require.Contains(t, high, "tier 2")

	low, err := tap.Orient(context.Background(), tapper.OrientOptions{Tier: -5})
	require.NoError(t, err)
	require.Contains(t, low, "tier 0")
}

func TestTap_Orient_FlightAtTier1EmitsPlaceholder(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 1,
		KegTargetOptions: tapper.KegTargetOptions{
			Flight: "f-demo",
		},
	})
	require.NoError(t, err)
	require.Contains(t, payload, "## Flight `f-demo`")
	require.Contains(t, payload, "not yet populated")
}

func TestTap_Orient_FlightAtTier0IsIgnored(t *testing.T) {
	t.Parallel()
	tap := newOrientTap(t)
	payload, err := tap.Orient(context.Background(), tapper.OrientOptions{
		Tier: 0,
		KegTargetOptions: tapper.KegTargetOptions{
			Flight: "f-demo",
		},
	})
	require.NoError(t, err)
	require.NotContains(t, payload, "Flight")
}

func TestTap_OrientableHosts_IsSortedAndIncludesClaude(t *testing.T) {
	t.Parallel()
	hosts := tapper.OrientableHosts()
	require.NotEmpty(t, hosts)
	require.Contains(t, hosts, "claude")
	for i := 1; i < len(hosts); i++ {
		require.LessOrEqual(t, hosts[i-1], hosts[i], "OrientableHosts must be sorted")
	}
}
