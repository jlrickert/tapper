package apidoc_test

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
	"unicode"
	"unicode/utf8"
)

type violation struct {
	position token.Position
	message  string
}

func (v violation) String() string {
	return fmt.Sprintf("%s: %s", v.position, v.message)
}

func TestExportedInterfaceDocumentation(t *testing.T) {
	root := repositoryRoot(t)
	violations, err := checkTree(filepath.Join(root, "pkg"))
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
	t.Fatalf("exported interface documentation violations:%s", report.String())
}

func TestCheckSource(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "documented interface",
			src: `package sample
// Service performs work.
type Service interface {
	// Run performs one operation.
	Run() error
}`,
		},
		{
			name: "missing interface comment",
			src: `package sample
type Service interface {
	// Run performs one operation.
	Run() error
}`,
			want: []string{"Service must have a doc comment beginning with Service"},
		},
		{
			name: "missing method comment",
			src: `package sample
// Service performs work.
type Service interface { Run() error }`,
			want: []string{"Service.Run must have a doc comment beginning with Run"},
		},
		{
			name: "misnamed comments",
			src: `package sample
// A service performs work.
type Service interface {
	// Execute performs one operation.
	Run() error
}`,
			want: []string{
				"Service must have a doc comment beginning with Service",
				"Service.Run must have a doc comment beginning with Run",
			},
		},
		{
			name: "non-interface exports are outside this gate",
			src: `package sample
type Concrete struct{}
func Exported() {}`,
		},
		{
			name: "unexported interface is outside this gate",
			src:  "package sample\ntype service interface { Run() error }",
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
		t.Fatal("locate api documentation test")
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
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
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
	var violations []violation
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || !typeSpec.Name.IsExported() {
				continue
			}
			iface, ok := typeSpec.Type.(*ast.InterfaceType)
			if !ok {
				continue
			}

			doc := typeSpec.Doc
			if doc == nil {
				doc = gen.Doc
			}
			if !startsWithIdentifier(doc, typeSpec.Name.Name) {
				violations = append(violations, violation{
					position: fset.Position(typeSpec.Pos()),
					message:  fmt.Sprintf("%s must have a doc comment beginning with %s", typeSpec.Name, typeSpec.Name),
				})
			}

			for _, field := range iface.Methods.List {
				for _, method := range field.Names {
					if !method.IsExported() || startsWithIdentifier(field.Doc, method.Name) {
						continue
					}
					violations = append(violations, violation{
						position: fset.Position(method.Pos()),
						message:  fmt.Sprintf("%s.%s must have a doc comment beginning with %s", typeSpec.Name, method.Name, method.Name),
					})
				}
			}
		}
	}
	return violations
}

func startsWithIdentifier(doc *ast.CommentGroup, identifier string) bool {
	if doc == nil {
		return false
	}
	text := strings.TrimSpace(doc.Text())
	if !strings.HasPrefix(text, identifier) {
		return false
	}
	if len(text) == len(identifier) {
		return true
	}
	next, _ := utf8.DecodeRuneInString(text[len(identifier):])
	return next != '_' && !unicode.IsLetter(next) && !unicode.IsDigit(next)
}
