package relay

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// handshakeV4 reads register and answers registered on protocol with the
// credential lifetime expiresIn (seconds).
func handshakeV4(t *testing.T, ctx context.Context, c *websocket.Conn, protocol, expiresIn int) {
	t.Helper()
	env := readEnv(t, ctx, c)
	if env.Type != relaycontract.TypeRegister {
		t.Fatalf("first frame = %q, want register", env.Type)
	}
	writeEnv(t, ctx, c, relaycontract.TypeRegistered, "", relaycontract.Registered{
		Protocol: protocol, Models: []relaycontract.CatalogBinding{}, CredentialExpiresIn: expiresIn,
	})
}

// readFrame reads one frame, or reports false when none arrives within wait.
func readFrame(t *testing.T, c *websocket.Conn, wait time.Duration) (relaycontract.Envelope, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	defer cancel()
	var env relaycontract.Envelope
	if err := wsjson.Read(ctx, c, &env); err != nil {
		if ctx.Err() != nil {
			return env, false
		}
		t.Fatalf("read frame: %v", err)
	}
	return env, true
}

// awaitReauth reads frames until a reauth, failing if none arrives before
// within: the hub would have ended the session by then.
func awaitReauth(t *testing.T, c *websocket.Conn, within time.Duration) (relaycontract.Envelope, relaycontract.Reauth) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		env, ok := readFrame(t, c, time.Until(deadline))
		if !ok {
			t.Fatalf("no reauth within %s; the hub would have expired the session", within)
		}
		if env.Type != relaycontract.TypeReauth {
			continue
		}
		var r relaycontract.Reauth
		if err := env.Decode(&r); err != nil {
			t.Fatal(err)
		}
		return env, r
	}
}

// refreshingHub is a hub whose Refresh hands out fresh-1, fresh-2, …
func refreshingHub(hub *fakeHub, token string, refreshes *atomic.Int32) Hub {
	h := hubFor(hub, token)
	h.Refresh = func(context.Context) (string, error) {
		return "fresh-" + string(rune('0'+refreshes.Add(1))), nil
	}
	return h
}

// assertNoRedial fails if the relay dials the hub again within wait.
func assertNoRedial(t *testing.T, hub *fakeHub, wait time.Duration) {
	t.Helper()
	select {
	case got := <-hub.auth:
		t.Fatalf("relay redialed (%s); the session should have continued", got)
	case <-time.After(wait):
	}
}

