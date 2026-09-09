package mcp

import (
	"fmt"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestTransportedOrientationRefusals(t *testing.T) {
	for _, sentinel := range []error{keg.ErrOrientationStale, keg.ErrOrientationDenied, keg.ErrOrientationUnavailable, keg.ErrOrientationRootUnavailable} {
		t.Run(sentinel.Error(), func(t *testing.T) {
			result := errorResult(fmt.Errorf("remote request: %w", sentinel))
			data := result.StructuredContent.(map[string]any)
			code, _ := keg.RemoteErrorCode(sentinel)
			require.Equal(t, code, data["code"])
			require.Equal(t, false, data["operationPerformed"])
			require.NotContains(t, fmt.Sprint(result.Content), "partial write")
		})
	}
}
