package tapper_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/tapper"
)

const opencodeUserConfig = "/home/testuser/.config/opencode/opencode.json"
const opencodeUserSkills = "/home/testuser/.config/opencode/skills"
const opencodeUserGuard = "/home/testuser/.config/opencode/plugin/tapper-guard.ts"

// readOpenCodeConfig decodes a written opencode.json for assertions.
func readOpenCodeConfig(t *testing.T, tap *tapper.Tap, path string) map[string]any {
	t.Helper()
	body, err := tap.Runtime.ReadFile(path)
	require.NoError(t, err)
	var out map[string]any
	require.NoError(t, json.Unmarshal(body, &out))
	return out
}

// tapperServer returns the mcp.tapper entry, failing if it is absent.
func tapperServer(t *testing.T, config map[string]any) map[string]any {
	t.Helper()
	mcp, ok := config["mcp"].(map[string]any)
	require.True(t, ok, "config has no mcp block: %v", config)
	server, ok := mcp["tapper"].(map[string]any)
	require.True(t, ok, "mcp block has no tapper server: %v", mcp)
	return server
}

func TestTap_Integrate_OpenCodeUserScopeWritesConfigAndSkills(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{
		Host: "opencode", Plugins: []string{"tapper-dev"},
	})
	require.NoError(t, err)

	server := tapperServer(t, readOpenCodeConfig(t, tap, opencodeUserConfig))
	require.Equal(t, "local", server["type"])
	require.Equal(t, []any{"tap", "mcp"}, server["command"])
	require.Equal(t, true, server["enabled"])

	for _, name := range []string{"tapper", "tapper-dev"} {
		skill, err := tap.Runtime.ReadFile(filepath.Join(opencodeUserSkills, name, "SKILL.md"))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(skill), "---\nname: "+name+"\n"),
			"skill %s is missing its frontmatter", name)
	}

	// The guard ships a plugin module and no skill, so it lands in opencode's
	// auto-load plugin directory instead of the skills tree.
	guard, err := tap.Runtime.ReadFile(opencodeUserGuard)
	require.NoError(t, err)
	require.Contains(t, string(guard), "tool.execute.before")
	require.Contains(t, string(guard), "tap hook pre-tool-use")
	_, err = tap.Runtime.Stat(filepath.Join(opencodeUserSkills, "tapper-guard"), false)
	require.Error(t, err, "the guard has no skill to install")
}

// The opencode install needs no host CLI: there is none to drive. Without the
// guard it needs no `tap hook` support either, so requiring one would reject a
// working setup for no gain.
func TestTap_Integrate_OpenCodeNeedsNoHostCLIOrHookSupportWithoutGuard(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	require.NoError(t, sb.Runtime().Env().Set("PATH", "/home/testuser/empty-bin"))
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode", NoSafety: true})
	require.NoError(t, err)
	_, err = tap.Runtime.ReadFile(opencodeUserConfig)
	require.NoError(t, err)
}

// With the guard the calculus inverts. The opencode guard fails closed, so
// installing it against a tap that cannot answer would block every tool call
// in the session rather than degrade quietly.
func TestTap_Integrate_OpenCodeGuardRequiresHookCapableTap(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	require.NoError(t, sb.Runtime().Env().Set("PATH", "/home/testuser/empty-bin"))
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode"})
	require.ErrorContains(t, err, "`tap hook`")
	_, err = tap.Runtime.Stat(opencodeUserConfig, false)
	require.Error(t, err, "the refusal must land before anything is written")
}

// opencode has no command to disable a plugin, and tap owns the exact file it
// wrote, so --no-safety has to remove it. Skipping the install alone would
// leave the guard enforcing with no off switch.
func TestTap_Integrate_OpenCodeNoSafetyRemovesInstalledGuard(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode"})
	require.NoError(t, err)
	_, err = tap.Runtime.ReadFile(opencodeUserGuard)
	require.NoError(t, err)

	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode", DryRun: true, NoSafety: true})
	require.NoError(t, err)
	require.Contains(t, strings.Join(result.Steps, "\n"), "remove "+filepath.FromSlash(opencodeUserGuard))

	_, err = tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode", NoSafety: true})
	require.NoError(t, err)
	_, err = tap.Runtime.Stat(opencodeUserGuard, false)
	require.Error(t, err, "--no-safety must remove the guard tap installed")

	// The rest of the install survives the removal.
	tapperServer(t, readOpenCodeConfig(t, tap, opencodeUserConfig))
}

