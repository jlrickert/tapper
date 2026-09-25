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
	// The last /audio/transcriptions upload: form fields and file.
	lastForm     map[string]string
	lastFile     string
	lastFileName string
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
	case "/audio/transcriptions":
		file, header, err := r.FormFile("file")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		data, _ := io.ReadAll(file)
		form := map[string]string{}
		for k, v := range r.MultipartForm.Value {
			form[k] = v[0]
		}
		fp.mu.Lock()
		fp.lastForm, fp.lastFile, fp.lastFileName = form, string(data), header.Filename
		fp.mu.Unlock()
		_, _ = io.WriteString(w, `{"text":"hello world"}`)
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

// hubFor points the relay at a fake hub with a fixed bearer token.
func hubFor(hub *fakeHub, token string) Hub {
	return Hub{URL: hub.srv.URL, Token: func(context.Context) (string, error) { return token, nil }}
}

func startRelay(t *testing.T, hub *fakeHub, providers []*Provider, mutate func(*Options)) (context.CancelFunc, chan error) {
	t.Helper()
	opts := Options{
		Hubs:            []Hub{hubFor(hub, "tok")},
		Name:            "laptop",
		Version:         "test",
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
	return ollamaLimited(t, baseURL, 2, allow...)
}

// ollamaLimited is ollama with an explicit in-flight limit.
func ollamaLimited(t *testing.T, baseURL string, limit int, allow ...string) *Provider {
	t.Helper()
	p, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, BaseURL: baseURL, Allow: allow, MaxConcurrent: limit}, func(string) string { return "" }, nil)
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
	startRelay(t, hub, []*Provider{ollamaLimited(t, psrv.URL, 1)}, nil)
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
		o.Hubs = []Hub{hubFor(hub, "revoked")}
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

func TestTranscriptionInferReturnsText(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	p, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, BaseURL: psrv.URL, Transcription: []string{"whisper-1"}}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{p}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	reg := handshake(t, ctx, conn)

	caps := map[string]string{}
	for _, m := range reg.Models {
		caps[m.ID] = strings.Join(m.Capabilities, ",")
	}
	if caps["qwen3:8b"] != "chat,stream" || caps["whisper-1"] != "transcription" {
		t.Fatalf("advertised capabilities = %v", caps)
	}

	body, _ := json.Marshal(relaycontract.TranscriptionRequest{Audio: []byte("RIFFdata"), MimeType: "audio/webm;codecs=opus", Language: "en"})
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "t1", relaycontract.Infer{
		API: relaycontract.APIOpenAIAudioTranscriptions, Provider: "ollama", Model: "whisper-1", Body: body,
	})
	env := readEnv(t, ctx, conn)
	if env.Type != relaycontract.TypeChunk {
		t.Fatalf("frame = %q (%s), want chunk", env.Type, env.Payload)
	}
	var chunk relaycontract.Chunk
	if err := env.Decode(&chunk); err != nil {
		t.Fatal(err)
	}
	var result relaycontract.TranscriptionResult
	if err := json.Unmarshal(chunk.Data, &result); err != nil || result.Text != "hello world" {
		t.Fatalf("result = %s (%v)", chunk.Data, err)
	}
	if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypeDone {
		t.Fatalf("frame = %q, want done", env.Type)
	}
	fp.mu.Lock()
	defer fp.mu.Unlock()
	if fp.lastFile != "RIFFdata" || fp.lastFileName != "audio.webm" || fp.lastForm["model"] != "whisper-1" || fp.lastForm["language"] != "en" {
		t.Fatalf("provider saw file %q named %q, form %v", fp.lastFile, fp.lastFileName, fp.lastForm)
	}
}

func TestInferRejectsWrongAPIForModel(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	p, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, BaseURL: psrv.URL, Transcription: []string{"whisper-1"}}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{p}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	handshake(t, ctx, conn)

	audio, _ := json.Marshal(relaycontract.TranscriptionRequest{Audio: []byte("x"), MimeType: "audio/webm"})
	for id, in := range map[string]relaycontract.Infer{
		"chat-on-whisper": {API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "whisper-1", Body: json.RawMessage(`{"messages":[]}`)},
		"audio-on-qwen":   {API: relaycontract.APIOpenAIAudioTranscriptions, Provider: "ollama", Model: "qwen3:8b", Body: audio},
	} {
		writeEnv(t, ctx, conn, relaycontract.TypeInfer, id, in)
		env := readEnv(t, ctx, conn)
		var e relaycontract.Error
		if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeUnsupported {
			t.Fatalf("%s: frame %q %s, want unsupported error", id, env.Type, env.Payload)
		}
	}
}

