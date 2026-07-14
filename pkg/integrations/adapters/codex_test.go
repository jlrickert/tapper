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
	for _, skill := range managementSkills {
		name := filepath.Join("skills", skill.Name+".md")
		body, err := os.ReadFile(filepath.Join("..", "..", "..", "integrations", "content", name))
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.ToSlash(name)] = &fstest.MapFile{Data: body}
	}
	flightSwitch, err := os.ReadFile(filepath.Join("..", "..", "..", "integrations", "content", "skills", "tapper-flight-switch.md"))
	if err != nil {
		t.Fatal(err)
	}
	files["skills/tapper-flight-switch.md"] = &fstest.MapFile{Data: flightSwitch}
	workflow, err := os.ReadFile(filepath.Join("..", "renderdata", "developer", "workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	files["developer/workflow.md"] = &fstest.MapFile{Data: workflow}
	for _, host := range []string{"claude", "codex"} {
		body, err := os.ReadFile(filepath.Join("..", "renderdata", host, "hooks", "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		files[host+"/hooks/hooks.json"] = &fstest.MapFile{Data: body}
	}
	return files
}

func testRuntime(t *testing.T) *toolkit.Runtime {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	return sb.Runtime()
}

func TestCodexAdapter_RendersNativeMarketplaceAndTwoPlugins(t *testing.T) {
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
		"codex/tapper/skills/tapper-mcp-reset/SKILL.md",
		"codex/tapper-dev/.codex-plugin/plugin.json",
		"codex/tapper-dev/skills/tapper-dev/SKILL.md",
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
	if marketplace.Name != marketplaceName || len(marketplace.Plugins) != 2 {
		t.Fatalf("unexpected marketplace: %+v", marketplace)
	}
	for _, plugin := range marketplace.Plugins {
		if plugin.Source.Path != "./"+plugin.Name || plugin.Policy["installation"] == "" || plugin.Policy["authentication"] == "" || plugin.Category == "" {
			t.Errorf("incomplete marketplace entry: %+v", plugin)
		}
	}

	var mcp map[string]struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
		EnvVars []string `json:"env_vars"`
	}
	if err := json.Unmarshal(mem.Files()["codex/tapper/.mcp.json"], &mcp); err != nil {
		t.Fatal(err)
	}
	tapperMCP, ok := mcp["tapper"]
	if !ok {
		t.Fatalf("missing tapper MCP config: %+v", mcp)
	}
	if tapperMCP.Command != "tap" || strings.Join(tapperMCP.Args, " ") != "mcp" {
		t.Errorf("unexpected tapper MCP command: %+v", tapperMCP)
	}
	wantEnvVars := "XDG_CONFIG_HOME,XDG_DATA_HOME,XDG_STATE_HOME,XDG_CACHE_HOME"
	if got := strings.Join(tapperMCP.EnvVars, ","); got != wantEnvVars {
		t.Errorf("tapper MCP env_vars = %q, want %q", got, wantEnvVars)
	}
}

func TestCodexAdapter_RendersPreToolUseGuardrailWithoutClaudeExpansion(t *testing.T) {
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
	if err := json.Unmarshal(mem.Files()["codex/tapper/hooks/hooks.json"], &hooks); err != nil {
		t.Fatal(err)
	}
	pre := hooks.Hooks["PreToolUse"]
	if len(pre) != 1 || pre[0].Matcher != "Bash" || len(pre[0].Hooks) != 1 {
		t.Fatalf("Codex PreToolUse hook = %+v", pre)
	}
	hook := pre[0].Hooks[0]
	if hook.Type != "command" || hook.Command != "tap hook pre-tool-use" {
		t.Fatalf("Codex command hook = %+v", hook)
	}
	if strings.Contains(string(mem.Files()["codex/tapper/hooks/hooks.json"]), "PLUGIN_ROOT") {
		t.Fatal("Codex hooks must not reference packaged plugin-root scripts")
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
	if len(hooks.Hooks["PreToolUse"]) != 1 {
		t.Fatal("Codex orientation reminder must retain the PreToolUse guard")
	}
}

func TestCodexAdapter_RendersManagementSkills(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	reset := string(mem.Files()["codex/tapper/skills/tapper-mcp-reset/SKILL.md"])
	for _, want := range []string{"tap version", "mcp__tapper__info", "mcp__tapper__orient", "new thread", "restart the Codex app", "Never kill"} {
		if !strings.Contains(reset, want) {
			t.Errorf("reset skill missing %q", want)
		}
	}
	if _, ok := mem.Files()["codex/tapper/skills/tapper-flight-switch/SKILL.md"]; ok {
		t.Error("Codex must not expose a flight-switching skill")
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
}

func TestPluginVersionNormalizesReleaseTag(t *testing.T) {
	rt := testRuntime(t)
	if err := rt.Env().Set(pluginVersionEnv, "v0.31.0"); err != nil {
		t.Fatal(err)
	}
	if got := pluginVersion(rt); got != "0.31.0" {
		t.Fatalf("version = %q", got)
	}
}
