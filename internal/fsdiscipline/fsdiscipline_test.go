// Package fsdiscipline_test enforces the Runtime Abstraction Rule for
// filesystem access: every read and write goes through toolkit.Runtime, whose
// jail confines paths to the sandbox. A direct os call escapes that jail and
// breaks test isolation.
package fsdiscipline_test

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// filesystemFuncs maps each os function that touches the filesystem to its
// Runtime equivalent. Everything else in os stays allowed on purpose: the
// sentinels (os.ErrNotExist) and predicates (os.IsNotExist) used in errors.Is
// checks, the open flags (os.O_CREATE) passed *into* rt.OpenFile, and the
// non-filesystem helpers (os.Exit, os.Args, os.Getenv, os.Stdout, os.Hostname)
// are all correct usage. Matching calls rather than text is what keeps those
// out of the report.
var filesystemFuncs = map[string]string{
	"Chmod":      "rt.Chmod",
	"Create":     "rt.OpenFile",
	"CreateTemp": "rt.GetTempDir with rt.WriteFile",
	"DirFS":      "rt.ReadDir and rt.ReadFile",
	"Lstat":      "rt.Stat",
	"Mkdir":      "rt.Mkdir",
	"MkdirAll":   "rt.Mkdir(path, perm, true)",
	"MkdirTemp":  "rt.GetTempDir with rt.Mkdir",
	"Open":       "rt.ReadFile",
	"OpenFile":   "rt.OpenFile",
	"ReadDir":    "rt.ReadDir",
	"ReadFile":   "rt.ReadFile",
	"Remove":     "rt.Remove",
	"RemoveAll":  "rt.Remove(path, true)",
	"Rename":     "rt.Rename",
	"Stat":       "rt.Stat",
	"Symlink":    "rt.Symlink",
	"WriteFile":  "rt.WriteFile",
}

// exemptFiles may call os directly. Each entry records why; add one only when
// the code genuinely runs outside a sandbox.
var exemptFiles = map[string]string{
	"cmd/render-integrations/main.go": "build-time codegen: regenerates integrations/rendered/ from the Taskfile and pre-commit hook, before any Runtime exists",
}

type violation struct {
	position token.Position
	message  string
}

func (v violation) String() string {
	return fmt.Sprintf("%s: %s", v.position, v.message)
}

func TestNoDirectFilesystemCalls(t *testing.T) {
	root := repositoryRoot(t)
	violations, err := checkTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) == 0 {
		return
	}

	var report strings.Builder
	for _, violation := range violations {
		rel, err := filepath.Rel(root, violation.position.Filename)
		if err != nil {
			rel = violation.position.Filename
		}
		fmt.Fprintf(&report, "\n%s:%d:%d: %s", filepath.ToSlash(rel), violation.position.Line, violation.position.Column, violation.message)
	}
	t.Fatalf("direct filesystem access bypassing toolkit.Runtime:%s", report.String())
}

func TestCheckSource(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "runtime call is the expected form",
			src: `package sample
func run(rt *toolkit.Runtime) { rt.WriteFile("a", nil, 0o644) }`,
		},
		{
			name: "direct write",
			src: `package sample
import "os"
func run() { os.WriteFile("a", nil, 0o644) }`,
			want: []string{"os.WriteFile bypasses the toolkit.Runtime path jail; use rt.WriteFile"},
		},
		{
			name: "directory creation",
			src: `package sample
import "os"
func run() { os.MkdirAll("a/b", 0o755) }`,
			want: []string{"os.MkdirAll bypasses the toolkit.Runtime path jail; use rt.Mkdir(path, perm, true)"},
		},
		{
			name: "error sentinels and predicates stay allowed",
			src: `package sample
import (
	"errors"
	"os"
)
func run(err error) bool { return errors.Is(err, os.ErrNotExist) || os.IsNotExist(err) }`,
		},
		{
			name: "open flags passed into rt.OpenFile stay allowed",
			src: `package sample
import "os"
func run(rt *toolkit.Runtime) { rt.OpenFile("log", os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644) }`,
		},
		{
			name: "non-filesystem helpers stay allowed",
			src: `package sample
import "os"
func run() { os.Exit(len(os.Args) + len(os.Getenv("HOME"))) }`,
		},
		{
			name: "aliased import is still matched",
			src: `package sample
import stdos "os"
func run() { stdos.ReadFile("a") }`,
			want: []string{"os.ReadFile bypasses the toolkit.Runtime path jail; use rt.ReadFile"},
		},
		{
			name: "a local identifier named os is not the os package",
			src: `package sample
type fake struct{}
func (fake) ReadFile(string) ([]byte, error) { return nil, nil }
func run() { var os fake; os.ReadFile("a") }`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "sample.go", tt.src, parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}
			violations := checkFile(fset, file)
			got := make([]string, len(violations))
			for i, violation := range violations {
				got[i] = violation.message
			}
			if strings.Join(got, "\n") != strings.Join(tt.want, "\n") {
				t.Fatalf("violations:\n got: %q\nwant: %q", got, tt.want)
			}
		})
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate filesystem discipline test")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(filename), "..", ".."))
}

func checkTree(root string) ([]violation, error) {
	fset := token.NewFileSet()
	var violations []violation
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case "testdata", "node_modules", "vendor":
				return filepath.SkipDir
			}
			if strings.HasPrefix(entry.Name(), ".") && path != root {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr == nil {
			if _, ok := exemptFiles[filepath.ToSlash(rel)]; ok {
				return nil
			}
		}
		file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
		if err != nil {
			return err
		}
		if ast.IsGenerated(file) {
			return nil
		}
		violations = append(violations, checkFile(fset, file)...)
		return nil
	})
	sort.Slice(violations, func(i, j int) bool {
		a, b := violations[i].position, violations[j].position
		if a.Filename != b.Filename {
			return a.Filename < b.Filename
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Column < b.Column
	})
	return violations, err
}

func checkFile(fset *token.FileSet, file *ast.File) []violation {
	local, ok := osImportName(file)
	if !ok {
		return nil
	}

	var violations []violation
	ast.Inspect(file, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		selector, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok || pkg.Name != local {
			return true
		}
		replacement, ok := filesystemFuncs[selector.Sel.Name]
		if !ok {
			return true
		}
		violations = append(violations, violation{
			position: fset.Position(selector.Pos()),
			message:  fmt.Sprintf("os.%s bypasses the toolkit.Runtime path jail; use %s", selector.Sel.Name, replacement),
		})
		return true
	})
	return violations
}

// osImportName returns the name "os" is bound to in this file, honoring an
// alias, and reports whether the file imports it at all.
func osImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		if spec.Path == nil || spec.Path.Value != `"os"` {
			continue
		}
		if spec.Name != nil {
			if spec.Name.Name == "_" || spec.Name.Name == "." {
				return "", false
			}
			return spec.Name.Name, true
		}
		return "os", true
	}
	return "", false
}
