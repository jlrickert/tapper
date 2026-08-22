package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// Sentinel errors for remote-API-specific failure conditions.
var (
	// ErrUnauthorized indicates the API request lacked valid authentication
	// credentials (HTTP 401).
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden indicates the authenticated user lacks permission for the
	// requested operation (HTTP 403).
	ErrForbidden = errors.New("forbidden")
)

// apiErrorEnvelope is the JSON error envelope returned by tapper-hub:
// {"error": msg, "code": CODE}.
type apiErrorEnvelope struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// RemoteKeg implements [Keg] over tapper-hub's operation-level HTTP API.
// Each Keg method is a single HTTP round trip against the per-keg base URL
// (`<hub>/api/v1/@{namespace}/kegs/{keg}`); all orchestration — locking
// discipline, dex/index maintenance, stats touching — happens server-side
// inside the hub's LocalKeg.
type RemoteKeg struct {
	// baseURL is the full URL prefix including the keg path, for example
	// "https://hub.example.com/api/v1/@myns/kegs/mykeg". No trailing slash.
	baseURL string

	// token is the bearer token sent in the Authorization header on every
	// request. Empty means unauthenticated.
	token string

	// tokenFn, when set, is consulted on every request instead of the static
	// token. NewKegFromTarget installs a closure over the target's token
	// resolution chain so a keg cached for hours (e.g. by a long-running MCP
	// server) picks up refreshed credentials instead of pinning the token it
	// was constructed with.
	tokenFn func() string

	// client is the HTTP client used for all requests.
	client *http.Client

	// logger receives diagnostic output (live watch retries and
	// terminations). When nil, diagnostics are dropped.
	logger *slog.Logger

	// rt provides the clock used to timestamp Create payloads and the
	// runtime threaded into content parsing.
	rt *toolkit.Runtime

	// target is the keg's resolved location, when known.
	target *Target
}

// NewRemoteKeg constructs a RemoteKeg speaking the hub's operation API at
// baseURL (the per-keg prefix, trailing slash trimmed) with bearer token
// authentication.
func NewRemoteKeg(baseURL, token string, rt *toolkit.Runtime) *RemoteKeg {
	var logger *slog.Logger
	if rt != nil {
		logger = rt.Logger()
	}
	return &RemoteKeg{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		client:  http.DefaultClient,
		logger:  logger,
		rt:      rt,
	}
}

// RemoteKeg implements the full Keg interface.
var _ Keg = (*RemoteKeg)(nil)

// BaseURL returns the per-keg API prefix this client targets.
func (k *RemoteKeg) BaseURL() string { return k.baseURL }

// Token returns the bearer token used for authentication ("" when none).
func (k *RemoteKeg) Token() string { return k.currentToken() }

// SetTokenFn installs a per-request token source. Each request calls fn and
// uses its return value for the Authorization header; an empty return sends
// the request unauthenticated. Pass nil to revert to the static token.
func (k *RemoteKeg) SetTokenFn(fn func() string) {
	k.tokenFn = fn
}

// currentToken returns the token for the next request: the per-request
// source when installed, else the static token from construction.
func (k *RemoteKeg) currentToken() string {
	if k.tokenFn != nil {
		return k.tokenFn()
	}
	return k.token
}

// Target returns the keg's resolved location, or nil when unknown.
func (k *RemoteKeg) Target() *Target {
	if k == nil {
		return nil
	}
	return k.target
}

// SetTarget records the keg's resolved location.
func (k *RemoteKeg) SetTarget(target *Target) {
	k.target = target
}

// --- HTTP plumbing ---

// do executes an HTTP request with authentication and context propagation.
func (k *RemoteKeg) do(ctx context.Context, method, path string, body io.Reader, contentType string, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, k.baseURL+path, body)
	if err != nil {
		return nil, NewBackendError("remote", method+" "+path, 0, err, false)
	}
	if token := k.currentToken(); token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	for key, vals := range header {
		for _, v := range vals {
			req.Header.Add(key, v)
		}
	}
	for key, val := range ValidationHeaderValues(ctx) {
		if req.Header.Get(key) == "" {
			req.Header.Set(key, val)
		}
	}
	resp, err := k.httpClient().Do(req)
	if err != nil {
		return nil, NewBackendError("remote", method+" "+path, 0, err, true)
	}
	return resp, nil
}

// httpClient returns the configured HTTP client or the default.
func (k *RemoteKeg) httpClient() *http.Client {
	if k.client != nil {
		return k.client
	}
	return http.DefaultClient
}

