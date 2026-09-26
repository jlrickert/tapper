package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/jlrickert/tapper/pkg/relaycontract"
)

// ModelMeta is what the user's config says about a model when the provider
// does not say, or says it wrong. Zero fields leave discovery alone.
type ModelMeta struct {
	// ContextWindow is the window the provider serves the model with.
	ContextWindow int
	// MaxContextWindow is the most the model supports.
	MaxContextWindow int
	// Reasoning is "none", relaycontract.ReasoningToggle, or
	// relaycontract.ReasoningEffort; empty keeps what discovery found.
	Reasoning string
	// Tools and Vision say whether the model calls tools and accepts images;
	// nil keeps what discovery found.
	Tools, Vision *bool
	// Canonical is the provider-neutral name Hub pools the model under;
	// empty derives it with relaycontract.CanonicalModel.
	Canonical string
}

// ReasoningNone marks, in config, a model that does not reason even though
// discovery says it does.
const ReasoningNone = "none"

// Variant is a model offered under its own id that runs another with fixed
// settings. On Ollama a variant with a ContextWindow is created as a real
// model (`from` plus num_ctx), since Ollama's OpenAI API cannot set the
// context per request; elsewhere it is an alias the relay rewrites on the
// way out.
type Variant struct {
	From string
	ModelMeta
}

func validateMeta(provider, id string, m ModelMeta) error {
	if m.ContextWindow < 0 || m.MaxContextWindow < 0 {
		return fmt.Errorf("relay provider %q: model %q: context windows must be positive", provider, id)
	}
	switch m.Reasoning {
	case "", ReasoningNone, relaycontract.ReasoningToggle, relaycontract.ReasoningEffort:
	default:
		return fmt.Errorf("relay provider %q: model %q: reasoning must be none, toggle, or effort", provider, id)
	}
	if m.Canonical != "" && !relaycontract.ValidCanonical(m.Canonical) {
		return fmt.Errorf("relay provider %q: model %q: canonical must be lowercase letters, digits, '.', '_' or '-', at most %d long", provider, id, relaycontract.MaxCanonicalLength)
	}
	return nil
}

// apply lays the config over what discovery found.
func (m ModelMeta) apply(model *relaycontract.Model) {
	if m.ContextWindow > 0 {
		model.ContextWindow = m.ContextWindow
	}
	if m.MaxContextWindow > 0 {
		model.MaxContextWindow = m.MaxContextWindow
	}
	switch m.Reasoning {
	case "":
	case ReasoningNone:
		model.Reasoning = ""
	default:
		model.Reasoning = m.Reasoning
	}
	setCapability(model, relaycontract.CapabilityTools, m.Tools)
	setCapability(model, relaycontract.CapabilityVision, m.Vision)
	if m.Canonical != "" {
		model.Canonical = m.Canonical
	}
}

// setCapability adds or removes capability from model as on says; nil leaves
// it alone.
func setCapability(model *relaycontract.Model, capability string, on *bool) {
	if on == nil {
		return
	}
	model.Capabilities = slices.DeleteFunc(model.Capabilities, func(c string) bool { return c == capability })
	if *on {
		model.Capabilities = append(model.Capabilities, capability)
	}
}

// metaFor applies every metadata rule matching id: globs first, in key order,
// then an exact entry, so the most specific rule wins.
func (p *Provider) metaFor(id string, model *relaycontract.Model) {
	keys := make([]string, 0, len(p.metadata))
	for k := range p.metadata {
		if k != id && matchModel(k, id) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		p.metadata[k].apply(model)
	}
	if m, ok := p.metadata[id]; ok {
		m.apply(model)
	}
}

// upstream is the provider's own name for an offered model: an alias
// variant's base, else the id itself.
func (p *Provider) upstream(model string) string {
	if v, ok := p.variants[model]; ok && !p.createsVariant(v) {
		return v.From
	}
	return model
}

// createsVariant reports whether v is built as a real model on the provider
// rather than aliased.
func (p *Provider) createsVariant(v Variant) bool {
	return p.kind == KindOllama && v.ContextWindow > 0
}

