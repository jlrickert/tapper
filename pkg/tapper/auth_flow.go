// Package tapper — browser-based OAuth2 PKCE login flow for hub auth.
//
// AuthLogin drives the end-to-end handshake:
//
//  1. discover the hub's authorize/token endpoints via RFC 8414
//     (/.well-known/oauth-authorization-server)
//  2. generate PKCE verifier/challenge + CSRF state
//  3. bind a loopback listener on 127.0.0.1:0 and derive redirect_uri
//  4. open the user's browser to the discovered authorization endpoint
//  5. accept exactly one callback, validate state, extract the code
//  6. exchange the code at the discovered token endpoint for an access
//     token
//
// The flow returns a populated AuthEntry on success. It never touches
// the on-disk AuthStore — persistence is the caller's job — so callers
// can compose the flow into a dry-run, an inspect command, or the
// `tap auth login` CLI path without the flow knowing which.
//
// All external dependencies (listener, browser opener, HTTP client,
// entropy source) are injected via AuthLoginOptions and default to
// production values when nil. That keeps the flow trivially testable
// against an httptest.Server mock hub.
package tapper

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// defaultAuthTimeout caps how long we wait for the user to complete the
// browser handshake. Two minutes is long enough for a password manager
// round-trip and short enough that a hung flow doesn't strand the CLI.
const defaultAuthTimeout = 2 * time.Minute

// AuthLoginOptions is the dependency envelope for AuthLogin. All nil /
// zero values use production defaults so the CLI caller can pass just
// HubURL + ClientID; tests fill in the injectable hooks.
type AuthLoginOptions struct {
	// HubURL is the hub base (e.g. "https://hub.example.com"). Required.
	// Trailing slashes are stripped; http/https schemes are enforced.
	HubURL string

	// ClientID is the OAuth2 client identifier to send to the hub. Required.
	ClientID string

	// Scope is an optional space-separated scope string. Omitted from
	// the authorize request when empty.
	Scope string

	// Timeout bounds the whole handshake; zero uses defaultAuthTimeout.
	Timeout time.Duration

	// ListenerFactory returns the loopback listener that receives the
	// /callback request. Default: net.Listen("tcp", "127.0.0.1:0").
	// Injection lets tests pin a specific address or inspect the listener.
	ListenerFactory func() (net.Listener, error)

	// BrowserOpener opens authURL in the user's browser. Default:
	// openBrowser (platform-specific exec). Tests substitute a goroutine
	// that drives authURL via http.Get.
	BrowserOpener func(ctx context.Context, rt *toolkit.Runtime, url string) error

	// HTTPClient executes the metadata GET and token POST. Default: http.DefaultClient.
	HTTPClient *http.Client

	// RandReader is the entropy source for verifier + state. Default:
	// crypto/rand.Reader. Never swap this for math/rand in production.
	RandReader io.Reader
}

// withDefaults returns a copy of o with all nil/zero injectable
// dependencies replaced by production defaults. Value receiver + value
// return by design — the caller's struct is never mutated. Callers
// (AuthLogin) guard the runtime dependency on the public entry point,
// so no rt parameter is threaded here: openBrowser receives rt as a
// normal argument when it's eventually called, not as a bound default.
func (o AuthLoginOptions) withDefaults() AuthLoginOptions {
	if o.Timeout <= 0 {
		o.Timeout = defaultAuthTimeout
	}
	if o.RandReader == nil {
		o.RandReader = rand.Reader
	}
	if o.HTTPClient == nil {
		o.HTTPClient = http.DefaultClient
	}
	if o.ListenerFactory == nil {
		o.ListenerFactory = func() (net.Listener, error) {
			return net.Listen("tcp", "127.0.0.1:0")
		}
	}
	if o.BrowserOpener == nil {
		o.BrowserOpener = openBrowser
	}
	return o
}

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
}

