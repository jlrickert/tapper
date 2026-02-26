package tapper

import (
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDefaultUserKegSearchPath(t *testing.T) {
	t.Parallel()

	got := defaultUserKegSearchPath(nil)

	switch runtime.GOOS {
	case "darwin", "linux":
		require.Equal(t, "~/Documents/kegs", got)
	default:
		require.NotEmpty(t, got)
		require.True(t, strings.Contains(got, "Documents"))
		require.True(t, strings.Contains(got, "kegs"))
	}
}
