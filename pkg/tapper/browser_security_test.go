package tapper_test

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestVerificationBrowserURLSecurity(t *testing.T) {
	for _, raw := range []string{"file:///etc/passwd", "/tmp/file", "javascript:alert(1)", "https://evil.test/device", "http://hub.test/device", "https://user@hub.test/device", "https://hub.test:444/device", "https://hub.test/\\evil", "https://hub.test/\n"} {
		require.Error(t, tapper.ValidateBrowserURL(raw, "https://hub.test"), raw)
	}
	for _, raw := range []string{"https://hub.test/device", "https://HUB.test:443/device?x=1&y=2"} {
		require.NoError(t, tapper.ValidateBrowserURL(raw, "https://hub.test"))
	}
}
