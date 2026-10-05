// Package relay implements the `tap relay` process: it offers the models of
// locally configured providers to Tapper Hub over an outbound WebSocket and
// forwards the inference requests Hub pushes back down to those providers.
//
// It also forwards tool calls to the MCP servers named in the same
// configuration, to hubs that speak relay protocol 2.
//
// The relay forwards inference and tool calls and nothing else. Destinations
// are the providers and MCP servers named in the user's own configuration;
// nothing Hub sends can name a command, URL, host, header, or environment
// variable. A tool call names only a server and tool the relay advertised.
package relay

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"path"
	"strings"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// Provider kinds.
const (
	KindOllama           = "ollama"
	KindOpenAI           = "openai"
	KindOpenRouter       = "openrouter"
	KindOpenAICompatible = "openai-compatible"
)

// Credential sources.
const (
	AuthNone         = "none"
	AuthAPIKey       = "apiKey"
	AuthSubscription = "subscription"
	AuthKeychain     = "keychain"
)

const (
	maxResponseBytes = 16 << 20
	maxSSELineBytes  = 4 << 20
)

// ProviderConfig describes one provider as configured by the user.
type ProviderConfig struct {
	Name      string
	Kind      string
	BaseURL   string
	Auth      string
	APIKeyEnv string
	Allow     []string
	Deny      []string
	// Transcription names the models (exact ids or path.Match globs) that
	// turn speech into text through /audio/transcriptions. /models does not
	// say which models those are, so the user does.
	Transcription []string
	// Embeddings names the models (exact ids or globs) that turn text into
	// vectors through /embeddings, for providers that do not say. Ollama
	// reports its own.
	Embeddings []string
	// MaxConcurrent bounds this provider's in-flight requests across every
	// hub. Zero means DefaultMaxConcurrent.
	MaxConcurrent int
	// Priority ranks the provider's models on Hub, lower preferred. Zero is
	// unranked.
	Priority int
	// Metadata fills in, or corrects, what the provider says about a model's
	// context and reasoning. Keys are exact ids or path.Match globs.
	Metadata map[string]ModelMeta
	// Variants are models offered under their own id that run another with
	// fixed settings, keyed by the offered id.
	Variants map[string]Variant
}

// DefaultMaxConcurrent is a provider's in-flight limit when its config sets
// none.
const DefaultMaxConcurrent = 4

// Provider reaches one OpenAI-compatible provider: its chat completions
// endpoint, and /audio/transcriptions for the Transcription models.
type Provider struct {
	name          string
	kind          string
	baseURL       string
	apiKey        string
	allow         []string
	deny          []string
	transcription []string
	embeddings    []string
	priority      int
	metadata      map[string]ModelMeta
	variants      map[string]Variant
	shows         showCache
	client        *http.Client
	// sem holds one token per in-flight request. A provider is its own
	// capacity — a local GPU and a hosted API have nothing to share.
	sem chan struct{}
}

type kindDefaults struct {
	baseURL   string
	auth      string
	apiKeyEnv string
}

var defaultsByKind = map[string]kindDefaults{
	KindOllama:           {baseURL: "http://127.0.0.1:11434/v1", auth: AuthNone},
	KindOpenAI:           {baseURL: "https://api.openai.com/v1", auth: AuthAPIKey, apiKeyEnv: "OPENAI_API_KEY"},
	KindOpenRouter:       {baseURL: "https://openrouter.ai/api/v1", auth: AuthAPIKey, apiKeyEnv: "OPENROUTER_API_KEY"},
	KindOpenAICompatible: {auth: AuthNone},
}

