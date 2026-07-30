package tapper

import (
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

func testEntry() keg.NodeIndexEntry {
	return keg.NodeIndexEntry{
		ID:       "3",
		Title:    "A Node",
		Updated:  time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC),
		Created:  time.Date(2026, 7, 1, 9, 30, 0, 0, time.UTC),
		Accessed: time.Date(2026, 7, 30, 8, 15, 0, 0, time.UTC),
	}
}

func renderOne(t *testing.T, format string, src nodeFieldSource) string {
	t.Helper()
	compiled, err := compileListFormat(format)
	if err != nil {
		t.Fatalf("compileListFormat(%q): %v", format, err)
	}
	return expandFormat(compiled, src)
}

func TestCompileListFormatDefaultIsUnchanged(t *testing.T) {
	// The default must stay byte-identical to the historical output, or every
	// script parsing `tap list` breaks.
	got := renderOne(t, "", nodeFieldSource{entry: testEntry()})
	want := "3\t2026-07-29T12:00:00Z\tA Node"
	if got != want {
		t.Errorf("default format = %q, want %q", got, want)
	}
}

func TestCompileListFormatLegacyVerbs(t *testing.T) {
	src := nodeFieldSource{entry: testEntry()}
	tests := map[string]string{
		"%i":       "3",
		"%t":       "A Node",
		"%d":       "2026-07-29T12:00:00Z",
		"%c":       "2026-07-01T09:30:00Z",
		"%a":       "2026-07-30T08:15:00Z",
		"%i|%t":    "3|A Node",
		"%i\t%c":   "3\t2026-07-01T09:30:00Z",
		"[%i] %t!": "[3] A Node!",
	}
	for format, want := range tests {
		if got := renderOne(t, format, src); got != want {
			t.Errorf("format %q = %q, want %q", format, got, want)
		}
	}
}

func TestCompileListFormatNamedSelectorsMatchLegacyVerbs(t *testing.T) {
	// The aliases must not drift from what they alias.
	src := nodeFieldSource{entry: testEntry()}
	pairs := [][2]string{
		{"%i", "%{id}"},
		{"%t", "%{title}"},
		{"%d", "%{.updated}"},
		{"%c", "%{.created}"},
		{"%a", "%{.accessed}"},
	}
	for _, pair := range pairs {
		legacy := renderOne(t, pair[0], src)
		named := renderOne(t, pair[1], src)
		if legacy != named {
			t.Errorf("%s = %q but %s = %q; aliases must agree", pair[0], legacy, pair[1], named)
		}
	}
}

func TestCompileListFormatLiteralPercent(t *testing.T) {
	src := nodeFieldSource{entry: testEntry()}
	tests := map[string]string{
		// Documented in the CLI help for a long time but never implemented.
		"100%%":  "100%",
		"%%":     "%",
		"%%i":    "%i",
		"%%%i":   "%3",
		"%i%%":   "3%",
		"%%{id}": "%{id}",
	}
	for format, want := range tests {
		if got := renderOne(t, format, src); got != want {
			t.Errorf("format %q = %q, want %q", format, got, want)
		}
	}
}

func TestCompileListFormatUnknownVerbPassesThrough(t *testing.T) {
	// Historical behaviour: an unrecognised %X is literal text. Erroring here
	// would break formats containing bare percents.
	src := nodeFieldSource{entry: testEntry()}
	tests := map[string]string{
		"%z":      "%z",
		"50% off": "50% off",
		"%":       "%",
		"%i %z":   "3 %z",
	}
	for format, want := range tests {
		if got := renderOne(t, format, src); got != want {
			t.Errorf("format %q = %q, want %q", format, got, want)
		}
	}
}

func TestCompileListFormatErrors(t *testing.T) {
	tests := map[string]string{
		"%{":            "unterminated",
		"%{id":          "unterminated",
		"%{}":           "empty selector",
		"%{   }":        "empty selector",
		"%{.bogus}":     "unknown stats field",
		"%{type=plan}":  "not a predicate",
		"%{omega>=0.5}": "not a predicate",
	}
	for format, wantSubstr := range tests {
		_, err := compileListFormat(format)
		if err == nil {
			t.Errorf("compileListFormat(%q) succeeded, want error", format)
			continue
		}
		if !strings.Contains(err.Error(), "invalid format") {
			t.Errorf("compileListFormat(%q) error = %q, want it prefixed with %q", format, err, "invalid format")
		}
		if !strings.Contains(err.Error(), wantSubstr) {
			t.Errorf("compileListFormat(%q) error = %q, want it to mention %q", format, err, wantSubstr)
		}
	}
}

