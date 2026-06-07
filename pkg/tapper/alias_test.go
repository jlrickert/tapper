package tapper_test

import (
	"errors"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
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
		{"underscore", "my_keg", true},
		{"mixed_allowed", "k_3-b_2", true},
		{"single_char", "a", true},
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
		{"underscore", "ns_1", true},
		// The flights.d sentinel must be rejected so a namespace dir can never
		// collide with <basePath>/flights.d.
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
