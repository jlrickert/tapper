// Command render-integrations regenerates integrations/rendered/ from the
// canonical content under integrations/content/. The Taskfile target
// `task render-integrations` shells out here; the pre-commit hook shells out
// here; developers invoking `go run ./cmd/render-integrations` get the same
// thing. This shim exists so the canonical-plus-adapter logic lives in Go
// packages and so the render is reproducible from a clean checkout with no
// external tools beyond the Go toolchain.
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/integrations"

	// Side-effect import registers ClaudeAdapter in the integrations
	// package-level registry. Additional adapters plug in the same way.
	_ "github.com/jlrickert/tapper/pkg/integrations/adapters"

	// renderdata supplies host-specific canonical-source bytes (plugin hook
	// manifests today) that must NOT ship inside the cmd/tap or cmd/keg
	// binaries. Only this command imports it; the overlay below merges
	// it onto the markdown-only canonical tree before adapter dispatch.
	"github.com/jlrickert/tapper/pkg/integrations/renderdata"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "render-integrations:", err)
		os.Exit(1)
	}
}

func run() error {
	rt, err := toolkit.NewRuntime()
	if err != nil {
		return fmt.Errorf("runtime init: %w", err)
	}

	// Resolve the canonical and rendered roots relative to the repository
	// root. The Taskfile invokes this command from the repo root, so the
	// relative paths below are stable in every supported workflow.
	const (
		canonicalDir = "integrations/content"
		renderedDir  = "integrations/rendered"
	)

	if _, err := rt.Stat(canonicalDir, false); err != nil {
		return fmt.Errorf("canonical dir %s: %w", canonicalDir, err)
	}

	// Wipe the rendered root before re-rendering so stale files from a
	// deleted adapter cannot linger. rt.WriteFile creates parent directories
	// on demand, so no explicit mkdir is required after the remove.
	if err := rt.Remove(renderedDir, true); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove %s: %w", renderedDir, err)
	}

	// Canonical content lives on the real filesystem and is read through an
	// fs.FS so adapters remain runtime-independent. The runtime resolves the
	// absolute path so this binary runs correctly from any working directory
	// the shell hands it.
	canonicalAbs, err := rt.ResolvePath(canonicalDir, false)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", canonicalDir, err)
	}
	primary := os.DirFS(canonicalAbs)

	// Overlay renderdata.FS onto the on-disk canonical tree. The overlay
	// is asymmetric: paths present in the secondary (renderdata) take
	// precedence on overlap, paths only in the primary fall through. In
	// practice the two name-spaces are disjoint — primary owns
	// markdown bodies at the root, secondary owns the "claude/..."
	// subtree — so precedence only documents intent.
	content := overlayFS{primary: primary, secondary: renderdata.FS}

	dst := integrations.NewDirWriter(rt, renderedDir)
	return integrations.RenderAll(rt, content, dst, integrations.DefaultAdapters())
}

// overlayFS composes two fs.FS views. Open consults secondary first; if the
// path is absent there it falls through to primary. io/fs does not ship a
// merged-FS helper, so this minimal local type carries the overlay without
// pulling in a third-party dependency.
type overlayFS struct {
	primary, secondary fs.FS
}

// Open implements fs.FS. Any error from secondary that is not
// fs.ErrNotExist is surfaced immediately so a malformed embed is not
// silently masked by the primary.
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
