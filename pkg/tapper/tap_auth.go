package tapper

// Tap.AuthStatus reports the login status for a tapper hub stored in the
// on-disk auth store. Unlike most Tap methods this is cross-keg —
// credentials live at the user/state level, not the keg level — so
// AuthStatusOptions intentionally does NOT embed KegTargetOptions.
//
// Formatting lives here (not in the CLI command) so both surfaces — the
// `tap auth status` CLI and the `auth_status` MCP tool — emit a
// byte-identical string. The CLI writes Formatted verbatim to
// rt.Stream().Out and the MCP tool returns it as TextContent. Any
// future renderer (e.g. a JSON output mode) should introduce new fields
// rather than reformatting Formatted.

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// errNoHubsStored is returned by resolveHubTarget when no hub is
// provided and the store is empty. Callers convert this to a soft-
// success result (AuthStatus prints a directed hint; AuthLogout prints
// "No hub logins stored."). It is intentionally unexported — this is a
// resolution-path signal, not a user-facing error.
var errNoHubsStored = errors.New("no hub logins stored")

// resolveHubTarget canonicalizes raw (when non-empty) or auto-resolves
// to the single stored hub when raw is empty. Returns errNoHubsStored
// when the store is empty and a multi-hub error when more than one is
// stored. Shared by AuthStatus and AuthLogout so both commands resolve
// the target hub identically.
func resolveHubTarget(store *AuthStore, raw string) (string, error) {
	if store == nil {
		return "", fmt.Errorf("auth: store is nil")
	}
	if raw != "" {
		return CanonicalHubURL(raw), nil
	}
	hubs := store.Hubs()
	switch len(hubs) {
	case 0:
		return "", errNoHubsStored
	case 1:
		return hubs[0], nil
	default:
		return "", fmt.Errorf("--hub is required when multiple hubs are stored (found: %v)", hubs)
	}
}

// AuthStatusOptions selects which hub to report on. Flat (no
// KegTargetOptions) because auth state is a user-level concern that
// spans kegs — a login is per-hub, not per-keg.
type AuthStatusOptions struct {
	// Hub is a raw or canonical hub URL. When empty and exactly one
	// hub is stored, AuthStatus auto-resolves to that single entry;
	// otherwise an empty Hub with zero or multiple stored hubs surfaces
	// a directed error/empty message (see AuthStatus docs).
	Hub string

	// Offline skips the live whoami probe and reports purely from the
	// on-disk store (no network, no account name). Useful for scripts,
	// air-gapped use, or when an agent must not make outbound calls.
	Offline bool
}

// AuthStatusResult is the pre-formatted human-readable status line
// plus structured fields for the MCP surface and any future renderers.
// Formatted is authoritative: both CLI and MCP emit it verbatim.
type AuthStatusResult struct {
	// Present is false when no matching entry exists (empty store
	// with no --hub, or --hub that isn't in the store).
	Present bool

	// HubURL is the canonical key that matched. Zero-value when the
	// caller omitted --hub and the store is empty.
	HubURL string

	// TokenPrefix is the first 12 chars of the access token followed by
	// "..." — matching the non-secret prefix the hub stores and shows in
	// its account UI — or "[set]" when the token is too short for a
	// meaningful prefix. The raw token is never exposed here.
	TokenPrefix string

	// TokenType mirrors the stored entry (e.g. "Bearer"). Empty when
	// the hub returned a bare token with no type.
	TokenType string

	// Account is the username the hub reported for this token via the
	// live whoami probe. Empty when validation was skipped (--offline) or
	// failed.
	Account string

	// DisplayName is the human name the hub reported alongside Account.
	// Empty when the hub omits it or validation didn't run.
	DisplayName string

	// Valid is true when the live whoami probe confirmed the token. False
	// when the token was rejected, the hub was unreachable, or --offline
	// skipped the check.
	Valid bool

	// ValidationError is the error message from a failed/ skipped probe,
	// empty on success. Lets structured consumers branch without parsing
	// Formatted.
	ValidationError string

	// Scope mirrors the stored entry. Empty when no scope was granted.
	Scope string

	// ExpiresAt mirrors the stored entry; zero when no expiry is known.
	ExpiresAt time.Time

	// ExpiryStatus is a three-way tag: "unknown" | "valid" | "expired".
	// Made explicit so callers (and future JSON consumers) don't have
	// to re-derive from ExpiresAt vs clock.
	ExpiryStatus string

	// LoginMethod records how the credential was obtained: "device" for the
	// OAuth2 browser/device flow (carries client + refresh + token endpoint),
	// "token" for a pasted API token. Lets structured consumers branch without
	// parsing Formatted.
	LoginMethod string

	// Renewable is true when a refresh token is stored — i.e. the access token
	// renews silently on expiry rather than forcing a re-login. Always false
	// for a pasted API token.
	Renewable bool

	// Formatted is the exact string the CLI prints; MCP returns it
	// verbatim as text content. Terminated with a trailing newline.
	Formatted string
}

