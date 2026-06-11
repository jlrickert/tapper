package keg

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

type apiNodeEvent struct {
	Type              string   `json:"type"`
	NodeID            int      `json:"node_id"`
	Fields            []string `json:"fields"`
	DestinationNodeID int      `json:"destination_node_id"`
}

// Watch implements RepositoryEvents for Hub-backed repositories.
func (a *ApiRepo) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("api live watch requires at least one node id")
	}
	watchCtx, cancel := context.WithCancel(ctx)
	out := make(chan NodeEvent, 16)
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.watchNodeEvents(watchCtx, id, out)
		}()
	}
	go func() {
		wg.Wait()
		cancel()
		close(out)
	}()
	return out, nil
}

// Emit is a no-op for ApiRepo. Hub is the source of truth for API-backed live
// events, and local writes publish by going through Hub's REST handlers.
func (a *ApiRepo) Emit(NodeEvent) {}

func (a *ApiRepo) watchNodeEvents(ctx context.Context, id NodeId, out chan<- NodeEvent) {
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		conn, resp, err := websocket.Dial(ctx, a.eventsURL(id), &websocket.DialOptions{
			HTTPClient: a.httpClient(),
			HTTPHeader: a.eventsHeader(),
		})
		if err != nil {
			if resp != nil && isPermanentWatchStatus(resp.StatusCode) {
				a.logDebug("api live watch terminated",
					"url", a.eventsURL(id), "status", resp.StatusCode)
				return
			}
			a.logDebug("api live watch dial failed; retrying",
				"url", a.eventsURL(id), "error", err, "backoff", backoff)
			if sleepContext(ctx, backoff) {
				return
			}
			if backoff < 2*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = 250 * time.Millisecond

		for ctx.Err() == nil {
			var ev apiNodeEvent
			if err := wsjson.Read(ctx, conn, &ev); err != nil {
				break
			}
			for _, nodeEvent := range ev.nodeEvents(id) {
				select {
				case <-ctx.Done():
					conn.CloseNow()
					return
				case out <- nodeEvent:
				}
			}
		}
		conn.CloseNow()
		if sleepContext(ctx, backoff) {
			return
		}
	}
}

// logDebug emits a diagnostic line when a Logger is configured. Live watch
// failures are otherwise invisible to the user, so every terminal path in
// watchNodeEvents must pass through here.
func (a *ApiRepo) logDebug(msg string, args ...any) {
	if a.Logger != nil {
		a.Logger.Debug(msg, args...)
	}
}

func (a *ApiRepo) eventsURL(id NodeId) string {
	raw := a.BaseURL + fmt.Sprintf("/nodes/%d/events", id.ID)
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	}
	return u.String()
}

func (a *ApiRepo) eventsHeader() http.Header {
	h := make(http.Header)
	if a.Token != "" {
		h.Set("Authorization", "Bearer "+a.Token)
	}
	return h
}

func isPermanentWatchStatus(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound:
		return true
	default:
		return false
	}
}

func sleepContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}

func (ev apiNodeEvent) nodeEvents(fallback NodeId) []NodeEvent {
	id := fallback
	if ev.NodeID != 0 || fallback.ID == 0 {
		id = NodeId{ID: ev.NodeID}
	}

	switch ev.Type {
	case "node.deleted", "node.moved":
		return []NodeEvent{{Kind: NodeEventDeleted, NodeID: id}}
	case "node.created":
		return []NodeEvent{{Kind: NodeEventCreated, NodeID: id}}
	case "snapshot.created":
		return nil
	}

	var out []NodeEvent
	for _, field := range ev.Fields {
		if field != "content" && field != "meta" {
			continue
		}
		out = append(out, NodeEvent{
			Kind:   NodeEventModified,
			NodeID: id,
			Field:  field,
		})
	}
	if len(out) == 0 && (ev.Type == "node.updated" || ev.Type == "node.restored") {
		out = append(out, NodeEvent{Kind: NodeEventModified, NodeID: id, Field: "content"})
	}
	return out
}

var _ RepositoryEvents = (*ApiRepo)(nil)
