package cli

import (
	"net/http"
	"runtime"
)

// UserAgent identifies tap to the hubs it talks to, so a hub's list of
// signed-in clients can say which tap, and on what platform, holds a login.
func UserAgent() string {
	return "tap/" + Version + " (" + runtime.GOOS + "/" + runtime.GOARCH + ")"
}

// userAgentTransport sets UserAgent on requests that do not set their own.
type userAgentTransport struct{ base http.RoundTripper }

func (t userAgentTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get("User-Agent") == "" {
		r = r.Clone(r.Context())
		r.Header.Set("User-Agent", UserAgent())
	}
	return t.base.RoundTrip(r)
}

// InstallUserAgent makes every request sent through http.DefaultTransport,
// which tap's hub clients use, carry UserAgent. Call it once from main.
func InstallUserAgent() {
	if _, done := http.DefaultTransport.(userAgentTransport); !done {
		http.DefaultTransport = userAgentTransport{base: http.DefaultTransport}
	}
}