// AuthStatus reports the login status for a stored hub.
//
// Resolution precedence:
//  1. Empty store AND empty Hub → not-present, directed hint message.
//  2. Hub set → canonicalize and look up exactly that hub.
//  3. Hub empty AND single hub stored → auto-resolve to that hub.
//  4. Hub empty AND multiple hubs stored → error (caller must pick).
//
// Missing entries (hub was provided but not stored) are NOT errors —
// they return a Result{Present: false} with a clear Formatted line.
// That keeps `tap auth status --hub X` usable from scripts without
// requiring caller-side error-matching.
func (t *Tap) AuthStatus(ctx context.Context, opts AuthStatusOptions) (*AuthStatusResult, error) {
	storePath := t.PathService.AuthStorePath()
	store, err := LoadAuthStore(ctx, t.Runtime, storePath)
	if err != nil {
		return nil, err
	}

	hub, err := resolveHubTarget(store, opts.Hub)
	if err != nil {
		if errors.Is(err, errNoHubsStored) {
			// Empty-store soft-success path: unlike AuthLogout this
			// includes a directed hint so first-run users know how to
			// proceed. Preserve the exact string — the parity and CLI
			// tests both assert on it byte-for-byte.
			return &AuthStatusResult{
				Present:   false,
				Formatted: "No hub logins stored. Run: tap auth login --hub URL\n",
			}, nil
		}
		return nil, err
	}

	entry, ok := store.Get(hub)
	if !ok {
		return &AuthStatusResult{
			Present:   false,
			HubURL:    hub,
			Formatted: fmt.Sprintf("No login stored for %s\n", hub),
		}, nil
	}

	// Renew an expired (or nearly expired) OAuth2 access token via its refresh
	// token before reporting, so status reflects a usable session rather than an
	// "expired" one. Best-effort and skipped under --offline; on failure we fall
	// through to the normal expired/rejected rendering below.
	if !opts.Offline {
		next, rerr := refreshAuthStoreEntryIfNeeded(ctx, t.Runtime, store, storePath, hub, hub, entry)
		if next != nil {
			entry = next
		}
		if rerr != nil {
			if logger := t.Runtime.Logger(); logger != nil {
				logger.Debug("auth refresh failed", "hub", hub, "err", rerr)
			}
		}
	}

	// Token prefix, matching the hub exactly: the hub mints `thub_<hex>` and
	// publishes token[:12] as the non-secret, DB-indexed prefix (rendered in
	// the account UI). Showing the same 12-char prefix lets the user correlate
	// the CLI with the hub's token list — the old last-4 suffix could not be.
	// Tokens too short for a meaningful prefix report "[set]" so the raw value
	// never leaks.
	tokenPrefix := "[set]"
	if len(entry.AccessToken) > 12 {
		tokenPrefix = entry.AccessToken[:12] + "..."
	}

	// Three-way coproduct: unknown (no expiry), valid (future), expired (past).
	// Zero-value ExpiresAt means the hub didn't advertise expiry — distinct
	// from an expiry that has passed, and the user should see the difference.
	expiryStatus := "unknown"
	now := t.Runtime.Clock().Now()
	if !entry.ExpiresAt.IsZero() {
		if now.After(entry.ExpiresAt) {
			expiryStatus = "expired"
		} else {
			expiryStatus = "valid"
		}
	}

	tokenTypeDisplay := entry.TokenType
	if tokenTypeDisplay == "" {
		tokenTypeDisplay = "unknown"
	}

	// Bare host for the gh-style header line (gh: "github.com").
	host := hub
	if u, err := url.Parse(hub); err == nil && u.Host != "" {
		host = u.Host
	}

	// Live validation against the hub's whoami probe (skipped by --offline).
	// This turns `status` from a local-store dump into a real "is my token
	// still good?" check, gh-style. It degrades gracefully: a refused token
	// shows "x", an unreachable hub shows "!", and in both cases we still
	// print the cached token/expiry so the user can correlate and diagnose.
	var (
		account    string
		display    string
		valid      bool
		validErr   string
		statusLine string
	)
	switch {
	case opts.Offline:
		// Host is already the header line above; don't repeat it here.
		statusLine = "  Logged in (offline; token not validated)"
	default:
		validateFn := t.AuthValidateFn
		if validateFn == nil {
			validateFn = ValidateToken
		}
		vctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		who, verr := validateFn(vctx, t.Runtime, hub, entry.AccessToken)
		cancel()
		switch {
		case verr == nil:
			valid = true
			account = who.Username
			display = who.DisplayName
			ident := account
			if display != "" {
				ident = fmt.Sprintf("%s (%s)", account, display)
			}
			// Host is the header line above; "as <ident>" avoids printing the
			// hub twice (it read as a duplicate in the old "Logged in to <host>
			// account <ident>" form).
			statusLine = fmt.Sprintf("  ✓ Logged in as %s", ident)
		case errors.Is(verr, ErrTokenRejected):
			validErr = verr.Error()
			// Trim the package "auth: " prefix so the reason reads cleanly;
			// the wrapped message keeps the status code and the what-to-do hint.
			statusLine = fmt.Sprintf("  x Failed to validate token: %s", strings.TrimPrefix(verr.Error(), "auth: "))
		default:
			validErr = verr.Error()
			statusLine = "  ! Could not reach hub to validate token (offline?)"
		}
	}

	// How the credential was obtained, derived from the stored entry: the
	// device flow populates ClientID / RefreshToken / TokenEndpoint, while a
	// pasted API token leaves all three empty (see AuthEntry). The two render
	// differently so the user can tell a renewable OAuth2 session from a
	// static token. Renewable narrows that to "a refresh token is present",
	// which is what actually lets the CLI renew silently on expiry.
	loginMethod := "token"
	methodLine := "  - Method: API token (no expiry)"
	isDevice := entry.ClientID != "" || entry.RefreshToken != "" || entry.TokenEndpoint != ""
	if isDevice {
		loginMethod = "device"
		methodLine = "  - Method: browser (device flow)"
	}
	renewable := entry.RefreshToken != ""
	if renewable {
		// The access token renews silently, so neither a token prefix nor an
		// expiry tells the user anything actionable; note the auto-renew on the
		// Method line so the session never reads as "about to be logged out".
		methodLine += ", renews automatically"
	}

	// Formatted is load-bearing: CLI and MCP both emit it verbatim, and the
	// parity test asserts byte-equality. Any change here must update both
	// surface tests in lockstep. No ANSI color — MCP receives this string too.
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n", host)
	fmt.Fprintf(&b, "%s\n", statusLine)
	fmt.Fprintf(&b, "%s\n", methodLine)
	// The token prefix is only useful for a pasted API token: the hub lists
	// token[:12] in the account UI, so showing the same prefix lets the user
	// correlate the CLI with that list. OAuth2 device-flow access tokens are
	// never shown in the portal, so the prefix would be a dead end — omit the
	// Token line for the device flow.
	if !isDevice {
		fmt.Fprintf(&b, "  - Token: %s (%s)\n", tokenPrefix, tokenTypeDisplay)
	}
	if entry.Scope != "" {
		// Only personal API tokens are scopeless; an OAuth2 device-flow login
		// may carry scopes worth showing. Space-separated → comma-joined.
		fmt.Fprintf(&b, "  - Scopes: %s\n", strings.Join(strings.Fields(entry.Scope), ", "))
	}
	if expiryStatus != "unknown" && !renewable {
		// A renewable (refresh-token) login renews its access token silently, so
		// the short-lived access-token expiry is noise — "renews automatically"
		// on the Method line already says what matters. Only surface Expires
		// when the credential actually lapses for good: a refresh-less device
		// login (older hub) forces a re-login on expiry. A pasted API token has
		// no expiry and never reaches here.
		fmt.Fprintf(&b, "  - Expires: %s\n", renderExpiry(expiryStatus, entry.ExpiresAt, now))
	}

	return &AuthStatusResult{
		Present:         true,
		HubURL:          hub,
		TokenPrefix:     tokenPrefix,
		TokenType:       entry.TokenType,
		Account:         account,
		DisplayName:     display,
		Valid:           valid,
		ValidationError: validErr,
		Scope:           entry.Scope,
		ExpiresAt:       entry.ExpiresAt,
		ExpiryStatus:    expiryStatus,
		LoginMethod:     loginMethod,
		Renewable:       renewable,
		Formatted:       b.String(),
	}, nil
}

