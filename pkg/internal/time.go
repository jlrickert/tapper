package internal

import "time"

// Clock allows deterministic time for tests.
type Clock interface {
	Now() time.Time
}

// RealClock uses time.Now.
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }

// FixedClock returns a constant time (useful for tests).
type FixedClock struct{ t time.Time }

func NewFixedClock(t time.Time) *FixedClock { return &FixedClock{t: t} }
func (f *FixedClock) Now() time.Time        { return f.t }

func ISO8601(clock Clock) string {
	return clock.Now().UTC().Format(time.RFC3339)
}
