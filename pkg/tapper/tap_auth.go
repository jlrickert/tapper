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

	// TokenSuffix is the last 4 chars of the access token, prefixed
	// with "..." — or "[set]" when the token is shorter than 4 chars.
	// The raw token is never exposed here.
	TokenSuffix string

	// TokenType mirrors the stored entry (e.g. "Bearer"). Empty when
	// the hub returned a bare token with no type.
	TokenType string

	// Scope mirrors the stored entry. Empty when no scope was granted.
	Scope string

	// ExpiresAt mirrors the stored entry; zero when no expiry is known.
	ExpiresAt time.Time

	// ExpiryStatus is a three-way tag: "unknown" | "valid" | "expired".
	// Made explicit so callers (and future JSON consumers) don't have
	// to re-derive from ExpiresAt vs clock.
	ExpiryStatus string

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

	// Redact to the last four chars. Inline because a single call site
	// doesn't justify a shared utility — per scanner guidelines, wait
	// for a second redaction site before abstracting.
	tokenSuffix := "[set]"
	if n := len(entry.AccessToken); n >= 4 {
		tokenSuffix = "..." + entry.AccessToken[n-4:]
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

	scopeDisplay := entry.Scope
	if scopeDisplay == "" {
		scopeDisplay = "(none)"
	}

	tokenTypeDisplay := entry.TokenType
	if tokenTypeDisplay == "" {
		tokenTypeDisplay = "unknown"
	}

	// Formatted is load-bearing: CLI and MCP both emit it verbatim, and
	// the parity test asserts byte-equality. Any change here must update
	// both surface tests in lockstep.
	var b strings.Builder
	fmt.Fprintf(&b, "Logged in to %s\n", hub)
	fmt.Fprintf(&b, "  token: %s (%s)\n", tokenSuffix, tokenTypeDisplay)
	fmt.Fprintf(&b, "  scope: %s\n", scopeDisplay)
	fmt.Fprintf(&b, "  expires: %s\n", renderExpiry(expiryStatus, entry.ExpiresAt, now))

	return &AuthStatusResult{
		Present:      true,
		HubURL:       hub,
		TokenSuffix:  tokenSuffix,
		TokenType:    entry.TokenType,
		Scope:        entry.Scope,
		ExpiresAt:    entry.ExpiresAt,
		ExpiryStatus: expiryStatus,
		Formatted:    b.String(),
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
