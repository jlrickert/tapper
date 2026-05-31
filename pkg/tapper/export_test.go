package tapper

import "time"

// SSEBroadcasterForTest wraps sseBroadcaster for testing.
type SSEBroadcasterForTest struct {
	b *sseBroadcaster
}

// NewSSEBroadcasterForTest creates a broadcaster with zero grace period so
// that broadcasts are delivered immediately. Production code uses a 2s grace.
func NewSSEBroadcasterForTest() *SSEBroadcasterForTest {
	b := newSSEBroadcaster(time.Now)
	b.clientGrace = 0
	return &SSEBroadcasterForTest{b: b}
}

// NewSSEBroadcasterForTestWithGrace creates a broadcaster with a custom grace period.
// Use zero to disable the grace period in tests where you want immediate delivery.
func NewSSEBroadcasterForTestWithGrace(grace time.Duration) *SSEBroadcasterForTest {
	b := newSSEBroadcaster(time.Now)
	b.clientGrace = grace
	return &SSEBroadcasterForTest{b: b}
}

func (t *SSEBroadcasterForTest) Subscribe() chan struct{} {
	return t.b.subscribe().ch
}

func (t *SSEBroadcasterForTest) Unsubscribe(ch chan struct{}) {
	// Find and remove the client with the matching channel.
	t.b.mu.Lock()
	defer t.b.mu.Unlock()
	for c := range t.b.clients {
		if c.ch == ch {
			delete(t.b.clients, c)
			return
		}
	}
}

func (t *SSEBroadcasterForTest) Broadcast() {
	t.b.broadcast()
}

func (t *SSEBroadcasterForTest) Count() int {
	return t.b.count()
}

// BroadcastForTest triggers an SSE broadcast on the ServeHandler.
// This is only available in tests. It is a no-op if the handler has
// no SSE broadcaster (watch mode disabled).
func (h *ServeHandler) BroadcastForTest() {
	if h.sse != nil {
		h.sse.broadcast()
	}
}

// DisableSSEGraceForTest sets the SSE client grace period to zero on
// the ServeHandler so that broadcasts are delivered immediately. This
// is necessary for integration tests that subscribe and broadcast in
// quick succession.
func (h *ServeHandler) DisableSSEGraceForTest() {
	if h.sse != nil {
		h.sse.clientGrace = 0
	}
}