// mapError translates a non-2xx response into the matching sentinel or typed
// error using the hub's error envelope. The response body is read and closed.
func (k *RemoteKeg) mapError(resp *http.Response, op string) error {
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var env apiErrorEnvelope
	var msg, code string
	if json.Unmarshal(body, &env) == nil && env.Error != "" {
		msg, code = env.Error, env.Code
	} else {
		msg = strings.TrimSpace(string(body))
	}

	// Include the request method + URL so the error names exactly which hub,
	// namespace, keg, and node were targeted. A bare "404 not found" can't
	// tell a missing keg from a missing node from an auth-masked resource.
	where := op
	if resp.Request != nil && resp.Request.URL != nil {
		where = fmt.Sprintf("%s %s %s", op, resp.Request.Method, resp.Request.URL)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		if msg == "" {
			msg = resp.Status
		}
		return NewRateLimitError(retryAfter, msg, nil)
	}

	// A 404 with no parseable envelope (e.g. a HEAD response or a proxy
	// error page) still means the resource is absent.
	if code == "" && resp.StatusCode == http.StatusNotFound {
		if msg == "" {
			msg = resp.Status
		}
		return fmt.Errorf("%w: %s: %s", ErrNotExist, where, msg)
	}

	if msg == "" {
		msg = resp.Status
	}
	return RemoteErrorFromCode(code, resp.StatusCode, where+": "+msg)
}

// parseRetryAfter parses a Retry-After header value as seconds.
func parseRetryAfter(val string) time.Duration {
	if val == "" {
		return 0
	}
	secs, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// readBody reads and closes the response body when the status is one of
// okStatuses; otherwise it maps the response to an error.
func (k *RemoteKeg) readBody(resp *http.Response, op string, okStatuses ...int) ([]byte, error) {
	for _, ok := range okStatuses {
		if resp.StatusCode == ok {
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, NewBackendError("remote", op, resp.StatusCode, err, true)
			}
			return data, nil
		}
	}
	return nil, k.mapError(resp, op)
}

// getJSON issues a GET and decodes the JSON response into out.
func (k *RemoteKeg) getJSON(ctx context.Context, path, op string, out any) error {
	resp, err := k.do(ctx, http.MethodGet, path, nil, "", nil)
	if err != nil {
		return err
	}
	body, err := k.readBody(resp, op, http.StatusOK)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return NewBackendError("remote", op, 0, fmt.Errorf("invalid response: %w", err), false)
	}
	return nil
}

// postJSON issues a POST with a JSON payload and decodes the JSON response
// into out (skipped when out is nil).
func (k *RemoteKeg) postJSON(ctx context.Context, path, op string, in, out any, okStatuses ...int) error {
	return k.jsonRequest(ctx, http.MethodPost, path, op, in, out, okStatuses...)
}

// putJSON issues a PUT with a JSON payload and decodes the JSON response.
func (k *RemoteKeg) putJSON(ctx context.Context, path, op string, in, out any, okStatuses ...int) error {
	return k.jsonRequest(ctx, http.MethodPut, path, op, in, out, okStatuses...)
}

func (k *RemoteKeg) jsonRequest(ctx context.Context, method, path, op string, in, out any, okStatuses ...int) error {
	payload, err := json.Marshal(in)
	if err != nil {
		return NewBackendError("remote", op, 0, err, false)
	}
	resp, err := k.do(ctx, method, path, bytes.NewReader(payload), "application/json", nil)
	if err != nil {
		return err
	}
	body, err := k.readBody(resp, op, okStatuses...)
	if err != nil {
		return err
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return NewBackendError("remote", op, 0, fmt.Errorf("invalid response: %w", err), false)
	}
	return nil
}

// --- Init / config ---

// Init implements Keg. Remote kegs are created through the hub's
// keg-creation endpoint (POST /api/v1/@{namespace}/kegs) at the Tap layer,
// not through the per-keg operation API.
func (k *RemoteKeg) Init(ctx context.Context) error {
	return fmt.Errorf("remote keg init: use the hub keg-creation endpoint: %w", ErrNotSupported)
}