func TestTap_Integrate_OpenCodeProjectScopeWritesProjectPaths(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	result, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{
		Host: "opencode", Scope: "project", DryRun: true,
	})
	require.NoError(t, err)
	require.Empty(t, result.Commands, "opencode is not driven through a host CLI")
	require.NotEmpty(t, result.Steps)

	projectRoot := filepath.Dir(tap.PathService.Project())
	require.Contains(t, result.Steps, filepath.Join(projectRoot, ".opencode", "skills", "tapper")+string(filepath.Separator))
	require.Contains(t, result.Steps[len(result.Steps)-1], filepath.Join(projectRoot, "opencode.json"))

	// Dry run wrote nothing.
	_, err = tap.Runtime.Stat(filepath.Join(projectRoot, "opencode.json"), false)
	require.Error(t, err)
	_, err = tap.Runtime.Stat(result.Root, false)
	require.Error(t, err)

	_, err = tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode", Scope: "project"})
	require.NoError(t, err)
	tapperServer(t, readOpenCodeConfig(t, tap, filepath.Join(projectRoot, "opencode.json")))
	_, err = tap.Runtime.ReadFile(filepath.Join(projectRoot, ".opencode", "skills", "tapper", "SKILL.md"))
	require.NoError(t, err)
}

func TestTap_Integrate_OpenCodePreservesUnrelatedConfig(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	existing := `{
  "$schema": "https://opencode.ai/config.json",
  "theme": "tokyonight",
  "model": "anthropic/claude-opus-4",
  "mcp": {
    "context7": { "type": "remote", "url": "https://mcp.context7.com/mcp" }
  }
}`
	require.NoError(t, tap.Runtime.WriteFile(opencodeUserConfig, []byte(existing), 0o644))

	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode"})
	require.NoError(t, err)

	config := readOpenCodeConfig(t, tap, opencodeUserConfig)
	require.Equal(t, "tokyonight", config["theme"])
	require.Equal(t, "anthropic/claude-opus-4", config["model"])
	mcp := config["mcp"].(map[string]any)
	require.Contains(t, mcp, "context7", "an unrelated MCP server must survive the merge")
	tapperServer(t, config)
}

// A second run must land on exactly the same state as the first: the install is
// how a user upgrades, and an upgrade that duplicates or drifts is a bug.
func TestTap_Integrate_OpenCodeIsIdempotent(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	opts := tapper.IntegrateOptions{Host: "opencode"}
	_, err := tap.Integrate(context.Background(), opts)
	require.NoError(t, err)
	first, err := tap.Runtime.ReadFile(opencodeUserConfig)
	require.NoError(t, err)

	_, err = tap.Integrate(context.Background(), opts)
	require.NoError(t, err)
	second, err := tap.Runtime.ReadFile(opencodeUserConfig)
	require.NoError(t, err)
	require.Equal(t, string(first), string(second))
}

func TestTap_Integrate_OpenCodeRefusesForeignTapperServer(t *testing.T) {
	tap, sb := newIntegrateTap(t)
	installFakeTap(t, sb)
	existing := `{"mcp":{"tapper":{"type":"local","command":["my-wrapper","mcp"]}}}`
	require.NoError(t, tap.Runtime.WriteFile(opencodeUserConfig, []byte(existing), 0o644))

	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode"})
	require.ErrorContains(t, err, "refusing to replace it")

	// The refusal happens before any skill is written, so nothing is left half done.
	body, err := tap.Runtime.ReadFile(opencodeUserConfig)
	require.NoError(t, err)
	require.Equal(t, existing, string(body))
	_, err = tap.Runtime.Stat(filepath.Join(opencodeUserSkills, "tapper"), false)
	require.Error(t, err)
}

func TestTap_Integrate_OpenCodeRefusesJSONCConfig(t *testing.T) {
	tap, _ := newIntegrateTap(t)
	jsonc := "/home/testuser/.config/opencode/opencode.jsonc"
	require.NoError(t, tap.Runtime.WriteFile(jsonc, []byte("// mine\n{}\n"), 0o644))

	_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{Host: "opencode"})
	require.ErrorContains(t, err, "without discarding its comments")

	_, err = tap.Runtime.Stat(opencodeUserConfig, false)
	require.Error(t, err, "tap must not shadow the .jsonc with a .json")
}

func TestTap_Integrate_OpenCodeRejectsLocalScopeWithoutSideEffects(t *testing.T) {
	for _, dryRun := range []bool{false, true} {
		tap, _ := newIntegrateTap(t)
		_, err := tap.Integrate(context.Background(), tapper.IntegrateOptions{
			Host: "opencode", Scope: "local", DryRun: dryRun,
		})
		require.ErrorContains(t, err, `opencode has no "local" scope`)
		_, err = tap.Runtime.Stat(opencodeUserConfig, false)
		require.Error(t, err)
	}
}

func TestTap_IntegrateScopes_AreHostSpecific(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"user", "project", "local"}, tapper.IntegrateScopes("claude"))
	require.Equal(t, []string{"user"}, tapper.IntegrateScopes("codex"))
	require.Equal(t, []string{"user", "project"}, tapper.IntegrateScopes("opencode"))
	require.Nil(t, tapper.IntegrateScopes("nope"))
}
