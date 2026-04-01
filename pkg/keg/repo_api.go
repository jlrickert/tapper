package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Sentinel errors for API-specific failure conditions.
var (
	// ErrUnauthorized indicates the API request lacked valid authentication
	// credentials (HTTP 401).
	ErrUnauthorized = errors.New("unauthorized")

	// ErrForbidden indicates the authenticated user lacks permission for the
	// requested operation (HTTP 403).
	ErrForbidden = errors.New("forbidden")
)

// apiErrorResponse is the JSON error envelope returned by tapper-hub.
type apiErrorResponse struct {
	Error string `json:"error"`
	Code  string `json:"code"`
}

// ApiRepo implements [Repository] using tapper-hub's REST API as the storage
// backend. It maps each Repository method to an HTTP call against
// `/api/v1/kegs/@{keg}/...` endpoints, with bearer token authentication on
// every request.
//
// ApiRepo also maintains a per-node ETag cache for optimistic concurrency
// control, following the decision in KEG node 307. Reads populate the cache,
// and writes include an `If-Match` header when an ETag is available. A 409
// response maps to [ErrConflict].
type ApiRepo struct {
	// BaseURL is the full URL prefix including the keg path, for example
	// "https://hub.example.com/api/v1/kegs/@mykeg". No trailing slash.
	BaseURL string

	// Token is the bearer token sent in the Authorization header on every
	// request. It is resolved from the target's Token or TokenEnv field.
	Token string

	// Client is the HTTP client used for all requests. When nil,
	// http.DefaultClient is used.
	Client *http.Client

	// etagMu guards the etags map.
	etagMu sync.RWMutex
	// etags caches the last-seen ETag per resource path (e.g., "/nodes/42/content").
	etags map[string]string
}

// NewApiRepo constructs an ApiRepo for the given base URL and bearer token.
func NewApiRepo(baseURL, token string) *ApiRepo {
	return &ApiRepo{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		etags:   make(map[string]string),
	}
}

// Name implements Repository.
func (a *ApiRepo) Name() string {
	return "api"
}

// httpClient returns the configured HTTP client or the default.
func (a *ApiRepo) httpClient() *http.Client {
	if a.Client != nil {
		return a.Client
	}
	return http.DefaultClient
}

// do executes an HTTP request with authentication and context propagation.
func (a *ApiRepo) do(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Response, error) {
	url := a.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, NewBackendError(a.Name(), method+" "+path, 0, err, false)
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// Include If-Match header when we have a cached ETag for this resource.
	if method == http.MethodPut || method == http.MethodPost || method == http.MethodDelete {
		a.etagMu.RLock()
		if etag, ok := a.etags[path]; ok {
			req.Header.Set("If-Match", etag)
		}
		a.etagMu.RUnlock()
	}

	resp, err := a.httpClient().Do(req)
	if err != nil {
		return nil, NewBackendError(a.Name(), method+" "+path, 0, err, true)
	}

	// Store ETag from response if present.
	if etag := resp.Header.Get("ETag"); etag != "" {
		a.etagMu.Lock()
		a.etags[path] = etag
		a.etagMu.Unlock()
	}

	return resp, nil
}

// mapError translates an HTTP status code to the appropriate sentinel or typed
// error. The response body is read and closed.
func (a *ApiRepo) mapError(resp *http.Response, op string) error {
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	var msg string
	var apiErr apiErrorResponse
	if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
		msg = apiErr.Error
	} else {
		msg = strings.TrimSpace(string(body))
	}
	if msg == "" {
		msg = resp.Status
	}

	switch resp.StatusCode {
	case http.StatusNotFound:
		return fmt.Errorf("%w: %s: %s", ErrNotExist, op, msg)
	case http.StatusConflict:
		return fmt.Errorf("%w: %s: %s", ErrConflict, op, msg)
	case http.StatusUnauthorized:
		return fmt.Errorf("%w: %s: %s", ErrUnauthorized, op, msg)
	case http.StatusForbidden:
		return fmt.Errorf("%w: %s: %s", ErrForbidden, op, msg)
	case http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return NewRateLimitError(retryAfter, msg, nil)
	default:
		transient := resp.StatusCode >= 500
		return NewBackendError(a.Name(), op, resp.StatusCode, errors.New(msg), transient)
	}
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

// readBody reads the full response body and closes it. Returns an error if the
// status is not one of the expected codes.
func (a *ApiRepo) readBody(resp *http.Response, op string, okStatuses ...int) ([]byte, error) {
	for _, ok := range okStatuses {
		if resp.StatusCode == ok {
			defer resp.Body.Close()
			data, err := io.ReadAll(resp.Body)
			if err != nil {
				return nil, NewBackendError(a.Name(), op, resp.StatusCode, err, true)
			}
			return data, nil
		}
	}
	return nil, a.mapError(resp, op)
}