// AuthLogin runs the browser-based PKCE flow against the hub described
// by opts and returns a populated AuthEntry. The store is not touched —
// the caller persists the result via AuthStore.Set + Save.
func AuthLogin(ctx context.Context, rt *toolkit.Runtime, opts AuthLoginOptions) (*AuthEntry, error) {
	if rt == nil {
		return nil, fmt.Errorf("auth login: runtime is required")
	}
	if strings.TrimSpace(opts.HubURL) == "" {
		return nil, fmt.Errorf("auth login: hub URL is required")
	}
	if strings.TrimSpace(opts.ClientID) == "" {
		return nil, fmt.Errorf("auth login: client ID is required")
	}

	hubURL, err := normalizeHubURL(opts.HubURL)
	if err != nil {
		return nil, err
	}

	opts = opts.withDefaults()

	metadata, err := discoverAuthServerMetadata(ctx, opts.HTTPClient, hubURL)
	if err != nil {
		return nil, err
	}

	verifier, err := GeneratePKCEVerifier(opts.RandReader)
	if err != nil {
		return nil, fmt.Errorf("auth login: %w", err)
	}
	challenge := PKCEChallenge(verifier)
	state, err := GenerateState(opts.RandReader)
	if err != nil {
		return nil, fmt.Errorf("auth login: %w", err)
	}

	listener, err := opts.ListenerFactory()
	if err != nil {
		return nil, fmt.Errorf("auth login: bind loopback listener: %w", err)
	}
	// closeListener is idempotent-ish; net.Listener.Close returns an
	// error on repeated calls but we deliberately ignore it — by the
	// time we hit the deferred close, any useful error has been handled
	// on the path that led here.
	defer func() { _ = listener.Close() }()

	tcpAddr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		return nil, fmt.Errorf("auth login: loopback listener returned non-TCP address %T", listener.Addr())
	}
	redirectURI := fmt.Sprintf("http://127.0.0.1:%d/callback", tcpAddr.Port)

	authURL, err := buildAuthorizeURL(metadata.AuthorizationEndpoint, opts.ClientID, redirectURI, challenge, state, opts.Scope)
	if err != nil {
		return nil, err
	}

	if err := opts.BrowserOpener(ctx, rt, authURL); err != nil {
		return nil, fmt.Errorf("auth login: open browser: %w", err)
	}

	code, err := awaitCallback(ctx, listener, state, opts.Timeout)
	if err != nil {
		return nil, err
	}

	tok, err := exchangeCode(ctx, opts.HTTPClient, metadata.TokenEndpoint, opts.ClientID, code, verifier, redirectURI)
	if err != nil {
		return nil, err
	}

	entry := &AuthEntry{
		AccessToken: tok.AccessToken,
		TokenType:   tok.TokenType,
		Scope:       tok.Scope,
	}
	if tok.ExpiresIn > 0 {
		entry.ExpiresAt = rt.Clock().Now().Add(time.Duration(tok.ExpiresIn) * time.Second)
	}
	return entry, nil
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
	if len(md.CodeChallengeMethodsSupported) > 0 {
		supportsS256 := false
		for _, m := range md.CodeChallengeMethodsSupported {
			if m == "S256" {
				supportsS256 = true
				break
			}
		}
		if !supportsS256 {
			return nil, fmt.Errorf("auth login: hub does not advertise PKCE S256 support (advertised: %v)", md.CodeChallengeMethodsSupported)
		}
	}
	return &md, nil
}