// renderExpiry formats the expires line suffix per the three-way
// ExpiryStatus. Kept package-private because the format is coupled to
// AuthStatus — a JSON renderer would go through the structured fields,
// not this helper.
func renderExpiry(status string, expiresAt, now time.Time) string {
	switch status {
	case "unknown":
		return "unknown"
	case "expired":
		// Round to nearest minute so the output is stable across
		// clock jitter in tests; a hub that expires in "3m12s" and
		// one that expires in "3m13s" should render identically.
		d := now.Sub(expiresAt).Round(time.Minute)
		return fmt.Sprintf("%s (expired %s ago)", expiresAt.UTC().Format(time.RFC3339), d)
	case "valid":
		d := expiresAt.Sub(now).Round(time.Minute)
		return fmt.Sprintf("%s (in %s)", expiresAt.UTC().Format(time.RFC3339), d)
	default:
		// Defensive: the only producer is AuthStatus; any other value
		// is a programming error, so fall through to "unknown" rather
		// than panicking.
		return "unknown"
	}
}

// AuthRefreshAll renews every stored hub credential whose access token is
// expired or within refreshSkew of expiring, persisting the rotated pairs.
// Entries without a refresh token (pasted API tokens) or without a known
// expiry are skipped. Best-effort by design: failures are logged at debug
// and never returned — this runs on every CLI/MCP startup and a broken hub
// must not block unrelated commands. Cost when all tokens are fresh: one
// file read, zero network calls.
func (t *Tap) AuthRefreshAll(ctx context.Context) {
	rt := t.Runtime
	storePath := t.PathService.AuthStorePath()
	store, err := LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		if logger := rt.Logger(); logger != nil {
			logger.Debug("auth refresh: store load failed", "path", storePath, "err", err)
		}
		return
	}

	for _, hub := range store.Hubs() {
		entry, ok := store.Get(hub)
		if !ok || entry == nil {
			continue
		}
		_, rerr := refreshAuthStoreEntryIfNeeded(ctx, rt, store, storePath, hub, hub, entry)
		if rerr != nil {
			if logger := rt.Logger(); logger != nil {
				logger.Debug("auth refresh failed", "hub", hub, "err", rerr)
			}
		}
	}
}

