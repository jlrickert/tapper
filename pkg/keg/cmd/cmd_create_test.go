package cmd_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
)

// Unit table-driven test for NormalizeTags (no external deps).
func TestNormalizeTags_Table(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"single", "alpha", []string{"alpha"}},
		{"comma-separated", "a,b,c", []string{"a", "b", "c"}},
		{"spaces and empty tokens", " a ,  b ,, c ", []string{"a", "b", "c"}},
		{"leading/trailing", "  x  ", []string{"x"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := keg.NormalizeTags(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeTags(%q) = %#v; want %#v", tc.in, got, tc.want)
			}
		})
	}
}

// TestCreate_HelpRenders ensures the create subcommand help text renders.
// Uses NewTestFixture to capture IO and Run to execute the CLI.
func TestCreate_HelpRenders(t *testing.T) {
	t.Parallel()
	f := NewTestFixture(t)

	if err := f.Run([]string{"create", "--help"}); err != nil {
		t.Fatalf("run failed: %v", err)
	}

	got := f.Stdout()
	if !strings.Contains(got, "Usage") && !strings.Contains(got, "Flags") {
		t.Fatalf("help output looks wrong; got:\n%s", got)
	}
}