// Config implements Keg via GET /config.
func (k *RemoteKeg) Config(ctx context.Context) (*Config, error) {
	cfg := &Config{}
	if err := k.getJSON(ctx, "/config", "Config", cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

// SetConfig implements Keg via PUT /config with the raw config bytes.
func (k *RemoteKeg) SetConfig(ctx context.Context, data []byte) error {
	resp, err := k.do(ctx, http.MethodPut, "/config", bytes.NewReader(data), "application/octet-stream", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "SetConfig", http.StatusOK, http.StatusNoContent)
	return err
}

// ListSchemas implements Keg via GET /schemas.
func (k *RemoteKeg) ListSchemas(ctx context.Context) ([]string, error) {
	var names []string
	if err := k.getJSON(ctx, "/schemas", "ListSchemas", &names); err != nil {
		return nil, err
	}
	return names, nil
}

// ReadSchema implements Keg via GET /schemas/{type}.
func (k *RemoteKeg) ReadSchema(ctx context.Context, typeName string) ([]byte, error) {
	resp, err := k.do(ctx, http.MethodGet, "/schemas/"+url.PathEscape(typeName), nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, "ReadSchema", http.StatusOK)
}

// WriteSchema implements Keg via PUT /schemas/{type}.
func (k *RemoteKeg) WriteSchema(ctx context.Context, typeName string, data []byte) error {
	resp, err := k.do(ctx, http.MethodPut, "/schemas/"+url.PathEscape(typeName), bytes.NewReader(data), "application/yaml", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "WriteSchema", http.StatusOK, http.StatusNoContent)
	return err
}

// DeleteSchema implements Keg via DELETE /schemas/{type}.
func (k *RemoteKeg) DeleteSchema(ctx context.Context, typeName string) error {
	resp, err := k.do(ctx, http.MethodDelete, "/schemas/"+url.PathEscape(typeName), nil, "", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "DeleteSchema", http.StatusOK, http.StatusNoContent)
	return err
}

// ValidateNode implements Keg via POST /nodes/{id}/validate.
func (k *RemoteKeg) ValidateNode(ctx context.Context, id NodeId) (*SchemaValidationResult, error) {
	var result SchemaValidationResult
	if err := k.postJSON(ctx, fmt.Sprintf("/nodes/%d/validate", id.ID), "ValidateNode", struct{}{}, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return &result, nil
}

// ValidateNodePayload implements Keg via POST /validate.
func (k *RemoteKeg) ValidateNodePayload(ctx context.Context, payload NodeValidationPayload) (*SchemaValidationResult, error) {
	req := struct {
		ID      int     `json:"id"`
		Schema  string  `json:"schema,omitempty"`
		Content *string `json:"content,omitempty"`
		Meta    *string `json:"meta,omitempty"`
	}{ID: payload.ID.ID, Schema: payload.Schema}
	if payload.HasContent {
		content := string(payload.Content)
		req.Content = &content
	}
	if payload.HasMeta {
		meta := string(payload.Meta)
		req.Meta = &meta
	}
	var result SchemaValidationResult
	if err := k.postJSON(ctx, "/validate", "ValidateNodePayload", req, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return &result, nil
}

// --- Node lifecycle ---

// Create implements Keg via a single POST /nodes carrying the composed
// content (and meta when tags/attrs are set). The node payload is composed
// client-side exactly as LocalKeg composes it; the hub assigns the id.
func (k *RemoteKeg) Create(ctx context.Context, opts *CreateOptions) (CreateResult, error) {
	if opts == nil {
		opts = &CreateOptions{}
	}
	results, err := k.CreateNodes(ctx, []NodeCreate{{Key: "node", Schema: opts.Schema, Title: opts.Title, Lead: opts.Lead, Body: opts.Body, Tags: opts.Tags, Attrs: opts.Attrs}})
	if len(results) == 0 {
		return CreateResult{}, err
	}
	return CreateResult{ID: results[0].ID, Validation: results[0].Validation}, err
}

// Next implements Keg via GET /nodes/next.
func (k *RemoteKeg) Next(ctx context.Context) (NodeId, error) {
	var result struct {
		ID int `json:"id"`
	}
	if err := k.getJSON(ctx, "/nodes/next", "Next", &result); err != nil {
		return NodeId{}, err
	}
	return NodeId{ID: result.ID}, nil
}

// ListNodes implements Keg via GET /nodes.
func (k *RemoteKeg) ListNodes(ctx context.Context) ([]NodeId, error) {
	var ids []int
	if err := k.getJSON(ctx, "/nodes", "ListNodes", &ids); err != nil {
		return nil, err
	}
	result := make([]NodeId, len(ids))
	for i, id := range ids {
		result[i] = NodeId{ID: id}
	}
	return result, nil
}

// NodeExists implements Keg via HEAD /nodes/{id}.
func (k *RemoteKeg) NodeExists(ctx context.Context, id NodeId) (bool, error) {
	resp, err := k.do(ctx, http.MethodHead, fmt.Sprintf("/nodes/%d", id.ID), nil, "", nil)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusNoContent:
		return true, nil
	case http.StatusNotFound:
		return false, nil
	default:
		return false, k.mapError(resp, "NodeExists")
	}
}

// parseRewritten decodes the {"rewritten": ["3","5"]} envelope shared by
// Move and Remove into node ids.
func parseRewritten(paths []string) ([]NodeId, error) {
	out := make([]NodeId, 0, len(paths))
	for _, p := range paths {
		id, err := ParseNode(p)
		if err != nil {
			return nil, NewBackendError("remote", "rewritten", 0,
				fmt.Errorf("invalid rewritten node id %q: %w", p, err), false)
		}
		out = append(out, *id)
	}
	return out, nil
}

// Move implements Keg via POST /nodes/{src}/move.
func (k *RemoteKeg) Move(ctx context.Context, src NodeId, dst NodeId) ([]NodeId, error) {
	var result struct {
		Rewritten []string `json:"rewritten"`
	}
	req := struct {
		Dst int `json:"dst"`
	}{Dst: dst.ID}
	path := fmt.Sprintf("/nodes/%d/move", src.ID)
	if err := k.postJSON(ctx, path, "Move", req, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return parseRewritten(result.Rewritten)
}

// Remove implements Keg via DELETE /nodes/{id}.
func (k *RemoteKeg) Remove(ctx context.Context, id NodeId) ([]NodeId, error) {
	resp, err := k.do(ctx, http.MethodDelete, fmt.Sprintf("/nodes/%d", id.ID), nil, "", nil)
	if err != nil {
		return nil, err
	}
	body, err := k.readBody(resp, "Remove", http.StatusOK)
	if err != nil {
		return nil, err
	}
	var result struct {
		Rewritten []string `json:"rewritten"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, NewBackendError("remote", "Remove", 0, fmt.Errorf("invalid response: %w", err), false)
	}
	return parseRewritten(result.Rewritten)
}

// Commit implements Keg via POST /nodes/{id}/commit.
func (k *RemoteKeg) Commit(ctx context.Context, id NodeId) error {
	return k.postJSON(ctx, fmt.Sprintf("/nodes/%d/commit", id.ID), "Commit",
		struct{}{}, nil, http.StatusOK, http.StatusNoContent)
}

// Touch implements Keg via POST /nodes/{id}/touch.
func (k *RemoteKeg) Touch(ctx context.Context, id NodeId) error {
	return k.postJSON(ctx, fmt.Sprintf("/nodes/%d/touch", id.ID), "Touch",
		struct{}{}, nil, http.StatusOK, http.StatusNoContent)
}

// --- Node data ---

// ReadNode implements Keg via GET /nodes/{id}: the node's full state in one
// round trip.
func (k *RemoteKeg) ReadNode(ctx context.Context, id NodeId) (*NodeView, error) {
	var resp struct {
		ID      string          `json:"id"`
		Content string          `json:"content"`
		Meta    string          `json:"meta"`
		Stats   json.RawMessage `json:"stats"`
		Assets  []string        `json:"assets"`
		Images  []string        `json:"images"`
	}
	if err := k.getJSON(ctx, fmt.Sprintf("/nodes/%d", id.ID), "ReadNode", &resp); err != nil {
		return nil, err
	}

	viewID := id
	if parsed, err := ParseNode(resp.ID); err == nil {
		viewID = *parsed
	}
	stats := &NodeStats{}
	if len(bytes.TrimSpace(resp.Stats)) > 0 && !bytes.Equal(bytes.TrimSpace(resp.Stats), []byte("null")) {
		parsed, err := ParseStats(ctx, resp.Stats)
		if err != nil {
			return nil, NewBackendError("remote", "ReadNode", 0,
				fmt.Errorf("invalid stats response: %w", err), false)
		}
		stats = parsed
	}
	return &NodeView{
		ID:      viewID,
		Content: []byte(resp.Content),
		Meta:    []byte(resp.Meta),
		Stats:   stats,
		Files:   resp.Assets,
		Images:  resp.Images,
	}, nil
}

// GetContent implements Keg via GET /nodes/{id}/content.
func (k *RemoteKeg) GetContent(ctx context.Context, id NodeId) ([]byte, error) {
	resp, err := k.do(ctx, http.MethodGet, fmt.Sprintf("/nodes/%d/content", id.ID), nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, "GetContent", http.StatusOK)
}

// SetContent implements Keg through the aggregate JSON mutation endpoint.
func (k *RemoteKeg) SetContent(ctx context.Context, id NodeId, data []byte) error {
	_, err := k.UpdateNode(ctx, NodeUpdateOptions{ID: id, Content: data})
	return err
}

// GetMeta implements Keg. Absent meta yields an empty meta rather than an
// error, matching LocalKeg semantics.
func (k *RemoteKeg) GetMeta(ctx context.Context, id NodeId) (*NodeMeta, error) {
	raw, err := k.GetMetaRaw(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			return NewMeta(ctx, time.Time{}), nil
		}
		return nil, err
	}
	return ParseMeta(ctx, raw)
}

// GetMetaRaw implements Keg via GET /nodes/{id}/meta. Missing meta
// propagates ErrNotExist.
func (k *RemoteKeg) GetMetaRaw(ctx context.Context, id NodeId) ([]byte, error) {
	resp, err := k.do(ctx, http.MethodGet, fmt.Sprintf("/nodes/%d/meta", id.ID), nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, "GetMetaRaw", http.StatusOK)
}

// SetMeta implements Keg through the aggregate JSON mutation endpoint.
func (k *RemoteKeg) SetMeta(ctx context.Context, id NodeId, meta *NodeMeta) error {
	if meta == nil {
		meta = NewMeta(ctx, time.Time{})
	}
	_, err := k.UpdateNodes(ctx, []NodeUpdateOptions{{ID: id, Meta: []byte(meta.ToYAML()), HasMeta: true}})
	return err
}

// GetStats implements Keg via GET /nodes/{id}/stats. Stats are
// server-managed; there is no write counterpart.
func (k *RemoteKeg) GetStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	resp, err := k.do(ctx, http.MethodGet, fmt.Sprintf("/nodes/%d/stats", id.ID), nil, "", nil)
	if err != nil {
		return nil, err
	}
	body, err := k.readBody(resp, "GetStats", http.StatusOK)
	if err != nil {
		return nil, err
	}
	return ParseStats(ctx, body)
}

// --- Listing, query, and index ---

// Dex implements Keg via GET /dex, which returns every index artifact in
// one response. The artifacts are loaded into a scratch in-memory repo and
// parsed through the same NewDexFromRepo path LocalKeg uses.
func (k *RemoteKeg) Dex(ctx context.Context) (*Dex, error) {
	var result struct {
		Indexes map[string]string `json:"indexes"`
	}
	if err := k.getJSON(ctx, "/dex", "Dex", &result); err != nil {
		return nil, err
	}
	scratch := NewMemoryRepo(k.rt)
	for name, content := range result.Indexes {
		if err := scratch.WriteIndex(ctx, name, []byte(content)); err != nil {
			return nil, NewBackendError("remote", "Dex", 0, err, false)
		}
	}
	return NewDexFromRepo(ctx, scratch)
}

// Query implements Keg via POST /query.
func (k *RemoteKeg) Query(ctx context.Context, opts QueryOptions) ([]NodeIndexEntry, error) {
	var result struct {
		Entries []NodeIndexEntry `json:"entries"`
	}
	req := struct {
		Expr string `json:"expr"`
	}{Expr: opts.Expr}
	if err := k.postJSON(ctx, "/query", "Query", req, &result, http.StatusOK); err != nil {
		return nil, err
	}
	return result.Entries, nil
}

// Grep implements Keg via POST /grep.
func (k *RemoteKeg) Grep(ctx context.Context, opts GrepOptions) ([]GrepMatch, error) {
	var result struct {
		Matches []struct {
			Entry NodeIndexEntry `json:"entry"`
			Lines []string       `json:"lines"`
		} `json:"matches"`
	}
	req := struct {
		Pattern    string `json:"pattern"`
		IgnoreCase bool   `json:"ignore_case"`
		MaxLines   int    `json:"max_lines"`
	}{Pattern: opts.Pattern, IgnoreCase: opts.IgnoreCase, MaxLines: opts.MaxLines}
	if err := k.postJSON(ctx, "/grep", "Grep", req, &result, http.StatusOK); err != nil {
		return nil, err
	}
	out := make([]GrepMatch, len(result.Matches))
	for i, m := range result.Matches {
		out[i] = GrepMatch{Entry: m.Entry, Lines: m.Lines}
	}
	return out, nil
}

// Index implements Keg via POST /index/rebuild.
func (k *RemoteKeg) Index(ctx context.Context, opts IndexOptions) error {
	req := struct {
		NoUpdate bool `json:"no_update"`
	}{NoUpdate: opts.NoUpdate}
	return k.postJSON(ctx, "/index/rebuild", "Index", req, nil, http.StatusOK, http.StatusNoContent)
}

// ListIndexes implements Keg via GET /indexes.
func (k *RemoteKeg) ListIndexes(ctx context.Context) ([]string, error) {
	var names []string
	if err := k.getJSON(ctx, "/indexes", "ListIndexes", &names); err != nil {
		return nil, err
	}
	return names, nil
}

// ReadIndex implements Keg via GET /indexes/{name}.
func (k *RemoteKeg) ReadIndex(ctx context.Context, name string) ([]byte, error) {
	resp, err := k.do(ctx, http.MethodGet, "/indexes/"+url.PathEscape(name), nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, "ReadIndex", http.StatusOK)
}

// Summary implements Keg via GET /summary.
func (k *RemoteKeg) Summary(ctx context.Context) (*KegSummary, error) {
	summary := &KegSummary{}
	if err := k.getJSON(ctx, "/summary", "Summary", summary); err != nil {
		return nil, err
	}
	return summary, nil
}

// --- Assets and images ---

// listAssets fetches an asset name list (assets or images) for a node.
func (k *RemoteKeg) listAssets(ctx context.Context, id NodeId, kind, op string) ([]string, error) {
	var names []string
	if err := k.getJSON(ctx, fmt.Sprintf("/nodes/%d/%s", id.ID, kind), op, &names); err != nil {
		return nil, err
	}
	return names, nil
}

// readAsset fetches one asset payload (asset or image) for a node.
func (k *RemoteKeg) readAsset(ctx context.Context, id NodeId, kind, name, op string) ([]byte, error) {
	path := fmt.Sprintf("/nodes/%d/%s/%s", id.ID, kind, url.PathEscape(name))
	resp, err := k.do(ctx, http.MethodGet, path, nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, op, http.StatusOK)
}

// writeAsset stores one asset payload (asset or image) for a node.
func (k *RemoteKeg) writeAsset(ctx context.Context, id NodeId, kind, name string, data []byte, op string) error {
	path := fmt.Sprintf("/nodes/%d/%s/%s", id.ID, kind, url.PathEscape(name))
	resp, err := k.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/octet-stream", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, op, http.StatusOK, http.StatusNoContent)
	return err
}

// deleteAsset removes one asset (asset or image) from a node.
func (k *RemoteKeg) deleteAsset(ctx context.Context, id NodeId, kind, name, op string) error {
	path := fmt.Sprintf("/nodes/%d/%s/%s", id.ID, kind, url.PathEscape(name))
	resp, err := k.do(ctx, http.MethodDelete, path, nil, "", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, op, http.StatusOK, http.StatusNoContent)
	return err
}

// ListFiles implements Keg via GET /nodes/{id}/assets.
func (k *RemoteKeg) ListFiles(ctx context.Context, id NodeId) ([]string, error) {
	return k.listAssets(ctx, id, "assets", "ListFiles")
}

// ReadFile implements Keg via GET /nodes/{id}/assets/{name}.
func (k *RemoteKeg) ReadFile(ctx context.Context, id NodeId, name string) ([]byte, error) {
	return k.readAsset(ctx, id, "assets", name, "ReadFile")
}

// WriteFile implements Keg via PUT /nodes/{id}/assets/{name}.
func (k *RemoteKeg) WriteFile(ctx context.Context, id NodeId, name string, data []byte) error {
	return k.writeAsset(ctx, id, "assets", name, data, "WriteFile")
}

// DeleteFile implements Keg via DELETE /nodes/{id}/assets/{name}.
func (k *RemoteKeg) DeleteFile(ctx context.Context, id NodeId, name string) error {
	return k.deleteAsset(ctx, id, "assets", name, "DeleteFile")
}

// ListImages implements Keg via GET /nodes/{id}/images.
func (k *RemoteKeg) ListImages(ctx context.Context, id NodeId) ([]string, error) {
	return k.listAssets(ctx, id, "images", "ListImages")
}

// ReadImage implements Keg via GET /nodes/{id}/images/{name}.
func (k *RemoteKeg) ReadImage(ctx context.Context, id NodeId, name string) ([]byte, error) {
	return k.readAsset(ctx, id, "images", name, "ReadImage")
}

// WriteImage implements Keg via PUT /nodes/{id}/images/{name}.
func (k *RemoteKeg) WriteImage(ctx context.Context, id NodeId, name string, data []byte) error {
	return k.writeAsset(ctx, id, "images", name, data, "WriteImage")
}

// DeleteImage implements Keg via DELETE /nodes/{id}/images/{name}.
func (k *RemoteKeg) DeleteImage(ctx context.Context, id NodeId, name string) error {
	return k.deleteAsset(ctx, id, "images", name, "DeleteImage")
}

// --- Snapshots ---

// remoteSnapshotEntry is the hub's snapshot wire shape (one revision's
// metadata), shared by list, get, and append responses.
type remoteSnapshotEntry struct {
	ID           int64  `json:"id"`
	Node         int    `json:"node"`
	Parent       int64  `json:"parent"`
	CreatedAt    string `json:"created_at"`
	Message      string `json:"message"`
	ContentHash  string `json:"content_hash,omitempty"`
	MetaHash     string `json:"meta_hash,omitempty"`
	StatsHash    string `json:"stats_hash,omitempty"`
	IsCheckpoint bool   `json:"is_checkpoint"`
}

// remoteSnapshotDetail extends the entry with the optionally resolved
// content/meta/stats payloads (GET /nodes/{id}/snapshots/{rev}).
type remoteSnapshotDetail struct {
	remoteSnapshotEntry
	Content []byte          `json:"content,omitempty"`
	Meta    []byte          `json:"meta,omitempty"`
	Stats   json.RawMessage `json:"stats,omitempty"`
}

func snapshotFromRemoteEntry(entry remoteSnapshotEntry) (Snapshot, error) {
	createdAt := time.Time{}
	if strings.TrimSpace(entry.CreatedAt) != "" {
		t, err := time.Parse(time.RFC3339, entry.CreatedAt)
		if err != nil {
			return Snapshot{}, err
		}
		createdAt = t
	}
	return Snapshot{
		ID:           RevisionID(entry.ID),
		Node:         NodeId{ID: entry.Node},
		Parent:       RevisionID(entry.Parent),
		CreatedAt:    createdAt,
		Message:      entry.Message,
		ContentHash:  entry.ContentHash,
		MetaHash:     entry.MetaHash,
		StatsHash:    entry.StatsHash,
		IsCheckpoint: entry.IsCheckpoint,
	}, nil
}

// AppendSnapshot implements Keg via POST /nodes/{id}/snapshots. The body
// carries only the message — the hub computes the snapshot payloads from the
// node's current server-side state.
func (k *RemoteKeg) AppendSnapshot(ctx context.Context, id NodeId, msg string) (Snapshot, error) {
	results, err := k.AppendSnapshots(ctx, []NodeSnapshotRequest{{ID: id, Message: msg}})
	if len(results) == 0 {
		return Snapshot{}, err
	}
	return results[0], err
}

// ListSnapshots implements Keg via GET /nodes/{id}/snapshots.
func (k *RemoteKeg) ListSnapshots(ctx context.Context, id NodeId) ([]Snapshot, error) {
	var entries []remoteSnapshotEntry
	if err := k.getJSON(ctx, fmt.Sprintf("/nodes/%d/snapshots", id.ID), "ListSnapshots", &entries); err != nil {
		return nil, err
	}
	result := make([]Snapshot, len(entries))
	for i, entry := range entries {
		snap, err := snapshotFromRemoteEntry(entry)
		if err != nil {
			return nil, NewBackendError("remote", "ListSnapshots", 0,
				fmt.Errorf("invalid response timestamp: %w", err), false)
		}
		result[i] = snap
	}
	return result, nil
}

// GetSnapshot implements Keg via GET /nodes/{id}/snapshots/{rev}, passing
// resolve_content when opts request materialized payloads.
func (k *RemoteKeg) GetSnapshot(ctx context.Context, id NodeId, rev RevisionID, opts SnapshotReadOptions) (Snapshot, []byte, []byte, *NodeStats, error) {
	path := fmt.Sprintf("/nodes/%d/snapshots/%d", id.ID, int64(rev))
	if opts.ResolveContent {
		path += "?resolve_content=true"
	}
	var detail remoteSnapshotDetail
	if err := k.getJSON(ctx, path, "GetSnapshot", &detail); err != nil {
		return Snapshot{}, nil, nil, nil, err
	}
	snap, err := snapshotFromRemoteEntry(detail.remoteSnapshotEntry)
	if err != nil {
		return Snapshot{}, nil, nil, nil, NewBackendError("remote", "GetSnapshot", 0,
			fmt.Errorf("invalid response timestamp: %w", err), false)
	}
	var stats *NodeStats
	if len(bytes.TrimSpace(detail.Stats)) > 0 && !bytes.Equal(bytes.TrimSpace(detail.Stats), []byte("null")) {
		stats, err = ParseStats(ctx, detail.Stats)
		if err != nil {
			return Snapshot{}, nil, nil, nil, NewBackendError("remote", "GetSnapshot", 0,
				fmt.Errorf("invalid stats response: %w", err), false)
		}
	}
	return snap, detail.Content, detail.Meta, stats, nil
}

// ReadContentAt implements Keg via GET /nodes/{id}/snapshots/{rev}/content.
func (k *RemoteKeg) ReadContentAt(ctx context.Context, id NodeId, rev RevisionID) ([]byte, error) {
	path := fmt.Sprintf("/nodes/%d/snapshots/%d/content", id.ID, int64(rev))
	resp, err := k.do(ctx, http.MethodGet, path, nil, "", nil)
	if err != nil {
		return nil, err
	}
	return k.readBody(resp, "ReadContentAt", http.StatusOK)
}

// RestoreSnapshot implements Keg via POST /nodes/{id}/snapshots/{rev}/restore.
func (k *RemoteKeg) RestoreSnapshot(ctx context.Context, id NodeId, rev RevisionID) error {
	path := fmt.Sprintf("/nodes/%d/snapshots/%d/restore", id.ID, int64(rev))
	return k.postJSON(ctx, path, "RestoreSnapshot", struct{}{}, nil, http.StatusOK, http.StatusNoContent)
}

// --- Bulk transfer ---

// ExportNodes implements Keg via GET /archive. The hub's response body is
// the keg-archive stream and is returned directly without buffering; the
// caller must Close it.
func (k *RemoteKeg) ExportNodes(ctx context.Context, opts ExportNodesOptions) (io.ReadCloser, error) {
	q := url.Values{}
	if len(opts.NodeIDs) > 0 {
		paths := make([]string, len(opts.NodeIDs))
		for i, id := range opts.NodeIDs {
			paths[i] = id.Path()
		}
		q.Set("nodes", strings.Join(paths, ","))
	}
	if strings.TrimSpace(opts.Query) != "" {
		q.Set("query", opts.Query)
	}
	if opts.SkipZeroNode {
		q.Set("skip_zero", "1")
	}
	if opts.WithHistory {
		q.Set("history", "1")
	}
	if opts.HistoryIfSupported {
		q.Set("history_if_supported", "1")
	}
	if opts.WithAssets {
		q.Set("assets", "1")
	}
	path := "/archive"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	resp, err := k.do(ctx, http.MethodGet, path, nil, "", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, k.mapError(resp, "ExportNodes")
	}
	return resp.Body, nil
}

// ImportNodes implements Keg via POST /archive, streaming r as the request
// body.
func (k *RemoteKeg) ImportNodes(ctx context.Context, r io.Reader, opts ImportNodesOptions) ([]ImportedNode, error) {
	q := url.Values{}
	if opts.AssignNewIDs {
		q.Set("assign_new_ids", "1")
	}
	if opts.HistoryIfSupported {
		q.Set("history_if_supported", "1")
	}
	if opts.SourceAlias != "" {
		q.Set("source_alias", opts.SourceAlias)
	}
	if opts.TargetAlias != "" {
		q.Set("target_alias", opts.TargetAlias)
	}
	path := "/archive"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	resp, err := k.do(ctx, http.MethodPost, path, r, "application/gzip", nil)
	if err != nil {
		return nil, err
	}
	body, err := k.readBody(resp, "ImportNodes", http.StatusOK, http.StatusCreated)
	if err != nil {
		return nil, err
	}
	var result struct {
		Imported []struct {
			Source     string `json:"source"`
			SourceHash string `json:"source_hash"`
			ID         string `json:"id"`
		} `json:"imported"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, NewBackendError("remote", "ImportNodes", 0,
			fmt.Errorf("invalid response: %w", err), false)
	}
	out := make([]ImportedNode, 0, len(result.Imported))
	for _, item := range result.Imported {
		id, err := ParseNode(item.ID)
		if err != nil {
			return nil, NewBackendError("remote", "ImportNodes", 0,
				fmt.Errorf("invalid imported node id %q: %w", item.ID, err), false)
		}
		out = append(out, ImportedNode{SourceID: item.Source, SourceHash: item.SourceHash, ID: *id})
	}
	return out, nil
}

// --- Cross-process locks ---

// remoteLockInfo is the hub's lock-state wire shape, shared by LockNode and
// LockStatus responses.
type remoteLockInfo struct {
	Token      string `json:"token"`
	TTLSeconds int    `json:"ttl_seconds"`
	AcquiredAt string `json:"acquired_at,omitempty"`
	Holder     string `json:"holder,omitempty"`
}

func (li remoteLockInfo) toLockInfo() LockInfo {
	info := LockInfo{
		Token:      LockToken(li.Token),
		TTLSeconds: li.TTLSeconds,
		Holder:     li.Holder,
	}
	if li.AcquiredAt != "" {
		if t, err := time.Parse(time.RFC3339, li.AcquiredAt); err == nil {
			info.AcquiredAt = t
		}
	}
	return info
}

// Lock implements Keg via POST /nodes/{id}/lock. The hub owns the lease; no
// client-side renewal goroutine runs.
func (k *RemoteKeg) Lock(ctx context.Context, id NodeId) (LockInfo, error) {
	var info remoteLockInfo
	path := fmt.Sprintf("/nodes/%d/lock", id.ID)
	if err := k.postJSON(ctx, path, "Lock", struct{}{}, &info, http.StatusOK, http.StatusCreated); err != nil {
		return LockInfo{}, err
	}
	return info.toLockInfo(), nil
}

// Unlock implements Keg via DELETE /nodes/{id}/lock with the X-Lock-Token
// header proving ownership.
func (k *RemoteKeg) Unlock(ctx context.Context, id NodeId, token LockToken) error {
	header := make(http.Header)
	if token != "" {
		header.Set("X-Lock-Token", string(token))
	}
	resp, err := k.do(ctx, http.MethodDelete, fmt.Sprintf("/nodes/%d/lock", id.ID), nil, "", header)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "Unlock", http.StatusOK, http.StatusNoContent)
	return err
}

// LockStatus implements Keg via GET /nodes/{id}/lock; a zero LockInfo means
// unheld.
func (k *RemoteKeg) LockStatus(ctx context.Context, id NodeId) (LockInfo, error) {
	var info remoteLockInfo
	if err := k.getJSON(ctx, fmt.Sprintf("/nodes/%d/lock", id.ID), "LockStatus", &info); err != nil {
		return LockInfo{}, err
	}
	return info.toLockInfo(), nil
}

// ForceUnlock implements Keg via DELETE /nodes/{id}/lock?force=1.
func (k *RemoteKeg) ForceUnlock(ctx context.Context, id NodeId) error {
	resp, err := k.do(ctx, http.MethodDelete, fmt.Sprintf("/nodes/%d/lock?force=1", id.ID), nil, "", nil)
	if err != nil {
		return err
	}
	_, err = k.readBody(resp, "ForceUnlock", http.StatusOK, http.StatusNoContent)
	return err
}
