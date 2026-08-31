package tapper

import (
	"context"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestDescribeKeg(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		k    *keg.LocalKeg
		want string
	}{
		{
			name: "nil keg",
			k:    nil,
			want: "the resolved keg",
		},
		{
			name: "nil target",
			k:    &keg.LocalKeg{},
			want: "the resolved keg",
		},
		{
			name: "remote keg shows ref and hub url",
			k:    kegWithTarget(&keg.Target{Namespace: "jlrickert", KegName: "example", HubURL: "https://tapper-1-jlrickert.dev.foldwise.ai"}),
			want: "keg:@jlrickert/example (hub https://tapper-1-jlrickert.dev.foldwise.ai)",
		},
		{
			name: "ordinary local namespace shows ref without hub",
			k:    kegWithTarget(&keg.Target{Namespace: "local", KegName: "example"}),
			want: "keg:@local/example",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, describeKeg(tt.k))
		})
	}
}

func TestFormatFrontmatter_ClosingDelimiterOnOwnLine(t *testing.T) {
	t.Parallel()
	meta := []byte("tags:\n  - wow\n  - gaming")
	content := []byte("# Devastation Evoker priorities\n")

	got := formatFrontmatter(context.Background(), meta, content)

	require.Contains(t, got, "\n---\n# Devastation Evoker priorities\n")
	require.NotContains(t, got, "gaming---")
}

func TestFormatFrontmatter_NoExtraBlankLineWhenMetaEndsWithNewline(t *testing.T) {
	t.Parallel()
	meta := []byte("title: Example\n")
	content := []byte("body")

	got := formatFrontmatter(context.Background(), meta, content)

	require.Equal(t, "---\ntitle: Example\n---\nbody", got)
}

// kegWithTarget builds a LocalKeg labeled with the given target for
// formatting tests.
func kegWithTarget(target *keg.Target) *keg.LocalKeg {
	k := &keg.LocalKeg{}
	k.SetTarget(target)
	return k
}