// AuthLogoutOptions selects which hub to log out of. Flat (no
// KegTargetOptions) because auth state is a user-level concern that
// spans kegs — a login is per-hub, not per-keg.
type AuthLogoutOptions struct {
	// Hub is the raw or canonical hub URL. When empty and exactly one
	// hub is stored, auto-resolves to that single entry; otherwise an
	// empty Hub with multiple stored hubs surfaces a directed error.
	Hub string
}

// AuthLogoutResult is the pre-formatted output plus structured fields
// for callers that need to route streams or surface structured output.
// Formatted is authoritative: the CLI emits it verbatim to stdout when
// Removed=true and stderr otherwise.
type AuthLogoutResult struct {
	// Removed is true when an entry was actually deleted from the store.
	// False when the store was empty or the hub was not found; both of
	// those cases are soft-successes (no error returned).
	Removed bool
	// HubURL is the canonical hub that was targeted, or "" when the
	// store was empty.
	HubURL string
	// Formatted is the authoritative output line terminated with \n.
	// CLI routes to stdout when Removed=true, stderr otherwise.
	Formatted string
}

// AuthLogout removes the cached credential for a hub from the on-disk
// auth store. Unlike AuthStatus this method is intentionally NOT
// exposed over MCP — an agent should never be able to yank a user's
// hub token out from under them. The CLI surface is the only consumer.
//
// Resolution precedence mirrors AuthStatus:
//  1. Hub set → canonicalize and delete that hub.
//  2. Hub empty AND single hub stored → auto-resolve to that hub.
//  3. Hub empty AND zero hubs stored → soft-success, "No hub logins stored.".
//  4. Hub empty AND multiple hubs stored → error (caller must pick).
//
// Missing entries (hub was provided but not stored) are NOT errors —
// they return Result{Removed: false} with a clear Formatted line. The
// command is idempotent by design so cleanup scripts can re-run without
// special-casing the already-logged-out state.
func (t *Tap) AuthLogout(ctx context.Context, opts AuthLogoutOptions) (*AuthLogoutResult, error) {
	rt := t.Runtime
	storePath := t.PathService.AuthStorePath()
	store, err := LoadAuthStore(ctx, rt, storePath)
	if err != nil {
		return nil, err
	}

	hub, err := resolveHubTarget(store, opts.Hub)
	if err != nil {
		if errors.Is(err, errNoHubsStored) {
			// Soft-success: nothing to remove. CLI routes this to
			// stderr since Removed is false. The exact string is
			// asserted byte-for-byte by cmd_auth_test.go.
			return &AuthLogoutResult{
				Removed:   false,
				HubURL:    "",
				Formatted: "No hub logins stored.\n",
			}, nil
		}
		return nil, err
	}

	if !store.Delete(hub) {
		// Already absent — soft-success, not an error. Communicate
		// on stderr (see CLI routing) so stdout remains clean for
		// scripts that pipe output.
		return &AuthLogoutResult{
			Removed:   false,
			HubURL:    hub,
			Formatted: fmt.Sprintf("No login stored for %s\n", hub),
		}, nil
	}

	// Save removes the file when the store is empty, which leaves the
	// filesystem in the canonical "no credentials" state after the
	// last logout.
	if err := store.Save(ctx, rt, storePath); err != nil {
		return nil, fmt.Errorf("auth logout: save store: %w", err)
	}
	return &AuthLogoutResult{
		Removed:   true,
		HubURL:    hub,
		Formatted: fmt.Sprintf("Logged out of %s\n", hub),
	}, nil
}
