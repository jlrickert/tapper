package mcp

import (
	"context"
	"fmt"
	"sync"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

type nodeResourceSubscriptions struct {
	tap      *tapper.Tap
	defaults KegDefaults
	notify   func(context.Context, string)

	mu      sync.Mutex
	watches map[string]*nodeResourceWatch
}

type nodeResourceWatch struct {
	cancel   context.CancelFunc
	sessions map[string]bool
}

func newNodeResourceSubscriptions(tap *tapper.Tap, defaults KegDefaults, notify func(context.Context, string)) *nodeResourceSubscriptions {
	return &nodeResourceSubscriptions{
		tap:      tap,
		defaults: defaults,
		notify:   notify,
		watches:  make(map[string]*nodeResourceWatch),
	}
}

func (s *nodeResourceSubscriptions) Subscribe(ctx context.Context, req *sdkmcp.SubscribeRequest) error {
	ref, ok := parseNodeResourceURI(req.Params.URI)
	if !ok {
		return fmt.Errorf("unsupported resource subscription %q", req.Params.URI)
	}
	sessionID := sessionKey(req.Session)

	s.mu.Lock()
	if watch, ok := s.watches[req.Params.URI]; ok {
		watch.sessions[sessionID] = true
		s.mu.Unlock()
		return nil
	}
	watchCtx, cancel := context.WithCancel(context.Background())
	ch, err := s.tap.WatchNode(watchCtx, tapper.WatchNodeOptions{
		NodeID:           ref.nodeID,
		KegTargetOptions: resolveKegTarget(ctx, ref.keg, s.defaults),
	})
	if err != nil {
		cancel()
		s.mu.Unlock()
		return err
	}
	s.watches[req.Params.URI] = &nodeResourceWatch{
		cancel:   cancel,
		sessions: map[string]bool{sessionID: true},
	}
	s.mu.Unlock()

	go s.forward(watchCtx, req.Params.URI, ch)
	_ = ctx
	return nil
}

func (s *nodeResourceSubscriptions) Unsubscribe(ctx context.Context, req *sdkmcp.UnsubscribeRequest) error {
	sessionID := sessionKey(req.Session)
	s.mu.Lock()
	watch, ok := s.watches[req.Params.URI]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	delete(watch.sessions, sessionID)
	if len(watch.sessions) > 0 {
		s.mu.Unlock()
		return nil
	}
	delete(s.watches, req.Params.URI)
	s.mu.Unlock()

	watch.cancel()
	_ = ctx
	return nil
}

func sessionKey(session *sdkmcp.ServerSession) string {
	if session == nil {
		return ""
	}
	if id := session.ID(); id != "" {
		return id
	}
	return fmt.Sprintf("%p", session)
}

func (s *nodeResourceSubscriptions) forward(ctx context.Context, uri string, ch <-chan keg.NodeEvent) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				s.removeWatch(uri)
				return
			}
			if ev.Kind == keg.NodeEventAccessed {
				continue
			}
			s.notify(context.Background(), uri)
		}
	}
}

func (s *nodeResourceSubscriptions) removeWatch(uri string) {
	s.mu.Lock()
	watch, ok := s.watches[uri]
	if ok {
		delete(s.watches, uri)
	}
	s.mu.Unlock()
	if ok {
		watch.cancel()
	}
}
