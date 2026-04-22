package integrations

import (
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
)

func TestMemWriter_StoresAndReportsPaths(t *testing.T) {
	m := NewMemWriter()
	if err := m.Write("claude/a.txt", []byte("one")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := m.Write("codex/b.txt", []byte("two")); err != nil {
		t.Fatalf("write: %v", err)
	}
	paths := m.Paths()
	if !sort.StringsAreSorted(paths) {
		t.Errorf("Paths() must be sorted, got %v", paths)
	}
	want := []string{"claude/a.txt", "codex/b.txt"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("Paths() = %v, want %v", paths, want)
	}
	if got := string(m.Files()["claude/a.txt"]); got != "one" {
		t.Errorf("stored content = %q, want %q", got, "one")
	}
}

func TestMemWriter_CopyIsolation(t *testing.T) {
	// Callers must be free to mutate the slice they passed in without
	// disturbing what MemWriter has recorded.
	m := NewMemWriter()
	payload := []byte("original")
	_ = m.Write("a/b.txt", payload)
	payload[0] = 'X'
	if got := string(m.Files()["a/b.txt"]); got != "original" {
		t.Errorf("MemWriter must copy input; got %q", got)
	}
}

func TestDirWriter_WritesThroughRuntime(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	rt := sb.Runtime()
	d := NewDirWriter(rt, "out")
	if err := d.Write("sub/dir/file.txt", []byte("hello")); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := sb.ReadFile("out/sub/dir/file.txt")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(got) != "hello" {
		t.Errorf("content = %q, want hello", got)
	}
	// A successful write must not leave the temp file behind.
	if _, err := sb.ReadFile("out/sub/dir/file.txt.render-tmp"); err == nil {
		t.Errorf("leftover temp file was not cleaned up")
	}
}

func TestDirWriter_OverwritesExisting(t *testing.T) {
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	rt := sb.Runtime()
	d := NewDirWriter(rt, "out")
	if err := d.Write("file.txt", []byte("v1")); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if err := d.Write("file.txt", []byte("v2")); err != nil {
		t.Fatalf("v2: %v", err)
	}
	got, _ := sb.ReadFile("out/file.txt")
	if string(got) != "v2" {
		t.Errorf("overwrite = %q, want v2", got)
	}
}

func TestDirWriter_NilRuntime(t *testing.T) {
	d := NewDirWriter(nil, "out")
	if err := d.Write("file.txt", []byte("x")); err == nil {
		t.Errorf("expected error from DirWriter with nil runtime")
	}
}

// newTestRuntime builds a sandbox-backed runtime for render-layer tests.
// Every RenderAll caller needs one so env lookups from within adapters
// stay jailed.
func newTestRuntime(t *testing.T) *toolkit.Runtime {
	t.Helper()
	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Home: filepath.FromSlash("/home/testuser"),
		User: "testuser",
	})
	return sb.Runtime()
}

func TestRenderAll_IteratesInCallerOrder(t *testing.T) {
	rt := newTestRuntime(t)
	content := fstest.MapFS{"x.md": &fstest.MapFile{Data: []byte("clean\n")}}
	mem := NewMemWriter()
	adapters := []Adapter{
		&testAdapter{n: "bravo"},
		&testAdapter{n: "alpha"},
	}
	if err := RenderAll(rt, content, mem, adapters); err != nil {
		t.Fatalf("RenderAll: %v", err)
	}
	paths := mem.Paths()
	want := []string{"alpha/marker.txt", "bravo/marker.txt"}
	if strings.Join(paths, ",") != strings.Join(want, ",") {
		t.Errorf("paths = %v, want %v", paths, want)
	}
}

func TestRenderAll_AbortsOnAdapterError(t *testing.T) {
	rt := newTestRuntime(t)
	content := fstest.MapFS{"x.md": &fstest.MapFile{Data: []byte("clean\n")}}
	mem := NewMemWriter()
	sentinel := errors.New("boom")
	adapters := []Adapter{&testAdapter{n: "broken", err: sentinel}}
	err := RenderAll(rt, content, mem, adapters)
	if err == nil || !errors.Is(err, sentinel) {
		t.Errorf("RenderAll err = %v, want wrap of %v", err, sentinel)
	}
}

func TestRenderAll_RejectsNilArgs(t *testing.T) {
	rt := newTestRuntime(t)
	content := fstest.MapFS{}
	if err := RenderAll(nil, content, NewMemWriter(), nil); err == nil {
		t.Errorf("expected error for nil runtime")
	}
	if err := RenderAll(rt, nil, NewMemWriter(), nil); err == nil {
		t.Errorf("expected error for nil content")
	}
	if err := RenderAll(rt, content, nil, nil); err == nil {
		t.Errorf("expected error for nil dst")
	}
}

func TestRenderAll_NilAdaptersUsesDefault(t *testing.T) {
	// With a fresh-registered test adapter, nil adapters should reach it.
	prev := registry
	t.Cleanup(func() { registry = prev })
	registry = []Adapter{&testAdapter{n: "defaulted"}}
	rt := newTestRuntime(t)
	content := fstest.MapFS{}
	mem := NewMemWriter()
	if err := RenderAll(rt, content, mem, nil); err != nil {
		t.Fatalf("RenderAll with nil adapters: %v", err)
	}
	if _, ok := mem.Files()["defaulted/marker.txt"]; !ok {
		t.Errorf("expected defaulted adapter to run, got %v", mem.Paths())
	}
}

func TestRegister_RejectsDuplicateNames(t *testing.T) {
	prev := registry
	t.Cleanup(func() { registry = prev })
	registry = nil
	Register(&testAdapter{n: "__dup__"})
	defer func() {
		if r := recover(); r == nil {
			t.Errorf("expected panic on duplicate adapter name")
		}
	}()
	Register(&testAdapter{n: "__dup__"})
}

func TestDefaultAdapters_SortedByName(t *testing.T) {
	prev := registry
	t.Cleanup(func() { registry = prev })
	registry = []Adapter{
		&testAdapter{n: "zzz"},
		&testAdapter{n: "aaa"},
		&testAdapter{n: "mmm"},
	}
	got := DefaultAdapters()
	if got[0].Name() != "aaa" || got[1].Name() != "mmm" || got[2].Name() != "zzz" {
		t.Errorf("DefaultAdapters order = %v, want aaa,mmm,zzz",
			[]string{got[0].Name(), got[1].Name(), got[2].Name()})
	}
}

// testAdapter is a minimal Adapter. If err is non-nil it returns it without
// writing; otherwise it writes a single marker file under its own namespace.
type testAdapter struct {
	n   string
	err error
}

func (a *testAdapter) Name() string { return a.n }

func (a *testAdapter) Render(_ *toolkit.Runtime, content fs.FS, dst DestWriter) error {
	if a.err != nil {
		return a.err
	}
	return dst.Write(a.n+"/marker.txt", []byte("marker"))
}

// OrientPath returns empty so testAdapter does not contribute to the
// orient host set. Tests that need an orient-capable stub override
// this by declaring their own Adapter value.
func (a *testAdapter) OrientPath() string { return "" }