func TestServesSeveralHubsWithOneCatalog(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	a, b := newFakeHub(t), newFakeHub(t)
	registered := make(chan string, 4)
	startRelay(t, a, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Hubs = append(o.Hubs, hubFor(b, "tok"))
		o.CatalogInterval = 20 * time.Millisecond
		o.OnRegistered = func(hubURL string, _ relaycontract.Registered) { registered <- hubURL }
	})
	ctx := context.Background()
	connA, connB := a.accept(t), b.accept(t)
	for _, conn := range []*websocket.Conn{connA, connB} {
		reg := handshake(t, ctx, conn)
		if len(reg.Models) != 1 || reg.Models[0].ID != "qwen3:8b" {
			t.Fatalf("register models = %+v", reg.Models)
		}
	}
	got := map[string]bool{<-registered: true, <-registered: true}
	if !got[a.srv.URL] || !got[b.srv.URL] {
		t.Fatalf("OnRegistered hubs = %v", got)
	}

	for i, conn := range []*websocket.Conn{connA, connB} {
		id := fmt.Sprintf("r%d", i)
		writeEnv(t, ctx, conn, relaycontract.TypeInfer, id, relaycontract.Infer{
			API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
			Body: json.RawMessage(`{"messages":[]}`),
		})
		if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypeChunk || env.ID != id {
			t.Fatalf("hub %d: frame %q %q, want chunk", i, env.Type, env.ID)
		}
		if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypeDone {
			t.Fatalf("hub %d: frame %q, want done", i, env.Type)
		}
	}

	// One provider listing change reaches both hubs.
	fp.setModels("qwen3:8b", "llama3:8b")
	for i, conn := range []*websocket.Conn{connA, connB} {
		env := readEnv(t, ctx, conn)
		var cat relaycontract.Catalog
		if env.Type != relaycontract.TypeCatalog || env.Decode(&cat) != nil || len(cat.Models) != 2 {
			t.Fatalf("hub %d: frame %s %s, want catalog with 2 models", i, env.Type, env.Payload)
		}
	}
}

