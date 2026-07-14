package tapper_test

import (
	"context"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/integrations"
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"
	"github.com/jlrickert/tapper/pkg/tapper"
)

func newIntegrateTap(t *testing.T) (*tapper.Tap, *sandbox.Sandbox) {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)
	return tap, sb
}

func installFakeHost(t *testing.T, sb *sandbox.Sandbox, host string) {
	t.Helper()
	capture, err := sb.Runtime().HostPath("/home/testuser/calls")
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().Env().Set("CAPTURE", capture))
	require.NoError(t, sb.Runtime().Env().Set("PATH", "/home/testuser/bin"))
	marketplaceDefault := `[]`
	pluginsDefault := `[]`
	if host == "codex" {
		marketplaceDefault = `{"marketplaces":[]}`
		pluginsDefault = `{"installed":[]}`
	}
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$CAPTURE"
if [ "$1 $2 $3 $4" = "plugin marketplace list --json" ]; then
  if [ -n "$MARKETPLACES_JSON" ]; then printf '%s\n' "$MARKETPLACES_JSON"; else printf '%s\n' '` + marketplaceDefault + `'; fi
elif [ "$1 $2 $3" = "plugin list --json" ]; then
  if [ -n "$PLUGINS_JSON" ]; then printf '%s\n' "$PLUGINS_JSON"; else printf '%s\n' '` + pluginsDefault + `'; fi
else
  printf '%s\n' "host-output:$*"
fi
`
	require.NoError(t, sb.Runtime().WriteFile("/home/testuser/bin/"+host, []byte(script), 0o755))
	installFakeTap(t, sb)
}

func installFakeTap(t *testing.T, sb *sandbox.Sandbox) {
	t.Helper()
	require.NoError(t, sb.Runtime().Env().Set("PATH", "/home/testuser/bin"))
	script := `#!/bin/sh
