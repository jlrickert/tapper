package tapper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func newTelemetryAccountFixture(t *testing.T) (*sandbox.Sandbox, *Tap) {
	t.Helper()
	fx := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"})
	tap, err := NewTap(TapOptions{Root: "/home/testuser", Runtime: fx.Runtime()})
	require.NoError(t, err)
	return fx, tap
}

// rotatingRefreshServer answers every refresh grant with a new token pair, as
// a hub that rotates refresh tokens single-use does.
func rotatingRefreshServer(t *testing.T) *httptest.Server {
	t.Helper()
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"access_token":"access-rotated-%d","token_type":"Bearer","expires_in":900,"refresh_token":"refresh-rotated-%d"}`, n, n)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func storedEntryForTest(t *testing.T, fx *sandbox.Sandbox, tap *Tap, hubURL string) *AuthEntry {
	t.Helper()
	store, err := LoadAuthStore(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath())
	require.NoError(t, err)
	entry, ok := store.Get(hubURL)
	require.True(t, ok)
	return entry
}

// rotateStoredLoginForTest spends the stored refresh token the way a command
// does when its access token has expired: RefreshHubToken against a rotating
// token endpoint, persisted through the auth store.
func rotateStoredLoginForTest(t *testing.T, fx *sandbox.Sandbox, tap *Tap, hubURL, tokenEndpoint string) {
	t.Helper()
	storePath := tap.PathService.AuthStorePath()
	store, err := LoadAuthStore(fx.Context(), fx.Runtime(), storePath)
	require.NoError(t, err)
	entry, ok := store.Get(hubURL)
	require.True(t, ok)
	before := entry.RefreshToken
	entry.TokenEndpoint = tokenEndpoint
	entry.ExpiresAt = fx.Runtime().Clock().Now().Add(-time.Minute)
	store.Set(hubURL, *entry)
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), storePath))
	_, err = refreshAuthStoreEntryIfNeeded(fx.Context(), fx.Runtime(), store, storePath, hubURL, hubURL, entry)
	require.NoError(t, err)
	require.NotEqual(t, before, storedEntryForTest(t, fx, tap, hubURL).RefreshToken, "the refresh token rotated")
}

// Events spooled while the hub was unreachable are still the same account's
// after its refresh token rotates, so the next process sends them.
func TestInvocationSpoolSurvivesRefreshTokenRotation(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hub := newFakeTelemetryHub(t)
	tokens := rotatingRefreshServer(t)

	hub.failing.Store(true)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-before")
	runTelemetryReporter(t, fx, tap, "tap offline")
	require.Empty(t, hub.commands())

	rotateStoredLoginForTest(t, fx, tap, hub.URL, tokens.URL)

	hub.failing.Store(false)
	r := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "test")
	require.NotNil(t, r)
	require.Eventually(t, func() bool { return len(hub.commands()) == 1 }, 2*time.Second, 5*time.Millisecond)
	r.Close(context.Background())
	require.Equal(t, []string{"tap offline"}, hub.commands())
	require.Len(t, telemetrySpoolDirsForTest(t, fx, tap), 1, "no directory is left behind for the spent credential")
}

// A refresh that started from an entry read before the account key was
// minted must not erase the key from disk.
func TestAuthRefreshKeepsAccountKeyMintedMeanwhile(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	tokens := rotatingRefreshServer(t)
	hubURL := "https://hub.example.com"
	storePath := tap.PathService.AuthStorePath()
	signInForTelemetry(t, fx, tap, hubURL, "refresh-old")
	expired := storedEntryForTest(t, fx, tap, hubURL)
	expired.TokenEndpoint = tokens.URL
	expired.ExpiresAt = fx.Runtime().Clock().Now().Add(-time.Minute)
	require.NoError(t, persistAuthStoreEntry(fx.Context(), fx.Runtime(), storePath, hubURL, expired))

	stale, err := LoadAuthStore(fx.Context(), fx.Runtime(), storePath)
	require.NoError(t, err)
	entry, _ := stale.Get(hubURL)
	require.Empty(t, entry.AccountKey)

	key, err := ensureAuthAccountKey(fx.Context(), fx.Runtime(), storePath, hubURL)
	require.NoError(t, err)
	require.NotEmpty(t, key)

	_, err = refreshAuthStoreEntryIfNeeded(fx.Context(), fx.Runtime(), stale, storePath, hubURL, hubURL, entry)
	require.NoError(t, err)
	stored := storedEntryForTest(t, fx, tap, hubURL)
	require.Equal(t, "refresh-rotated-1", stored.RefreshToken)
	require.Equal(t, key, stored.AccountKey)
}

// An entry written by an earlier release gets a key once, persisted, and
// every later process uses that same key.
func TestEnsureAuthAccountKeyIsMintedOnceAndPersisted(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hubURL := "https://hub.example.com"
	signInForTelemetry(t, fx, tap, hubURL, "refresh")

	first, err := ensureAuthAccountKey(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath(), hubURL)
	require.NoError(t, err)
	second, err := ensureAuthAccountKey(fx.Context(), fx.Runtime(), tap.PathService.AuthStorePath(), hubURL)
	require.NoError(t, err)
	require.NotEmpty(t, first)
	require.Equal(t, first, second)
	require.Equal(t, first, storedEntryForTest(t, fx, tap, hubURL).AccountKey)
	require.Equal(t, "access-refresh", storedEntryForTest(t, fx, tap, hubURL).AccessToken, "minting leaves the credential alone")
}

// A spool directory from an earlier release, keyed by the then-current
// credential, is claimed by the account and sent.
func TestInvocationLegacyCredentialSpoolIsClaimed(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hub := newFakeTelemetryHub(t)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-legacy")
	legacy := invocationTelemetrySpoolDirFor(tap.ConfigService, invocationTelemetryCredentialKey(hub.URL, storedEntryForTest(t, fx, tap, hub.URL)))
	writeSpoolForTest(t, fx, legacy, []InvocationEvent{{Surface: "cli", Command: "tap from-legacy"}})

	r := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "test")
	require.NotNil(t, r)
	require.Eventually(t, func() bool { return len(hub.commands()) == 1 }, 2*time.Second, 5*time.Millisecond)
	r.Close(context.Background())
	require.Equal(t, []string{"tap from-legacy"}, hub.commands())
	_, err := fx.Runtime().Stat(legacy, false)
	require.Error(t, err, "the legacy directory is gone once claimed")
}

// A spool an earlier release keyed by the credential is not stranded when the
// first command of the new release rotates that credential before telemetry
// starts.
func TestInvocationLegacyCredentialSpoolSurvivesRotationOnUpgrade(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hub := newFakeTelemetryHub(t)
	tokens := rotatingRefreshServer(t)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-legacy")
	legacy := invocationTelemetrySpoolDirFor(tap.ConfigService, invocationTelemetryCredentialKey(hub.URL, storedEntryForTest(t, fx, tap, hub.URL)))
	writeSpoolForTest(t, fx, legacy, []InvocationEvent{{Surface: "cli", Command: "tap before-upgrade"}})

	rotateStoredLoginForTest(t, fx, tap, hub.URL, tokens.URL)

	r := NewInvocationReporter(fx.Runtime(), tap.ConfigService, "test")
	require.NotNil(t, r)
	require.Eventually(t, func() bool { return len(hub.commands()) == 1 }, 2*time.Second, 5*time.Millisecond)
	r.Close(context.Background())
	require.Equal(t, []string{"tap before-upgrade"}, hub.commands())
	_, err := fx.Runtime().Stat(legacy, false)
	require.Error(t, err, "the legacy directory is gone once claimed")
}

// Another account's spool is never sent, and is kept while it is recent.
func TestInvocationSpoolOfAnotherLoginIsKeptUnsent(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hub := newFakeTelemetryHub(t)

	hub.failing.Store(true)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-one")
	setAccountKeyForTest(t, fx, tap, hub.URL, "account-one")
	runTelemetryReporter(t, fx, tap, "tap account-one")

	hub.failing.Store(false)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh-two")
	setAccountKeyForTest(t, fx, tap, hub.URL, "account-two")
	runTelemetryReporter(t, fx, tap, "tap account-two")
	time.Sleep(100 * time.Millisecond)
	require.NotContains(t, hub.commands(), "tap account-one")

	one := invocationTelemetrySpoolDirFor(tap.ConfigService, invocationTelemetryKey(hub.URL, "account-one"))
	require.Len(t, readSpoolForTest(t, fx, one), 1, "the other account's recent events are retained")
}

// Directories of accounts that have not spooled anything for a week are
// pruned, and at most invocationTelemetrySpoolDirLimit directories remain.
func TestInvocationSpoolPrunesStaleAndExcessDirectories(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hub := newFakeTelemetryHub(t)
	hub.failing.Store(true)
	signInForTelemetry(t, fx, tap, hub.URL, "refresh")
	setAccountKeyForTest(t, fx, tap, hub.URL, "current")
	root := filepath.Join(tap.PathService.StateRoot, invocationTelemetrySpoolDir)
	now := fx.Runtime().Clock().Now()

	writeAgedSpoolForTest(t, fx, filepath.Join(root, "stale"), now.Add(-8*24*time.Hour))
	for i := range invocationTelemetrySpoolDirLimit + 2 {
		writeAgedSpoolForTest(t, fx, filepath.Join(root, fmt.Sprintf("recent-%02d", i)), now.Add(-time.Duration(20-i)*time.Hour))
	}

	runTelemetryReporter(t, fx, tap, "tap now")

	dirs := telemetrySpoolDirsForTest(t, fx, tap)
	require.Len(t, dirs, invocationTelemetrySpoolDirLimit)
	require.NotContains(t, dirs, "stale")
	require.NotContains(t, dirs, "recent-00", "the oldest directories go first")
	require.Contains(t, dirs, invocationTelemetryKey(hub.URL, "current"), "the current account's spool is kept")
	require.Contains(t, dirs, fmt.Sprintf("recent-%02d", invocationTelemetrySpoolDirLimit+1))
}

func setAccountKeyForTest(t *testing.T, fx *sandbox.Sandbox, tap *Tap, hubURL, key string) {
	t.Helper()
	storePath := tap.PathService.AuthStorePath()
	store, err := LoadAuthStore(fx.Context(), fx.Runtime(), storePath)
	require.NoError(t, err)
	entry, ok := store.Get(hubURL)
	require.True(t, ok)
	entry.AccountKey = key
	store.Set(hubURL, *entry)
	require.NoError(t, store.Save(fx.Context(), fx.Runtime(), storePath))
}

func writeAgedSpoolForTest(t *testing.T, fx *sandbox.Sandbox, dir string, at time.Time) {
	t.Helper()
	raw, err := json.Marshal([]InvocationEvent{{Surface: "cli", Command: "tap aged"}})
	require.NoError(t, err)
	require.NoError(t, fx.Runtime().Mkdir(dir, 0o700, true))
	require.NoError(t, fx.Runtime().AtomicWriteFile(filepath.Join(dir, fmt.Sprintf("%020d-1-1.json", at.UnixNano())), raw, 0o600))
}

func telemetrySpoolDirsForTest(t *testing.T, fx *sandbox.Sandbox, tap *Tap) []string {
	t.Helper()
	entries, err := fx.Runtime().ReadDir(filepath.Join(tap.PathService.StateRoot, invocationTelemetrySpoolDir))
	require.NoError(t, err)
	var dirs []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	return dirs
}

// Two processes that both find a login from an earlier release name it at
// the same time. The auth store lock makes them agree: each one returns the
// key that ends up on disk, so neither spools under a key nothing will read.
func TestEnsureAuthAccountKeyConcurrentMintsAgree(t *testing.T) {
	fx, tap := newTelemetryAccountFixture(t)
	hubURL := "https://hub.example.com"
	storePath := tap.PathService.AuthStorePath()
	signInForTelemetry(t, fx, tap, hubURL, "refresh")

	const processes = 8
	for round := range 10 {
		entry := storedEntryForTest(t, fx, tap, hubURL)
		entry.AccountKey = ""
		store, err := LoadAuthStore(fx.Context(), fx.Runtime(), storePath)
		require.NoError(t, err)
		store.Set(hubURL, *entry)
		require.NoError(t, store.Save(fx.Context(), fx.Runtime(), storePath))

		keys := make([]string, processes)
		errs := make([]error, processes)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for i := range processes {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				keys[i], errs[i] = ensureAuthAccountKey(fx.Context(), fx.Runtime(), storePath, hubURL)
			}()
		}
		close(start)
		wg.Wait()

		stored := storedEntryForTest(t, fx, tap, hubURL).AccountKey
		require.NotEmpty(t, stored)
		for i := range processes {
			require.NoError(t, errs[i])
			require.Equal(t, stored, keys[i], "round %d: process %d returned the key on disk", round, i)
		}
	}
}
