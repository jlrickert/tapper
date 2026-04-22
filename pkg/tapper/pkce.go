// Package tapper — PKCE (RFC 7636) primitives and CSRF state helpers.
//
// These are the pure, dependency-free building blocks for the hub auth
// flow. They accept an io.Reader so tests can inject a
// deterministic entropy source; production callers pass crypto/rand.Reader.
//
// Why kept separate from auth_flow.go: PKCE math has well-known RFC 7636
// test vectors we want to pin in isolation. A bug in the challenge
// derivation would otherwise hide behind the full login flow's mocks.
package tapper

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
)

// pkceVerifierByteLen is the raw-byte length fed into base64url before
// the verifier is emitted. 32 bytes → 43 base64url chars (no padding),
// which is both the RFC 7636 minimum and comfortably above its 256-bit
// entropy recommendation.
const pkceVerifierByteLen = 32

// pkceVerifierMinChars is RFC 7636's lower bound on verifier length.
// GeneratePKCEVerifier guarantees at least this many characters.
const pkceVerifierMinChars = 43

// stateMinByteLen is the raw-byte lower bound for the CSRF state token.
// 16 bytes (128 bits) is the commonly cited floor; we emit base64url
// without padding, giving 22 characters.
const stateMinByteLen = 16

// GeneratePKCEVerifier returns a fresh RFC 7636 code_verifier drawn from
// reader. The verifier is base64url-encoded without padding so it is
// URL-safe and matches the wire format used by the challenge helper.
//
// reader is injectable so tests can seed a deterministic source; the
// normal caller passes crypto/rand.Reader. Any short read is surfaced as
// an error — we never quietly fall back to a weaker source.
func GeneratePKCEVerifier(reader io.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("pkce: reader is nil")
	}
	buf := make([]byte, pkceVerifierByteLen)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", fmt.Errorf("pkce: read entropy: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(buf)
	// Defensive check — 32 bytes always encodes to 43 chars, but we
	// still assert the RFC lower bound so future tuning of
	// pkceVerifierByteLen can't silently produce an invalid verifier.
	if len(verifier) < pkceVerifierMinChars {
		return "", fmt.Errorf("pkce: verifier too short (%d < %d)", len(verifier), pkceVerifierMinChars)
	}
	return verifier, nil
}

// PKCEChallenge derives the S256 code_challenge for a given verifier:
// base64url(SHA-256(verifier)), no padding. Per RFC 7636 §4.2, the hash
// is taken over the ASCII bytes of the verifier string itself — not over
// the raw entropy the verifier was encoded from.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// GenerateState returns a CSRF state token drawn from reader. It is
// base64url-encoded (no padding) so it's safe to round-trip through a
// URL query string without further escaping.
//
// We don't expose the byte length as a knob: 16 bytes is the minimum
// that's both conventional and plenty for a short-lived state value,
// and callers that need more entropy can trivially generate their own.
func GenerateState(reader io.Reader) (string, error) {
	if reader == nil {
		return "", fmt.Errorf("pkce: reader is nil")
	}
	buf := make([]byte, stateMinByteLen)
	if _, err := io.ReadFull(reader, buf); err != nil {
		return "", fmt.Errorf("pkce: read state entropy: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
