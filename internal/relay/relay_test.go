package relay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// fakeProvider serves an OpenAI-compatible /models and /chat/completions.
// A chat request whose last message is "block" waits until the request is
// cancelled.
type fakeProvider struct {
	mu       sync.Mutex
	models   []string
	lastBody map[string]any
	lastAuth string
	blocked  chan struct{}
}

func newFakeProvider(t *testing.T, models ...string) (*fakeProvider, *httptest.Server) {
	t.Helper()
	fp := &fakeProvider{models: models, blocked: make(chan struct{}, 1)}
	srv := httptest.NewServer(http.HandlerFunc(fp.serve))
	t.Cleanup(srv.Close)
	return fp, srv
}

func (fp *fakeProvider) setModels(models ...string) {
	fp.mu.Lock()
	fp.models = models
	fp.mu.Unlock()
}

func (fp *fakeProvider) serve(w http.ResponseWriter, r *http.Request) {
	fp.mu.Lock()
	fp.lastAuth = r.Header.Get("Authorization")
	models := append([]string(nil), fp.models...)
	fp.mu.Unlock()
	switch r.URL.Path {
	case "/models":
		data := make([]map[string]string, 0, len(models))
		for _, m := range models {
			data = append(data, map[string]string{"id": m})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
	case "/chat/completions":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		fp.mu.Lock()
		fp.lastBody = body
		fp.mu.Unlock()
		msgs, _ := body["messages"].([]any)
		if len(msgs) > 0 {
			if m, _ := msgs[len(msgs)-1].(map[string]any); m["content"] == "block" {
				fp.blocked <- struct{}{}
				<-r.Context().Done()
				return
			}
		}
		if body["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, tok := range []string{"hel", "lo"} {
				fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", tok)
			}
			fmt.Fprint(w, "data: {\"choices\":[],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"hello"}}],"usage":{"prompt_tokens":3,"completion_tokens":1}}`)
	default:
		http.NotFound(w, r)
	}
}

// fakeHub accepts one relay connection and hands it to the test.
type fakeHub struct {
	srv   *httptest.Server
	conns chan *websocket.Conn
	auth  chan string
}

func newFakeHub(t *testing.T) *fakeHub {
	t.Helper()
	h := &fakeHub{conns: make(chan *websocket.Conn, 4), auth: make(chan string, 4)}
	h.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != relaycontract.ConnectPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") == "Bearer revoked" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h.auth <- r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		h.conns <- conn
		<-r.Context().Done()
	}))
	t.Cleanup(h.srv.Close)
	return h
}

func (h *fakeHub) accept(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case c := <-h.conns:
		t.Cleanup(func() { c.CloseNow() })
		return c
	case <-time.After(5 * time.Second):
		t.Fatal("relay did not connect")
		return nil
	}
}

func readEnv(t *testing.T, ctx context.Context, c *websocket.Conn) relaycontract.Envelope {
	t.Helper()
	rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var env relaycontract.Envelope
	if err := wsjson.Read(rctx, c, &env); err != nil {
		t.Fatalf("read frame: %v", err)
	}
	return env
}

