package tapper_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

func TestCanonicalHubURL(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in, want string
	}{
		{"https://Hub.Example.COM/", "https://hub.example.com"},
		{"HTTP://HUB.example.com/path/", "http://hub.example.com/path"},
		{"https://hub.example.com", "https://hub.example.com"},
		{"  https://hub.example.com/  ", "https://hub.example.com"},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, tapper.CanonicalHubURL(tc.in), "in=%q", tc.in)
	}
}
