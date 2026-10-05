package relay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// OpenRouter is told which app a request came from; no other provider is.
func TestProviderAttributesOnlyOpenRouter(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[]}`))
	}))
	t.Cleanup(srv.Close)
	provider := func(kind string) *Provider {
		t.Helper()
		p, err := NewProvider(ProviderConfig{Name: kind, Kind: kind, BaseURL: srv.URL, APIKeyEnv: "KEY"}, func(string) string { return "k" }, nil)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	call := func(p *Provider, ctx context.Context) {
		t.Helper()
		if _, err := p.ChatCompletions(ctx, json.RawMessage(`{"messages":[]}`), "m", false, func(json.RawMessage) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	ctx := withApp(context.Background(), app{title: "Foldwise Atlas Chat", url: "https://hub.example/chat"})

	call(provider(KindOpenRouter), ctx)
	if got.Get("X-Title") != "Foldwise Atlas Chat" || got.Get("HTTP-Referer") != "https://hub.example/chat" {
		t.Fatalf("openrouter headers = %v", got)
	}
	call(provider(KindOpenRouter), context.Background())
	if got.Get("X-Title") != "" || got.Get("HTTP-Referer") != "" {
		t.Fatalf("a request without an app was attributed: %v", got)
	}
	for _, kind := range []string{KindOllama, KindOpenAI, KindOpenAICompatible} {
		call(provider(kind), ctx)
		if got.Get("X-Title") != "" || got.Get("HTTP-Referer") != "" {
			t.Fatalf("%s was told the app: %v", kind, got)
		}
	}
}