func writeEnv(t *testing.T, ctx context.Context, c *websocket.Conn, typ, id string, payload any) {
	t.Helper()
	env, err := relaycontract.NewEnvelope(typ, id, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := wsjson.Write(ctx, c, env); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func startRelay(t *testing.T, hub *fakeHub, providers []*Provider, mutate func(*Options)) (context.CancelFunc, chan error) {
	t.Helper()
	opts := Options{
		HubURL:          hub.srv.URL,
		Token:           func(context.Context) (string, error) { return "tok", nil },
		Name:            "laptop",
		Version:         "test",
		MaxConcurrent:   2,
		Providers:       providers,
		CatalogInterval: time.Hour,
	}
	if mutate != nil {
		mutate(&opts)
	}
	client, err := NewClient(opts)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- client.Run(ctx) }()
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

// handshake reads register and answers registered.
func handshake(t *testing.T, ctx context.Context, c *websocket.Conn) relaycontract.Register {
	t.Helper()
	env := readEnv(t, ctx, c)
	if env.Type != relaycontract.TypeRegister {
		t.Fatalf("first frame = %q, want register", env.Type)
	}
	var reg relaycontract.Register
	if err := env.Decode(&reg); err != nil {
		t.Fatal(err)
	}
	writeEnv(t, ctx, c, relaycontract.TypeRegistered, "", relaycontract.Registered{Protocol: 1, Models: []relaycontract.CatalogBinding{}})
	return reg
}

func ollama(t *testing.T, baseURL string, allow ...string) *Provider {
	t.Helper()
	p, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, BaseURL: baseURL, Allow: allow}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRegisterAdvertisesFilteredModels(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b", "llama3:70b", "embed-small")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL, "qwen*", "llama3:70b")}, nil)

	conn := hub.accept(t)
	if got := <-hub.auth; got != "Bearer tok" {
		t.Fatalf("Authorization = %q", got)
	}
	reg := handshake(t, context.Background(), conn)
	if reg.Relay.Name != "laptop" || reg.Limits.MaxConcurrent != 2 {
		t.Fatalf("register = %+v", reg)
	}
	var ids []string
	for _, m := range reg.Models {
		ids = append(ids, m.Provider+"/"+m.ID)
	}
	if strings.Join(ids, ",") != "ollama/qwen3:8b,ollama/llama3:70b" {
		t.Fatalf("offered models = %v", ids)
	}
}

func TestStreamingInferForwardsChunksAndUsage(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r1", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b", Stream: true,
		Body: json.RawMessage(`{"model":"laptop/ollama/qwen3:8b","messages":[{"role":"user","content":"hi"}]}`),
	})
	var chunks int
	for {
		env := readEnv(t, ctx, conn)
		if env.ID != "r1" {
			t.Fatalf("frame id = %q", env.ID)
		}
		if env.Type == relaycontract.TypeChunk {
			chunks++
			continue
		}
		if env.Type != relaycontract.TypeDone {
			t.Fatalf("frame type = %q (%s)", env.Type, env.Payload)
		}
		var done relaycontract.Done
		if err := env.Decode(&done); err != nil {
			t.Fatal(err)
		}
		if done.Usage == nil || done.Usage.PromptTokens != 3 || done.Usage.CompletionTokens != 2 {
			t.Fatalf("usage = %+v", done.Usage)
		}
		break
	}
	if chunks != 3 {
		t.Fatalf("chunks = %d, want 3", chunks)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if fp.lastBody["model"] != "qwen3:8b" {
		t.Fatalf("provider saw model %v, want the local name", fp.lastBody["model"])
	}
}

func TestNonStreamingInferSendsOneChunk(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r1", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypeChunk {
		t.Fatalf("frame = %q, want chunk", env.Type)
	}
	if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypeDone {
		t.Fatalf("frame = %q, want done", env.Type)
	}
}

func TestInferRejectsModelNotOffered(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b", "secret-model")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL, "qwen3:8b")}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r1", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "secret-model",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeUnknownModel {
		t.Fatalf("frame = %s %s, want unknown_model error", env.Type, env.Payload)
	}
}

func TestInferRejectsUnknownFields(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	env := relaycontract.Envelope{V: 1, ID: "r1", Type: relaycontract.TypeInfer,
		Payload: json.RawMessage(`{"api":"openai.chat.completions","provider":"ollama","model":"qwen3:8b","stream":false,"body":{},"baseUrl":"http://evil"}`)}
	if err := wsjson.Write(ctx, conn, env); err != nil {
		t.Fatal(err)
	}
	got := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if got.Type != relaycontract.TypeError || got.Decode(&e) != nil || e.Code != relaycontract.CodeBadRequest {
		t.Fatalf("frame = %s %s, want bad_request error", got.Type, got.Payload)
	}
}

func TestCancelStopsProviderCall(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r1", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b", Stream: true,
		Body: json.RawMessage(`{"messages":[{"role":"user","content":"block"}]}`),
	})
	select {
	case <-fp.blocked:
	case <-time.After(5 * time.Second):
		t.Fatal("provider never received the request")
	}
	writeEnv(t, ctx, conn, relaycontract.TypeCancel, "r1", nil)
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeCancelled {
		t.Fatalf("frame = %s %s, want cancelled error", env.Type, env.Payload)
	}
}

