package adapters

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/jlrickert/tapper/pkg/integrations"
)

// TestCodexAdapter_GoldenByteParity mirrors TestClaudeAdapter_GoldenByteParity:
// running CodexAdapter against the canonical fixtures in testdata/canonical
// must produce byte-identical copies of the golden tree in testdata/codex/.
// Drift here flags either canonical content or adapter changes that need
// deliberate review.
func TestCodexAdapter_GoldenByteParity(t *testing.T) {
	rt := newTestRuntime(t)
	canonical := os.DirFS("testdata/canonical")
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(rt, canonical, mem); err != nil {
		t.Fatalf("Render: %v", err)
	}

	cases := []struct {
		rendered string
		golden   string
	}{
		{
			rendered: "codex/AGENTS.md",
			golden:   "testdata/codex/AGENTS.md",
		},
		{
			rendered: "codex/config-snippet.toml",
			golden:   "testdata/codex/config-snippet.toml",
		},
		{
			rendered: "codex/prompts/tapper-orient.md",
			golden:   "testdata/codex/prompts/tapper-orient.md",
		},
		{
			rendered: "codex/prompts/tapper-search.md",
			golden:   "testdata/codex/prompts/tapper-search.md",
		},
		{
			rendered: "codex/prompts/tapper-snapshot.md",
			golden:   "testdata/codex/prompts/tapper-snapshot.md",
		},
		{
			rendered: "codex/prompts/tapper-cross-keg.md",
			golden:   "testdata/codex/prompts/tapper-cross-keg.md",
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

// TestCodexAdapter_AGENTSMDHasSingleH1 guards the concat invariant: the
// codex preamble declares exactly one "# tapper" heading and the concat
// loop strips the leading H1 from every canonical section. If a future
// change adds a second top-level heading, this test catches it.
func TestCodexAdapter_AGENTSMDHasSingleH1(t *testing.T) {
	rt := newTestRuntime(t)
	canonical := os.DirFS("testdata/canonical")
	mem := integrations.NewMemWriter()
	if err := (CodexAdapter{}).Render(rt, canonical, mem); err != nil {
		t.Fatalf("Render: %v", err)
	}
	body := mem.Files()["codex/AGENTS.md"]
	count := 0
	for _, line := range bytes.Split(body, []byte{'\n'}) {
		if bytes.HasPrefix(line, []byte("# ")) {
			count++
		}
	}
	if count != 1 {
		t.Errorf("AGENTS.md has %d top-level H1 headings, want 1", count)
	}
}

// TestCodexAdapter_RegistersWithDefaults verifies the init()-time
// registration survives: DefaultAdapters() must contain a "codex" entry.
func TestCodexAdapter_RegistersWithDefaults(t *testing.T) {
	found := false
	for _, a := range integrations.DefaultAdapters() {
		if a.Name() == "codex" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Codex adapter not registered in DefaultAdapters()")
	}
}