// NewProvider validates cfg and resolves its credential through getenv. The
// resolved key stays inside the Provider and is never logged.
func NewProvider(cfg ProviderConfig, getenv func(string) string, client *http.Client) (*Provider, error) {
	if !relaycontract.ValidName(cfg.Name) {
		return nil, fmt.Errorf("relay provider %q: name may contain only letters, digits, '.', '_' and '-'", cfg.Name)
	}
	kind := cfg.Kind
	if kind == "" {
		kind = cfg.Name
	}
	def, ok := defaultsByKind[kind]
	if !ok {
		return nil, fmt.Errorf("relay provider %q: unknown kind %q (want ollama, openai, openrouter, or openai-compatible)", cfg.Name, kind)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		baseURL = def.baseURL
	}
	if baseURL == "" {
		return nil, fmt.Errorf("relay provider %q: baseUrl is required for kind %s", cfg.Name, kind)
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		return nil, fmt.Errorf("relay provider %q: baseUrl must be http or https", cfg.Name)
	}
	auth := cfg.Auth
	if auth == "" {
		auth = def.auth
	}
	limit := cfg.MaxConcurrent
	if limit == 0 {
		limit = DefaultMaxConcurrent
	}
	if limit < 1 || limit > relaycontract.MaxConcurrentCap {
		return nil, fmt.Errorf("relay provider %q: maxConcurrent must be between 1 and %d, got %d", cfg.Name, relaycontract.MaxConcurrentCap, cfg.MaxConcurrent)
	}
	if cfg.Priority < 0 || cfg.Priority > relaycontract.MaxPriority {
		return nil, fmt.Errorf("relay provider %q: priority must be between 1 and %d, got %d", cfg.Name, relaycontract.MaxPriority, cfg.Priority)
	}
	for id, m := range cfg.Metadata {
		if err := validateMeta(cfg.Name, id, m); err != nil {
			return nil, err
		}
	}
	for id, v := range cfg.Variants {
		if id == "" || len(id) > relaycontract.MaxModelIDLength || strings.ContainsAny(id, "\x00\n\r*?[\\") {
			return nil, fmt.Errorf("relay provider %q: variant id %q is not a valid model id", cfg.Name, id)
		}
		if strings.TrimSpace(v.From) == "" || v.From == id {
			return nil, fmt.Errorf("relay provider %q: variant %q needs a different model in from", cfg.Name, id)
		}
		if err := validateMeta(cfg.Name, id, v.ModelMeta); err != nil {
			return nil, err
		}
	}
	p := &Provider{
		name: cfg.Name, kind: kind, baseURL: baseURL, allow: cfg.Allow, deny: cfg.Deny, transcription: cfg.Transcription, embeddings: cfg.Embeddings,
		priority: cfg.Priority, metadata: cfg.Metadata, variants: cfg.Variants, client: client, sem: make(chan struct{}, limit),
	}
	if p.client == nil {
		p.client = http.DefaultClient
	}
	switch auth {
	case AuthNone:
	case AuthAPIKey:
		env := cfg.APIKeyEnv
		if env == "" {
			env = def.apiKeyEnv
		}
		if env == "" {
			return nil, fmt.Errorf("relay provider %q: auth apiKey requires apiKeyEnv", cfg.Name)
		}
		p.apiKey = strings.TrimSpace(getenv(env))
		if p.apiKey == "" {
			return nil, fmt.Errorf("relay provider %q: environment variable %s is empty", cfg.Name, env)
		}
	case AuthSubscription, AuthKeychain:
		return nil, fmt.Errorf("relay provider %q: auth %s is not supported yet", cfg.Name, auth)
	default:
		return nil, fmt.Errorf("relay provider %q: unknown auth %q (want none or apiKey)", cfg.Name, auth)
	}
	return p, nil
}

// Name returns the provider name advertised to Hub.
func (p *Provider) Name() string { return p.name }

// Priority returns the provider's ranking on Hub, 0 when unranked.
func (p *Provider) Priority() int { return p.priority }

// MaxConcurrent returns the provider's in-flight limit.
func (p *Provider) MaxConcurrent() int { return cap(p.sem) }