func TestConcurrencyLimitReturnsOverloaded(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) { o.MaxConcurrent = 1 })
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	block := relaycontract.Infer{API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[{"role":"user","content":"block"}]}`)}
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r1", block)
	<-fp.blocked
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r2", block)
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.ID != "r2" || env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeOverloaded {
		t.Fatalf("frame = %s %s %s, want overloaded for r2", env.ID, env.Type, env.Payload)
	}
	writeEnv(t, ctx, conn, relaycontract.TypeCancel, "r1", nil)
}

func TestPingAnsweredWithPong(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	writeEnv(t, ctx, conn, relaycontract.TypePing, "p1", nil)
	if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypePong || env.ID != "p1" {
		t.Fatalf("frame = %+v, want pong p1", env)
	}
}

func TestCatalogSentWhenModelsChange(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) { o.CatalogInterval = 20 * time.Millisecond })
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	fp.setModels("qwen3:8b", "llama3:8b")
	env := readEnv(t, ctx, conn)
	var cat relaycontract.Catalog
	if env.Type != relaycontract.TypeCatalog || env.Decode(&cat) != nil || len(cat.Models) != 2 {
		t.Fatalf("frame = %s %s, want catalog with 2 models", env.Type, env.Payload)
	}
}

func TestUnauthorizedIsPermanent(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Token = func(context.Context) (string, error) { return "revoked", nil }
	})
	select {
	case err := <-done:
		if !errors.Is(err, ErrUnauthorized) {
			t.Fatalf("Run = %v, want ErrUnauthorized", err)
		}
		done <- err // let cleanup observe completion
	case <-time.After(5 * time.Second):
		t.Fatal("relay kept retrying a rejected credential")
	}
}

func TestUnsupportedVersionIsPermanent(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	readEnv(t, ctx, conn)
	writeEnv(t, ctx, conn, relaycontract.TypeError, "", relaycontract.Error{Code: relaycontract.CodeUnsupported, Message: "protocol 1 not supported"})
	select {
	case err := <-done:
		if !errors.Is(err, ErrIncompatible) {
			t.Fatalf("Run = %v, want ErrIncompatible", err)
		}
		done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("relay kept retrying an incompatible hub")
	}
}

func TestOwnerDisconnectIsPermanent(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	hub := newFakeHub(t)
	_, done := startRelay(t, hub, []*Provider{ollama(t, psrv.URL)}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)
	writeEnv(t, ctx, conn, relaycontract.TypeError, "", relaycontract.Error{Code: relaycontract.CodeDisconnected, Message: "disconnected by owner"})
	select {
	case err := <-done:
		if !errors.Is(err, ErrDisconnected) {
			t.Fatalf("Run = %v, want ErrDisconnected", err)
		}
		done <- err
	case <-time.After(5 * time.Second):
		t.Fatal("relay reconnected after its owner disconnected it")
	}
}

func TestNewProviderCredentials(t *testing.T) {
	env := map[string]string{"OPENROUTER_API_KEY": "sk-or"}
	getenv := func(k string) string { return env[k] }

	fp, psrv := newFakeProvider(t, "m")
	p, err := NewProvider(ProviderConfig{Name: "openrouter", BaseURL: psrv.URL}, getenv, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	fp.mu.Lock()
	auth := fp.lastAuth
	fp.mu.Unlock()
	if auth != "Bearer sk-or" {
		t.Fatalf("provider Authorization = %q", auth)
	}

	for name, cfg := range map[string]ProviderConfig{
		"missing key":  {Name: "openai", Kind: KindOpenAI},
		"subscription": {Name: "openai", Kind: KindOpenAI, Auth: AuthSubscription},
		"keychain":     {Name: "anthropic", Kind: KindOpenAICompatible, BaseURL: "https://x", Auth: AuthKeychain},
		"unknown kind": {Name: "x", Kind: "shell"},
		"bad name":     {Name: "a/b", Kind: KindOllama},
		"no base url":  {Name: "custom", Kind: KindOpenAICompatible},
		"bad scheme":   {Name: "ollama", BaseURL: "file:///etc"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := NewProvider(cfg, getenv, nil); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}
