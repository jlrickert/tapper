// Package tapper — shared OAuth2 hub helpers.
//
// This file holds the hub-auth building blocks that are independent of any
// single grant type: RFC 8414 authorization-server metadata discovery, hub
// URL normalization/canonicalization, and opening the user's browser. The
// device authorization grant (auth_device_flow.go) composes these; there is
// no longer a loopback/PKCE flow — `tap auth login` standardizes on the
// device flow for browser-based login.
//
// The metadata + browser helpers take their external dependencies (HTTP
// client, runtime) as arguments so the flow stays trivially testable against
// an httptest.Server mock hub.
package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// tokenResponse is the parsed token endpoint JSON reply. Fields mirror
// the RFC 6749 §5.1 success response; ExpiresIn is seconds from now.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
	Scope       string `json:"scope"`
}

// authServerMetadata is the RFC 8414 subset we actually use. The hub
// must advertise both endpoints; we fail closed rather than guessing
// paths because a wrong-URL POST can leak the authorization code to an
// unintended origin.
type authServerMetadata struct {
	AuthorizationEndpoint         string   `json:"authorization_endpoint"`
	TokenEndpoint                 string   `json:"token_endpoint"`
	CodeChallengeMethodsSupported []string `json:"code_challenge_methods_supported,omitempty"`
	// DeviceAuthorizationEndpoint is the RFC 8628 §3.1 endpoint, advertised
	// only by hubs that support the device flow. Empty on older hubs;
	// AuthLoginDevice surfaces a clear error in that case rather than
	// guessing the path.
	DeviceAuthorizationEndpoint string `json:"device_authorization_endpoint,omitempty"`
}

// normalizeHubURL strips trailing slashes from the base URL and enforces
// an http/https scheme. We do not canonicalize the path — hubs may host
// the OAuth endpoints under a mount point we must preserve verbatim.
func normalizeHubURL(raw string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("auth login: parse hub URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("auth login: hub URL must use http or https (got %q)", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("auth login: hub URL is missing a host")
	}
	return trimmed, nil
}

// CanonicalHubURL returns the canonical form used as the AuthStore key.
// It strips trailing slashes, lowercases the scheme and host, and
// preserves the path exactly (per RFC 3986 the path is case-sensitive).
// Invalid URLs fall through to TrimRight'd input so callers don't have
// to pre-validate — they'll hit a clearer error at the login step.
func CanonicalHubURL(s string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(s), "/")
	parsed, err := url.Parse(trimmed)
	if err != nil || parsed.Host == "" {
		return trimmed
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimRight(parsed.String(), "/")
}

// discoverAuthServerMetadata fetches RFC 8414 authorization server
// metadata from the hub. Tapper requires the hub to advertise its
// endpoints explicitly rather than assuming path conventions — a POST
// to a wrong token URL would leak the authorization code to whoever
// happens to own the guessed path.
func discoverAuthServerMetadata(ctx context.Context, client *http.Client, hubURL string) (*authServerMetadata, error) {
	metadataURL := hubURL + "/.well-known/oauth-authorization-server"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metadataURL, nil)
	if err != nil {
		return nil, fmt.Errorf("auth login: build metadata request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth login: fetch oauth metadata: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("auth login: hub returned %s for %s", resp.Status, metadataURL)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth login: read oauth metadata: %w", err)
	}
	var md authServerMetadata
	if err := json.Unmarshal(body, &md); err != nil {
		return nil, fmt.Errorf("auth login: parse oauth metadata: %w", err)
	}
	if md.AuthorizationEndpoint == "" || md.TokenEndpoint == "" {
		return nil, fmt.Errorf("auth login: hub metadata missing authorization_endpoint or token_endpoint")
	}
	return &md, nil
}

// OpenBrowser launches the platform-native "open URL" command and
// returns once the process has been started. We do NOT wait for exit
// because most browsers daemonize and exec.Wait would deadlock the
// CLI behind the user's window manager.
//
// Quarantined stdlib exception (like pkg/tapper/editor_live.go): this
// spawns an external browser process whose timing is governed by the
// user's environment rather than any in-process clock. Routing through
// exec.CommandContext keeps cancellation working if the caller ctx
// is cancelled before the command launches.
//
// On unrecognized platforms or launch failure, we print the URL to
// rt.Stream().Err so the user can copy-paste it manually — that's a
// successful fallback, not a hard error.
func OpenBrowser(ctx context.Context, rt *toolkit.Runtime, rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", rawURL)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", rawURL)
	case "windows":
		// cmd /c start "" <url> — the empty title avoids cmd.exe
		// treating the URL as a window title (a well-known Windows
		// cmd gotcha).
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", rawURL)
	default:
		return fallbackBrowserPrompt(rt, rawURL, fmt.Errorf("no browser opener for GOOS=%s", runtime.GOOS))
	}

	if err := cmd.Start(); err != nil {
		return fallbackBrowserPrompt(rt, rawURL, err)
	}
	// Detach: we do not Wait(). The browser process is expected to
	// outlive this CLI invocation.
	return nil
}

// fallbackBrowserPrompt surfaces the URL on stderr when we can't launch a
// browser, converting a launch failure into a graceful manual-paste path.
// Returns nil because the flow can still complete if the user visits the
// URL on another device.
func fallbackBrowserPrompt(rt *toolkit.Runtime, rawURL string, cause error) error {
	streams := rt.Stream()
	if streams.Err != nil {
		_, _ = fmt.Fprintf(streams.Err,
			"warning: unable to open browser (%v). Visit this URL to continue:\n  %s\n",
			cause, rawURL)
	}
	return nil
}
