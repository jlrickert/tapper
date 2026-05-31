// Package tapper — RFC 8628 OAuth2 Device Authorization Grant.
//
// AuthLoginDevice is the single browser-based login flow behind
// `tap auth login`. It drives the device flow handshake against a hub:
//
//  1. discover the hub's device + token endpoints via RFC 8414
//  2. POST to /oauth/device_authorization with the public client_id
//  3. present the user_code + verification URI — via the caller's
//     OnUserCode handler when set (the CLI opens a browser on Enter,
//     gh-style), otherwise a plain copy/paste prompt to PromptOut
//  4. poll the token endpoint with grant_type=device_code, honoring
//     RFC 8628 §3.5 (authorization_pending, slow_down, access_denied,
//     expired_token)
//  5. return the populated AuthEntry on success
//
// Because the user completes the handshake in a browser keyed to a short
// code, the flow works everywhere — desktops, containers, remote shells —
// without binding any local callback listener.
//
// The store is not touched; the caller persists the result. All injectable
// dependencies (HTTP client, output writer, clock, sleeper, OnUserCode)
// default to production values and tests substitute stubs against an
// httptest mock hub.

package tapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// deviceCodeGrantType matches the RFC 8628 §3.4 literal the hub expects on
// the token endpoint. Spelled out here once so the flow and any tests key
// off the same value.
const deviceCodeGrantType = "urn:ietf:params:oauth:grant-type:device_code"

// defaultDeviceTimeout caps the entire device flow including user-side
// browser action. 10 minutes mirrors the hub's user_code expiry; longer
// would just fail with expired_token on the next poll.
const defaultDeviceTimeout = 10 * time.Minute

// minPollInterval guards against a buggy or malicious hub that advertises
// interval=0 (or omits it). Two seconds is a reasonable floor that won't
// hammer the hub even if its slow_down enforcement is broken.
const minPollInterval = 2 * time.Second

// slowDownIncrement is the additional delay added when the hub responds
// with slow_down per RFC 8628 §3.5. Five seconds matches the spec's
// recommended increment.
const slowDownIncrement = 5 * time.Second

// AuthLoginDeviceOptions is the dependency envelope for AuthLoginDevice.
// Required: HubURL, ClientID. Everything else has a production default.
type AuthLoginDeviceOptions struct {
	// HubURL is the hub base (e.g. "https://hub.example.com"). Required.
	HubURL string

	// ClientID is the OAuth2 public client identifier the hub will validate
	// against its registered clients. Required.
	ClientID string

	// Scope is an optional space-separated scope string. Omitted from the
	// device_authorization request when empty.
	Scope string

	// Timeout bounds the whole flow including the user's browser action;
	// zero uses defaultDeviceTimeout.
	Timeout time.Duration

	// HTTPClient executes the metadata GET, device_authorization POST, and
	// token POSTs. Default: http.DefaultClient.
	HTTPClient *http.Client

	// PromptOut is the writer that receives the user-facing prompt
	// ("Open <URL> and enter <CODE>"). Default: os.Stderr via the runtime
	// stream when Out is nil; an in-memory buffer in tests.
	PromptOut io.Writer

	// OnUserCode, when non-nil, is invoked once the hub has issued the
	// user_code + verification URI and before polling begins. It owns the
	// user-facing presentation: the CLI installs a handler that prints the
	// code, offers to open the browser (gh-style "press Enter to open"),
	// and falls back to printing the URL for manual copy/paste. When nil,
	// the flow prints a plain copy/paste prompt to PromptOut — the library
	// default never opens a browser, keeping pkg/tapper free of any
	// interactive/TTY dependency. A non-nil error aborts the login.
	OnUserCode func(context.Context, DeviceUserPrompt) error

	// Now returns the current time. Default: time.Now. Tests inject a fake
	// clock to control timeout behavior deterministically.
	Now func() time.Time

	// Sleep blocks for d. Default: time.Sleep. Tests inject a no-op or
	// channel-coordinated stub so polling loops don't actually wait.
	Sleep func(d time.Duration)
}