// listedModel is one /models entry. OpenRouter adds limits and parameters;
// other providers send the id alone.
type listedModel struct {
	ID            string `json:"id"`
	ContextLength int    `json:"context_length"`
	TopProvider   struct {
		ContextLength int `json:"context_length"`
	} `json:"top_provider"`
	SupportedParameters []string `json:"supported_parameters"`
	Architecture        struct {
		InputModalities []string `json:"input_modalities"`
	} `json:"architecture"`
}

// ListModels returns the models the provider offers after allow and deny,
// with what the provider says about their context and reasoning, overlaid by
// the configured metadata, plus configured variants. The error is fatal only
// when models is nil; otherwise it reports metadata the relay could not get
// (an Ollama variant that could not be created, say) while still offering
// what it has.
func (p *Provider) ListModels(ctx context.Context) ([]relaycontract.Model, error) {
	var warnings []error
	if p.kind == KindOllama {
		warnings = append(warnings, p.ensureOllamaVariants(ctx)...)
	}
	listed, err := p.fetchModels(ctx)
	if err != nil {
		return nil, err
	}
	var ollama *ollamaState
	if p.kind == KindOllama {
		ollama = p.ollamaState(ctx)
	}
	out := make([]relaycontract.Model, 0, len(listed))
	seen := map[string]bool{}
	add := func(m relaycontract.Model) {
		if seen[m.ID] {
			return
		}
		seen[m.ID] = true
		p.metaFor(m.ID, &m)
		if v, ok := p.variants[m.ID]; ok {
			v.ModelMeta.apply(&m)
		}
		out = append(out, m)
	}
	byID := map[string]relaycontract.Model{}
	for _, l := range listed {
		if l.ID == "" {
			continue
		}
		m := relaycontract.Model{ID: l.ID, Provider: p.name}
		if l.ContextLength > 0 {
			m.MaxContextWindow = l.ContextLength
			m.ContextWindow = l.ContextLength
		}
		if l.TopProvider.ContextLength > 0 {
			m.ContextWindow = l.TopProvider.ContextLength
		}
		if slices.Contains(l.SupportedParameters, "reasoning") || slices.Contains(l.SupportedParameters, "reasoning_effort") {
			m.Reasoning = relaycontract.ReasoningEffort
		}
		if slices.Contains(l.SupportedParameters, "tools") {
			m.Capabilities = append(m.Capabilities, relaycontract.CapabilityTools)
		}
		if slices.Contains(l.Architecture.InputModalities, "image") {
			m.Capabilities = append(m.Capabilities, relaycontract.CapabilityVision)
		}
		if ollama != nil {
			ollama.describe(ctx, p, &m)
		}
		if p.Embeds(l.ID) {
			m.Capabilities = []string{relaycontract.CapabilityEmbeddings}
		}
		byID[l.ID] = m
		if p.offers(l.ID) || p.isVariant(l.ID) {
			add(m)
		}
	}
	// Alias variants ride on a listed base; one whose base is missing is not
	// offered, since every request to it would fail.
	names := make([]string, 0, len(p.variants))
	for n := range p.variants {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := p.variants[n]
		if p.createsVariant(v) {
			continue
		}
		base, ok := byID[v.From]
		if !ok {
			warnings = append(warnings, fmt.Errorf("variant %q: base model %q is not listed", n, v.From))
			continue
		}
		base.ID = n
		add(base)
	}
	return out, errors.Join(warnings...)
}

func (p *Provider) isVariant(id string) bool {
	_, ok := p.variants[id]
	return ok
}

