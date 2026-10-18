package mcp

import (
	"bytes"
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// repeatFailureThreshold is the identical failure that earns the stop
	// action. Two tries is an ordinary retry; a third is a loop.
	repeatFailureThreshold = 3
	// repeatFailureKeysPerSession bounds the failing calls remembered for one
	// session. Only the latest arguments per tool are kept, so this is also
	// the number of distinct tools tracked at once.
	repeatFailureKeysPerSession = 32
	// repeatFailureSessions bounds the sessions tracked at once. The least
	// recently active session is forgotten first, so a long-lived Hub process
	// never grows this without limit.
	repeatFailureSessions = 256
)

// repeatFailureTracker flags a tool call that keeps failing with the same
// arguments. An agent that misreads an error tends to resend the identical
// call; the third identical failure keeps its original code and gains
// repeat_count and an action telling the agent to stop and read instead.
//
// A call is identified by session, tool, and a digest of its canonicalized
// arguments. A success, or a call with different arguments, resets that
// tool's count.
type repeatFailureTracker struct {
	mu       sync.Mutex
	sessions map[string]*list.Element // of *repeatFailureSession
	lru      *list.List               // most recently active session first
}

type repeatFailureSession struct {
	id    string
	tools map[string]*list.Element // tool name -> element of keys
	keys  *list.List               // *repeatFailureEntry, most recent first
}

type repeatFailureEntry struct {
	tool   string
	digest [sha256.Size]byte
	count  int
}

func newRepeatFailureTracker() *repeatFailureTracker {
	return &repeatFailureTracker{sessions: map[string]*list.Element{}, lru: list.New()}
}

// middleware must wrap publicResponseMiddleware, so that it sees SDK input
// validation failures in their public form, with their code already set.
//
// The digest is taken before the call runs. Inner layers rewrite the
// arguments in place (the flight gate strips `flight`, legacy names are
// renamed), and a digest of the rewritten form would count calls under
// different flights as one repeated call.
func (r *repeatFailureTracker) middleware(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
	return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		params, _ := req.GetParams().(*sdkmcp.CallToolParamsRaw)
		if params == nil || IsRelayedToolName(params.Name) {
			return next(ctx, method, req)
		}
		tool, digest := params.Name, canonicalArgumentsDigest(params.Arguments)
		result, err := next(ctx, method, req)
		if err != nil {
			return result, err
		}
		call, ok := result.(*sdkmcp.CallToolResult)
		if !ok || call == nil {
			return result, nil
		}
		sessionID := ""
		if sess, ok := req.GetSession().(*sdkmcp.ServerSession); ok && sess != nil {
			sessionID = sess.ID()
		}
		count := r.record(sessionID, tool, digest, call.IsError)
		if count < repeatFailureThreshold {
			return result, nil
		}
		return withRepeatAction(call, count), nil
	}
}

// record notes one call's outcome and returns how many times in a row this
// session has failed the same call, or 0 after a success.
func (r *repeatFailureTracker) record(sessionID, tool string, digest [sha256.Size]byte, failed bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	sess := r.session(sessionID, failed)
	if sess == nil {
		return 0
	}
	if el, ok := sess.tools[tool]; ok {
		entry := el.Value.(*repeatFailureEntry)
		if failed && entry.digest == digest {
			entry.count++
			sess.keys.MoveToFront(el)
			return entry.count
		}
		sess.keys.Remove(el)
		delete(sess.tools, tool)
	}
	if !failed {
		return 0
	}
	sess.tools[tool] = sess.keys.PushFront(&repeatFailureEntry{tool: tool, digest: digest, count: 1})
	for sess.keys.Len() > repeatFailureKeysPerSession {
		oldest := sess.keys.Back()
		delete(sess.tools, oldest.Value.(*repeatFailureEntry).tool)
		sess.keys.Remove(oldest)
	}
	return 1
}

// session returns the state for id, marking it most recently active. A
// session with nothing recorded is only created for a failure, since a
// success has nothing to reset.
func (r *repeatFailureTracker) session(id string, create bool) *repeatFailureSession {
	if el, ok := r.sessions[id]; ok {
		r.lru.MoveToFront(el)
		return el.Value.(*repeatFailureSession)
	}
	if !create {
		return nil
	}
	sess := &repeatFailureSession{id: id, tools: map[string]*list.Element{}, keys: list.New()}
	r.sessions[id] = r.lru.PushFront(sess)
	for r.lru.Len() > repeatFailureSessions {
		oldest := r.lru.Back()
		delete(r.sessions, oldest.Value.(*repeatFailureSession).id)
		r.lru.Remove(oldest)
	}
	return sess
}

// canonicalArgumentsDigest hashes arguments independent of key order and
// whitespace, so a resent call matches however the client serialized it.
// Arguments that do not decode are hashed as sent.
func canonicalArgumentsDigest(raw json.RawMessage) [sha256.Size]byte {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err == nil {
		if canonical, err := json.Marshal(value); err == nil {
			return sha256.Sum256(canonical)
		}
	}
	return sha256.Sum256(raw)
}

// withRepeatAction adds the repeat guidance to a failure's public payload,
// keeping its code and its original action after the stop instruction.
func withRepeatAction(call *sdkmcp.CallToolResult, count int) sdkmcp.Result {
	payload, ok := call.StructuredContent.(map[string]any)
	if !ok {
		return call
	}
	out := make(map[string]any, len(payload)+1)
	for key, value := range payload {
		out[key] = value
	}
	action := fmt.Sprintf("This identical call has failed %d times; stop retrying it. Read the target (node_read) or change the arguments named in the error.", count)
	if original, _ := payload["action"].(string); original != "" {
		action += " " + original
	}
	out["action"] = action
	out["repeat_count"] = count
	raw, err := json.Marshal(out)
	if err != nil {
		return call
	}
	rendered := *call
	rendered.StructuredContent = out
	rendered.Content = append([]sdkmcp.Content{&sdkmcp.TextContent{Text: string(raw)}}, call.Content[min(1, len(call.Content)):]...)
	return &rendered
}
