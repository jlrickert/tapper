package tapper

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/relay"
	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// relayAuthHub is a hub that issues OAuth tokens and accepts relays, judging
// every access token by its own clock and records alone.
type relayAuthHub struct {
	srv *httptest.Server

	mu      sync.Mutex
	valid   map[string]bool // access tokens the hub accepts
	refresh string          // the one live refresh token
	issued  int
	revoked bool // the token endpoint refuses every refresh

	tokenPosts atomic.Int32
	dials      chan string // the bearer each accepted dial presented
	reauths    chan string // the token each reauth presented
	// onRefused runs when a dial is refused, before the 401 goes out.
	onRefused func()
}

func newRelayAuthHub(t *testing.T, refresh string) *relayAuthHub {
	t.Helper()
	h := &relayAuthHub{
		valid:   map[string]bool{},
		refresh: refresh,
		dials:   make(chan string, 8),
		reauths: make(chan string, 8),
	}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *relayAuthHub) serve(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/token":
		h.tokenPosts.Add(1)
		_ = r.ParseForm()
		h.mu.Lock()
		defer h.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if h.revoked || r.PostForm.Get("refresh_token") != h.refresh {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
			return
		}
		h.issued++
		access := fmt.Sprintf("thub_fresh_%d", h.issued)
		h.refresh = fmt.Sprintf("rt-%d", h.issued)
		h.valid[access] = true
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": access, "token_type": "Bearer", "expires_in": 3600, "refresh_token": h.refresh,
		})
	case relaycontract.ConnectPath:
		bearer := r.Header.Get("Authorization")
		token := bearer[len("Bearer "):]
		h.mu.Lock()
		ok := h.valid[token]
		onRefused := h.onRefused
		h.mu.Unlock()
		if !ok {
			if onRefused != nil {
				onRefused()
			}
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.dials <- token
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		h.session(r.Context(), conn)
	default:
		http.NotFound(w, r)
	}
}

// session registers the relay with a two-second credential and answers
// every reauth that presents a token the hub issued.
func (h *relayAuthHub) session(ctx context.Context, conn *websocket.Conn) {
	var env relaycontract.Envelope
	if wsjson.Read(ctx, conn, &env) != nil {
		return
	}
	reg, _ := relaycontract.NewEnvelope(relaycontract.TypeRegistered, "", relaycontract.Registered{
		Protocol: relaycontract.ProtocolVersion, Models: []relaycontract.CatalogBinding{}, CredentialExpiresIn: 2,
	})
	if wsjson.Write(ctx, conn, reg) != nil {
		return
	}
	for {
		if wsjson.Read(ctx, conn, &env) != nil {
			return
		}
		if env.Type != relaycontract.TypeReauth {
			continue
		}
		var re relaycontract.Reauth
		if env.Decode(&re) != nil {
			return
		}
		h.reauths <- re.Token
		h.mu.Lock()
		ok := h.valid[re.Token]
		h.mu.Unlock()
		var answer relaycontract.Envelope
		if ok {
			answer, _ = relaycontract.NewEnvelope(relaycontract.TypeReauthed, env.ID, relaycontract.Reauthed{CredentialExpiresIn: 2})
		} else {
			answer, _ = relaycontract.NewEnvelope(relaycontract.TypeError, env.ID, relaycontract.Error{Code: relaycontract.CodeReauthRejected})
		}
		if wsjson.Write(ctx, conn, answer) != nil {
			return
		}
	}
}

// skewedRelayTap is a Tap whose clock runs 60 seconds behind the hub, with
// a stored login whose access token the hub already considers expired but
// whose recorded expiry, read on the skewed clock, is 55 seconds away: past
// refreshSkew, so an expiry-based refresh would not renew it.
func skewedRelayTap(t *testing.T, hub *relayAuthHub) (*Tap, *sandbox.Sandbox, string) {
	t.Helper()
	trueNow := time.Now()
	sb := sandbox.NewSandbox(t, &sandbox.Options{Home: "/home/testuser", User: "testuser"},
		sandbox.WithClock(trueNow.Add(-60*time.Second)))
	cfg := fmt.Sprintf("hub: test\nhubs:\n  test: {url: %q}\n"+
		"relay:\n  name: laptop\n  providers:\n    ollama: {kind: ollama, baseUrl: %q}\n", hub.srv.URL, hub.srv.URL+"/v1")
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/.config/tapper/config.yaml", []byte(cfg), 0o644))
	tap, err := NewTap(TapOptions{Runtime: sb.Runtime()})
	require.NoError(t, err)

	path := tap.ConfigService.PathService.AuthStorePath()
	store := &AuthStore{}
	store.Set(CanonicalHubURL(hub.srv.URL), AuthEntry{
		AccessToken:   "thub_stale",
		TokenType:     "Bearer",
		ExpiresAt:     trueNow.Add(-5 * time.Second),
		RefreshToken:  "rt-0",
		ClientID:      "tapper-cli",
		TokenEndpoint: hub.srv.URL + "/token",
	})
	require.NoError(t, store.Save(context.Background(), sb.Runtime(), path))
	require.False(t, authEntryNeedsRefresh(sb.Runtime(), &AuthEntry{RefreshToken: "rt-0", ExpiresAt: trueNow.Add(-5 * time.Second)}),
		"precondition: the skewed clock thinks the stale token is still good")
	return tap, sb, path
}

