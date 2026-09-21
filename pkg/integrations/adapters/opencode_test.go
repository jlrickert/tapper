package adapters

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/integrations"
)

func TestOpenCodeAdapter_RendersSkillsAndConfigFragment(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (OpenCodeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"opencode/.tapper/marketplace.json",
		"opencode/tapper/.tapper/plugin.json",
		"opencode/tapper/opencode.json",
		"opencode/tapper/skills/tapper/SKILL.md",
		"opencode/tapper-guard/.tapper/plugin.json",
		"opencode/tapper-guard/plugin/tapper-guard.ts",
		"opencode/tapper-dev/.tapper/plugin.json",
		"opencode/tapper-dev/skills/tapper-dev/SKILL.md",
	}
	if len(mem.Paths()) != len(want) {
		t.Fatalf("rendered files = %v, want exactly %v", mem.Paths(), want)
	}
	for _, name := range want {
		if _, ok := mem.Files()[name]; !ok {
			t.Errorf("missing %s; got %v", name, mem.Paths())
		}
	}

	// opencode has no hook format that can run the JSON command table the other
	// hosts consume, so shipping one would only look like it worked. Its guard
	// is a plugin module instead, asserted below.
	for _, name := range mem.Paths() {
		if bytes.Contains([]byte(name), []byte("hooks")) {
			t.Errorf("opencode plugin must not package hooks: %s", name)
		}
	}

	var fragment struct {
		MCP map[string]struct {
			Type    string   `json:"type"`
			Command []string `json:"command"`
			Enabled bool     `json:"enabled"`
		} `json:"mcp"`
	}
	if err := json.Unmarshal(mem.Files()["opencode/tapper/opencode.json"], &fragment); err != nil {
		t.Fatal(err)
	}
	server, ok := fragment.MCP["tapper"]
	if !ok {
		t.Fatalf("fragment has no mcp.tapper: %s", mem.Files()["opencode/tapper/opencode.json"])
	}
	if server.Type != "local" || !server.Enabled {
		t.Errorf("mcp.tapper = %+v, want an enabled local server", server)
	}
	if len(server.Command) != 2 || server.Command[0] != "tap" || server.Command[1] != "mcp" {
		t.Errorf("command = %v, want [tap mcp]", server.Command)
	}

	var manifest struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := json.Unmarshal(mem.Files()["opencode/tapper-dev/.tapper/plugin.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Dependencies) != 1 || manifest.Dependencies[0] != "tapper" {
		t.Fatalf("dependencies = %v", manifest.Dependencies)
	}
}

// The baseline guidance is the same guidance whichever host reads it: it is
// assembled from one set of canonical sections, and a host-specific drift in it
// would be a divergence nobody asked for.
func TestOpenCodeAdapter_BaselineSkillMatchesClaude(t *testing.T) {
	opencode := integrations.NewMemWriter()
	if err := (OpenCodeAdapter{}).Render(testRuntime(t), testContentFS(t), opencode); err != nil {
		t.Fatal(err)
	}
	claude := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(testRuntime(t), testContentFS(t), claude); err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"opencode/tapper/skills/tapper/SKILL.md", "claude/tapper/skills/tapper/SKILL.md"},
		{"opencode/tapper-dev/skills/tapper-dev/SKILL.md", "claude/tapper-dev/skills/tapper-dev/SKILL.md"},
	} {
		if !bytes.Equal(opencode.Files()[pair[0]], claude.Files()[pair[1]]) {
			t.Errorf("%s differs from %s", pair[0], pair[1])
		}
	}
}

// The opencode guard reaches the same `tap hook pre-tool-use` binary Claude and
// Codex run from their hook tables, so one implementation of the policy serves
// all three hosts.
func TestOpenCodeAdapter_GuardPluginCallsSharedHookBinary(t *testing.T) {
	mem := integrations.NewMemWriter()
	if err := (OpenCodeAdapter{}).Render(testRuntime(t), testContentFS(t), mem); err != nil {
		t.Fatal(err)
	}
	guard := string(mem.Files()["opencode/tapper-guard/plugin/tapper-guard.ts"])
	for _, want := range []string{
		"tool.execute.before",
		"tap hook pre-tool-use",
		"hook_event_name",
		"permissionDecision",
		"throw new Error",
	} {
		if !strings.Contains(guard, want) {
			t.Errorf("guard plugin missing %q", want)
		}
	}
	// opencode resolves plugin imports against a package.json in its config
	// directory. A guard that fails to load because a dependency is absent is
	// a guard that silently stops guarding, so it must import nothing.
	if strings.Contains(guard, "import ") || strings.Contains(guard, "require(") {
		t.Error("guard plugin must be self-contained: no imports")
	}

	var manifest struct {
		Dependencies []string `json:"dependencies"`
	}
	if err := json.Unmarshal(mem.Files()["opencode/tapper-guard/.tapper/plugin.json"], &manifest); err != nil {
		t.Fatal(err)
	}
	if len(manifest.Dependencies) != 1 || manifest.Dependencies[0] != "tapper" {
		t.Fatalf("guard dependencies = %v", manifest.Dependencies)
	}
}
