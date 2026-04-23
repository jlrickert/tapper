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
	"fmt"
	"strings"
	"time"
)

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

	if opts.Hub == "" && store.IsEmpty() {
		return &AuthStatusResult{
			Present:   false,
			Formatted: "No hub logins stored. Run: tap auth login --hub URL\n",
		}, nil
	}

	var hub string
	switch {
	case opts.Hub != "":
		hub = CanonicalHubURL(opts.Hub)
	default:
		hubs := store.Hubs()
		if len(hubs) == 1 {
			hub = hubs[0]
		} else {
			// len == 0 is handled by the earlier IsEmpty branch; the
			// only way to land here is len > 1.
			return nil, fmt.Errorf("--hub is required when multiple hubs are stored (found: %v)", hubs)
		}
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
