package keg

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// apiNodeEvent is the hub's live node event wire shape, delivered over the
// /nodes/{id}/events websocket.
type apiNodeEvent struct {
	Type              string   `json:"type"`
	NodeID            int      `json:"node_id"`
	Fields            []string `json:"fields"`
	DestinationNodeID int      `json:"destination_node_id"`
}

// Watch implements Keg by subscribing to the hub's per-node websocket event
// stream (/nodes/{id}/events) for each requested node.
func (k *RemoteKeg) Watch(ctx context.Context, ids ...NodeId) (<-chan NodeEvent, error) {
	if len(ids) == 0 {
		return nil, fmt.Errorf("remote live watch requires at least one node id")
	}
	watchCtx, cancel := context.WithCancel(ctx)
	out := make(chan NodeEvent, 16)
	var wg sync.WaitGroup
	for _, id := range ids {
		id := id
		wg.Add(1)
		go func() {
			defer wg.Done()
			k.watchNodeEvents(watchCtx, id, out)
		}()
	}
	go func() {
		wg.Wait()
		cancel()
		close(out)
	}()
	return out, nil
}

func (k *RemoteKeg) watchNodeEvents(ctx context.Context, id NodeId, out chan<- NodeEvent) {
	backoff := 250 * time.Millisecond
	for ctx.Err() == nil {
		conn, resp, err := websocket.Dial(ctx, k.eventsURL(id), &websocket.DialOptions{
			HTTPClient: k.httpClient(),
			HTTPHeader: k.eventsHeader(ctx),
		})
		if err != nil {
			watchErr := err
			if resp != nil {
				watchErr = k.mapError(resp, "watch node events")
			}
			if isPermanentWatchError(watchErr) {
				k.logDebug("remote live watch terminated",
					"url", k.eventsURL(id), "error", watchErr)
				return
			}
			k.logDebug("remote live watch dial failed; retrying",
				"url", k.eventsURL(id), "error", watchErr, "backoff", backoff)
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

// logDebug emits a diagnostic line when a logger is configured. Live watch
// failures are otherwise invisible to the user, so every terminal path in
// watchNodeEvents must pass through here.
func (k *RemoteKeg) logDebug(msg string, args ...any) {
	if k.logger != nil {
		k.logger.Debug(msg, args...)
	}
}

func (k *RemoteKeg) eventsURL(id NodeId) string {
	raw := k.baseURL + fmt.Sprintf("/nodes/%d/events", id.ID)
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

func (k *RemoteKeg) eventsHeader(ctx context.Context) http.Header {
	h := make(http.Header)
	if token := k.currentToken(); token != "" {
		h.Set("Authorization", "Bearer "+token)
	}
	if orientation, ok := OrientationHeaderValue(ctx); ok {
		h.Set(OrientationHeaderName, orientation)
	}
	return h
}

func isPermanentWatchError(err error) bool {
	return errors.Is(err, ErrUnauthorized) || errors.Is(err, ErrForbidden) ||
		errors.Is(err, ErrNotExist) || errors.Is(err, ErrOrientationStale) ||
		errors.Is(err, ErrOrientationDenied) || errors.Is(err, ErrOrientationUnavailable) ||
		errors.Is(err, ErrOrientationRootUnavailable)
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