// tryAcquire takes an in-flight slot without waiting; release returns it.
func (p *Provider) tryAcquire() bool {
	select {
	case p.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

func (p *Provider) release() { <-p.sem }

// Transcribes reports whether model is one of the configured transcription
// models.
func (p *Provider) Transcribes(model string) bool {
	for _, pattern := range p.transcription {
		if matchModel(pattern, model) {
			return true
		}
	}
	return false
}

// Embeds reports whether model is one of the configured embedding models.
func (p *Provider) Embeds(model string) bool {
	for _, pattern := range p.embeddings {
		if matchModel(pattern, model) {
			return true
		}
	}
	return false
}

// Embeddings posts an OpenAI /embeddings request and returns the provider's
// response as is. body is the caller's request; model is the offered id,
// rewritten to the provider's own name for an alias variant.
func (p *Provider) Embeddings(ctx context.Context, body json.RawMessage, model string) (json.RawMessage, *relaycontract.Usage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, nil, errors.New("request body must be a JSON object")
	}
	fields["model"], _ = json.Marshal(p.upstream(model))
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("call %s: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, nil, providerStatusError(p.name, resp)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return nil, nil, fmt.Errorf("read %s response: %w", p.name, err)
	}
	return raw, usageOf(raw), nil
}

// WithTranscriptionModels returns listed plus every exact (non-glob)
// transcription model id missing from it: a speech server often lists no
// models at all, or not the one configured.
func (p *Provider) WithTranscriptionModels(listed []relaycontract.Model) []relaycontract.Model {
	out := append([]relaycontract.Model(nil), listed...)
	seen := make(map[string]bool, len(listed))
	for _, m := range listed {
		seen[m.ID] = true
	}
	for _, pattern := range p.transcription {
		if strings.ContainsAny(pattern, "*?[\\") || seen[pattern] || !p.offers(pattern) {
			continue
		}
		seen[pattern] = true
		out = append(out, relaycontract.Model{ID: pattern, Provider: p.name})
	}
	return out
}

func (p *Provider) offers(model string) bool {
	for _, pattern := range p.deny {
		if matchModel(pattern, model) {
			return false
		}
	}
	if len(p.allow) == 0 {
		return true
	}
	for _, pattern := range p.allow {
		if matchModel(pattern, model) {
			return true
		}
	}
	return false
}

func matchModel(pattern, model string) bool {
	if pattern == model {
		return true
	}
	ok, err := path.Match(pattern, model)
	return err == nil && ok
}

// ProviderError reports a failure returned by the provider itself.
type ProviderError struct {
	Provider string
	Status   int
	Message  string
}

func (e *ProviderError) Error() string {
	return fmt.Sprintf("provider %s returned %d: %s", e.Provider, e.Status, e.Message)
}

func providerStatusError(name string, resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	msg := strings.TrimSpace(string(body))
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		msg = parsed.Error.Message
	}
	return &ProviderError{Provider: name, Status: resp.StatusCode, Message: msg}
}

// Transcribe posts a recording to the provider's OpenAI-style
// /audio/transcriptions endpoint and returns the transcript as a
// TranscriptionResult object. body is a relaycontract.TranscriptionRequest.
func (p *Provider) Transcribe(ctx context.Context, body json.RawMessage, model string) (json.RawMessage, error) {
	var in relaycontract.TranscriptionRequest
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		return nil, fmt.Errorf("transcription request: %w", err)
	}
	if err := in.Validate(); err != nil {
		return nil, fmt.Errorf("transcription request: %w", err)
	}
	var form bytes.Buffer
	w := multipart.NewWriter(&form)
	filename := in.Filename
	if filename == "" {
		filename = "audio" + audioExtension(in.MimeType)
	}
	part, err := w.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := part.Write(in.Audio); err != nil {
		return nil, err
	}
	fields := [][2]string{{"model", model}, {"response_format", "json"}, {"language", in.Language}, {"prompt", in.Prompt}}
	for _, f := range fields {
		if f[1] == "" {
			continue
		}
		if err := w.WriteField(f[0], f[1]); err != nil {
			return nil, err
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/audio/transcriptions", &form)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, providerStatusError(p.name, resp)
	}
	var out relaycontract.TranscriptionResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&out); err != nil {
		return nil, fmt.Errorf("read %s transcription: %w", p.name, err)
	}
	return json.Marshal(out)
}

