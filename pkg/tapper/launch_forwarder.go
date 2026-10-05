package tapper

// EXPERIMENTAL — part of `tap launch`; see tap_launch.go.

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"
)

// Hub's provider-shaped inference surfaces, relative to the hub base URL.
// They sit outside /api/v1 because unmodified harnesses cannot send
// Tapper-API-Version.
const (
	hubOpenAIPath    = "/inference/openai/v1"
	hubAnthropicPath = "/inference/anthropic/v1"
)

// forwarderRoutes maps each request the forwarder accepts, as "METHOD path",
// to its Hub path. It is the whole surface: nothing else is forwarded.
var forwarderRoutes = map[string]string{
	"GET /v1/models":                           hubOpenAIPath + "/models",
	"POST /v1/chat/completions":                hubOpenAIPath + "/chat/completions",
	"POST /v1/responses":                       hubOpenAIPath + "/responses",
	"POST /anthropic/v1/messages":              hubAnthropicPath + "/messages",
	"POST /anthropic/v1/messages/count_tokens": hubAnthropicPath + "/messages/count_tokens",
}

// forwardedRequestHeaders are the client headers Hub needs to read a request.
var forwardedRequestHeaders = []string{"Content-Type", "Accept", "Anthropic-Version", "Anthropic-Beta"}

// forwardedResponseHeaders are the upstream headers a harness needs to read a
// response correctly. Nothing else is copied back.
var forwardedResponseHeaders = []string{"Content-Type", "Cache-Control", "Retry-After"}

// DefaultAnthropicURL is where a split launch sends Claude models when the
// launching environment names no ANTHROPIC_BASE_URL of its own.
const DefaultAnthropicURL = "https://api.anthropic.com"

// maxSplitBody bounds the request body a split launch buffers to read its
// model. Anthropic's own Messages limit is lower.
const maxSplitBody = 64 << 20

// launchForwarderConfig configures a forwarder beyond Hub inference.
type launchForwarderConfig struct {
	hubURL  string
	harness string
	token   func() string
	// anthropic, when set, turns on split routing for requests under the
	// pinned prefix: Claude models go here with the harness's own
	// credential, everything else to Hub with the Hub credential.
	anthropic string
}

// launchForwarder is a loopback proxy between a launched harness and Hub's
// inference endpoints, alive for as long as the harness runs.
//
// It exists because a harness takes one static API key at startup, while a
// `tap auth login` access token expires after an hour. The forwarder resolves
// the Hub token per request — refreshing it through the same resolver keg
// access uses — so a long session keeps working, and the Hub token never
// enters the child's environment or config at all.
//
// It is deliberately not a general proxy: a fixed table of routes, no header
// passthrough beyond content negotiation, and a per-launch secret so another
// local process cannot borrow the user's identity through it.
//
// The secret is presented one of two ways. Harnesses that take an API key send
// it as one. A split Claude Code launch cannot, because its API credential is
// the user's own Claude login, so the secret is a path prefix instead:
// /t/<secret>/... Under that prefix the forwarder also passes Claude models
// through to Anthropic untouched. Requests outside it still need the key.
type launchForwarder struct {
	upstream  string // hub base URL
	harness   string // sent to Hub as X-Tap-Harness
	token     func() string
	secret    string
	client    *http.Client
	anthropic *httputil.ReverseProxy // nil unless split routing is on
	ln        net.Listener
	srv       *http.Server
}

// startLaunchForwarder listens on a random loopback port and serves until
// Close. token is called once per forwarded request. harness names the
// launched harness to Hub, which labels the requests with it for providers
// that attribute usage to apps; it grants nothing.
func startLaunchForwarder(hubURL, harness string, token func() string) (*launchForwarder, error) {
	return startLaunchForwarderWith(launchForwarderConfig{hubURL: hubURL, harness: harness, token: token})
}