// withDefaults returns a copy of o with all nil/zero injectable fields
// replaced by production defaults.
func (o AuthLoginDeviceOptions) withDefaults() AuthLoginDeviceOptions {
	if o.Timeout <= 0 {
		o.Timeout = defaultDeviceTimeout
	}
	if o.HTTPClient == nil {
		o.HTTPClient = http.DefaultClient
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Sleep == nil {
		o.Sleep = time.Sleep
	}
	return o
}

// DeviceUserPrompt is the subset of the RFC 8628 device_authorization
// response an OnUserCode handler needs to drive the user-facing step. It
// is the exported projection of deviceAuthResponse so callers in other
// packages (the CLI) can present the code without seeing the wire type.
// VerificationURIComplete embeds the user_code in the URL; prefer it when
// non-empty so the website pre-fills the code.
type DeviceUserPrompt struct {
	UserCode                string
	VerificationURI         string
	VerificationURIComplete string
	ExpiresIn               int64
}

// VerificationURL returns the URL the user should open, always including the
// user_code so a copied or auto-opened link pre-fills the code instead of
// forcing the user to type it. It prefers the hub-provided
// verification_uri_complete (RFC 8628 §3.2) and, when the hub omits it (older
// hubs do), appends ?user_code=<code> to verification_uri — matching the
// query parameter the hub's /device page reads.
func (p DeviceUserPrompt) VerificationURL() string {
	if strings.TrimSpace(p.VerificationURIComplete) != "" {
		return p.VerificationURIComplete
	}
	if strings.TrimSpace(p.UserCode) == "" {
		return p.VerificationURI
	}
	sep := "?"
	if strings.Contains(p.VerificationURI, "?") {
		sep = "&"
	}
	return p.VerificationURI + sep + "user_code=" + url.QueryEscape(p.UserCode)
}

// deviceAuthResponse is the parsed RFC 8628 §3.2 device_authorization JSON
// reply. VerificationURIComplete is optional; clients that prefer it should
// fall back to VerificationURI when it's empty.
type deviceAuthResponse struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int64  `json:"expires_in"`
	Interval                int64  `json:"interval"`
}

// tokenErrorResponse is the parsed RFC 6749 §5.2 / RFC 8628 §3.5 error JSON.
// The four device-flow polling errors (authorization_pending, slow_down,
// access_denied, expired_token) are dispatched against the Error field.
type tokenErrorResponse struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description,omitempty"`
}

