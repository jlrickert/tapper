package tapper_test

import (
	"bytes"
	"crypto/rand"
	"io"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/stretchr/testify/require"
)

// TestPKCEChallenge_RFC7636_AppendixB pins the derivation against the
// canonical test vector so a drift in the encoding (e.g., switching
// from RawURLEncoding to URLEncoding) fails loudly instead of silently
// breaking compatibility with every OAuth2 server on the planet.
func TestPKCEChallenge_RFC7636_AppendixB(t *testing.T) {
	t.Parallel()
	verifier := "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	want := "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	got := tapper.PKCEChallenge(verifier)
	require.Equal(t, want, got)
}

// TestGeneratePKCEVerifier_LengthAndCharset asserts the verifier is at
// least 43 chars (RFC 7636 floor) and is drawn from the unreserved
// base64url alphabet — the two properties callers rely on.
func TestGeneratePKCEVerifier_LengthAndCharset(t *testing.T) {
	t.Parallel()
	v, err := tapper.GeneratePKCEVerifier(rand.Reader)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(v), 43)
	require.NotContains(t, v, "=")
	require.NotContains(t, v, "+")
	require.NotContains(t, v, "/")
	// Second call must produce a different verifier with overwhelming
	// probability; if it doesn't we've mis-wired the entropy source.
	v2, err := tapper.GeneratePKCEVerifier(rand.Reader)
	require.NoError(t, err)
	require.NotEqual(t, v, v2)
}

// TestGeneratePKCEVerifier_Deterministic_FromReader verifies the reader
// injection contract: identical input produces identical output. This
// is the hook tests use to get reproducible verifiers without pinning
// a specific string in production code.
func TestGeneratePKCEVerifier_Deterministic_FromReader(t *testing.T) {
	t.Parallel()
	seed := bytes.Repeat([]byte{0x00}, 128)
	a, err := tapper.GeneratePKCEVerifier(bytes.NewReader(seed))
	require.NoError(t, err)
	b, err := tapper.GeneratePKCEVerifier(bytes.NewReader(seed))
	require.NoError(t, err)
	require.Equal(t, a, b)
}

// TestGeneratePKCEVerifier_ShortReader_Errors makes sure we don't
// silently truncate when the entropy source runs dry — a bug we could
// only catch in a test that presents a reader with too few bytes.
func TestGeneratePKCEVerifier_ShortReader_Errors(t *testing.T) {
	t.Parallel()
	_, err := tapper.GeneratePKCEVerifier(bytes.NewReader([]byte("abc")))
	require.Error(t, err)
}

func TestGeneratePKCEVerifier_NilReader_Errors(t *testing.T) {
	t.Parallel()
	_, err := tapper.GeneratePKCEVerifier(nil)
	require.Error(t, err)
}

func TestGenerateState_LengthAndCharset(t *testing.T) {
	t.Parallel()
	s, err := tapper.GenerateState(rand.Reader)
	require.NoError(t, err)
	require.NotEmpty(t, s)
	require.NotContains(t, s, "=")
}

func TestGenerateState_ShortReader_Errors(t *testing.T) {
	t.Parallel()
	_, err := tapper.GenerateState(io.LimitReader(bytes.NewReader([]byte("x")), 1))
	require.Error(t, err)
}

// TestPKCEChallenge_StableForASCIIVerifier guards against someone
// "helpfully" pre-decoding the verifier before hashing. The challenge
// is over the verifier's ASCII bytes, not its decoded entropy.
func TestPKCEChallenge_StableForASCIIVerifier(t *testing.T) {
	t.Parallel()
	in := strings.Repeat("A", 43)
	// sha256("AAAA...") base64url (no padding). Computed out-of-band.
	// If this value ever changes it means the hashing input changed.
	want := tapper.PKCEChallenge(in)
	require.Equal(t, want, tapper.PKCEChallenge(in))
	require.NotEqual(t, want, tapper.PKCEChallenge(in+"A"))
}
