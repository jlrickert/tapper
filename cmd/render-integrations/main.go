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
	content := os.DirFS(canonicalAbs)

	dst := integrations.NewDirWriter(rt, renderedDir)
	return integrations.RenderAll(content, dst, integrations.DefaultAdapters())
}
