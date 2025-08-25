package keg

import (
	"reflect"
	"testing"
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
			got := NormalizeTags(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("NormalizeTags(%q) = %#v; want %#v", tc.in, got, tc.want)
			}
		})
	}
}
