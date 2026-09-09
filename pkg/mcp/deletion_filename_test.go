package mcp

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestDeletionFilenameAliases(t *testing.T) {
	p := func(s string) *string { return &s }
	for _, tc := range []struct {
		filename, alias *string
		want            string
		bad             bool
	}{
		{p("doc.txt"), nil, "doc.txt", false}, {nil, p("doc.txt"), "doc.txt", false}, {p("doc.txt"), p("doc.txt"), "doc.txt", false},
		{nil, nil, "", true}, {p(""), nil, "", true}, {nil, p("  "), "", true}, {p("a"), p("b"), "", true}, {p(""), p("a"), "", true},
	} {
		got, err := deletionFilename(tc.filename, tc.alias)
		require.Equal(t, tc.bad, err != nil)
		require.Equal(t, tc.want, got)
	}
}