if [ "$1 $2" = "hook --help" ]; then exit 0; fi
exit 1
`
	require.NoError(t, sb.Runtime().WriteFile("/home/testuser/bin/tap", []byte(script), 0o755))
}

func TestTap_Integrate_DryRunIsSideEffectFreeAndShowsExactCommands(t *testing.T) {
	t.Parallel()
	tap, sb := newIntegrateTap(t)
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex", DryRun: true, Plugins: []string{"tapper-dev"}})
	require.NoError(t, err)
	require.Contains(t, result.Root, filepath.FromSlash(".local/share/tapper/integrations/codex"))
	require.Len(t, result.Commands, 5)
	require.Equal(t, []string{"codex", "plugin", "marketplace", "add", result.Root}, result.Commands[2])
	require.Equal(t, []string{"codex", "plugin", "add", "tapper@tapper-local"}, result.Commands[3])
	require.Equal(t, []string{"codex", "plugin", "add", "tapper-dev@tapper-local"}, result.Commands[4])
	for _, target := range result.Paths {
		require.NotEqual(t, ".py", filepath.Ext(target), "dry-run must not advertise legacy Python hooks")
	}
	_, err = sb.Runtime().Stat(result.Root, false)
	require.Error(t, err)
	_, err = sb.ReadFile("calls")
	require.Error(t, err)
}

func TestTap_Integrate_CodexExtractsAndInvokesNativeCLI(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "codex")
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex", Plugins: []string{"tapper-dev"}})
	require.NoError(t, err)

	got, err := sb.Runtime().ReadFile(filepath.Join(result.Root, "tapper", ".codex-plugin", "plugin.json"))
	require.NoError(t, err)
	want, err := fs.ReadFile(integrations.IntegrationsFS, "rendered/codex/tapper/.codex-plugin/plugin.json")
	require.NoError(t, err)
	require.Equal(t, string(want), string(got))

	calls, err := sb.ReadFile("calls")
	require.NoError(t, err)
	text := string(calls)
	require.Contains(t, text, "plugin marketplace list --json")
	require.Contains(t, text, "plugin list --json")
	require.Contains(t, text, "plugin marketplace add "+result.Root)
	require.Contains(t, text, "plugin add tapper@tapper-local")
	require.Contains(t, text, "plugin add tapper-dev@tapper-local")
}

func TestTap_Integrate_ClaudeUsesInstallThenUpdateForExistingPlugin(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "claude")
	require.NoError(t, sb.Runtime().Env().Set("PLUGINS_JSON", `[{"id":"tapper@tapper-local","scope":"project"}]`))
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude", Scope: "project", Plugins: []string{"tapper-dev"}})
	require.NoError(t, err)
	calls, err := sb.ReadFile("calls")
	require.NoError(t, err)
	text := string(calls)
	require.Contains(t, text, "plugin update tapper@tapper-local --scope project")
	require.Contains(t, text, "plugin install tapper-dev@tapper-local --scope project")
	require.Contains(t, text, "plugin marketplace add "+result.Root+" --scope project")
	require.Contains(t, result.Root, filepath.FromSlash("integrations/claude"))
}

func TestTap_Integrate_RefreshRemovesStaleFilesAndReusesMarketplace(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "codex")
	first, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex"})
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().WriteFile(filepath.Join(first.Root, "stale.txt"), []byte("old"), 0o644))
	require.NoError(t, sb.Runtime().WriteFile(filepath.Join(first.Root, "tapper", "hooks", "orient-tapper.py"), []byte("legacy"), 0o644))
	require.NoError(t, sb.Runtime().WriteFile(filepath.Join(first.Root, "tapper", "hooks", "block-tap-cli.py"), []byte("legacy"), 0o644))
	require.NoError(t, sb.Runtime().Env().Set("MARKETPLACES_JSON", `{"marketplaces":[{"name":"tapper-local","root":"`+first.Root+`"}]}`))
	_, err = tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex"})
	require.NoError(t, err)
	_, err = sb.Runtime().Stat(filepath.Join(first.Root, "stale.txt"), false)
	require.Error(t, err)
	for _, legacy := range []string{"orient-tapper.py", "block-tap-cli.py"} {
		_, err = sb.Runtime().Stat(filepath.Join(first.Root, "tapper", "hooks", legacy), false)
		require.Error(t, err, "legacy hook %s must be removed by atomic refresh", legacy)
	}
	calls, err := sb.ReadFile("calls")
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(string(calls), "plugin marketplace add "+first.Root))
}

func TestTap_Integrate_MarketplaceConflictFailsBeforeExtraction(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "claude")
	require.NoError(t, sb.Runtime().Env().Set("MARKETPLACES_JSON", `[{"name":"tapper-local","source":"directory","path":"/elsewhere"}]`))
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude"})
	require.ErrorContains(t, err, "refusing to replace")
	require.Nil(t, result)
	dataDir := "/home/testuser/.local/share/tapper/integrations/claude"
	_, statErr := sb.Runtime().Stat(dataDir, false)
	require.Error(t, statErr)
}

func TestTap_Integrate_MissingHostCLIIsActionable(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex"})
	require.ErrorContains(t, err, "codex CLI not found on PATH")
}

func TestTap_Integrate_RejectsTapWithoutHookSupportBeforeExtraction(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "codex")
	require.NoError(t, sb.Runtime().WriteFile("/home/testuser/bin/tap", []byte("#!/bin/sh\nexit 1\n"), 0o755))

	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex"})
	require.Nil(t, result)
	require.ErrorContains(t, err, "does not support `tap hook`")
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/share/tapper/integrations/codex", false)
	require.Error(t, statErr)
	_, callErr := sb.ReadFile("calls")
	require.Error(t, callErr, "host commands must not run when tap is incompatible")
}

func TestTap_Integrate_MissingTapIsActionableAndDoesNotExtract(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	require.NoError(t, sb.Runtime().Env().Set("PATH", "/empty"))
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex"})
	require.ErrorContains(t, err, "current tap executable")
	require.ErrorContains(t, err, "install or upgrade tap")
	_, statErr := sb.Runtime().Stat("/home/testuser/.local/share/tapper/integrations/codex", false)
	require.Error(t, statErr)
}

func TestTap_Integrate_UnknownHostReturnsError(t *testing.T) {
	tap, _ := newIntegrateTap(t)
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "other"})
	require.ErrorContains(t, err, "unknown host")
}

func TestTap_Integrate_PluginsPreserveOrderAndDeduplicate(t *testing.T) {
	t.Parallel()
	tap, _ := newIntegrateTap(t)
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{
		Host: "codex", DryRun: true,
		Plugins: []string{"tapper-dev", "tapper", "tapper-dev"},
	})
	require.NoError(t, err)
	require.Len(t, result.Commands, 5)
	require.Equal(t, []string{"codex", "plugin", "add", "tapper@tapper-local"}, result.Commands[3])
	require.Equal(t, []string{"codex", "plugin", "add", "tapper-dev@tapper-local"}, result.Commands[4])
}

func TestTap_Integrate_UnknownPluginListsMarketplaceNames(t *testing.T) {
	t.Parallel()
	tap, _ := newIntegrateTap(t)
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude", DryRun: true, Plugins: []string{"missing"}})
	require.ErrorContains(t, err, `unknown claude plugin "missing"`)
	require.ErrorContains(t, err, "available plugins: tapper, tapper-dev")
}

func TestTap_Integrate_CodexRejectsUnsupportedScopeWithoutSideEffects(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		t.Run(fmt.Sprintf("dry_run_%t", dryRun), func(t *testing.T) {
			tap, sb := newIntegrateTap(t)
			installFakeHost(t, sb, "codex")
			_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "codex", Scope: "local", DryRun: dryRun})
			require.ErrorContains(t, err, "Codex currently supports only --scope user")
			_, err = sb.ReadFile("calls")
			require.Error(t, err)
			_, err = sb.Runtime().Stat("/home/testuser/.local/share/tapper/integrations/codex", false)
			require.Error(t, err)
		})
	}
}

func TestTap_Integrate_ClaudeMatchesInstalledPluginByScope(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "claude")
	require.NoError(t, sb.Runtime().Env().Set("PLUGINS_JSON", `[{"id":"tapper@tapper-local","scope":"user"},{"id":"tapper-dev@tapper-local","scope":"local"}]`))
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude", Scope: "local", Plugins: []string{"tapper-dev"}})
	require.NoError(t, err)
	calls, err := sb.ReadFile("calls")
	require.NoError(t, err)
	require.Contains(t, string(calls), "plugin install tapper@tapper-local --scope local")
	require.Contains(t, string(calls), "plugin update tapper-dev@tapper-local --scope local")
}

func TestTap_Integrate_ClaudeDefaultScopeIsUser(t *testing.T) {
	t.Parallel()
	tap, _ := newIntegrateTap(t)
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude", DryRun: true})
	require.NoError(t, err)
	require.Equal(t, []string{"claude", "plugin", "marketplace", "add", result.Root, "--scope", "user"}, result.Commands[2])
	require.Equal(t, []string{"claude", "plugin", "install", "tapper@tapper-local", "--scope", "user"}, result.Commands[3])
}

func TestTap_Integrate_ClaudeMarketplaceRegistrationIsScopeSpecific(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeHost(t, sb, "claude")
	root := "/home/testuser/.local/share/tapper/integrations/claude"
	require.NoError(t, sb.Runtime().Env().Set("MARKETPLACES_JSON", `[{"name":"tapper-local","path":"`+root+`","scope":"user"}]`))
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "claude", Scope: "local"})
	require.NoError(t, err)
	calls, err := sb.ReadFile("calls")
	require.NoError(t, err)
	require.Contains(t, string(calls), "plugin marketplace add "+root+" --scope local")
}

func TestTap_IntegrateHosts_IsSortedAndContainsDefaults(t *testing.T) {
	hosts := tapper.IntegrateHosts()
	require.Equal(t, []string{"claude", "codex"}, hosts)
}