// Issue 295: a session whose token lives two seconds stays up across
// several lifetimes, in-flight work included, because the relay reauths
// before each expiry.
func TestReauthKeepsSessionAcrossCredentialExpiry(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	var refreshes atomic.Int32
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Hubs = []Hub{refreshingHub(hub, "tok", &refreshes)}
	})
	ctx := context.Background()
	conn := hub.accept(t)
	<-hub.auth
	const ttl = 2
	handshakeV4(t, ctx, conn, relaycontract.ProtocolVersion, ttl)
	registeredAt := time.Now()

	// A long request in flight across the expiry.
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "long", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[{"role":"user","content":"block"}]}`),
	})
	<-fp.blocked

	for i := 1; i <= 2; i++ {
		env, r := awaitReauth(t, conn, ttl*time.Second)
		if env.ID == "" {
			t.Fatal("reauth has no id")
		}
		if want := "fresh-" + string(rune('0'+i)); r.Token != want {
			t.Fatalf("reauth %d token = %q, want the force-refreshed %q", i, r.Token, want)
		}
		if i == 1 && time.Since(registeredAt) >= ttl*time.Second {
			t.Fatalf("first reauth came %s after registered, at or past the expiry", time.Since(registeredAt))
		}
		writeEnv(t, ctx, conn, relaycontract.TypeReauthed, env.ID, relaycontract.Reauthed{CredentialExpiresIn: ttl})
	}
	assertNoRedial(t, hub, 0)

	// The request that started under the first token is still live on the
	// same connection.
	writeEnv(t, ctx, conn, relaycontract.TypeCancel, "long", nil)
	for {
		env, ok := readFrame(t, conn, 5*time.Second)
		if !ok {
			t.Fatal("in-flight request did not answer its cancel")
		}
		if env.Type == relaycontract.TypeReauth {
			writeEnv(t, ctx, conn, relaycontract.TypeReauthed, env.ID, relaycontract.Reauthed{CredentialExpiresIn: ttl})
			continue
		}
		var e relaycontract.Error
		if env.ID != "long" || env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeCancelled {
			t.Fatalf("frame = %s %s %s, want the in-flight request's cancellation", env.ID, env.Type, env.Payload)
		}
		break
	}
}

// Old hubs keep working: a protocol 3 session, or a protocol 4 session that
// advertises no expiry, never sees a reauth frame it would not understand.
func TestNoReauthWithoutAdvertisedExpiry(t *testing.T) {
	for name, tc := range map[string]struct{ protocol, expiresIn int }{
		"protocol 3":           {3, 1},
		"protocol 4 no expiry": {relaycontract.ProtocolVersion, 0},
	} {
		t.Run(name, func(t *testing.T) {
			_, psrv := newFakeProvider(t, "qwen3:8b")
			hub := newFakeHub(t)
			var refreshes atomic.Int32
			startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
				o.Hubs = []Hub{refreshingHub(hub, "tok", &refreshes)}
			})
			ctx := context.Background()
			conn := hub.accept(t)
			handshakeV4(t, ctx, conn, tc.protocol, tc.expiresIn)
			if env, ok := readFrame(t, conn, 1500*time.Millisecond); ok && env.Type == relaycontract.TypeReauth {
				t.Fatalf("relay sent reauth on a %s session", name)
			}
			writeEnv(t, ctx, conn, relaycontract.TypePing, "", nil)
			for {
				env := readEnv(t, ctx, conn)
				if env.Type == relaycontract.TypeReauth {
					t.Fatalf("relay sent reauth on a %s session", name)
				}
				if env.Type == relaycontract.TypePong {
					break
				}
			}
			if n := refreshes.Load(); n != 0 {
				t.Fatalf("relay refreshed %d times with no expiry to beat", n)
			}
		})
	}
}

// A rejected reauth does not end the session: the old credential stands
// until Hub expires it.
func TestRejectedReauthKeepsSession(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	var refreshes atomic.Int32
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Hubs = []Hub{refreshingHub(hub, "tok", &refreshes)}
	})
	ctx := context.Background()
	conn := hub.accept(t)
	<-hub.auth
	handshakeV4(t, ctx, conn, relaycontract.ProtocolVersion, 2)
	env, _ := awaitReauth(t, conn, 2*time.Second)
	writeEnv(t, ctx, conn, relaycontract.TypeError, env.ID, relaycontract.Error{Code: relaycontract.CodeReauthRejected, Message: "token is for another account"})
	writeEnv(t, ctx, conn, relaycontract.TypePing, "", nil)
	if got := readEnv(t, ctx, conn); got.Type != relaycontract.TypePong {
		t.Fatalf("frame = %q, want pong on the same session", got.Type)
	}
	assertNoRedial(t, hub, 200*time.Millisecond)
}

// recordLogs is a slog handler that keeps every message.
type recordLogs struct {
	mu   sync.Mutex
	msgs []string
}

func (r *recordLogs) Enabled(context.Context, slog.Level) bool { return true }
func (r *recordLogs) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	r.msgs = append(r.msgs, rec.Message)
	r.mu.Unlock()
	return nil
}
func (r *recordLogs) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *recordLogs) WithGroup(string) slog.Handler      { return r }

func (r *recordLogs) has(msg string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, m := range r.msgs {
		if m == msg {
			return true
		}
	}
	return false
}

// Issue 334: every end code Hub can send has its own outcome. The final
// ones stop the relay with their own error, each naming what the user can
// do; the retryable ones redial, each logging its own reason. An owner
// disconnect still says so.
func TestEndCodesHaveDistinctOutcomes(t *testing.T) {
	final := map[string]struct {
		want    error
		mention string
	}{
		relaycontract.CodeDisconnected:      {ErrDisconnected, "owner"},
		relaycontract.CodeCredentialRevoked: {ErrRevoked, "tap auth login"},
	}
	messages := map[string]string{}
	for code, tc := range final {
		t.Run(code, func(t *testing.T) {
			_, psrv := newFakeProvider(t, "qwen3:8b")
			hub := newFakeHub(t)
			var refreshes atomic.Int32
			_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
				o.Hubs = []Hub{refreshingHub(hub, "tok", &refreshes)}
			})
			ctx := context.Background()
			conn := hub.accept(t)
			<-hub.auth
			handshakeV4(t, ctx, conn, relaycontract.ProtocolVersion, 3600)
			writeEnv(t, ctx, conn, relaycontract.TypeError, "", relaycontract.Error{Code: code, Message: "bye"})
			select {
			case err := <-done:
				if !errors.Is(err, tc.want) {
					t.Fatalf("Run = %v, want %v", err, tc.want)
				}
				if !strings.Contains(err.Error(), tc.mention) {
					t.Fatalf("message %q does not mention %q", err, tc.mention)
				}
				messages[code] = err.Error()
				done <- err
			case <-time.After(5 * time.Second):
				t.Fatalf("relay kept running after %s", code)
			}
			assertNoRedial(t, hub, 200*time.Millisecond)
		})
	}
	if messages[relaycontract.CodeDisconnected] == messages[relaycontract.CodeCredentialRevoked] {
		t.Fatal("owner disconnect and revocation read the same")
	}
	if strings.Contains(messages[relaycontract.CodeCredentialRevoked], "owner") {
		t.Fatalf("revocation reported as an owner disconnect: %q", messages[relaycontract.CodeCredentialRevoked])
	}

	retry := map[string]struct {
		log         string
		withinFirst time.Duration // a redial within this, with no backoff
		wantToken   string
	}{
		relaycontract.CodeCredentialExpired: {"relay credential expired; reconnecting with a fresh token", minBackoff / 2, "Bearer fresh-1"},
		relaycontract.CodeGoingAway:         {"hub is restarting; reconnecting", minBackoff + 200*time.Millisecond, "Bearer tok"},
		"some_future_code":                  {"relay disconnected; reconnecting", 5 * time.Second, "Bearer tok"},
	}
	logs := map[string]bool{}
	for code, tc := range retry {
		t.Run(code, func(t *testing.T) {
			_, psrv := newFakeProvider(t, "qwen3:8b")
			hub := newFakeHub(t)
			rec := &recordLogs{}
			var refreshes atomic.Int32
			_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
				o.Hubs = []Hub{refreshingHub(hub, "tok", &refreshes)}
				o.Logger = slog.New(rec)
			})
			ctx := context.Background()
			conn := hub.accept(t)
			<-hub.auth
			handshakeV4(t, ctx, conn, relaycontract.ProtocolVersion, 3600)
			time.Sleep(minBackoff + 100*time.Millisecond)
			writeEnv(t, ctx, conn, relaycontract.TypeError, "", relaycontract.Error{Code: code, Message: "later"})
			_ = conn.Close(websocket.StatusTryAgainLater, code)
			select {
			case got := <-hub.auth:
				if got != tc.wantToken {
					t.Fatalf("redial auth = %q, want %q", got, tc.wantToken)
				}
			case err := <-done:
				done <- err
				t.Fatalf("relay stopped after %s: %v", code, err)
			case <-time.After(tc.withinFirst):
				t.Fatalf("relay did not redial within %s after %s", tc.withinFirst, code)
			}
			if !rec.has(tc.log) {
				t.Fatalf("logs %v, want %q", rec.msgs, tc.log)
			}
			logs[tc.log] = true
			handshake(t, ctx, hub.accept(t))
		})
	}
	if len(logs) != len(retry) {
		t.Fatalf("retryable end codes share log messages: %v", logs)
	}
}

// Issue 335: Hub refuses a token the relay's clock thinks is still good.
// One forced refresh decides: the fresh token connects.
func TestRefusedDialRetriesOnceWithForcedRefresh(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	var refreshes atomic.Int32
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Hubs = []Hub{refreshingHub(hub, "revoked", &refreshes)}
	})
	select {
	case got := <-hub.auth:
		if got != "Bearer fresh-1" {
			t.Fatalf("auth = %q, want the force-refreshed token", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay gave up instead of retrying with a refreshed token")
	}
	handshake(t, context.Background(), hub.accept(t))
}

// Revocation still stops the relay: a refreshed token Hub refuses too is
// final after exactly one retry, and a refresh Hub rejects outright is
// final at once.
func TestRevokedSignInStillStopsTheRelay(t *testing.T) {
	cases := map[string]func(context.Context) (string, error){
		"refused again": func(context.Context) (string, error) { return "revoked", nil },
		"refresh rejected": func(context.Context) (string, error) {
			return "", errors.Join(ErrUnauthorized, errors.New("invalid_grant"))
		},
	}
	for name, refresh := range cases {
		t.Run(name, func(t *testing.T) {
			_, psrv := newFakeProvider(t, "qwen3:8b")
			hub := newFakeHub(t)
			var calls atomic.Int32
			_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
				h := hubFor(hub, "revoked")
				h.Refresh = func(ctx context.Context) (string, error) {
					calls.Add(1)
					return refresh(ctx)
				}
				o.Hubs = []Hub{h}
			})
			select {
			case err := <-done:
				if !errors.Is(err, ErrUnauthorized) {
					t.Fatalf("Run = %v, want ErrUnauthorized", err)
				}
				done <- err
			case <-time.After(5 * time.Second):
				t.Fatal("relay kept retrying a revoked sign-in")
			}
			if n := calls.Load(); n != 1 {
				t.Fatalf("forced refreshes = %d, want exactly 1", n)
			}
		})
	}

	// And mid-session, on a session that reauths.
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	var refreshes atomic.Int32
	_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Hubs = []Hub{refreshingHub(hub, "tok", &refreshes)}
	})
	ctx := context.Background()
	conn := hub.accept(t)
	<-hub.auth
	handshakeV4(t, ctx, conn, relaycontract.ProtocolVersion, 2)
	writeEnv(t, ctx, conn, relaycontract.TypeError, "", relaycontract.Error{Code: relaycontract.CodeCredentialRevoked, Message: "signed out"})
	select {
	case err := <-done:
		if !errors.Is(err, ErrRevoked) {
			t.Fatalf("Run = %v, want ErrRevoked", err)
		}
		done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("revocation did not stop the relay")
	}
}

// After credential_expired the redial presents a forced refresh, and if Hub
// refuses that token it gets one more forced refresh before giving up.
func TestCredentialExpiredRedialGetsOneRefusedRetry(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	var calls atomic.Int32
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		h := hubFor(hub, "tok")
		h.Refresh = func(context.Context) (string, error) {
			if calls.Add(1) == 1 {
				return "revoked", nil // not yet visible to this hub
			}
			return "fresh", nil
		}
		o.Hubs = []Hub{h}
	})
	ctx := context.Background()
	conn := hub.accept(t)
	<-hub.auth
	handshakeV4(t, ctx, conn, relaycontract.ProtocolVersion, 3600)
	time.Sleep(minBackoff + 100*time.Millisecond)
	writeEnv(t, ctx, conn, relaycontract.TypeError, "", relaycontract.Error{Code: relaycontract.CodeCredentialExpired})
	_ = conn.Close(websocket.StatusTryAgainLater, "credential expired")
	select {
	case got := <-hub.auth:
		if got != "Bearer fresh" {
			t.Fatalf("auth = %q, want the second forced refresh", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("relay stopped after a refused redial")
	}
	handshake(t, ctx, hub.accept(t))
}
