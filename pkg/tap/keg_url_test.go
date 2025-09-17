package tap_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/tap"
)

func TestParseKegTarget_HTTP_KegSuffix(t *testing.T) {
	raw := "https://example.com/project/keg"
	kt, err := tap.ParseKegTarget(raw)
	if err != nil {
		t.Fatalf("ParseKegTarget failed: %v", err)
	}
	if kt.Type != "https" {
		t.Fatalf("expected scheme https, got %q", kt.Type)
	}
	if !strings.HasSuffix(kt.Value, "keg") {
		t.Fatalf("expected uri to end with 'keg', got %q", kt.Value)
	}
	if kt.IsLocal() {
		t.Fatal("expected remote target, got local")
	}

	// Ensure ToURI preserves path ending and produces a parseable URL.
	uStr := kt.ToURI()
	u, err := url.Parse(uStr)
	if err != nil {
		t.Fatalf("ToURI produced invalid URL: %v", err)
	}
	if filepath.Base(u.Path) != "keg" {
		t.Fatalf("expected URL path base 'keg', got %q", filepath.Base(u.Path))
	}
}

func TestParseKegTarget_ReadonlyQueryAppended(t *testing.T) {
	raw := "https://example.com/foo/keg?x=1"
	kt, err := tap.ParseKegTarget(raw)
	if err != nil {
		t.Fatalf("ParseKegTarget failed: %v", err)
	}

	kt.Readonly = true
	out := kt.ToURI()
	u, err := url.Parse(out)
	if err != nil {
		t.Fatalf("ToURI produced invalid URL: %v", err)
	}
	q := u.Query()
	if q.Get("readonly") != "true" {
		t.Fatalf("expected readonly=true in query, got %v", u.RawQuery)
	}
	if q.Get("x") != "1" {
		t.Fatalf("expected original query key x=1 to be preserved, got %v", u.RawQuery)
	}
}

func TestParseKegTarget_FileURIAndPath(t *testing.T) {
	// file:// URI case
	raw := "file:///tmp/keg"
	kt, err := tap.ParseKegTarget(raw)
	if err != nil {
		t.Fatalf("ParseKegTarget failed: %v", err)
	}
	if kt.Type != "file" {
		t.Fatalf("expected type file, got %q", kt.Type)
	}
	if filepath.Base(kt.Value) != "keg" {
		t.Fatalf("expected parsed file uri path base 'keg', got %q", kt.Value)
	}
	out := kt.ToURI()
	u, err := url.Parse(out)
	if err != nil {
		t.Fatalf("ToURI produced invalid URL: %v", err)
	}
	if filepath.Base(u.Path) != "keg" {
		t.Fatalf("expected ToURI path base 'keg', got %q", filepath.Base(u.Path))
	}

	// plain filesystem path (no scheme)
	tmp := t.TempDir()
	rawPath := filepath.Join(tmp, "keg")
	kt2, err := tap.ParseKegTarget(rawPath)
	if err != nil {
		t.Fatalf("ParseKegTarget failed for path: %v", err)
	}
	if kt2.Type != "file" {
		t.Fatalf("expected type file for plain path, got %q", kt2.Type)
	}
	abs, _ := filepath.Abs(rawPath)
	if filepath.Clean(kt2.Value) != filepath.Clean(abs) {
		t.Fatalf("expected Uri to be absolute %q, got %q", abs, kt2.Value)
	}
	out2 := kt2.ToURI()
	u2, err := url.Parse(out2)
	if err != nil {
		t.Fatalf("ToURI produced invalid URL for path: %v", err)
	}
	if filepath.Base(u2.Path) != "keg" {
		t.Fatalf("expected ToURI path base 'keg', got %q", filepath.Base(u2.Path))
	}
}

func TestParseKegTarget_EmptyError(t *testing.T) {
	_, err := tap.ParseKegTarget("")
	if err == nil {
		t.Fatal("expected error for empty target, got nil")
	}
}

func TestNormalize_ExpandsEnvAndMakesAbsolute(t *testing.T) {
	tmp := t.TempDir()
	// set env var used in Uri
	if err := os.Setenv("KEG_TEST_DIR", tmp); err != nil {
		t.Fatalf("failed to set env: %v", err)
	}
	defer os.Unsetenv("KEG_TEST_DIR")

	kt := tap.KegUrl{
		Type:  "file",
		Value: "$KEG_TEST_DIR/keg",
	}
	if err := kt.Normalize(context.Background()); err != nil {
		t.Fatalf("Normalize failed: %v", err)
	}
	if !filepath.IsAbs(kt.Value) {
		t.Fatalf("expected normalized Uri to be absolute, got %q", kt.Value)
	}
	if filepath.Base(kt.Value) != "keg" {
		t.Fatalf("expected normalized path base 'keg', got %q", filepath.Base(kt.Value))
	}
}