func TestExpandFormatDoesNotReexpandValues(t *testing.T) {
	// The old implementation was a chain of strings.Replace over the whole
	// line, so an expanded value containing a verb was expanded again. A
	// single left-to-right pass makes that structurally impossible.
	entry := testEntry()
	entry.Title = "%c and %{id} and %%"
	got := renderOne(t, "%t", nodeFieldSource{entry: entry})
	if got != "%c and %{id} and %%" {
		t.Errorf("title = %q, want it rendered literally", got)
	}
}

func TestExpandFormatZeroTimestampsRenderEmpty(t *testing.T) {
	// 0001-01-01T00:00:00Z parses as a real date and would poison sorting.
	src := nodeFieldSource{entry: keg.NodeIndexEntry{ID: "1", Title: "T"}}
	if got := renderOne(t, "%i|%d|%c|%a", src); got != "1|||" {
		t.Errorf("zero timestamps = %q, want %q", got, "1|||")
	}
}

func TestExpandFormatSanitizesControlCharacters(t *testing.T) {
	// Each rendered string is one output line. A value containing a newline
	// would silently emit extra lines and desynchronise every line-counting
	// caller. Literal text in the format is untouched, so an explicit tab
	// separator must survive.
	entry := testEntry()
	entry.Title = "line one\nline two\ttabbed"
	got := renderOne(t, "%i\t%t", nodeFieldSource{entry: entry})
	want := "3\tline one line two tabbed"
	if got != want {
		t.Errorf("sanitised = %q, want %q", got, want)
	}
	if strings.ContainsAny(strings.TrimPrefix(got, "3\t"), "\n\t") {
		t.Errorf("expanded value still contains control characters: %q", got)
	}
}

func TestExpandFormatCollapsesRunsOfControlCharacters(t *testing.T) {
	entry := testEntry()
	entry.Title = "a\n\n\nb"
	if got := renderOne(t, "%t", nodeFieldSource{entry: entry}); got != "a b" {
		t.Errorf("got %q, want %q", got, "a b")
	}
}

func TestCompileListFormatNeedsFlags(t *testing.T) {
	// These flags gate whether the renderer performs per-node I/O at all, so
	// a false positive silently makes every listing slow.
	tests := []struct {
		format    string
		wantMeta  bool
		wantStats bool
	}{
		{format: ""},
		{format: "%i\t%d\t%t"},
		{format: "%{id} %{title} %{.updated} %{.created} %{.accessed}"},
		{format: "%{type}", wantMeta: true},
		{format: "%{tags}", wantMeta: true},
		{format: "%{.hash}", wantStats: true},
		{format: "%{.omega}", wantStats: true},
		{format: "%{type} %{.omega}", wantMeta: true, wantStats: true},
	}
	for _, tc := range tests {
		compiled, err := compileListFormat(tc.format)
		if err != nil {
			t.Fatalf("compileListFormat(%q): %v", tc.format, err)
		}
		if compiled.needsMeta != tc.wantMeta {
			t.Errorf("format %q needsMeta = %v, want %v", tc.format, compiled.needsMeta, tc.wantMeta)
		}
		if compiled.needsStats != tc.wantStats {
			t.Errorf("format %q needsStats = %v, want %v", tc.format, compiled.needsStats, tc.wantStats)
		}
	}
}

func TestExpandFormatAbsentMetaAndStatsRenderEmpty(t *testing.T) {
	// Absent values keep a stable column count rather than using a sentinel
	// that could collide with a real value.
	src := nodeFieldSource{entry: testEntry()}
	if got := renderOne(t, "%i|%{type}|%{tags}|%{.hash}", src); got != "3|||" {
		t.Errorf("absent fields = %q, want %q", got, "3|||")
	}
}

func TestExpandFormatAccessCountRendersZero(t *testing.T) {
	// accessCount has no absent state on disk, so it always renders a number.
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	src := nodeFieldSource{entry: testEntry(), stats: keg.NewStats(now)}
	if got := renderOne(t, "%{.accessCount}", src); got != "0" {
		t.Errorf("accessCount = %q, want %q", got, "0")
	}
}

func TestExpandFormatIntrinsicsShadowMetadata(t *testing.T) {
	// A node carrying a `title` metadata key still renders the index title.
	// This is the documented cost of reserving id/title/tags.
	meta := keg.NewMeta(t.Context(), time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC))
	if err := meta.Set(t.Context(), "title", "metadata title"); err != nil {
		t.Fatalf("set title: %v", err)
	}
	src := nodeFieldSource{entry: testEntry(), meta: meta}
	if got := renderOne(t, "%{title}", src); got != "A Node" {
		t.Errorf("title = %q, want the intrinsic %q", got, "A Node")
	}
}
