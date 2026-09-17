package tapper_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// maxSegment is the longest accepted segment; oneOver is the first rejected.
var (
	maxSegment = "a" + strings.Repeat("b", 63)
	oneOver    = "a" + strings.Repeat("b", 64)
)

func TestValidateKegAlias(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		alias string
		ok    bool
	}{
		{"lowercase", "blog", true},
		{"digits", "keg42", true},
		{"hyphen", "my-keg", true},
		{"single_char", "a", true},
		{"max_length", maxSegment, true},
		// Hub accepts a trailing hyphen, so tapper must too: a client stricter
		// than the server rejects references the server would accept.
		{"trailing_hyphen", "keg-", true},
		{"over_length", oneOver, false},
		{"underscore", "my_keg", false},
		{"mixed_underscore", "k_3-b_2", false},
		{"leading_hyphen", "-keg", false},
		{"empty", "", false},
		{"uppercase", "Blog", false},
		{"space", "my keg", false},
		{"slash", "kegs/blog", false},
		{"dot", "blog.keg", false},
		{"plus", "a+b", false},
		{"unicode", "kég", false},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := tapper.ValidateKegAlias(c.alias)
			if c.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, errors.Is(err, keg.ErrInvalid),
				"expected keg.ErrInvalid in chain, got %v", err)
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ns   string
		ok   bool
	}{
		{"local", "local", true},
		{"username", "jlrickert", true},
		{"hyphen", "team-a", true},
		{"single_char", "a", true},
		{"max_length", maxSegment, true},
		{"trailing_hyphen", "team-", true},
		{"over_length", oneOver, false},
		{"underscore", "ns_1", false},
		{"leading_hyphen", "-ns", false},
		{"flights_d", "flights.d", false},
		{"any_dotted", "a.b", false},
		{"trailing_dot", "x.", false},
		{"leading_dot", ".y", false},
		{"slash", "ns/sub", false},
		{"uppercase", "Up", false},
		{"space", "a b", false},
		{"empty", "", false},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			err := tapper.ValidateNamespace(c.ns)
			if c.ok {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.True(t, errors.Is(err, keg.ErrInvalid),
				"expected keg.ErrInvalid in chain, got %v", err)
		})
	}
}

func TestParseCanonicalKegRef(t *testing.T) {
	t.Parallel()

	t.Run("splits a valid reference", func(t *testing.T) {
		t.Parallel()
		ns, alias, err := tapper.ParseCanonicalKegRef("@acme/notes")
		require.NoError(t, err)
		require.Equal(t, "acme", ns)
		require.Equal(t, "notes", alias)
	})

	for _, ref := range []string{
		"", "notes", "@acme", "@/notes", "@acme/", "@acme/notes/extra",
		"@Bad/notes", "@acme/my_notes", "@acme/-notes", "@-acme/notes",
		"@acme/" + oneOver,
	} {
		t.Run(ref, func(t *testing.T) {
			t.Parallel()
			_, _, err := tapper.ParseCanonicalKegRef(ref)
			require.Error(t, err)
			require.True(t, errors.Is(err, keg.ErrInvalid),
				"expected keg.ErrInvalid in chain, got %v", err)
		})
	}
}
