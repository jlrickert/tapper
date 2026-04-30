package adapters

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// claudeContentFS layers an in-memory hook fixture over the on-disk
// canonical markdown fixtures. The Claude adapter expects the canonical
// markdown bodies at the root of the content FS and the hook bytes under
// "claude/hooks/...", which is exactly the shape cmd/render-integrations
// constructs at runtime by overlaying renderdata.FS onto
// integrations/content. Building the same shape here keeps the adapter
// test self-contained — it does not import renderdata, so the test does
// not pull the canonical-source bytes into the adapter package.
func claudeContentFS(t *testing.T) fs.FS {
	t.Helper()
	hookPy, err := os.ReadFile(filepath.FromSlash("testdata/claude/hooks/block-tap-cli.py"))
	if err != nil {
		t.Fatalf("read hook fixture block-tap-cli.py: %v", err)
	}
	hookJSON, err := os.ReadFile(filepath.FromSlash("testdata/claude/hooks/hooks.json"))
	if err != nil {
		t.Fatalf("read hook fixture hooks.json: %v", err)
	}
	hookOverlay := fstest.MapFS{
		"claude/hooks/block-tap-cli.py": &fstest.MapFile{Data: hookPy},
		"claude/hooks/hooks.json":       &fstest.MapFile{Data: hookJSON},
	}
	return overlayFS{
		primary:   os.DirFS("testdata/canonical"),
		secondary: hookOverlay,
	}
}

// overlayFS mirrors the merged-FS shape used by cmd/render-integrations
// without taking a dependency on it. Secondary takes precedence on
// overlap; missing paths fall through to primary.
type overlayFS struct {
	primary, secondary fs.FS
}

func (o overlayFS) Open(name string) (fs.File, error) {
	f, err := o.secondary.Open(name)
	if err == nil {
		return f, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	return o.primary.Open(name)
}

// newTestRuntime builds a sandbox-backed runtime so adapter tests stay
// insulated from the process environment. The Claude adapter consults
// rt.Env().Get for release-time configuration; a sandbox runtime keeps
// that lookup predictable across parallel tests.
func newTestRuntime(t *testing.T) *toolkit.Runtime {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	return sb.Runtime()
}

// TestClaudeAdapter_GoldenByteParity is the load-bearing test for Phase 1:
// running ClaudeAdapter against the canonical fixtures in testdata/canonical
// must produce byte-identical copies of the golden plugin tree in
// testdata/claude/. If this test drifts, either the canonical content has
// changed or the adapter has — both must be reviewed deliberately.
func TestClaudeAdapter_GoldenByteParity(t *testing.T) {
	rt := newTestRuntime(t)
	content := claudeContentFS(t)
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(rt, content, mem); err != nil {
		t.Fatalf("Render: %v", err)
	}

	cases := []struct {
		rendered string
		golden   string
	}{
		{
			rendered: "claude/.claude-plugin/plugin.json",
			golden:   "testdata/claude/.claude-plugin/plugin.json",
		},
		{
			rendered: "claude/.claude-plugin/.mcp.json",
			golden:   "testdata/claude/.claude-plugin/.mcp.json",
		},
		{
			rendered: "claude/hooks/block-tap-cli.py",
			golden:   "testdata/claude/hooks/block-tap-cli.py",
		},
		{
			rendered: "claude/hooks/hooks.json",
			golden:   "testdata/claude/hooks/hooks.json",
		},
		{
			rendered: "claude/skills/tapper/SKILL.md",
			golden:   "testdata/claude/skills/tapper/SKILL.md",
		},
	}
	for _, c := range cases {
		t.Run(c.rendered, func(t *testing.T) {
			got, ok := mem.Files()[c.rendered]
			if !ok {
				t.Fatalf("adapter did not produce %s; produced %v", c.rendered, mem.Paths())
			}
			want, err := os.ReadFile(filepath.FromSlash(c.golden))
			if err != nil {
				t.Fatalf("read golden %s: %v", c.golden, err)
			}
			if !bytes.Equal(got, want) {
				// Surface the first byte of divergence to speed up debugging.
				n := len(got)
				if len(want) < n {
					n = len(want)
				}
				diffAt := -1
				for i := 0; i < n; i++ {
					if got[i] != want[i] {
						diffAt = i
						break
					}
				}
				t.Fatalf("byte mismatch in %s (got %d bytes, want %d bytes, first diff at offset %d)", c.rendered, len(got), len(want), diffAt)
			}
		})
	}
}

// TestClaudeAdapter_SKILLMDHasSingleH1 guards the concat invariant: exactly
// one "# tapper" heading should appear at the start of SKILL.md, even though
// four of the five canonical files declare their own H1 for standalone
// readability.
func TestClaudeAdapter_SKILLMDHasSingleH1(t *testing.T) {
	rt := newTestRuntime(t)
	content := claudeContentFS(t)
	mem := integrations.NewMemWriter()
	if err := (ClaudeAdapter{}).Render(rt, content, mem); err != nil {
		t.Fatalf("Render: %v", err)
	}
	body := mem.Files()["claude/skills/tapper/SKILL.md"]
	// Count lines beginning with "# " (single hash + space). Backtick hash
	// markers inside code fences do not start at column 0 here, so a simple
	// prefix count is enough.
	count := 0
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("# ")) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("SKILL.md has %d top-level H1 headings, want 1", count)
	}
}

// TestClaudeAdapter_RegistersWithDefaults verifies the init()-time
// registration survives: DefaultAdapters() must contain a "claude" entry.
func TestClaudeAdapter_RegistersWithDefaults(t *testing.T) {
	found := false
	for _, a := range integrations.DefaultAdapters() {
		if a.Name() == "claude" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Claude adapter not registered in DefaultAdapters()")
	}
}

// TestClaudeAdapter_StripLeadingH1 verifies the helper behaves correctly on
// the edge cases that matter for concat: a normal H1 + blank line, an H1
// with no following blank, and a file that starts without any H1.
func TestStripLeadingH1(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantOut string
	}{
		{"h1 with blank", "# Heading\n\nbody\n", "body\n"},
		{"h1 no blank", "# Heading\nbody\n", "body\n"},
		{"no h1", "body\n", "body\n"},
		{"empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := stripLeadingH1([]byte(c.in))
			if string(got) != c.wantOut {
				t.Errorf("stripLeadingH1(%q) = %q, want %q", c.in, got, c.wantOut)
			}
		})
	}
}
