package apicontract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGateBeforeApplication(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		code   string
	}{
		{"missing", nil, Required}, {"empty", []string{""}, Unsupported}, {"valid", []string{Revision}, ""},
		{"duplicate", []string{Revision, Revision}, Unsupported}, {"comma", []string{Revision + ", " + Revision}, Unsupported},
		{"malformed", []string{" " + Revision}, Unsupported}, {"unsupported", []string{"2099-01-01"}, Unsupported},
	} {
		t.Run(tc.name, func(t *testing.T) {
			called := false
			var logs bytes.Buffer
			h := Middleware("0.24.0", slog.New(slog.NewJSONHandler(&logs, nil)))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called = true; w.WriteHeader(204) }))
			req := httptest.NewRequest("POST", "/api/v1/@ns/kegs/k/nodes", strings.NewReader("private content"))
			req.Header[http.CanonicalHeaderKey(VersionHeader)] = tc.values
			req.Header.Set("Authorization", "Bearer secret")
			req.Header.Set(ClientHeader, "0.40.0")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if tc.code == "" {
				if !called || rec.Code != 204 || rec.Header().Get(VersionHeader) != Revision {
					t.Fatal(rec, called)
				}
				return
			}
			if called || rec.Code != 400 {
				t.Fatal("rejection reached application", rec.Code)
			}
			var e CompatibilityError
			if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			if e.Code != tc.code || e.OperationPerformed || len(e.Supported) != 1 {
				t.Fatalf("%+v", e)
			}
			if !strings.Contains(logs.String(), `"level":"WARN"`) || strings.Contains(logs.String(), "secret") || strings.Contains(logs.String(), "private content") {
				t.Fatal(logs.String())
			}
		})
	}
}

func TestDiscoveryAndExemptions(t *testing.T) {
	h := Middleware("build-release", nil)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	for _, path := range []string{"/health", "/mcp", "/oauth/token", "/home", "/api/v10"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest("POST", path, nil))
		if r.Code != 204 {
			t.Fatal(path, r.Code)
		}
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/api/version", nil))
	var d Discovery
	if err := json.Unmarshal(r.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" || d.ServerVersion != "build-release" || len(d.APIVersions) != 1 || d.APIVersions[0] != Revision {
		t.Fatal(r, d)
	}
}

func TestClientDiscoveryMatrix(t *testing.T) {
	for _, tc := range []struct {
		name, body, code string
		status           int
	}{
		{"matching", `{"server_version":"0.24.0","api_versions":["2026-09-11"]}`, "", 200},
		{"newer compatible", `{"server_version":"99.0.0","api_versions":["2026-09-11","2099-01-01"]}`, "", 200},
		{"newer incompatible", `{"server_version":"99.0.0","api_versions":["2099-01-01"]}`, Unsupported, 200},
		{"legacy", "", DiscoveryUnavailable, 404}, {"redirect", "", DiscoveryUnavailable, 302},
		{"malformed", "oops", DiscoveryInvalid, 200}, {"missing versions", `{"server_version":"v1"}`, DiscoveryInvalid, 200},
		{"trailing json", `{"server_version":"v1","api_versions":[]} {}`, DiscoveryInvalid, 200},
		{"empty supported", `{"server_version":"v1","api_versions":[]}`, Unsupported, 200},
		{"auth", "", "auth", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var ops, discovery atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/version" {
					discovery.Add(1)
					if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" || r.Header.Get(VersionHeader) != "" {
						t.Error("discovery sent credentials or selected revision")
					}
					w.Header().Set("Location", "/should-not-follow")
					w.WriteHeader(tc.status)
					io.WriteString(w, tc.body)
					return
				}
				ops.Add(1)
				if r.Header.Get(VersionHeader) != Revision || r.Header.Get(ClientHeader) != "client-build" || r.Header.Get("Authorization") != "Bearer secret" {
					t.Error("missing request metadata")
				}
				w.WriteHeader(204)
			}))
			defer server.Close()
			jar, _ := cookiejar.New(nil)
			c := server.Client()
			c.Jar = jar
			s := NewSession("client-build", slog.New(slog.NewTextHandler(io.Discard, nil)))
			ctx := WithSession(context.Background(), s)
			for i := 0; i < 2; i++ {
				req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/v1/operation", nil)
				req.Header.Set("Authorization", "Bearer secret")
				jar.SetCookies(req.URL, []*http.Cookie{{Name: "login", Value: "secret"}})
				resp, err := Do(c, req)
				if tc.code == "" {
					if err != nil {
						t.Fatal(err)
					}
					resp.Body.Close()
				} else if tc.code == "auth" {
					if err == nil || IsCompatibility(err) {
						t.Fatal(err)
					}
				} else {
					var e *CompatibilityError
					if !errors.As(err, &e) || e.Code != tc.code || e.OperationPerformed {
						t.Fatalf("%v", err)
					}
				}
			}
			if discovery.Load() != 1 {
				t.Fatal(discovery.Load())
			}
			if tc.code != "" && ops.Load() != 0 {
				t.Fatal("operation sent before compatibility established")
			}
		})
	}
}

func TestDiscoveryDeadlineAndConcurrentOnce(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); <-r.Context().Done() }))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	s := NewSession("test", nil)
	start := time.Now()
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.Check(ctx, server.Client(), server.URL)
			if !errors.Is(err, context.DeadlineExceeded) || IsCompatibility(err) {
				t.Errorf("%v", err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 || time.Since(start) > time.Second {
		t.Fatal(calls.Load(), time.Since(start))
	}
}

func TestDeploymentMismatchPreservesErrorAndDoesNotReplay(t *testing.T) {
	var discoveries, operations atomic.Int32
	var logs bytes.Buffer
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/version" {
			discoveries.Add(1)
			json.NewEncoder(w).Encode(Discovery{"v1", []string{Revision}})
			return
		}
		operations.Add(1)
		if operations.Load() == 1 {
			w.WriteHeader(204)
			return
		}
		w.Header().Set("X-Request-ID", "request-123")
		w.WriteHeader(400)
		json.NewEncoder(w).Encode(failure(Unsupported, "upgrade", "v2", []string{Revision}, []string{"2099-01-01"}))
	}))
	defer server.Close()
	s := NewSession("client-release", slog.New(slog.NewJSONHandler(&logs, nil)))
	ctx := WithSession(context.Background(), s)
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/api/v1/nodes", strings.NewReader("secret content"))
		resp, err := Do(server.Client(), req)
		if i == 0 {
			if err != nil {
				t.Fatal(err)
			}
			resp.Body.Close()
		} else {
			var e *CompatibilityError
			if !errors.As(err, &e) || e.ServerVersion != "v2" || e.Supported[0] != "2099-01-01" {
				t.Fatal(err)
			}
		}
	}
	if discoveries.Load() != 1 || operations.Load() != 2 {
		t.Fatal(discoveries.Load(), operations.Load())
	}
	for _, value := range []string{`"level":"ERROR"`, `"client_version":"client-release"`, `"server_version":"v2"`, `"request_id":"request-123"`} {
		if !strings.Contains(logs.String(), value) {
			t.Fatal(logs.String())
		}
	}
	if strings.Contains(logs.String(), "secret content") {
		t.Fatal(logs.String())
	}
}
