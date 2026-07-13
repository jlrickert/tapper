package integrations_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"

	"github.com/jlrickert/tapper/pkg/integrations"

	// Blank import so ClaudeAdapter (and any future adapter) registers
	// itself before TestRenderedTreeMatchesAdapters consults
	// DefaultAdapters(). Without it the registry would be empty under
	// `go test ./pkg/integrations/` and the drift test would vacuously pass.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"

	// renderdata supplies host-specific canonical-source bytes (Claude
	// hooks today) that cmd/render-integrations overlays onto the
	// canonical content tree. The drift test must replicate that overlay
	// so the adapter sees the same content shape it does at render time.
	// This is an external test package, so importing renderdata here
	// does not pull those bytes into cmd/tap or cmd/keg.
	"github.com/jlrickert/tapper/pkg/integrations/renderdata"
)

// overlayFS composes two fs.FS views so the drift test can mirror the
// content-FS shape that cmd/render-integrations builds at runtime. Path
// lookups consult secondary first; misses fall through to primary.
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

// TestRenderedTreeMatchesAdapters re-runs every registered adapter over
// the embedded canonical content and compares the output to the embedded
// rendered tree byte-for-byte. If a contributor edits integrations/content
// without running `task render-integrations`, the embedded rendered/ tree
// diverges from what the adapters would produce and this test fails.
//
// The test is symmetric: every adapter-produced file must appear in the
// embedded rendered tree, AND every embedded rendered file must be
// produced by some adapter. The second direction catches stale leftovers
// from a removed adapter.
func TestRenderedTreeMatchesAdapters(t *testing.T) {
	contentFS, err := fs.Sub(integrations.IntegrationsFS, "content")
	if err != nil {
		t.Fatalf("fs.Sub(content): %v", err)
	}
	// Mirror cmd/render-integrations: overlay renderdata.FS onto the
	// canonical content. Without this, the Claude adapter cannot find
	// "claude/hooks/..." in the content FS and Render fails.
	content := overlayFS{primary: contentFS, secondary: renderdata.FS}

	// The embedded rendered/claude/.claude-plugin/plugin.json is the
	// source of truth for the Claude adapter's "version" field: the
	// release workflow bakes the tag into it, and Claude Code uses that
	// field as its update gate. Read the embedded value and feed it to
	// the sandbox runtime via TAPPER_PLUGIN_VERSION so the adapter
	// re-renders the same bytes whether main carries "dev" (developer
	// checkout) or "v0.X.0" (post-release).
	pluginJSON, err := fs.ReadFile(integrations.IntegrationsFS, "rendered/claude/tapper/.claude-plugin/plugin.json")
	if err != nil {
		t.Fatalf("read embedded plugin.json: %v", err)
	}
	var pluginMeta struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(pluginJSON, &pluginMeta); err != nil {
		t.Fatalf("parse embedded plugin.json: %v", err)
	}
	if pluginMeta.Version == "" {
		t.Fatalf("embedded plugin.json has empty version field; real drift")
	}

	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	rt := sb.Runtime()
	if err := rt.Env().Set("TAPPER_PLUGIN_VERSION", pluginMeta.Version); err != nil {
		t.Fatalf("set TAPPER_PLUGIN_VERSION: %v", err)
	}

	mem := integrations.NewMemWriter()
	if err := integrations.RenderAll(rt, content, mem, integrations.DefaultAdapters()); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}

	produced := mem.Files()
	if len(produced) == 0 {
		t.Fatal("no adapter output; did the adapters package fail to register?")
	}

	for _, relPath := range mem.Paths() {
		got := produced[relPath]
		embPath := "rendered/" + relPath
		want, err := fs.ReadFile(integrations.IntegrationsFS, embPath)
		if err != nil {
			t.Errorf("adapter produced %q but it is absent from the embedded rendered tree: %v", embPath, err)
			continue
		}
		if !bytes.Equal(got, want) {
			t.Errorf("drift at %s: adapter output and embedded rendered bytes differ (%d vs %d bytes); run `task render-integrations`", embPath, len(got), len(want))
		}
	}

	renderedFS, err := fs.Sub(integrations.IntegrationsFS, "rendered")
	if err != nil {
		t.Fatalf("fs.Sub(rendered): %v", err)
	}
	err = fs.WalkDir(renderedFS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if _, ok := produced[p]; !ok {
			t.Errorf("embedded rendered file %q is not produced by any registered adapter (stale leftover?)", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk rendered: %v", err)
	}
}

// TestEmbedIntegrity walks the on-disk integrations/ tree and asserts that
// every non-Go file appears in IntegrationsFS with identical bytes. It
// guards against a missing //go:embed directive, a file slipping into the
// repo that the build ignored (for example one placed in a dot-directory
// without the "all:" prefix), or a rendered artifact that exists on disk
// but not in the binary.
func TestEmbedIntegrity(t *testing.T) {
	repoRoot := findRepoRoot(t)
	diskRoot := filepath.Join(repoRoot, "integrations")

	err := filepath.WalkDir(diskRoot, func(absPath string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		// The embed.go declaration file sits alongside the content and
		// rendered subtrees; it is Go source, not embedded data.
		if strings.HasSuffix(absPath, ".go") {
			return nil
		}

		rel, err := filepath.Rel(diskRoot, absPath)
		if err != nil {
			return err
		}
		fsPath := filepath.ToSlash(rel)

		diskBytes, err := os.ReadFile(absPath)
		if err != nil {
			return err
		}
		embBytes, err := fs.ReadFile(integrations.IntegrationsFS, fsPath)
		if err != nil {
			t.Errorf("on-disk file %s is not embedded: %v", fsPath, err)
			return nil
		}
		if !bytes.Equal(diskBytes, embBytes) {
			t.Errorf("embed mismatch at %s: disk has %d bytes, embedded has %d bytes", fsPath, len(diskBytes), len(embBytes))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk on-disk integrations: %v", err)
	}
}

// findRepoRoot walks up from the test's working directory until it finds
// go.mod. Tests execute from the package directory, not the repo root, so
// the on-disk integrations/ tree has to be located relative to the module
// root rather than the current working directory.
func findRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for dir := wd; ; {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("go.mod not found starting from %s", wd)
		}
		dir = parent
	}
}
