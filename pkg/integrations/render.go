package integrations

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"sort"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// DestWriter is the write surface adapters use. Keeping it abstract lets
// RenderAll target either an on-disk staging directory (DirWriter) or an
// in-memory map (MemWriter) for byte-parity tests without touching the FS.
type DestWriter interface {
	// Write places b at the given relative path. Implementations must
	// create any missing parent directories. Calls with the same path
	// overwrite prior writes.
	Write(path string, b []byte) error
}

// MemWriter buffers writes in memory for tests. Files() returns a stable
// snapshot of the accumulated writes keyed by forward-slash relative path.
type MemWriter struct {
	files map[string][]byte
}

// NewMemWriter allocates an empty MemWriter.
func NewMemWriter() *MemWriter { return &MemWriter{files: map[string][]byte{}} }

// Write stores a copy of b at path. Paths are normalised to forward slashes.
func (m *MemWriter) Write(path string, b []byte) error {
	if m.files == nil {
		m.files = map[string][]byte{}
	}
	cp := make([]byte, len(b))
	copy(cp, b)
	m.files[filepath.ToSlash(path)] = cp
	return nil
}

// Files returns the accumulated writes. Callers must not mutate the returned
// slices; treat the map as an immutable snapshot of the render.
func (m *MemWriter) Files() map[string][]byte { return m.files }

// Paths returns the sorted list of paths MemWriter has seen. Useful for
// deterministic test assertions.
func (m *MemWriter) Paths() []string {
	out := make([]string, 0, len(m.files))
	for p := range m.files {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// DirWriter writes to an on-disk root directory through a toolkit.Runtime,
// so writes honor sandboxed test environments and respect the project's
// Runtime Abstraction Rule. Writes go to a sibling ".render-tmp" file and
// are then atomically renamed into place; a half-written file never becomes
// visible at the final rendered path.
type DirWriter struct {
	rt   *toolkit.Runtime
	root string
}

// NewDirWriter returns a DirWriter that places files under root via rt.
// root is resolved relative to rt's working directory, so production callers
// pass a path like "integrations/rendered" and test sandboxes see writes
// confined to their jail.
func NewDirWriter(rt *toolkit.Runtime, root string) *DirWriter {
	return &DirWriter{rt: rt, root: root}
}

// Write writes b to relPath under the DirWriter root. rt.WriteFile creates
// any missing parent directories internally; the temp-rename step gives
// callers atomic visibility.
func (d *DirWriter) Write(relPath string, b []byte) error {
	if d == nil || d.rt == nil {
		return errors.New("integrations: DirWriter has nil runtime")
	}
	full := path.Join(d.root, filepath.ToSlash(relPath))
	tmp := full + ".render-tmp"
	if err := d.rt.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("integrations: write %s: %w", tmp, err)
	}
	if err := d.rt.Rename(tmp, full); err != nil {
		_ = d.rt.Remove(tmp, false)
		return fmt.Errorf("integrations: rename %s -> %s: %w", tmp, full, err)
	}
	return nil
}

// Adapter renders canonical content into a host-specific file tree under a
// DestWriter. Each adapter owns its own output namespace (a subdirectory of
// the rendered root keyed by Name()). Adapters must not read or write
// anything outside that namespace and must not import each other.
type Adapter interface {
	// Name returns the adapter's directory name under the rendered root.
	// Examples: "claude", "codex". It must be a single path segment.
	Name() string

	// Render reads canonical from content and writes every host-specific
	// file to dst with a path prefix of Name()+"/". The runtime carries
	// env, clock, and filesystem access for adapters that need them
	// (for example to consult release-time configuration through
	// rt.Env().Get). rt is required and must not be nil; tests that do
	// not want to touch the real process environment should use
	// sandbox.NewSandbox to construct a jailed runtime.
	Render(rt *toolkit.Runtime, content fs.FS, dst DestWriter) error
}

// registry holds the package-level adapter set. Registration happens in
// adapters/*.go via init() functions, mirroring pkg/mcp's register*Tools fan
// out — the renderer does not need to know any adapter by type.
var registry []Adapter

// Register adds a to the default adapter set. It is safe to call from init().
// Duplicate names are rejected eagerly so a copy-paste bug in a new adapter
// fails during test setup rather than silently clobbering an earlier entry.
func Register(a Adapter) {
	for _, existing := range registry {
		if existing.Name() == a.Name() {
			panic(fmt.Sprintf("integrations: adapter %q already registered", a.Name()))
		}
	}
	registry = append(registry, a)
}

// DefaultAdapters returns a copy of the registered adapter set, sorted by
// name so RenderAll is deterministic across runs and platforms.
func DefaultAdapters() []Adapter {
	out := make([]Adapter, len(registry))
	copy(out, registry)
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// RenderAll reads canonical content from content and dispatches each Adapter
// onto dst. Adapters are invoked in DefaultAdapters() order. The first
// adapter error aborts the render; partial writes to dst are left in place
// for inspection by the caller, which for DirWriter means they sit in the
// staging directory and never become user-visible. rt is required — it
// carries env, clock, and filesystem access that adapters consult for
// release-time configuration. Tests construct a sandbox runtime via
// sandbox.NewSandbox so environment lookups stay jailed.
func RenderAll(rt *toolkit.Runtime, content fs.FS, dst DestWriter, adapters []Adapter) error {
	if rt == nil {
		return fmt.Errorf("integrations: runtime is nil")
	}
	if content == nil {
		return fmt.Errorf("integrations: canonical fs.FS is nil")
	}
	if dst == nil {
		return fmt.Errorf("integrations: dst writer is nil")
	}
	if adapters == nil {
		adapters = DefaultAdapters()
	}
	for _, a := range adapters {
		if err := a.Render(rt, content, dst); err != nil {
			return fmt.Errorf("integrations: adapter %q: %w", a.Name(), err)
		}
	}
	return nil
}

// Compile-time guarantee that embed.FS satisfies fs.FS so an eventual
// //go:embed wiring can be dropped in without changing adapter signatures.
var _ fs.FS = embed.FS{}