// startLaunchForwarderWith is startLaunchForwarder with split routing
// available.
func startLaunchForwarderWith(cfg launchForwarderConfig) (*launchForwarder, error) {
	hubURL, harness, token := cfg.hubURL, cfg.harness, cfg.token
	var anthropic *httputil.ReverseProxy
	if cfg.anthropic != "" {
		target, err := url.Parse(strings.TrimRight(cfg.anthropic, "/"))
		if err != nil || target.Scheme == "" || target.Host == "" {
			return nil, fmt.Errorf("anthropic upstream %q is not an absolute URL", cfg.anthropic)
		}
		anthropic = anthropicPassthrough(target)
	}
	secret, err := launchSecret()
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("start inference forwarder: %w", err)
	}
	f := &launchForwarder{
		upstream: strings.TrimRight(hubURL, "/"),
		harness:  harness,
		token:    token,
		secret:   secret,
		// No overall timeout: a streamed completion legitimately runs for
		// minutes. Cancellation follows the harness's request context instead.
		client:    hubHTTPClient(),
		anthropic: anthropic,
		ln:        ln,
	}
	f.srv = &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = f.srv.Serve(ln) }()
	return f, nil
}

// Origin is the forwarder's origin. A harness is given it plus the path of the
// protocol it speaks: /v1 for OpenAI clients, /anthropic for Anthropic ones.
func (f *launchForwarder) Origin() string { return "http://" + f.ln.Addr().String() }

// Secret is the API key a harness must present.
func (f *launchForwarder) Secret() string { return f.secret }

// Close stops the forwarder, abandoning any request still in flight: the
// harness that made it has exited.
func (f *launchForwarder) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := f.srv.Shutdown(ctx); err != nil {
		return f.srv.Close()
	}
	return nil
}

// PinnedPrefix is the path prefix that stands in for the launch key, for a
// harness whose API credential is its own: /t/<secret>.
func (f *launchForwarder) PinnedPrefix() string { return "/t/" + f.secret }