func (p *Provider) fetchModels(ctx context.Context) ([]listedModel, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("list models from %s: %w", p.name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, providerStatusError(p.name, resp)
	}
	var list struct {
		Data []listedModel `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&list); err != nil {
		return nil, fmt.Errorf("list models from %s: %w", p.name, err)
	}
	return list.Data, nil
}

// --- Ollama ------------------------------------------------------------------

// ollamaRoot is the native API root: the OpenAI base with its /v1 removed.
func (p *Provider) ollamaRoot() string {
	return strings.TrimSuffix(p.baseURL, "/v1")
}

// ollamaShow is what /api/show says about one model, as the relay uses it.
type ollamaShow struct {
	max       int
	numCtx    int
	reasoning string
	embedding bool
	tools     bool
	vision    bool
}

// showCache keeps /api/show answers per model until its modified_at moves,
// so a catalog refresh does not re-read every model.
type showCache struct {
	mu      sync.Mutex
	entries map[string]showEntry
}

type showEntry struct {
	modifiedAt string
	show       ollamaShow
}

// ollamaState is one listing's view of the Ollama server: when each model
// was last modified, and the context of each loaded one.
type ollamaState struct {
	modified map[string]string
	loaded   map[string]int
}

func (p *Provider) ollamaState(ctx context.Context) *ollamaState {
	st := &ollamaState{modified: map[string]string{}, loaded: map[string]int{}}
	var tags struct {
		Models []struct {
			Name       string `json:"name"`
			ModifiedAt string `json:"modified_at"`
		} `json:"models"`
	}
	if p.ollamaGet(ctx, "/api/tags", &tags) == nil {
		for _, m := range tags.Models {
			st.modified[m.Name] = m.ModifiedAt
		}
	}
	var ps struct {
		Models []struct {
			Name          string `json:"name"`
			ContextLength int    `json:"context_length"`
		} `json:"models"`
	}
	if p.ollamaGet(ctx, "/api/ps", &ps) == nil {
		for _, m := range ps.Models {
			if m.ContextLength > 0 {
				st.loaded[m.Name] = m.ContextLength
			}
		}
	}
	return st
}

// describe fills m from /api/show and /api/ps. The allocated window is the
// model's num_ctx, else what the loaded model runs with; left 0 when neither
// is known, since Ollama's server default is not reported.
func (st *ollamaState) describe(ctx context.Context, p *Provider, m *relaycontract.Model) {
	show, ok := p.cachedShow(ctx, m.ID, st.modified[m.ID])
	if !ok {
		return
	}
	m.MaxContextWindow = show.max
	m.Reasoning = show.reasoning
	switch {
	case show.embedding:
		m.Capabilities = []string{relaycontract.CapabilityEmbeddings}
	default:
		m.Capabilities = nil
		if show.tools {
			m.Capabilities = append(m.Capabilities, relaycontract.CapabilityTools)
		}
		if show.vision {
			m.Capabilities = append(m.Capabilities, relaycontract.CapabilityVision)
		}
	}
	switch {
	case show.numCtx > 0:
		m.ContextWindow = show.numCtx
	case st.loaded[m.ID] > 0:
		m.ContextWindow = st.loaded[m.ID]
	}
	if m.MaxContextWindow > 0 && m.ContextWindow > m.MaxContextWindow {
		m.ContextWindow = m.MaxContextWindow
	}
}

func (p *Provider) cachedShow(ctx context.Context, model, modifiedAt string) (ollamaShow, bool) {
	p.shows.mu.Lock()
	e, ok := p.shows.entries[model]
	p.shows.mu.Unlock()
	if ok && modifiedAt != "" && e.modifiedAt == modifiedAt {
		return e.show, true
	}
	show, err := p.showOllama(ctx, model)
	if err != nil {
		return ollamaShow{}, false
	}
	p.shows.mu.Lock()
	if p.shows.entries == nil {
		p.shows.entries = map[string]showEntry{}
	}
	p.shows.entries[model] = showEntry{modifiedAt: modifiedAt, show: show}
	p.shows.mu.Unlock()
	return show, true
}

var numCtxParam = regexp.MustCompile(`(?m)^\s*num_ctx\s+(\d+)\s*$`)

// errNotFound is an Ollama 404: the model does not exist.
var errNotFound = errors.New("not found")

func (p *Provider) showOllama(ctx context.Context, model string) (ollamaShow, error) {
	var resp struct {
		ModelInfo    map[string]json.RawMessage `json:"model_info"`
		Parameters   string                     `json:"parameters"`
		Capabilities []string                   `json:"capabilities"`
		Thinking     *struct {
			Values []json.RawMessage `json:"values"`
		} `json:"thinking"`
	}
	if err := p.ollamaPost(ctx, "/api/show", map[string]any{"model": model}, &resp); err != nil {
		return ollamaShow{}, err
	}
	var show ollamaShow
	show.embedding = slices.Contains(resp.Capabilities, "embedding")
	show.tools = slices.Contains(resp.Capabilities, "tools")
	show.vision = slices.Contains(resp.Capabilities, "vision")
	for k, v := range resp.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			var n int
			if json.Unmarshal(v, &n) == nil && n > 0 {
				show.max = n
			}
		}
	}
	if m := numCtxParam.FindStringSubmatch(resp.Parameters); m != nil {
		show.numCtx, _ = strconv.Atoi(m[1])
	}
	// thinking.values lists what the model accepts: strings are effort
	// levels, booleans alone only switch thinking on or off. Older servers
	// omit it and say "thinking" in capabilities.
	if resp.Thinking != nil {
		thinks := false
		for _, v := range resp.Thinking.Values {
			var level string
			if json.Unmarshal(v, &level) == nil && level != "" {
				show.reasoning = relaycontract.ReasoningEffort
				break
			}
			var on bool
			if json.Unmarshal(v, &on) == nil && on {
				thinks = true
			}
		}
		if show.reasoning == "" && thinks {
			show.reasoning = relaycontract.ReasoningToggle
		}
	} else if slices.Contains(resp.Capabilities, "thinking") {
		show.reasoning = relaycontract.ReasoningToggle
	}
	return show, nil
}

// ensureOllamaVariants creates each context variant that is missing, or whose
// num_ctx no longer matches the config. Creating from an existing model only
// writes a new manifest; the weights are shared.
func (p *Provider) ensureOllamaVariants(ctx context.Context) []error {
	var errs []error
	names := make([]string, 0, len(p.variants))
	for n := range p.variants {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		v := p.variants[n]
		if !p.createsVariant(v) {
			continue
		}
		show, err := p.showOllama(ctx, n)
		if err == nil && show.numCtx == v.ContextWindow {
			continue
		}
		if err != nil && !errors.Is(err, errNotFound) {
			errs = append(errs, fmt.Errorf("variant %q: %w", n, err))
			continue
		}
		body := map[string]any{"model": n, "from": v.From, "parameters": map[string]any{"num_ctx": v.ContextWindow}, "stream": false}
		if err := p.ollamaPost(ctx, "/api/create", body, nil); err != nil {
			errs = append(errs, fmt.Errorf("variant %q: create from %q: %w", n, v.From, err))
			continue
		}
		p.shows.mu.Lock()
		delete(p.shows.entries, n)
		p.shows.mu.Unlock()
	}
	return errs
}

func (p *Provider) ollamaGet(ctx context.Context, route string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.ollamaRoot()+route, nil)
	if err != nil {
		return err
	}
	return p.ollamaDo(req, out)
}

func (p *Provider) ollamaPost(ctx context.Context, route string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.ollamaRoot()+route, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return p.ollamaDo(req, out)
}

func (p *Provider) ollamaDo(req *http.Request, out any) error {
	p.authorize(req)
	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errNotFound
	}
	if resp.StatusCode/100 != 2 {
		return providerStatusError(p.name, resp)
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(out)
}

// --- Reasoning on the way out -----------------------------------------------

// translateReasoning rewrites Hub's normalized reasoning_effort (none, low,
// medium, high) into what the provider takes. Ollama and OpenAI read it as
// is; OpenRouter wants a reasoning object.
func (p *Provider) translateReasoning(fields map[string]json.RawMessage) {
	raw, ok := fields["reasoning_effort"]
	if !ok || p.kind != KindOpenRouter {
		return
	}
	var effort string
	if json.Unmarshal(raw, &effort) != nil {
		return
	}
	delete(fields, "reasoning_effort")
	if effort == "none" {
		fields["reasoning"], _ = json.Marshal(map[string]any{"enabled": false})
		return
	}
	fields["reasoning"], _ = json.Marshal(map[string]any{"effort": effort})
}
