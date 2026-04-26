package tapper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// fakeHub stages a hub that supports the device flow with controllable
// behavior at each polling step. The Steps slice drives the token endpoint:
// one entry is consumed per /oauth/token POST. nil entries return the success
// shape; non-nil entries return the error shape (and the polling loop
// dispatches on Error).
type fakeHub struct {
	URL          string
	Server       *httptest.Server
	DeviceCode   string
	UserCode     string
	Interval     int
	IssuedToken  string
	PollCount    int32
	steps        []*tokenErrorResponse
	stepIndex    int32
	authEndpoint string
}

func newFakeHub(t *testing.T, steps []*tokenErrorResponse) *fakeHub {
	t.Helper()

	h := &fakeHub{
		DeviceCode:  "dev-" + randomString(t),
		UserCode:    "ABCD-EFGH",
		Interval:    1,
		IssuedToken: "thub_" + randomString(t),
		steps:       steps,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		base := "http://" + r.Host
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint":           base + "/authorize",
			"token_endpoint":                   base + "/oauth/token",
			"device_authorization_endpoint":    base + "/oauth/device_authorization",
			"code_challenge_methods_supported": []string{"S256"},
		})
	})

	mux.HandleFunc("/oauth/device_authorization", func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("client_id") != "tapper-cli" {
			http.Error(w, "bad client_id", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"device_code":               h.DeviceCode,
			"user_code":                 h.UserCode,
			"verification_uri":          h.URL + "/device",
			"verification_uri_complete": h.URL + "/device?user_code=" + h.UserCode,
			"expires_in":                600,
			"interval":                  h.Interval,
		})
	})

	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&h.PollCount, 1)
		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad form", http.StatusBadRequest)
			return
		}
		if r.FormValue("grant_type") != deviceCodeGrantType {
			http.Error(w, "wrong grant", http.StatusBadRequest)
			return
		}
		if r.FormValue("device_code") != h.DeviceCode {
			http.Error(w, "wrong device_code", http.StatusBadRequest)
			return
		}

		idx := int(atomic.LoadInt32(&h.stepIndex))
		atomic.AddInt32(&h.stepIndex, 1)
		if idx < len(h.steps) && h.steps[idx] != nil {
			step := h.steps[idx]
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":             step.Error,
				"error_description": step.ErrorDescription,
			})
			return
		}
		// Default / nil step → success.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": h.IssuedToken,
			"token_type":   "Bearer",
			"expires_in":   3600,
		})
	})

	h.Server = httptest.NewServer(mux)
	h.URL = h.Server.URL
	t.Cleanup(h.Server.Close)
	return h
}

// randomString returns a short hex identifier so each test instance gets
// distinct device_code / token values without coordinating with peers.
func randomString(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

func newDeviceFlowOpts(t *testing.T, hub *fakeHub) (AuthLoginDeviceOptions, *bytes.Buffer, *atomic.Int64) {
	t.Helper()
	prompt := &bytes.Buffer{}
	sleeps := &atomic.Int64{}
	return AuthLoginDeviceOptions{
		HubURL:    hub.URL,
		ClientID:  "tapper-cli",
		PromptOut: prompt,
		Sleep:     func(d time.Duration) { sleeps.Add(int64(d)) },
		// Tight timeout so a flow that gets stuck doesn't hang the test;
		// the fake hub responds immediately so this is never reached on
		// the success path.
		Timeout: 30 * time.Second,
	}, prompt, sleeps
}

func TestAuthLoginDevice_HappyPath(t *testing.T) {
	hub := newFakeHub(t, []*tokenErrorResponse{
		{Error: "authorization_pending"},
		nil, // success
	})
	rt, _ := toolkit.NewRuntime()
	opts, prompt, _ := newDeviceFlowOpts(t, hub)

	entry, err := AuthLoginDevice(context.Background(), rt, opts)
	if err != nil {
		t.Fatalf("AuthLoginDevice: %v", err)
	}
	if entry.AccessToken != hub.IssuedToken {
		t.Errorf("expected token %q, got %q", hub.IssuedToken, entry.AccessToken)
	}
	if !strings.Contains(prompt.String(), hub.UserCode) {
		t.Errorf("prompt should include user_code %q; got %q", hub.UserCode, prompt.String())
	}
	if !strings.Contains(prompt.String(), "/device") {
		t.Errorf("prompt should include verification URI; got %q", prompt.String())
	}
}

func TestAuthLoginDevice_AccessDenied(t *testing.T) {
	hub := newFakeHub(t, []*tokenErrorResponse{
		{Error: "access_denied", ErrorDescription: "user said no"},
	})
	rt, _ := toolkit.NewRuntime()
	opts, _, _ := newDeviceFlowOpts(t, hub)

	_, err := AuthLoginDevice(context.Background(), rt, opts)
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected access_denied error; got %v", err)
	}
}

func TestAuthLoginDevice_ExpiredToken(t *testing.T) {
	hub := newFakeHub(t, []*tokenErrorResponse{
		{Error: "expired_token"},
	})
	rt, _ := toolkit.NewRuntime()
	opts, _, _ := newDeviceFlowOpts(t, hub)

	_, err := AuthLoginDevice(context.Background(), rt, opts)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expected expired_token error; got %v", err)
	}
}

func TestAuthLoginDevice_SlowDownIncreasesInterval(t *testing.T) {
	hub := newFakeHub(t, []*tokenErrorResponse{
		{Error: "authorization_pending"},
		{Error: "slow_down"},
		{Error: "authorization_pending"},
		nil, // success
	})
	rt, _ := toolkit.NewRuntime()
	opts, _, sleeps := newDeviceFlowOpts(t, hub)

	if _, err := AuthLoginDevice(context.Background(), rt, opts); err != nil {
		t.Fatalf("AuthLoginDevice: %v", err)
	}

	// Three sleeps should have happened (one after each non-success response).
	// Without slow_down, total would be 3 × max(2s, hub.Interval=1s) = 6s.
	// With slow_down adding 5s once, total ≥ 11s.
	got := time.Duration(sleeps.Load())
	if got < 10*time.Second {
		t.Errorf("expected slow_down to bump cumulative sleep past 10s; got %s", got)
	}
}

func TestAuthLoginDevice_HubMissingDeviceEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/oauth-authorization-server", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"authorization_endpoint": "http://" + r.Host + "/authorize",
			"token_endpoint":         "http://" + r.Host + "/oauth/token",
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	rt, _ := toolkit.NewRuntime()
	prompt := &bytes.Buffer{}
	_, err := AuthLoginDevice(context.Background(), rt, AuthLoginDeviceOptions{
		HubURL:    srv.URL,
		ClientID:  "tapper-cli",
		PromptOut: prompt,
		Sleep:     func(time.Duration) {},
	})
	if err == nil || !strings.Contains(err.Error(), "device_authorization_endpoint") {
		t.Fatalf("expected error about missing device endpoint; got %v", err)
	}
}

func TestAuthLoginDevice_RequiresHubAndClient(t *testing.T) {
	rt, _ := toolkit.NewRuntime()

	if _, err := AuthLoginDevice(context.Background(), rt, AuthLoginDeviceOptions{ClientID: "tapper-cli"}); err == nil {
		t.Error("expected error when HubURL is empty")
	}
	if _, err := AuthLoginDevice(context.Background(), rt, AuthLoginDeviceOptions{HubURL: "https://hub.example.com"}); err == nil {
		t.Error("expected error when ClientID is empty")
	}
}