func (f *launchForwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqPath, pinned := r.URL.Path, false
	if rest, ok := strings.CutPrefix(reqPath, "/t/"); ok {
		nonce, tail, _ := strings.Cut(rest, "/")
		if subtle.ConstantTimeCompare([]byte(nonce), []byte(f.secret)) != 1 {
			forwarderError(w, http.StatusNotFound, "not_found", "unknown launch")
			return
		}
		reqPath, pinned = "/"+tail, true
	}
	if pinned && f.anthropic != nil {
		if tail, ok := strings.CutPrefix(reqPath, "/anthropic/"); ok {
			switch f.splitRoute(w, r, reqPath) {
			case splitAnthropic:
				f.passToAnthropic(w, r, "/"+tail)
				return
			case splitAnswered:
				return
			}
		}
	}
	path, ok := forwarderRoutes[r.Method+" "+reqPath]
	if !ok {
		forwarderError(w, http.StatusNotFound, "not_found", "the tap launch forwarder serves only Hub's inference routes")
		return
	}
	target := f.upstream + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
	if !pinned {
		// OpenAI clients send the key as a bearer token, Anthropic ones as
		// x-api-key or a bearer token.
		presented, _ := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if presented == "" {
			presented = r.Header.Get("X-Api-Key")
		}
		if subtle.ConstantTimeCompare([]byte(presented), []byte(f.secret)) != 1 {
			forwarderError(w, http.StatusUnauthorized, "unauthorized", "invalid launch key")
			return
		}
	}
	token := f.token()
	if token == "" {
		forwarderError(w, http.StatusUnauthorized, "hub_unauthenticated", "no Hub credential available; run `tap auth login`")
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, target, r.Body)
	if err != nil {
		forwarderError(w, http.StatusInternalServerError, "internal", "could not build the Hub request")
		return
	}
	req.ContentLength = r.ContentLength
	for _, name := range forwardedRequestHeaders {
		if v := r.Header.Get(name); v != "" {
			req.Header.Set(name, v)
		}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if f.harness != "" {
		req.Header.Set("X-Tap-Harness", f.harness)
	}

	resp, err := f.client.Do(req)
	if err != nil {
		if r.Context().Err() != nil {
			return // the harness gave up; nobody is listening
		}
		forwarderError(w, http.StatusBadGateway, "hub_unreachable", "could not reach Hub: "+err.Error())
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for _, name := range forwardedResponseHeaders {
		if v := resp.Header.Get(name); v != "" {
			w.Header().Set(name, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	copyFlushing(w, resp.Body)
}

// Where a split launch sends one Anthropic-protocol request.
type splitDestination int

const (
	splitHub       splitDestination = iota // a Hub catalog model
	splitAnthropic                         // the user's own Claude account
	splitAnswered                          // refused; the response is written
)

// splitRoute decides where a split launch's Anthropic request goes: Anthropic
// for any route other than Messages and token counting, or for one naming a
// Claude model; Hub otherwise. A Hub-bound request's buffered body is put back
// on r.
func (f *launchForwarder) splitRoute(w http.ResponseWriter, r *http.Request, reqPath string) splitDestination {
	if _, ok := forwarderRoutes[r.Method+" "+reqPath]; !ok {
		return splitAnthropic
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxSplitBody+1))
	_ = r.Body.Close()
	if err != nil || len(body) > maxSplitBody {
		forwarderError(w, http.StatusRequestEntityTooLarge, "request_too_large", "request body is unreadable or too large to route")
		return splitAnswered
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	var peek struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &peek)
	if IsClaudeModel(peek.Model) {
		return splitAnthropic
	}
	return splitHub
}

// passToAnthropic sends a split launch's request to Anthropic as the harness
// made it, credential included. The Hub credential is never added: this leg
// is the user's own Claude account, not Hub.
func (f *launchForwarder) passToAnthropic(w http.ResponseWriter, r *http.Request, upstreamPath string) {
	out := r.Clone(r.Context())
	out.URL.Path, out.URL.RawPath = upstreamPath, ""
	out.RequestURI = ""
	f.anthropic.ServeHTTP(w, out)
}

// anthropicPassthrough proxies to target, streaming responses as they arrive.
// Rewrite (rather than Director) adds no X-Forwarded-* headers, and the proxy
// drops hop-by-hop ones; everything else the harness sends, its credential
// and Anthropic's beta flags included, goes through unchanged.
func anthropicPassthrough(target *url.URL) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite:       func(pr *httputil.ProxyRequest) { pr.SetURL(target) },
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return
			}
			forwarderError(w, http.StatusBadGateway, "anthropic_unreachable", "could not reach Anthropic: "+err.Error())
		},
	}
}

// claudeModelAliases are the model names Claude Code resolves itself.
var claudeModelAliases = []string{"default", "best", "sonnet", "opus", "haiku", "fable", "opusplan"}

// IsClaudeModel reports whether model names one of Anthropic's own models
// (claude-*, or an alias Claude Code resolves) rather than a Hub catalog id.
// A [1m]-style context suffix is ignored.
func IsClaudeModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if i := strings.IndexByte(model, '['); i > 0 {
		model = model[:i]
	}
	if strings.HasPrefix(model, "claude-") || strings.HasPrefix(model, "anthropic/") {
		return true
	}
	for _, alias := range claudeModelAliases {
		if model == alias {
			return true
		}
	}
	return false
}

// copyFlushing streams body to w, flushing after every read so server-sent
// events reach the harness as they arrive rather than when a buffer fills.
func copyFlushing(w http.ResponseWriter, body io.Reader) {
	rc := http.NewResponseController(w)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if rc.Flush() != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

// forwarderError writes the OpenAI error envelope, so a harness reports the
// forwarder's own failures the same way it reports Hub's.
func forwarderError(w http.ResponseWriter, status int, code, msg string) {
	body, _ := json.Marshal(map[string]any{
		"error": map[string]string{"message": msg, "type": "invalid_request_error", "code": code},
	})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func launchSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", errors.New("generate launch key: " + err.Error())
	}
	return "tap-launch-" + hex.EncodeToString(b), nil
}
