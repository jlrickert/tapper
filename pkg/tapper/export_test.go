package tapper

// SSEBroadcasterForTest wraps sseBroadcaster for testing.
type SSEBroadcasterForTest struct {
	b *sseBroadcaster
}

func NewSSEBroadcasterForTest() *SSEBroadcasterForTest {
	return &SSEBroadcasterForTest{b: newSSEBroadcaster()}
}

func (t *SSEBroadcasterForTest) Subscribe() chan struct{} {
	return t.b.subscribe()
}

func (t *SSEBroadcasterForTest) Unsubscribe(ch chan struct{}) {
	t.b.unsubscribe(ch)
}

func (t *SSEBroadcasterForTest) Broadcast() {
	t.b.broadcast()
}

func (t *SSEBroadcasterForTest) Count() int {
	return t.b.count()
}