func TestConcurrencyIsSharedAcrossHubs(t *testing.T) {
	fp, psrv := newFakeProvider(t, "qwen3:8b")
	a, b := newFakeHub(t), newFakeHub(t)
	startRelay(t, a, []*Provider{ollamaLimited(t, psrv.URL, 1)}, func(o *Options) {
		o.Hubs = append(o.Hubs, hubFor(b, "tok"))
	})
	ctx := context.Background()
	connA, connB := a.accept(t), b.accept(t)
	handshake(t, ctx, connA)
	handshake(t, ctx, connB)

	writeEnv(t, ctx, connA, relaycontract.TypeInfer, "busy", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[{"role":"user","content":"block"}]}`),
	})
	<-fp.blocked
	writeEnv(t, ctx, connB, relaycontract.TypeInfer, "second", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	env := readEnv(t, ctx, connB)
	var e relaycontract.Error
	if env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeOverloaded {
		t.Fatalf("second hub got %s %s, want overloaded", env.Type, env.Payload)
	}

	// Freeing the slot on hub A frees it for hub B.
	writeEnv(t, ctx, connA, relaycontract.TypeCancel, "busy", nil)
	if env := readEnv(t, ctx, connA); env.Type != relaycontract.TypeError {
		t.Fatalf("cancelled request ended with %q", env.Type)
	}
	writeEnv(t, ctx, connB, relaycontract.TypeInfer, "third", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	if env := readEnv(t, ctx, connB); env.Type != relaycontract.TypeChunk {
		t.Fatalf("after the slot freed, hub B got %s %s", env.Type, env.Payload)
	}
}

func TestRejectedHubDoesNotStopTheOthers(t *testing.T) {
	_, psrv := newFakeProvider(t, "qwen3:8b")
	bad, good := newFakeHub(t), newFakeHub(t)
	_, done := startRelay(t, bad, []*Provider{ollama(t, psrv.URL)}, func(o *Options) {
		o.Hubs = []Hub{hubFor(bad, "revoked"), hubFor(good, "tok")}
	})
	ctx := context.Background()
	conn := good.accept(t)
	handshake(t, ctx, conn)
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "r1", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	if env := readEnv(t, ctx, conn); env.Type != relaycontract.TypeChunk {
		t.Fatalf("good hub got %s %s", env.Type, env.Payload)
	}
	select {
	case err := <-done:
		t.Fatalf("Run returned %v while a hub was still served", err)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestNewClientRejectsDuplicateHubs(t *testing.T) {
	hub := Hub{URL: "https://hub.example", Token: func(context.Context) (string, error) { return "t", nil }}
	_, err := NewClient(Options{Hubs: []Hub{hub, {URL: "https://hub.example/", Token: hub.Token}}, Name: "laptop", Providers: []*Provider{ollama(t, "http://127.0.0.1:1")}})
	if err == nil || !strings.Contains(err.Error(), "twice") {
		t.Fatalf("NewClient = %v, want duplicate hub error", err)
	}
}

func TestProviderLimitsAreIndependent(t *testing.T) {
	fpA, srvA := newFakeProvider(t, "qwen3:8b")
	_, srvB := newFakeProvider(t, "gpt-4o")
	local := ollamaLimited(t, srvA.URL, 1)
	hosted, err := NewProvider(ProviderConfig{Name: "hosted", Kind: KindOpenAICompatible, BaseURL: srvB.URL, MaxConcurrent: 3}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{local, hosted}, nil)
	ctx := context.Background()
	conn := hub.accept(t)
	reg := handshake(t, ctx, conn)
	if reg.Limits.MaxConcurrent != 4 {
		t.Fatalf("advertised limit = %d, want the providers' sum 4", reg.Limits.MaxConcurrent)
	}

	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "busy", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[{"role":"user","content":"block"}]}`),
	})
	<-fpA.blocked
	// The full local provider turns its next request away...
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "local2", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "ollama", Model: "qwen3:8b",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	env := readEnv(t, ctx, conn)
	var e relaycontract.Error
	if env.ID != "local2" || env.Type != relaycontract.TypeError || env.Decode(&e) != nil || e.Code != relaycontract.CodeOverloaded {
		t.Fatalf("second local request got %s %s %s, want overloaded", env.ID, env.Type, env.Payload)
	}
	// ...while the hosted one still serves.
	writeEnv(t, ctx, conn, relaycontract.TypeInfer, "hosted1", relaycontract.Infer{
		API: relaycontract.APIOpenAIChatCompletions, Provider: "hosted", Model: "gpt-4o",
		Body: json.RawMessage(`{"messages":[]}`),
	})
	if env := readEnv(t, ctx, conn); env.ID != "hosted1" || env.Type != relaycontract.TypeChunk {
		t.Fatalf("hosted request got %s %s %s", env.ID, env.Type, env.Payload)
	}
}

func TestNewProviderValidatesMaxConcurrent(t *testing.T) {
	p, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama}, func(string) string { return "" }, nil)
	if err != nil || p.MaxConcurrent() != DefaultMaxConcurrent {
		t.Fatalf("default limit = %v, %v", p, err)
	}
	for _, bad := range []int{-1, relaycontract.MaxConcurrentCap + 1} {
		if _, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, MaxConcurrent: bad}, func(string) string { return "" }, nil); err == nil || !strings.Contains(err.Error(), "maxConcurrent") {
			t.Fatalf("maxConcurrent %d: err = %v", bad, err)
		}
	}
}

func TestProviderPriorityIsAdvertised(t *testing.T) {
	_, srvA := newFakeProvider(t, "qwen3:8b")
	_, srvB := newFakeProvider(t, "gpt-4o")
	local, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, BaseURL: srvA.URL, Priority: 1}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	hosted, err := NewProvider(ProviderConfig{Name: "hosted", Kind: KindOpenAICompatible, BaseURL: srvB.URL}, func(string) string { return "" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub(t)
	startRelay(t, hub, []*Provider{local, hosted}, nil)
	reg := handshake(t, context.Background(), hub.accept(t))
	got := map[string]int{}
	for _, m := range reg.Models {
		got[m.Provider+"/"+m.ID] = m.Priority
	}
	if got["ollama/qwen3:8b"] != 1 || got["hosted/gpt-4o"] != 0 {
		t.Fatalf("advertised priorities = %v", got)
	}

	for _, bad := range []int{-1, relaycontract.MaxPriority + 1} {
		if _, err := NewProvider(ProviderConfig{Name: "ollama", Kind: KindOllama, Priority: bad}, func(string) string { return "" }, nil); err == nil || !strings.Contains(err.Error(), "priority") {
			t.Fatalf("priority %d: err = %v", bad, err)
		}
	}
}
