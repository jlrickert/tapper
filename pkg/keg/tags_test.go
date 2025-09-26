package keg_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestNormalizeTag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want string
	}{
		{"Simple", "simple"},
		{"My Tag", "my-tag"},
		{"  Leading/Trailing  ", "leading-trailing"},
		{"A--B", "a-b"},
		{"___", ""},
		{"pkg:Zeke", "pkg-zeke"},
		{"multi   space", "multi-space"},
		{"with,comma", "with-comma"},
	}

	for i, tc := range cases {
		t.Run(string(rune(i)), func(t *testing.T) {
			t.Parallel()
			got := keg.NormalizeTag(tc.in)
			require.Equal(t, tc.want, got, "input: %q", tc.in)
		})
	}
}

func TestParseTags(t *testing.T) {
	t.Parallel()

	cases := []struct {
		in   string
		want []string
	}{
		{"", []string{}},
		// note: ParseTags returns lexicographically sorted tokens
		{"one two three", []string{"one", "three", "two"}},
		{"Foo, Bar; baz\nFoo", []string{"bar", "baz", "foo"}},
		{"My Tag, Another Tag", []string{"another-tag", "my-tag"}},
		{",, ,", []string{}},
		{"pkg:Zeke other", []string{"other", "pkg-zeke"}},
	}

	for i, tc := range cases {
		t.Run(string(rune(i)), func(t *testing.T) {
			t.Parallel()
			got := keg.ParseTags(tc.in)
			require.Equal(t, tc.want, got, "input: %q", tc.in)
		})
	}
}
