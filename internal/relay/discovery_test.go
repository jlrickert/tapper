package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// fakeOllama serves the OpenAI /v1 routes and the native /api routes the
// relay reads model details from.
type fakeOllama struct {
	mu       sync.Mutex
	models   map[string]fakeOllamaModel
	loaded   map[string]int
	shows    int
	creates  []map[string]any
	lastChat map[string]any
}

type fakeOllamaModel struct {
	embedding bool
	caps      []string // other /api/show capabilities, e.g. tools, vision
	max       int
	numCtx    int
	thinking  []any // thinking.values; nil omits the field
	modified  string
}

func newFakeOllama(t *testing.T, models map[string]fakeOllamaModel) (*fakeOllama, *httptest.Server) {
	t.Helper()
	f := &fakeOllama{models: models, loaded: map[string]int{}}
	srv := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(srv.Close)
	return f, srv
}

func (f *fakeOllama) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var body map[string]any
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch r.URL.Path {
	case "/v1/models":
		data := []map[string]string{}
		for id := range f.models {
			data = append(data, map[string]string{"id": id})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	case "/api/tags":
		models := []map[string]string{}
		for id, m := range f.models {
			models = append(models, map[string]string{"name": id, "modified_at": m.modified})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	case "/api/ps":
		models := []map[string]any{}
		for id, n := range f.loaded {
			models = append(models, map[string]any{"name": id, "context_length": n})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
	case "/api/show":
		f.shows++
		m, ok := f.models[body["model"].(string)]
		if !ok {
			http.Error(w, `{"error":"model not found"}`, http.StatusNotFound)
			return
		}
		resp := map[string]any{
			"model_info": map[string]any{"general.architecture": "qwen3", "qwen3.context_length": m.max},
			"parameters": "",
		}
		if m.numCtx > 0 {
			resp["parameters"] = fmt.Sprintf("temperature 0.6\nnum_ctx %d", m.numCtx)
		}
		if m.thinking != nil {
			resp["thinking"] = map[string]any{"values": m.thinking}
		}
		caps := append([]string{"completion"}, m.caps...)
		if m.embedding {
			caps = []string{"embedding"}
		}
		resp["capabilities"] = caps
		_ = json.NewEncoder(w).Encode(resp)
	case "/api/create":
		f.creates = append(f.creates, body)
		base := f.models[body["from"].(string)]
		params, _ := body["parameters"].(map[string]any)
		n, _ := params["num_ctx"].(float64)
		f.models[body["model"].(string)] = fakeOllamaModel{max: base.max, numCtx: int(n), thinking: base.thinking, modified: "new"}
		_, _ = w.Write([]byte(`{"status":"success"}`))
	case "/v1/chat/completions":
		f.lastChat = body
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"hi"}}]}`))
	default:
		http.NotFound(w, r)
	}
}

func newProvider(t *testing.T, cfg ProviderConfig) *Provider {
	t.Helper()
	p, err := NewProvider(cfg, func(string) string { return "key" }, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func byID(models []relaycontract.Model) map[string]relaycontract.Model {
	out := map[string]relaycontract.Model{}
	for _, m := range models {
		out[m.ID] = m
	}
	return out
}

func TestOllamaDiscoveryReportsContextAndReasoning(t *testing.T) {
	f, srv := newFakeOllama(t, map[string]fakeOllamaModel{
		"qwen3.6:35b": {max: 262144, numCtx: 32768, thinking: []any{false, true}, caps: []string{"tools", "vision"}, modified: "a"},
		"gpt-oss:20b": {max: 131072, thinking: []any{"low", "medium", "high"}, modified: "b"},
		"llama3:8b":   {max: 8192, modified: "c"},
		"bge-m3":      {max: 8192, embedding: true, modified: "d"},
	})
	f.loaded["gpt-oss:20b"] = 16384
	p := newProvider(t, ProviderConfig{Name: "ollama", Kind: KindOllama, BaseURL: srv.URL + "/v1"})
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byID(models)
	want := map[string]relaycontract.Model{
		"qwen3.6:35b": {ID: "qwen3.6:35b", Provider: "ollama", ContextWindow: 32768, MaxContextWindow: 262144, Reasoning: relaycontract.ReasoningToggle, Capabilities: []string{relaycontract.CapabilityTools, relaycontract.CapabilityVision}},
		"gpt-oss:20b": {ID: "gpt-oss:20b", Provider: "ollama", ContextWindow: 16384, MaxContextWindow: 131072, Reasoning: relaycontract.ReasoningEffort},
		// Neither num_ctx nor loaded: the allocation is unknown.
		"llama3:8b": {ID: "llama3:8b", Provider: "ollama", MaxContextWindow: 8192},
		// Ollama says which models embed.
		"bge-m3": {ID: "bge-m3", Provider: "ollama", MaxContextWindow: 8192, Capabilities: []string{relaycontract.CapabilityEmbeddings}},
	}
	for id, w := range want {
		if !reflect.DeepEqual(got[id], w) {
			t.Errorf("%s = %+v, want %+v", id, got[id], w)
		}
	}
	// A second listing with unchanged modified_at reads /api/show from cache.
	shows := f.shows
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.shows != shows {
		t.Fatalf("/api/show called %d more times on an unchanged catalog", f.shows-shows)
	}
}

func TestOllamaVariantIsCreatedOnceWithItsContext(t *testing.T) {
	f, srv := newFakeOllama(t, map[string]fakeOllamaModel{
		"qwen3.6:35b": {max: 262144, thinking: []any{false, true}, modified: "a"},
	})
	p := newProvider(t, ProviderConfig{
		Name: "ollama", Kind: KindOllama, BaseURL: srv.URL + "/v1", Allow: []string{"qwen3.6:35b"},
		Variants: map[string]Variant{"qwen3.6:35b-256k": {From: "qwen3.6:35b", ModelMeta: ModelMeta{ContextWindow: 262144}}},
	})
	for range 2 {
		models, err := p.ListModels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		v := byID(models)["qwen3.6:35b-256k"]
		if v.ContextWindow != 262144 || v.MaxContextWindow != 262144 || v.Reasoning != relaycontract.ReasoningToggle {
			t.Fatalf("variant = %+v", v)
		}
	}
	if len(f.creates) != 1 {
		t.Fatalf("creates = %v, want exactly one", f.creates)
	}
	c := f.creates[0]
	if c["model"] != "qwen3.6:35b-256k" || c["from"] != "qwen3.6:35b" || c["stream"] != false {
		t.Fatalf("create body = %v", c)
	}
	// A changed context in config recreates it.
	p.variants["qwen3.6:35b-256k"] = Variant{From: "qwen3.6:35b", ModelMeta: ModelMeta{ContextWindow: 131072}}
	if _, err := p.ListModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(f.creates) != 2 {
		t.Fatalf("creates after config change = %d, want 2", len(f.creates))
	}
	// The variant is a real Ollama model: requests keep its name.
	if _, err := p.ChatCompletions(context.Background(), json.RawMessage(`{"messages":[]}`), "qwen3.6:35b-256k", false, func(json.RawMessage) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if f.lastChat["model"] != "qwen3.6:35b-256k" {
		t.Fatalf("forwarded model = %v", f.lastChat["model"])
	}
}

func TestOpenRouterDiscoveryAndMetadataOverride(t *testing.T) {
	yes := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"id":"anthropic/claude-x","context_length":200000,"top_provider":{"context_length":180000},"supported_parameters":["tools","reasoning"]},
			{"id":"meta/llama","context_length":131072,"supported_parameters":["tools"]}
		]}`))
	}))
	t.Cleanup(srv.Close)
	p := newProvider(t, ProviderConfig{
		Name: "openrouter", Kind: KindOpenRouter, BaseURL: srv.URL,
		Metadata: map[string]ModelMeta{
			"meta/*":     {ContextWindow: 65536},
			"meta/llama": {Reasoning: relaycontract.ReasoningToggle, Vision: &yes, Canonical: "llama-3"},
		},
	})
	models, err := p.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := byID(models)
	if m := got["anthropic/claude-x"]; m.ContextWindow != 180000 || m.MaxContextWindow != 200000 || m.Reasoning != relaycontract.ReasoningEffort {
		t.Fatalf("claude = %+v", m)
	}
	if m := got["meta/llama"]; m.ContextWindow != 65536 || m.MaxContextWindow != 131072 || m.Reasoning != relaycontract.ReasoningToggle {
		t.Fatalf("llama = %+v", m)
	}
	// supported_parameters says which models call tools; config adds vision
	// and the canonical name.
	if m := got["anthropic/claude-x"]; !reflect.DeepEqual(m.Capabilities, []string{relaycontract.CapabilityTools}) {
		t.Fatalf("claude capabilities = %v", m.Capabilities)
	}
	if m := got["meta/llama"]; !reflect.DeepEqual(m.Capabilities, []string{relaycontract.CapabilityTools, relaycontract.CapabilityVision}) || m.Canonical != "llama-3" {
		t.Fatalf("llama = %+v", m)
	}
}