// --- Repository: Node lifecycle ---

// HasNode implements Repository.
func (a *ApiRepo) HasNode(ctx context.Context, id NodeId) (bool, error) {
	path := fmt.Sprintf("/nodes/%d", id.ID)
	resp, err := a.do(ctx, http.MethodHead, path, nil, "")
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
		return false, a.mapError(resp, "HasNode")
	}
}

// Next implements Repository.
func (a *ApiRepo) Next(ctx context.Context) (NodeId, error) {
	resp, err := a.do(ctx, http.MethodPost, "/nodes/next", nil, "")
	if err != nil {
		return NodeId{}, err
	}
	body, err := a.readBody(resp, "Next", http.StatusOK, http.StatusCreated)
	if err != nil {
		return NodeId{}, err
	}

	var result struct {
		ID int `json:"id"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return NodeId{}, NewBackendError(a.Name(), "Next", 0,
			fmt.Errorf("invalid response: %w", err), false)
	}
	return NodeId{ID: result.ID}, nil
}

// ListNodes implements Repository.
func (a *ApiRepo) ListNodes(ctx context.Context) ([]NodeId, error) {
	resp, err := a.do(ctx, http.MethodGet, "/nodes", nil, "")
	if err != nil {
		return nil, err
	}
	body, err := a.readBody(resp, "ListNodes", http.StatusOK)
	if err != nil {
		return nil, err
	}

	var ids []int
	if err := json.Unmarshal(body, &ids); err != nil {
		return nil, NewBackendError(a.Name(), "ListNodes", 0,
			fmt.Errorf("invalid response: %w", err), false)
	}

	result := make([]NodeId, len(ids))
	for i, id := range ids {
		result[i] = NodeId{ID: id}
	}
	return result, nil
}

// MoveNode implements Repository.
func (a *ApiRepo) MoveNode(ctx context.Context, id NodeId, dst NodeId) error {
	path := fmt.Sprintf("/nodes/%d/move", id.ID)
	payload, _ := json.Marshal(struct {
		Dst int `json:"dst"`
	}{Dst: dst.ID})

	resp, err := a.do(ctx, http.MethodPost, path, bytes.NewReader(payload), "application/json")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "MoveNode", http.StatusOK, http.StatusNoContent)
	return err
}

// DeleteNode implements Repository.
func (a *ApiRepo) DeleteNode(ctx context.Context, id NodeId) error {
	path := fmt.Sprintf("/nodes/%d", id.ID)
	resp, err := a.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "DeleteNode", http.StatusOK, http.StatusNoContent)
	return err
}

// --- Repository: Node primary data ---

// WithNodeLock implements Repository. The lock is managed server-side using a
// lease mechanism. The client acquires a lease, executes the callback, and
// releases the lease.
func (a *ApiRepo) WithNodeLock(ctx context.Context, id NodeId, fn func(context.Context) error) error {
	if fn == nil {
		return fmt.Errorf("fn required")
	}
	if contextHasNodeLock(ctx, id) {
		return fn(ctx)
	}

	// Acquire lock.
	lockPath := fmt.Sprintf("/nodes/%d/lock", id.ID)
	resp, err := a.do(ctx, http.MethodPost, lockPath, nil, "")
	if err != nil {
		return errors.Join(ErrLock, err)
	}
	body, err := a.readBody(resp, "WithNodeLock/acquire", http.StatusOK, http.StatusCreated)
	if err != nil {
		return errors.Join(ErrLock, err)
	}

	var lease struct {
		Token string `json:"token"`
		TTL   int    `json:"ttl"`
	}
	if err := json.Unmarshal(body, &lease); err != nil {
		return errors.Join(ErrLock, NewBackendError(a.Name(), "WithNodeLock/acquire", 0,
			fmt.Errorf("invalid lease response: %w", err), false))
	}

	// Start background lease renewal if TTL is available.
	renewCtx, renewCancel := context.WithCancel(ctx)
	defer renewCancel()
	if lease.TTL > 0 && lease.Token != "" {
		go a.renewLease(renewCtx, id.ID, lease.Token, time.Duration(lease.TTL)*time.Second)
	}

	// Execute callback.
	lockedCtx := contextWithNodeLock(ctx, id)
	runErr := fn(lockedCtx)

	// Release lock.
	renewCancel() // Stop renewal goroutine.
	releaseErr := a.releaseLock(ctx, id.ID, lease.Token)

	return errors.Join(runErr, releaseErr)
}

// renewLease periodically renews the lock lease until the context is canceled.
func (a *ApiRepo) renewLease(ctx context.Context, nodeID int, token string, ttl time.Duration) {
	// Renew at half the TTL interval to give margin for network latency.
	interval := ttl / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			renewPath := fmt.Sprintf("/nodes/%d/lock/renew", nodeID)
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.BaseURL+renewPath, nil)
			if err != nil {
				return
			}
			if a.Token != "" {
				req.Header.Set("Authorization", "Bearer "+a.Token)
			}
			req.Header.Set("X-Lock-Token", token)
			resp, err := a.httpClient().Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return
			}
		}
	}
}

// releaseLock sends a DELETE to release the node lock.
func (a *ApiRepo) releaseLock(ctx context.Context, nodeID int, token string) error {
	releasePath := fmt.Sprintf("/nodes/%d/lock", nodeID)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, a.BaseURL+releasePath, nil)
	if err != nil {
		return errors.Join(ErrLock, NewBackendError(a.Name(), "WithNodeLock/release", 0, err, false))
	}
	if a.Token != "" {
		req.Header.Set("Authorization", "Bearer "+a.Token)
	}
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
	resp, err := a.httpClient().Do(req)
	if err != nil {
		return errors.Join(ErrLock, NewBackendError(a.Name(), "WithNodeLock/release", 0, err, true))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return a.mapError(resp, "WithNodeLock/release")
	}
	return nil
}

// ReadContent implements Repository.
func (a *ApiRepo) ReadContent(ctx context.Context, id NodeId) ([]byte, error) {
	path := fmt.Sprintf("/nodes/%d/content", id.ID)
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return a.readBody(resp, "ReadContent", http.StatusOK)
}

// WriteContent implements Repository.
func (a *ApiRepo) WriteContent(ctx context.Context, id NodeId, data []byte) error {
	path := fmt.Sprintf("/nodes/%d/content", id.ID)
	resp, err := a.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteContent", http.StatusOK, http.StatusNoContent)
	return err
}

// ReadMeta implements Repository.
func (a *ApiRepo) ReadMeta(ctx context.Context, id NodeId) ([]byte, error) {
	path := fmt.Sprintf("/nodes/%d/meta", id.ID)
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return a.readBody(resp, "ReadMeta", http.StatusOK)
}

// WriteMeta implements Repository.
func (a *ApiRepo) WriteMeta(ctx context.Context, id NodeId, data []byte) error {
	path := fmt.Sprintf("/nodes/%d/meta", id.ID)
	resp, err := a.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteMeta", http.StatusOK, http.StatusNoContent)
	return err
}

// ReadStats implements Repository.
func (a *ApiRepo) ReadStats(ctx context.Context, id NodeId) (*NodeStats, error) {
	path := fmt.Sprintf("/nodes/%d/stats", id.ID)
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	body, err := a.readBody(resp, "ReadStats", http.StatusOK)
	if err != nil {
		return nil, err
	}
	return ParseStats(ctx, body)
}

// WriteStats implements Repository.
func (a *ApiRepo) WriteStats(ctx context.Context, id NodeId, stats *NodeStats) error {
	if stats == nil {
		stats = &NodeStats{}
	}
	data, err := stats.ToJSON()
	if err != nil {
		return NewBackendError(a.Name(), "WriteStats", 0, err, false)
	}
	path := fmt.Sprintf("/nodes/%d/stats", id.ID)
	resp, err := a.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/json")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteStats", http.StatusOK, http.StatusNoContent)
	return err
}

// --- Repository: Indexes ---

// GetIndex implements Repository.
func (a *ApiRepo) GetIndex(ctx context.Context, name string) ([]byte, error) {
	path := "/indexes/" + url.PathEscape(name)
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return a.readBody(resp, "GetIndex", http.StatusOK)
}

// WriteIndex implements Repository.
func (a *ApiRepo) WriteIndex(ctx context.Context, name string, data []byte) error {
	path := "/indexes/" + url.PathEscape(name)
	resp, err := a.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteIndex", http.StatusOK, http.StatusNoContent)
	return err
}

// ListIndexes implements Repository.
func (a *ApiRepo) ListIndexes(ctx context.Context) ([]string, error) {
	resp, err := a.do(ctx, http.MethodGet, "/indexes", nil, "")
	if err != nil {
		return nil, err
	}
	body, err := a.readBody(resp, "ListIndexes", http.StatusOK)
	if err != nil {
		return nil, err
	}

	var names []string
	if err := json.Unmarshal(body, &names); err != nil {
		return nil, NewBackendError(a.Name(), "ListIndexes", 0,
			fmt.Errorf("invalid response: %w", err), false)
	}
	return names, nil
}

// ClearIndexes implements Repository.
func (a *ApiRepo) ClearIndexes(ctx context.Context) error {
	resp, err := a.do(ctx, http.MethodDelete, "/indexes", nil, "")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "ClearIndexes", http.StatusOK, http.StatusNoContent)
	return err
}

// --- Repository: Config ---

// ReadConfig implements Repository.
func (a *ApiRepo) ReadConfig(ctx context.Context) (*Config, error) {
	resp, err := a.do(ctx, http.MethodGet, "/config", nil, "")
	if err != nil {
		return nil, err
	}
	body, err := a.readBody(resp, "ReadConfig", http.StatusOK)
	if err != nil {
		return nil, err
	}

	cfg := &Config{}
	if err := json.Unmarshal(body, cfg); err != nil {
		return nil, NewBackendError(a.Name(), "ReadConfig", 0,
			fmt.Errorf("invalid config response: %w", err), false)
	}
	return cfg, nil
}

// WriteConfig implements Repository.
func (a *ApiRepo) WriteConfig(ctx context.Context, config *Config) error {
	data, err := config.ToJSON()
	if err != nil {
		return NewBackendError(a.Name(), "WriteConfig", 0, err, false)
	}
	resp, err := a.do(ctx, http.MethodPut, "/config", bytes.NewReader(data), "application/json")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteConfig", http.StatusOK, http.StatusNoContent)
	return err
}

// --- Optional interfaces ---

// ListFiles implements RepositoryFiles.
func (a *ApiRepo) ListFiles(ctx context.Context, id NodeId) ([]string, error) {
	path := fmt.Sprintf("/nodes/%d/files", id.ID)
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	body, err := a.readBody(resp, "ListFiles", http.StatusOK)
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(body, &names); err != nil {
		return nil, NewBackendError(a.Name(), "ListFiles", 0,
			fmt.Errorf("invalid response: %w", err), false)
	}
	return names, nil
}

// ReadFile implements RepositoryFiles.
func (a *ApiRepo) ReadFile(ctx context.Context, id NodeId, name string) ([]byte, error) {
	path := fmt.Sprintf("/nodes/%d/files/%s", id.ID, url.PathEscape(name))
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return a.readBody(resp, "ReadFile", http.StatusOK)
}

// WriteFile implements RepositoryFiles.
func (a *ApiRepo) WriteFile(ctx context.Context, id NodeId, name string, data []byte) error {
	path := fmt.Sprintf("/nodes/%d/files/%s", id.ID, url.PathEscape(name))
	resp, err := a.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteFile", http.StatusOK, http.StatusNoContent)
	return err
}

// DeleteFile implements RepositoryFiles.
func (a *ApiRepo) DeleteFile(ctx context.Context, id NodeId, name string) error {
	path := fmt.Sprintf("/nodes/%d/files/%s", id.ID, url.PathEscape(name))
	resp, err := a.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "DeleteFile", http.StatusOK, http.StatusNoContent)
	return err
}

// ListImages implements RepositoryImages.
func (a *ApiRepo) ListImages(ctx context.Context, id NodeId) ([]string, error) {
	path := fmt.Sprintf("/nodes/%d/images", id.ID)
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	body, err := a.readBody(resp, "ListImages", http.StatusOK)
	if err != nil {
		return nil, err
	}
	var names []string
	if err := json.Unmarshal(body, &names); err != nil {
		return nil, NewBackendError(a.Name(), "ListImages", 0,
			fmt.Errorf("invalid response: %w", err), false)
	}
	return names, nil
}

// ReadImage implements RepositoryImages.
func (a *ApiRepo) ReadImage(ctx context.Context, id NodeId, name string) ([]byte, error) {
	path := fmt.Sprintf("/nodes/%d/images/%s", id.ID, url.PathEscape(name))
	resp, err := a.do(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return nil, err
	}
	return a.readBody(resp, "ReadImage", http.StatusOK)
}

// WriteImage implements RepositoryImages.
func (a *ApiRepo) WriteImage(ctx context.Context, id NodeId, name string, data []byte) error {
	path := fmt.Sprintf("/nodes/%d/images/%s", id.ID, url.PathEscape(name))
	resp, err := a.do(ctx, http.MethodPut, path, bytes.NewReader(data), "application/octet-stream")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "WriteImage", http.StatusOK, http.StatusNoContent)
	return err
}

// DeleteImage implements RepositoryImages.
func (a *ApiRepo) DeleteImage(ctx context.Context, id NodeId, name string) error {
	path := fmt.Sprintf("/nodes/%d/images/%s", id.ID, url.PathEscape(name))
	resp, err := a.do(ctx, http.MethodDelete, path, nil, "")
	if err != nil {
		return err
	}
	_, err = a.readBody(resp, "DeleteImage", http.StatusOK, http.StatusNoContent)
	return err
}

// --- Compile-time interface checks ---

var _ Repository = (*ApiRepo)(nil)
var _ RepositoryFiles = (*ApiRepo)(nil)
var _ RepositoryImages = (*ApiRepo)(nil)