// AuthLogin runs the device authorization grant against the hub described
// by opts and returns a populated AuthEntry. The store is not touched —
// the caller persists the result via AuthStore.Set + Save, mirroring the
// browser-based AuthLogin contract.
func AuthLoginDevice(ctx context.Context, rt *toolkit.Runtime, opts AuthLoginDeviceOptions) (*AuthEntry, error) {
	if rt == nil {
		return nil, fmt.Errorf("auth login device: runtime is required")
	}
	if strings.TrimSpace(opts.HubURL) == "" {
		return nil, fmt.Errorf("auth login device: hub URL is required")
	}
	if strings.TrimSpace(opts.ClientID) == "" {
		return nil, fmt.Errorf("auth login device: client ID is required")
	}

	hubURL, err := normalizeHubURL(opts.HubURL)
	if err != nil {
		return nil, err
	}

	opts = opts.withDefaults()
	if opts.PromptOut == nil {
		opts.PromptOut = rt.Stream().Err
	}

	metadata, err := discoverAuthServerMetadata(ctx, opts.HTTPClient, hubURL)
	if err != nil {
		return nil, err
	}
	if metadata.DeviceAuthorizationEndpoint == "" {
		return nil, fmt.Errorf("auth login device: hub does not advertise device_authorization_endpoint; the hub may be too old to support the device flow")
	}

	dar, err := requestDeviceAuthorization(ctx, opts.HTTPClient, metadata.DeviceAuthorizationEndpoint, opts.ClientID, opts.Scope)
	if err != nil {
		return nil, err
	}

	// Present the code + verification URI. A caller-supplied OnUserCode
	// owns the interaction (the CLI opens the browser on Enter); otherwise
	// fall back to the plain copy/paste prompt so the library default never
	// reaches for a TTY or a browser.
	prompt := DeviceUserPrompt{
		UserCode:                dar.UserCode,
		VerificationURI:         dar.VerificationURI,
		VerificationURIComplete: dar.VerificationURIComplete,
		ExpiresIn:               dar.ExpiresIn,
	}
	if opts.OnUserCode != nil {
		if err := opts.OnUserCode(ctx, prompt); err != nil {
			return nil, err
		}
	} else {
		printDevicePrompt(opts.PromptOut, prompt)
	}

	tok, err := pollForDeviceToken(ctx, opts, metadata.TokenEndpoint, dar)
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

// requestDeviceAuthorization performs the RFC 8628 §3.1 POST and parses the
// response. Returns a populated deviceAuthResponse on success.
func requestDeviceAuthorization(ctx context.Context, client *http.Client, endpoint, clientID, scope string) (*deviceAuthResponse, error) {
	form := url.Values{}
	form.Set("client_id", clientID)
	if scope != "" {
		form.Set("scope", scope)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("auth login device: build device_authorization request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("auth login device: post device_authorization: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("auth login device: read device_authorization response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var e tokenErrorResponse
		_ = json.Unmarshal(body, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("auth login device: hub returned %s: %s (%s)", resp.Status, e.Error, e.ErrorDescription)
		}
		return nil, fmt.Errorf("auth login device: hub returned %s", resp.Status)
	}

	var dar deviceAuthResponse
	if err := json.Unmarshal(body, &dar); err != nil {
		return nil, fmt.Errorf("auth login device: parse device_authorization response: %w", err)
	}
	if dar.DeviceCode == "" || dar.UserCode == "" || dar.VerificationURI == "" {
		return nil, fmt.Errorf("auth login device: hub response missing required fields (device_code, user_code, verification_uri)")
	}
	return &dar, nil
}

// printDevicePrompt writes the user-facing instructions to out. The format
// matches the gh / aws sso convention so the message is recognizable to
// anyone who has used a device flow before. The URL always carries the code
// (see DeviceUserPrompt.VerificationURL), so visiting it pre-fills the code.
func printDevicePrompt(out io.Writer, p DeviceUserPrompt) {
	_, _ = fmt.Fprintf(out, "\nOpen this URL in a browser to continue (the code is pre-filled):\n\n  %s\n  Code: %s\n\n",
		p.VerificationURL(), p.UserCode)
	if p.ExpiresIn > 0 {
		_, _ = fmt.Fprintf(out, "  (Code expires in %d minutes.)\n\n", p.ExpiresIn/60)
	}
	_, _ = fmt.Fprintf(out, "Waiting for approval...\n")
}

// pollForDeviceToken implements the RFC 8628 §3.5 polling state machine.
// Returns a tokenResponse on access_token issuance; surfaces the documented
// terminal errors (access_denied, expired_token) verbatim. Returns context
// errors on caller cancellation.
func pollForDeviceToken(ctx context.Context, opts AuthLoginDeviceOptions, tokenEndpoint string, dar *deviceAuthResponse) (*tokenResponse, error) {
	interval := time.Duration(dar.Interval) * time.Second
	if interval < minPollInterval {
		interval = minPollInterval
	}

	deadline := opts.Now().Add(opts.Timeout)
	for {
		// Check both the caller's context and our own deadline before each
		// poll. The deadline cap exists in case the caller passed a context
		// without a deadline.
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("auth login device: %w", err)
		}
		if !opts.Now().Before(deadline) {
			return nil, fmt.Errorf("auth login device: timed out after %s waiting for browser approval", opts.Timeout)
		}

		tok, errCode, err := pollOnce(ctx, opts.HTTPClient, tokenEndpoint, opts.ClientID, dar.DeviceCode)
		if err == nil {
			return tok, nil
		}

		switch errCode {
		case "authorization_pending":
			// Keep polling at the current interval.
		case "slow_down":
			// Per §3.5: bump the polling interval and try again.
			interval += slowDownIncrement
		case "access_denied":
			return nil, fmt.Errorf("auth login device: user denied the request")
		case "expired_token":
			return nil, fmt.Errorf("auth login device: device_code expired before approval; run the command again")
		default:
			// Any other error (transport, invalid_grant, server_error)
			// bubbles up immediately — recoverable cases are the four
			// codes named above.
			return nil, err
		}

		opts.Sleep(interval)
	}
}

// pollOnce performs a single token endpoint POST and classifies the result.
// On success, returns (token, "", nil). On a documented polling error,
// returns (nil, error_code, err). On any other failure, returns (nil, "",
// err). The error_code is the discriminator the caller's polling loop
// dispatches on.
func pollOnce(ctx context.Context, client *http.Client, tokenEndpoint, clientID, deviceCode string) (*tokenResponse, string, error) {
	form := url.Values{}
	form.Set("grant_type", deviceCodeGrantType)
	form.Set("device_code", deviceCode)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, "", fmt.Errorf("auth login device: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("auth login device: post token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("auth login device: read token response: %w", err)
	}

	if resp.StatusCode == http.StatusOK {
		var tok tokenResponse
		if err := json.Unmarshal(body, &tok); err != nil {
			return nil, "", fmt.Errorf("auth login device: parse token response: %w", err)
		}
		if tok.AccessToken == "" {
			return nil, "", fmt.Errorf("auth login device: token response missing access_token")
		}
		return &tok, "", nil
	}

	var e tokenErrorResponse
	if err := json.Unmarshal(body, &e); err != nil || e.Error == "" {
		return nil, "", fmt.Errorf("auth login device: hub returned %s", resp.Status)
	}
	return nil, e.Error, errors.New(e.ErrorDescription)
}