// audioExtension picks a file extension for a recording's MIME type; speech
// servers commonly sniff the format from the upload's name.
func audioExtension(mimeType string) string {
	base, _, _ := mime.ParseMediaType(mimeType)
	switch base {
	case "audio/webm", "video/webm":
		return ".webm"
	case "audio/ogg":
		return ".ogg"
	case "audio/mp4", "audio/x-m4a", "video/mp4":
		return ".m4a"
	case "audio/mpeg":
		return ".mp3"
	case "audio/wav", "audio/x-wav", "audio/wave":
		return ".wav"
	}
	return ""
}

// ChatCompletions sends an OpenAI chat completions request with model and
// stream set from the caller, and passes each response object to emit: one
// object for a non-streaming request, one per SSE event otherwise.
func (p *Provider) ChatCompletions(ctx context.Context, body json.RawMessage, model string, stream bool, emit func(json.RawMessage) error) (*relaycontract.Usage, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil || fields == nil {
		return nil, errors.New("request body must be a JSON object")
	}
	fields["model"], _ = json.Marshal(p.upstream(model))
	fields["stream"], _ = json.Marshal(stream)
	p.translateReasoning(fields)
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, providerStatusError(p.name, resp)
	}
	if !stream {
		raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		if err != nil {
			return nil, fmt.Errorf("read %s response: %w", p.name, err)
		}
		if err := emit(raw); err != nil {
			return nil, err
		}
		return usageOf(raw), nil
	}
	return readSSE(resp.Body, emit)
}

func (p *Provider) authorize(req *http.Request) {
	if p.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	p.attribute(req)
}

// app is where a request came from, as Hub named it in infer.
type app struct{ title, url string }

type appKey struct{}

// withApp carries a request's app to the provider call it makes.
func withApp(ctx context.Context, a app) context.Context {
	if a == (app{}) {
		return ctx
	}
	return context.WithValue(ctx, appKey{}, a)
}

// attribute names the request's app to OpenRouter, which shows it in its
// logs and rankings (X-Title, HTTP-Referer). No other provider kind is told.
func (p *Provider) attribute(req *http.Request) {
	if p.kind != KindOpenRouter {
		return
	}
	a, _ := req.Context().Value(appKey{}).(app)
	if a.title != "" {
		req.Header.Set("X-Title", a.title)
	}
	if a.url != "" {
		req.Header.Set("HTTP-Referer", a.url)
	}
}

// readSSE forwards each `data:` event and returns the last usage reported.
func readSSE(r io.Reader, emit func(json.RawMessage) error) (*relaycontract.Usage, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64<<10), maxSSELineBytes)
	var usage *relaycontract.Usage
	for scanner.Scan() {
		line := scanner.Bytes()
		data, ok := bytes.CutPrefix(line, []byte("data:"))
		if !ok {
			continue
		}
		data = bytes.TrimSpace(data)
		if len(data) == 0 {
			continue
		}
		if bytes.Equal(data, []byte("[DONE]")) {
			break
		}
		obj := append(json.RawMessage(nil), data...)
		if u := usageOf(obj); u != nil {
			usage = u
		}
		if err := emit(obj); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read stream: %w", err)
	}
	return usage, nil
}

func usageOf(raw json.RawMessage) *relaycontract.Usage {
	var obj struct {
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(raw, &obj) != nil || obj.Usage == nil {
		return nil
	}
	return &relaycontract.Usage{PromptTokens: obj.Usage.PromptTokens, CompletionTokens: obj.Usage.CompletionTokens}
}
