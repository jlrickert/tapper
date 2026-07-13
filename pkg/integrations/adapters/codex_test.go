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
	workflow, err := os.ReadFile(filepath.Join("..", "renderdata", "developer", "workflow.md"))
	if err != nil {
		t.Fatal(err)
	}
	files["developer/workflow.md"] = &fstest.MapFile{Data: workflow}
	for _, name := range []string{"block-tap-cli.py", "hooks.json"} {
		body, err := os.ReadFile(filepath.Join("..", "renderdata", "claude", "hooks", name))
		if err != nil {
			t.Fatal(err)
		}
		files["claude/hooks/"+name] = &fstest.MapFile{Data: body}
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
		"codex/tapper/skills/tapper/SKILL.md",
		"codex/tapper/skills/tapper-flight-switch/SKILL.md",
		"codex/tapper/skills/tapper-mcp-reset/SKILL.md",
		"codex/tapper-dev/.codex-plugin/plugin.json",
		"codex/tapper-dev/skills/tapper-dev/SKILL.md",
	}
	for _, name := range want {
		if _, ok := mem.Files()[name]; !ok {
			t.Errorf("missing %s; got %v", name, mem.Paths())
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
}

func TestCodexAdapter_RendersManagementSkills(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	reset := string(mem.Files()["codex/tapper/skills/tapper-mcp-reset/SKILL.md"])
	switcher := string(mem.Files()["codex/tapper/skills/tapper-flight-switch/SKILL.md"])
	for _, want := range []string{"tap version", "mcp__tapper__info", "mcp__tapper__orient", "new thread", "restart the Codex app", "Never kill"} {
		if !strings.Contains(reset, want) {
			t.Errorf("reset skill missing %q", want)
		}
	}
	for _, want := range []string{"explicitly asks", "mcp__tapper__flight_show", "every subsequent Tapper MCP", ".tapper/config.yaml"} {
		if !strings.Contains(switcher, want) {
			t.Errorf("flight switch skill missing %q", want)
		}
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
