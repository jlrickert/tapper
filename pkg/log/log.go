package log

import (
	"context"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"log/slog"
)

// LoggerConfig is a minimal, convenient set of options.
type LoggerConfig struct {
	Version string

	// If Out is nil, stdout is used.
	Out io.Writer

	Level slog.Level
	JSON  bool // true => JSON output, false => text
}

// NewLogger creates a configured *slog.Logger and a shutdown func (no-op here).
// Call the shutdown func on process exit if you add async/file writers later.
func NewLogger(cfg LoggerConfig) (*slog.Logger, func() error, error) {
	out := cfg.Out
	if out == nil {
		out = os.Stdout
	}

	var handler slog.Handler
	if cfg.JSON {
		handler = slog.NewJSONHandler(
			out,
			&slog.HandlerOptions{Level: cfg.Level, AddSource: true})
	} else {
		handler = slog.NewTextHandler(
			out,
			&slog.HandlerOptions{Level: cfg.Level, AddSource: true})
	}

	attrs := []slog.Attr{
		slog.String("version", cfg.Version),
	}
	hn, _ := os.Hostname()
	attrs = append(attrs, slog.Int("pid", os.Getpid()))

	logger := slog.New(handler).With(
		slog.String("version", cfg.Version),
		slog.String("host", hn),
	)

	// shutdown noop for now
	return logger, func() error { return nil }, nil
}

// nopHandler is a tiny no-op slog.Handler.
type nopHandler struct{}

func (n *nopHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (n *nopHandler) Handle(context.Context, slog.Record) error { return nil }
func (n *nopHandler) WithAttrs(attrs []slog.Attr) slog.Handler  { return n }
func (n *nopHandler) WithGroup(name string) slog.Handler        { return n }

// NewNopLogger returns a logger that discards all log events.
func NewNopLogger() *slog.Logger {
	return slog.New(&nopHandler{})
}

var _ slog.Handler = (*nopHandler)(nil)

///////////////////////////////////////////////////////////////////////////////
// Context helpers
///////////////////////////////////////////////////////////////////////////////

type ctxKeyType struct{}

var ctxKey ctxKeyType

// ContextWithLogger stores lg on ctx.
func ContextWithLogger(ctx context.Context, lg *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey, lg)
}

// FromContext returns logger from ctx or slog.Default().
func FromContext(ctx context.Context) *slog.Logger {
	if ctx == nil {
		return slog.Default()
	}
	if v := ctx.Value(ctxKey); v != nil {
		if lg, ok := v.(*slog.Logger); ok && lg != nil {
			return lg
		}
	}
	return slog.Default()
}

// GetLogger returns logger from context if present, otherwise fallback, otherwise slog.Default().
func GetLogger(ctx context.Context, fallback *slog.Logger) *slog.Logger {
	if lg := FromContext(ctx); lg != nil {
		return lg
	}
	if fallback != nil {
		return fallback
	}
	return slog.Default()
}

///////////////////////////////////////////////////////////////////////////////
// Test handler (simple, thread-safe)
///////////////////////////////////////////////////////////////////////////////

type LoggedEntry struct {
	Time  time.Time
	Level slog.Level
	Msg   string
	Attrs map[string]any
}

// testingT is a tiny subset of *testing.T used for optional logging.
type testingT interface {
	Logf(format string, args ...any)
}

// TestHandler captures structured entries for assertions.
type TestHandler struct {
	mu      sync.Mutex
	Entries []LoggedEntry
	T       testingT
}

func NewTestHandler(t testingT) *TestHandler {
	return &TestHandler{T: t}
}

func (h *TestHandler) Enabled(_ context.Context, _ slog.Level) bool { return true }

func (h *TestHandler) Handle(ctx context.Context, r slog.Record) error {
	e := LoggedEntry{
		Time:  r.Time,
		Level: r.Level,
		Msg:   r.Message,
		Attrs: map[string]any{},
	}
	h.mu.Lock()
	h.Entries = append(h.Entries, e)
	h.mu.Unlock()

	if h.T != nil {
		h.T.Logf("LOG %s %v %v", e.Msg, e.Level, e.Attrs)
	}
	return nil
}

func (h *TestHandler) WithAttrs(_ []slog.Attr) slog.Handler { return h }
func (h *TestHandler) WithGroup(_ string) slog.Handler      { return h }

// NewTestLogger returns a logger that writes to a TestHandler (and the handler).
func NewTestLogger(t testingT, level slog.Level) (*slog.Logger, *TestHandler) {
	th := NewTestHandler(t)
	logger := slog.New(th).With(slog.String("test", "true"))
	return logger, th
}

var _ slog.Handler = (*TestHandler)(nil)

///////////////////////////////////////////////////////////////////////////////
// Small helpers for tests
///////////////////////////////////////////////////////////////////////////////

// FindEntries copies entries that match pred.
func FindEntries(th *TestHandler, pred func(LoggedEntry) bool) []LoggedEntry {
	th.mu.Lock()
	entries := append([]LoggedEntry(nil), th.Entries...)
	th.mu.Unlock()

	out := make([]LoggedEntry, 0)
	for _, e := range entries {
		if pred(e) {
			out = append(out, e)
		}
	}
	return out
}

// RequireEntry fails the test if a matching entry isn't found within timeout.
func RequireEntry(t *testing.T, th *TestHandler, pred func(LoggedEntry) bool, timeout time.Duration) LoggedEntry {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		th.mu.Lock()
		for _, e := range th.Entries {
			if pred(e) {
				out := e
				th.mu.Unlock()
				return out
			}
		}
		th.mu.Unlock()
		if time.Now().After(deadline) {
			th.mu.Lock()
			entries := append([]LoggedEntry(nil), th.Entries...)
			th.mu.Unlock()
			t.Fatalf("required log entry not found in %s; captured %d entries: %#v", timeout, len(entries), entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