func runRelay(t *testing.T, tap *Tap) (context.CancelFunc, chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- tap.Relay(ctx, RelayOptions{Version: "test"}) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("relay did not stop")
		}
	})
	return cancel, done
}

// Issue 335: with the client clock 60 seconds behind the hub, the relay
// still connects and keeps its session: the refused dial is retried with a
// forced refresh, and the in-session reauth refreshes on the hub's announced
// lifetime, not on the stored expiry.
func TestRelayRefreshesDespiteClockSkew(t *testing.T) {
	hub := newRelayAuthHub(t, "rt-0")
	tap, sb, path := skewedRelayTap(t, hub)
	_, done := runRelay(t, tap)

	select {
	case got := <-hub.dials:
		require.Equal(t, "thub_fresh_1", got, "the relay connects with a token refreshed after the refusal")
	case err := <-done:
		done <- err
		t.Fatalf("relay stopped instead of refreshing: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("relay never connected")
	}
	select {
	case got := <-hub.reauths:
		require.Equal(t, "thub_fresh_2", got,
			"the reauth presents a new token although the local clock gives the current one an hour")
	case <-time.After(3 * time.Second):
		t.Fatal("relay did not reauth before the hub's two-second expiry")
	}

	stored, err := LoadAuthStore(context.Background(), sb.Runtime(), path)
	require.NoError(t, err)
	entry, ok := stored.Get(CanonicalHubURL(hub.srv.URL))
	require.True(t, ok)
	require.Equal(t, "thub_fresh_2", entry.AccessToken, "the forced refresh persists the rotated pair")
	require.Equal(t, "rt-2", entry.RefreshToken)
}

// A forced refresh still never spends a refresh token another process has
// already rotated: when the stored login moved on, it adopts that one.
func TestRelayForcedRefreshAdoptsSiblingRotation(t *testing.T) {
	hub := newRelayAuthHub(t, "rt-0")
	tap, sb, path := skewedRelayTap(t, hub)
	var once sync.Once
	hub.onRefused = func() {
		once.Do(func() {
			// Another tap process refreshed while this one held the stale
			// token.
			hub.mu.Lock()
			hub.valid["thub_sibling"] = true
			hub.refresh = "rt-sibling"
			hub.mu.Unlock()
			store, err := LoadAuthStore(context.Background(), sb.Runtime(), path)
			if err != nil {
				return
			}
			e, _ := store.Get(CanonicalHubURL(hub.srv.URL))
			e.AccessToken, e.RefreshToken = "thub_sibling", "rt-sibling"
			store.Set(CanonicalHubURL(hub.srv.URL), *e)
			_ = store.Save(context.Background(), sb.Runtime(), path)
		})
	}
	runRelay(t, tap)
	select {
	case got := <-hub.dials:
		require.Equal(t, "thub_sibling", got)
	case <-time.After(5 * time.Second):
		t.Fatal("relay never connected")
	}
	require.Equal(t, int32(0), hub.tokenPosts.Load(), "no refresh token spent before the reauth")
}

// Revocation still stops the relay: a refresh the hub refuses is final.
func TestRelayStopsWhenRefreshIsRevoked(t *testing.T) {
	hub := newRelayAuthHub(t, "rt-0")
	hub.revoked = true
	tap, _, _ := skewedRelayTap(t, hub)
	_, done := runRelay(t, tap)
	select {
	case err := <-done:
		require.True(t, errors.Is(err, relay.ErrUnauthorized), "err: %v", err)
		done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("relay kept running on a revoked sign-in")
	}
	require.Equal(t, int32(1), hub.tokenPosts.Load(), "one forced refresh, then final")
}