// buildAuthorizeURL assembles the authorization endpoint URL with all
// PKCE parameters. Using url.Values guarantees correct percent-encoding
// for the challenge (which contains base64url chars but never + or /).
// The endpoint is taken verbatim from the hub's RFC 8414 metadata so
// non-standard paths (e.g. /authorize vs /oauth/authorize) are honored.
func buildAuthorizeURL(authorizeEndpoint, clientID, redirectURI, challenge, state, scope string) (string, error) {
	u, err := url.Parse(authorizeEndpoint)
	if err != nil {
		return "", fmt.Errorf("auth login: build authorize URL: %w", err)
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	q.Set("state", state)
	if scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// callbackResult is the accept-goroutine's output envelope.
type callbackResult struct {
	code string
	err  error
}

// awaitCallback performs a one-shot Accept on listener, parses the
// single inbound HTTP request, validates the state token (constant-time),
// and returns the authorization code.
//
// Note on time.After: cli-toolkit's Clock interface exposes only Now();
// there is no runtime-scheduled timer primitive. This auth flow bounds
// a wall-clock, user-interactive browser handshake, so the exception
// matches the rationale documented for editor_live.go and
// repo_fs_events.go in CLAUDE.md — real elapsed time is the correct
// measurement here regardless of any frozen test clock.
func awaitCallback(ctx context.Context, listener net.Listener, state string, timeout time.Duration) (string, error) {
	resultCh := make(chan callbackResult, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			resultCh <- callbackResult{err: fmt.Errorf("auth login: accept callback: %w", err)}
			return
		}
		defer func() { _ = conn.Close() }()

		reader := bufio.NewReader(conn)
		req, err := http.ReadRequest(reader)
		if err != nil {
			resultCh <- callbackResult{err: fmt.Errorf("auth login: read callback request: %w", err)}
			return
		}
		defer func() {
			if req.Body != nil {
				_ = req.Body.Close()
			}
		}()

		if req.Method != http.MethodGet {
			writeCallbackResponse(conn, http.StatusMethodNotAllowed, "Method not allowed.")
			resultCh <- callbackResult{err: fmt.Errorf("auth login: callback used method %q", req.Method)}
			return
		}
		if !strings.HasPrefix(req.URL.Path, "/callback") {
			writeCallbackResponse(conn, http.StatusNotFound, "Not found.")
			resultCh <- callbackResult{err: fmt.Errorf("auth login: callback on unexpected path %q", req.URL.Path)}
			return
		}

		gotState := req.URL.Query().Get("state")
		// Constant-time comparison defeats timing-based oracle attacks
		// on the state token — trivial in practice for a short-lived
		// one-shot listener, but the cost of == vs ConstantTimeCompare
		// is nil and it keeps the anti-pattern out of the codebase.
		if subtle.ConstantTimeCompare([]byte(gotState), []byte(state)) != 1 {
			writeCallbackResponse(conn, http.StatusBadRequest, "State mismatch.")
			resultCh <- callbackResult{err: fmt.Errorf("auth login: callback state mismatch")}
			return
		}

		if errParam := req.URL.Query().Get("error"); errParam != "" {
			desc := req.URL.Query().Get("error_description")
			writeCallbackResponse(conn, http.StatusBadRequest, "Authorization denied. You may close this window.")
			if desc != "" {
				resultCh <- callbackResult{err: fmt.Errorf("auth login: authorization error %q: %s", errParam, desc)}
			} else {
				resultCh <- callbackResult{err: fmt.Errorf("auth login: authorization error %q", errParam)}
			}
			return
		}

		code := req.URL.Query().Get("code")
		if code == "" {
			writeCallbackResponse(conn, http.StatusBadRequest, "Missing authorization code.")
			resultCh <- callbackResult{err: fmt.Errorf("auth login: callback missing code")}
			return
		}

		writeCallbackResponse(conn, http.StatusOK, "You may close this window.")
		resultCh <- callbackResult{code: code}
	}()

	select {
	case r := <-resultCh:
		if r.err != nil {
			return "", r.err
		}
		return r.code, nil
	case <-ctx.Done():
		_ = listener.Close()
		return "", ctx.Err()
	case <-time.After(timeout):
		_ = listener.Close()
		return "", fmt.Errorf("auth login: timed out after %s waiting for browser callback", timeout)
	}
}

// writeCallbackResponse sends a minimal HTTP/1.1 response directly on
// the raw connection. We don't use net/http's ResponseWriter because
// we're not running a full http.Server — one-shot Accept keeps the
// lifecycle explicit and avoids a goroutine leak if the hub never
// redirects back.
func writeCallbackResponse(w io.Writer, status int, message string) {
	body := fmt.Sprintf(`<!doctype html><html><body><p>%s</p></body></html>`, message)
	statusText := http.StatusText(status)
	if statusText == "" {
		statusText = "OK"
	}
	_, _ = fmt.Fprintf(w,
		"HTTP/1.1 %d %s\r\nContent-Type: text/html; charset=utf-8\r\nContent-Length: %d\r\nConnection: close\r\n\r\n%s",
		status, statusText, len(body), body)
}

// exchangeCode POSTs the form-encoded token request and decodes the
// JSON response. Non-2xx status codes surface the body in the error so
// hub-side misconfigurations are visible at the CLI.
func exchangeCode(ctx context.Context, client *http.Client, tokenEndpoint, clientID, code, verifier, redirectURI string) (*tokenResponse, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth login: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth login: token request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth login: read token response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("auth login: token endpoint returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var tok tokenResponse
	if err := json.Unmarshal(body, &tok); err != nil {
		return nil, fmt.Errorf("auth login: parse token response: %w", err)
	}
	if tok.AccessToken == "" {
		return nil, fmt.Errorf("auth login: token response missing access_token")
	}
	return &tok, nil
}

// openBrowser launches the platform-native "open URL" command and
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
func openBrowser(ctx context.Context, rt *toolkit.Runtime, rawURL string) error {
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

// fallbackBrowserPrompt surfaces the authorize URL on stderr when we
// can't launch a browser, converting a launch failure into a graceful
// manual-paste path. Returns nil because the flow can still complete
// if the user visits the URL on another device.
func fallbackBrowserPrompt(rt *toolkit.Runtime, rawURL string, cause error) error {
	streams := rt.Stream()
	if streams.Err != nil {
		_, _ = fmt.Fprintf(streams.Err,
			"warning: unable to open browser (%v). Visit this URL to continue:\n  %s\n",
			cause, rawURL)
	}
	return nil
}
