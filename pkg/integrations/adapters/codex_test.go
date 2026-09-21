package adapters

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"
)

func testContentFS(t *testing.T) fs.FS {
	t.Helper()
	files := fstest.MapFS{}
	for _, name := range baselineOrder {
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "integrations", "content", name))
		if err != nil {
			t.Fatal(err)
		}
		files[name] = &fstest.MapFile{Data: body}
	}
	workflow, err := os.ReadFile(filepath.Join("..", "renderdata", "developer", "workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	files["developer/workflow.md"] = &fstest.MapFile{Data: workflow}
	codexHooks, err := os.ReadFile(filepath.Join("..", "renderdata", "codex", "hooks", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	files["codex/hooks/hooks.json"] = &fstest.MapFile{Data: codexHooks}
	guardHooks, err := os.ReadFile(filepath.Join("..", "renderdata", "guard", "hooks.json"))
	if err != nil {
		t.Fatal(err)
	}
	files["guard/hooks.json"] = &fstest.MapFile{Data: guardHooks}
	return files
}

func testRuntime(t *testing.T) *toolkit.Runtime {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	return sb.Runtime()
}

func TestCodexAdapter_RendersNativeMarketplaceAndThreePlugins(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"codex/.agents/plugins/marketplace.json",
		"codex/tapper/.codex-plugin/plugin.json",
		"codex/tapper/.mcp.json",
		"codex/tapper/hooks/hooks.json",
		"codex/tapper/skills/tapper/SKILL.md",
		"codex/tapper-guard/.codex-plugin/plugin.json",
		"codex/tapper-guard/hooks/hooks.json",
		"codex/tapper-dev/.codex-plugin/plugin.json",
		"codex/tapper-dev/skills/tapper-dev/SKILL.md",
	}
	if len(mem.Paths()) != len(want) {
		t.Fatalf("rendered files = %v, want exactly %v", mem.Paths(), want)
	}
	for _, name := range want {
		if _, ok := mem.Files()[name]; !ok {
			t.Errorf("missing %s; got %v", name, mem.Paths())
		}
	}
	for _, name := range mem.Paths() {
		if strings.HasSuffix(name, ".py") {
			t.Errorf("rendered plugin must not package Python hook %s", name)
		}
	}

	var marketplace struct {
		Name    string `json:"name"`
		Plugins []struct {
			Name   string `json:"name"`
			Source struct {
				Path string `json:"path"`
			} `json:"source"`
			Policy   map[string]string `json:"policy"`
			Category string            `json:"category"`
		} `json:"plugins"`
	}
	if err := json.Unmarshal(mem.Files()[want[0]], &marketplace); err != nil {
		t.Fatal(err)
	}
	if marketplace.Name != marketplaceName || len(marketplace.Plugins) != 3 {
		t.Fatalf("unexpected marketplace: %+v", marketplace)
	}
	for _, plugin := range marketplace.Plugins {
		if plugin.Source.Path != "./"+plugin.Name || plugin.Policy["installation"] == "" || plugin.Policy["authentication"] == "" || plugin.Category == "" {
			t.Errorf("incomplete marketplace entry: %+v", plugin)
		}
	}

	var mcp struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
			EnvVars []string `json:"env_vars"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(mem.Files()["codex/tapper/.mcp.json"], &mcp); err != nil {
		t.Fatal(err)
	}
	tapperMCP, ok := mcp.MCPServers["tapper"]
	if !ok {
		t.Fatalf("missing tapper MCP config: %+v", mcp)
	}
	if tapperMCP.Command != "tap" || strings.Join(tapperMCP.Args, " ") != "mcp" {
		t.Errorf("unexpected tapper MCP command: %+v", tapperMCP)
	}
	// HOME must be forwarded alongside the XDG roots: tap falls back to it when a
	// root is unset and when expanding "~", so without it tap mcp fails to
	// authenticate under Codex while the same tap works in the shell. TAP_AGENT
	// carries model/telemetry identity and TAP_FLIGHT carries the pinned root.
	wantEnvVars := "HOME,TAP_AGENT,TAP_FLIGHT,XDG_CONFIG_HOME,XDG_DATA_HOME,XDG_STATE_HOME,XDG_CACHE_HOME"
	if got := strings.Join(tapperMCP.EnvVars, ","); got != wantEnvVars {
		t.Errorf("tapper MCP env_vars = %q, want %q", got, wantEnvVars)
	}
}

func TestCodexAdapter_RendersPreToolUseGuardrailInSeparateGuardPlugin(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}

	var hooks struct {
		Hooks map[string][]struct {
			Matcher string `json:"matcher"`
			Hooks   []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(mem.Files()["codex/tapper-guard/hooks/hooks.json"], &hooks); err != nil {
		t.Fatal(err)
	}
	pre := hooks.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != "^(Bash|Write|Edit|MultiEdit|NotebookEdit|Shell|exec_command|apply_patch|write_file|edit_file|delete_file|move_file|rename_file)$" || len(pre[0].Hooks) != 1 {
		t.Fatalf("Codex PreToolUse hook = %+v", pre)
	}
	hook := pre[0].Hooks[0]
	if hook.Type != "command" || hook.Command != "tap hook pre-tool-use" {
		t.Fatalf("Codex command hook = %+v", hook)
	}
	if strings.Contains(string(mem.Files()["codex/tapper-guard/hooks/hooks.json"]), "PLUGIN_ROOT") {
		t.Fatal("Codex hooks must not reference packaged plugin-root scripts")
	}
	// The guard is separately installable, so the baseline must not carry a
	// second copy of the same PreToolUse hook.
	var baselineHooks struct {
		Hooks map[string][]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(mem.Files()["codex/tapper/hooks/hooks.json"], &baselineHooks); err != nil {
		t.Fatal(err)
	}
	if len(baselineHooks.Hooks["PreToolUse"]) != 0 {
		t.Fatal("baseline Codex plugin must not ship the PreToolUse guard")
	}
	if strings.Contains(string(mem.Files()["codex/tapper-guard/.codex-plugin/plugin.json"]), `"skills"`) {
		t.Fatal("guard plugin ships hooks only and must declare no skills")
	}
	if strings.Contains(string(mem.Files()["codex/tapper-guard/.codex-plugin/plugin.json"]), "mcpServers") {
		t.Fatal("guard plugin must not register MCP")
	}
	if _, ok := hooks.Hooks["UserPromptExpansion"]; ok {
		t.Fatal("Claude-only prompt expansion hooks must not leak into Codex")
	}
	if strings.Contains(string(mem.Files()["codex/tapper/.codex-plugin/plugin.json"]), `"hooks"`) {
		t.Fatal("Codex discovers hooks/hooks.json without a manifest entry")
	}
}

func TestCodexAdapter_RendersSessionStartOrientationReminder(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}

	type commandHook struct {
		Type          string `json:"type"`
		Command       string `json:"command"`
		Timeout       int    `json:"timeout"`
		StatusMessage string `json:"statusMessage"`
	}
	var hooks struct {
		Hooks map[string][]struct {
			Matcher string        `json:"matcher"`
			Hooks   []commandHook `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(mem.Files()["codex/tapper/hooks/hooks.json"], &hooks); err != nil {
		t.Fatal(err)
	}
	starts := hooks.Hooks["SessionStart"]
	if len(starts) != 1 || starts[0].Matcher != "startup|resume|clear|compact" || len(starts[0].Hooks) != 1 {
		t.Fatalf("Codex SessionStart hook = %+v", starts)
	}
	hook := starts[0].Hooks[0]
	if hook.Type != "command" || hook.Timeout != 5 || hook.StatusMessage == "" || hook.Command != "tap hook session-start" {
		t.Fatalf("Codex orientation command hook = %+v", hook)
	}
	for _, duplicate := range []string{"SubagentStart", "PreCompact", "PostCompact"} {
		if _, ok := hooks.Hooks[duplicate]; ok {
			t.Errorf("Codex must not register duplicate lifecycle hook %s", duplicate)
		}
	}
	if len(hooks.Hooks["PreToolUse"]) != 0 {
		t.Fatal("the PreToolUse guard belongs to tapper-guard, not the baseline plugin")
	}
}

func TestCodexAdapter_SeparatesBaselineAndDeveloperWorkflow(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	baseline := string(mem.Files()["codex/tapper/skills/tapper/SKILL.md"])
	dev := string(mem.Files()["codex/tapper-dev/skills/tapper-dev/SKILL.md"])
	if !strings.Contains(baseline, "mcp__tapper__orient") || !strings.Contains(baseline, "Secret handling") {
		t.Fatalf("baseline lacks orientation or safety: %s", baseline)
	}
	for _, want := range []string{
		"`[title](../NODEID)`",
		"`[title](keg:ALIAS/NODEID)`",
		"`[title](keg:@NAMESPACE/ALIAS/NODEID)`",
		"A bare `keg:` reference in node prose is plain text",
	} {
		if !strings.Contains(baseline, want) {
			t.Errorf("baseline link guidance missing %q", want)
		}
	}
	for _, lifecycle := range []string{"## Plan", "## Code", "## Review", "## Commit"} {
		if strings.Contains(baseline, lifecycle) {
			t.Errorf("baseline leaked %s", lifecycle)
		}
		if !strings.Contains(dev, lifecycle) {
			t.Errorf("developer workflow missing %s", lifecycle)
		}
	}
	if !strings.Contains(dev, "baseline `tapper` plugin is required") || strings.Contains(string(mem.Files()["codex/tapper-dev/.codex-plugin/plugin.json"]), "mcpServers") {
		t.Errorf("Codex prerequisite/MCP separation is wrong")
	}
	for _, want := range []string{
		"recompute knowledge discovery",
		"`mcp__tapper__backlinks`",
		"`mcp__tapper__links`",
		"`mcp__tapper__grep`",
		"active or stale interfaces and verifications",
		"Each needs a surviving subject or consumer",
		"word `legacy`",
	} {
		if !strings.Contains(dev, want) {
			t.Errorf("developer review workflow missing %q", want)
		}
	}
	if strings.Contains(dev, "@foldwise/dev") {
		t.Error("developer review workflow must not hardcode a project-specific KEG")
	}
}

// Every rendered manifest carries the placeholder, and no environment variable
// can change it. A render that reads the environment is not reproducible, and
// an irreproducible render is what made the pre-commit hook fight every
// commit; the real version is stamped at install time instead.
func TestRenderedManifestsCarryPlaceholderVersion(t *testing.T) {
	rt := testRuntime(t)
	if err := rt.Env().Set("TAPPER_PLUGIN_VERSION", "v0.31.0"); err != nil {
		t.Fatal(err)
	}
	mem := integrations.NewMemWriter()
	for _, adapter := range []integrations.Adapter{ClaudeAdapter{}, CodexAdapter{}} {
		if err := adapter.Render(rt, testContentFS(t), mem); err != nil {
			t.Fatal(err)
		}
	}
	manifests := 0
	for name, body := range mem.Files() {
		if !strings.HasSuffix(name, "plugin.json") {
			continue
		}
		var manifest struct {
			Version string `json:"version"`
		}
		if err := json.Unmarshal(body, &manifest); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if manifest.Version != pluginVersionPlaceholder {
			t.Errorf("%s version = %q, want the placeholder %q", name, manifest.Version, pluginVersionPlaceholder)
		}
		manifests++
	}
	if manifests == 0 {
		t.Fatal("no plugin manifests rendered")
	}
}