func TestAliasVariantAndReasoningTranslation(t *testing.T) {
	var last map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/models" {
			_, _ = w.Write([]byte(`{"data":[{"id":"gpt-x"}]}`))
			return
		}
		last = nil
		_ = json.NewDecoder(r.Body).Decode(&last)
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(srv.Close)
	for _, tc := range []struct {
		kind   string
		effort string
		check  func(map[string]any) bool
	}{
		{KindOpenAI, "low", func(b map[string]any) bool { return b["reasoning_effort"] == "low" && b["reasoning"] == nil }},
		{KindOpenRouter, "high", func(b map[string]any) bool {
			r, _ := b["reasoning"].(map[string]any)
			return b["reasoning_effort"] == nil && r["effort"] == "high"
		}},
		{KindOpenRouter, "none", func(b map[string]any) bool {
			r, _ := b["reasoning"].(map[string]any)
			return r["enabled"] == false
		}},
	} {
		p := newProvider(t, ProviderConfig{
			Name: "p", Kind: tc.kind, BaseURL: srv.URL,
			Variants: map[string]Variant{"gpt-x-small": {From: "gpt-x", ModelMeta: ModelMeta{ContextWindow: 8000, Reasoning: relaycontract.ReasoningEffort}}},
		})
		models, err := p.ListModels(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if v := byID(models)["gpt-x-small"]; v.ContextWindow != 8000 || v.Reasoning != relaycontract.ReasoningEffort {
			t.Fatalf("%s alias = %+v", tc.kind, v)
		}
		body := json.RawMessage(`{"messages":[],"reasoning_effort":"` + tc.effort + `"}`)
		if _, err := p.ChatCompletions(context.Background(), body, "gpt-x-small", false, func(json.RawMessage) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if last["model"] != "gpt-x" || !tc.check(last) {
			t.Fatalf("%s %s forwarded %v", tc.kind, tc.effort, last)
		}
	}
}

func TestNewProviderValidatesModelConfig(t *testing.T) {
	for name, cfg := range map[string]ProviderConfig{
		"reasoning":    {Metadata: map[string]ModelMeta{"x": {Reasoning: "maybe"}}},
		"negative":     {Metadata: map[string]ModelMeta{"x": {ContextWindow: -1}}},
		"no from":      {Variants: map[string]Variant{"x": {}}},
		"self":         {Variants: map[string]Variant{"x": {From: "x"}}},
		"glob variant": {Variants: map[string]Variant{"x*": {From: "y"}}},
	} {
		cfg.Name, cfg.Kind = "ollama", KindOllama
		if _, err := NewProvider(cfg, func(string) string { return "" }, nil); err == nil || !strings.Contains(err.Error(), "ollama") {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestSameCatalogSeesMetadataChanges(t *testing.T) {
	a := []relaycontract.Model{{ID: "m", Provider: "p", ContextWindow: 4096}}
	b := []relaycontract.Model{{ID: "m", Provider: "p", ContextWindow: 8192}}
	if sameCatalog(a, b) {
		t.Fatal("a changed context window must count as a catalog change")
	}
	if !sameCatalog(append(a, b...), append(b, a...)) {
		t.Fatal("order must not matter")
	}
}
