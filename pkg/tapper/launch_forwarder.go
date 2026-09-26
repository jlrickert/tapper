package tapper

// EXPERIMENTAL — part of `tap launch`; see tap_launch.go.

import (
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
type launchForwarder struct {
	upstream string // hub base URL
	token    func() string
	secret   string
	client   *http.Client
	ln       net.Listener
	srv      *http.Server
}

// startLaunchForwarder listens on a random loopback port and serves until
// Close. token is called once per forwarded request.
func startLaunchForwarder(hubURL string, token func() string) (*launchForwarder, error) {
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
		token:    token,
		secret:   secret,
		// No overall timeout: a streamed completion legitimately runs for
		// minutes. Cancellation follows the harness's request context instead.
		client: hubHTTPClient(),
		ln:     ln,
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

func (f *launchForwarder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path, ok := forwarderRoutes[r.Method+" "+r.URL.Path]
	if !ok {
		forwarderError(w, http.StatusNotFound, "not_found", "the tap launch forwarder serves only Hub's inference routes")
		return
	}
	target := f.upstream + path
	if r.URL.RawQuery != "" {
		target += "?" + r.URL.RawQuery
	}
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
