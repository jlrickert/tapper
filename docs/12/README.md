# Idiomatic Go error handling

Purpose

- Concise reference for letting callers "pattern match" on errors using errors.Is and errors.As (Go 1.13+).
- Covers sentinel errors, typed errors, interface-based matching (timeouts/temporary/retryable), wrapping, multiple errors, helper constructors, convenience predicates, and small examples suitable for copy/paste into this repo's docs.

Summary

- Use errors.Is for equality-style checks (sentinel errors).
- Use errors.As to extract concrete types or interfaces from possibly-wrapped errors when callers need structured data.
- Wrap errors with fmt.Errorf("%w", err) (or other Unwrap-compatible wrappers) so Is/As can traverse the chain.
- Prefer Is/As over string matching.
- Provide small helper constructors and convenience predicates for common checks.

Quick rules

- Export sentinel error variables for simple boolean conditions (e.g., ErrNodeNotFound, ErrMetaNotFound).
- Export concrete error types when callers need access to fields (IDs, HTTP status, metadata).
- Define small behavior interfaces (Temporary(), Retryable()) when multiple error types should expose the same behavior.
- Always wrap with %w when returning a new error that should preserve the original error for matching.
- Provide constructor helpers (NewFooError) and convenience predicates (IsNotFound, IsRetryable) to make callers' code clearer.

Sentinel errors (equality checks)

- Use sentinels for simple yes/no conditions callers check by identity.
- Current package sentinels:
  - ErrNodeNotFound
  - ErrMetaNotFound
  - ErrPermissionDenied
  - ErrInvalidMeta
  - ErrConflict
  - ErrQuotaExceeded
  - ErrRateLimited

Example (sentinel, package [keg](../5)):

```go
package keg

import (
	"errors"
	"fmt"
)

// ErrNodeNotFound is a sentinel exported for callers who only need a simple
// boolean "not found" check via errors.Is.
var ErrNodeNotFound = errors.New("keg: node not found")

// GetNodeSimple returns a wrapped sentinel for callers that only need errors.Is.
func GetNodeSimple(id NodeID) (*Node, error) {
	// pretend missing
	return nil, fmt.Errorf("GetNode %s: %w", id.Path(), ErrNodeNotFound)
}
```

Caller:

```go
data, err := keg.GetNodeSimple(keg.NodeID(123))
if errors.Is(err, keg.ErrNodeNotFound) {
	// handle not-found
	_ = data
}
```

Typed errors (errors.As) — extract structured data

- Return concrete, exported types when callers need fields. Use errors.As to obtain the concrete type through wrappers.
- This repo provides several exported typed errors with useful fields and behavior methods:
  - NodeNotFoundError { ID NodeID } — also implements Is/Unwrap to match ErrNodeNotFound.
  - ConflictError { ID NodeID, Expected, Got int, Cause error } — implements Unwrap and Is to match ErrConflict.
  - BackendError { Backend, Op, StatusCode, Cause, Transient } — implements Unwrap and Retryable().
  - RateLimitError { RetryAfter time.Duration, Message, Cause } — implements Unwrap and Retryable().
  - TransientError { Cause error } — implements Unwrap, Temporary() and Retryable().

Example (typed error):

```go
package keg

import "fmt"

// NodeNotFoundError is an exported typed error so callers can inspect fields.
type NodeNotFoundError struct {
	ID NodeID
}

func (e *NodeNotFoundError) Error() string { return "node not found: " + e.ID.Path() }

// NewNodeNotFoundError is a helper constructor returning the typed error.
func NewNodeNotFoundError(id NodeID) error { return &NodeNotFoundError{ID: id} }
```

Caller:

```go
err := keg.GetNodeDetailed(keg.NodeID(42))
var nf *keg.NodeNotFoundError
if errors.As(err, &nf) {
	fmt.Printf("missing node id: %s\n", nf.ID.Path())
}
```

Is / Unwrap on typed errors

- Typed errors in this package sometimes implement Is(target error) bool and Unwrap() error so they can both be matched by a sentinel and still expose fields via errors.As.
- Example: NodeNotFoundError implements Is to return true for ErrNodeNotFound and Unwrap returns the sentinel. This lets callers choose the simple sentinel check or extract the typed error for more info.

Interface matching (timeouts, temporary, retryable)

- Use errors.As to extract implementations of behavior interfaces (standard or custom).
- This package defines small local behavior interfaces used by the convenience predicates:
  - type temporary interface{ Temporary() bool }
  - type retryable interface{ Retryable() bool }

Example (net.Error):

```go
var nerr net.Error
if errors.As(err, &nerr) && nerr.Timeout() {
	// retry or special handling
}
```

Custom interface example (package predicates):

```go
var t temporary
if errors.As(err, &t) && t.Temporary() {
	// transient -> retry
}
```

Matching multiple patterns (switch-style)

- Combine errors.Is and errors.As in a switch or cascade for clear handling.

Example:

```go
var nf *keg.NodeNotFoundError
var nerr net.Error

switch {
case errors.Is(err, keg.ErrNodeNotFound):
	// simple not-found
case errors.As(err, &nf):
	// typed NodeNotFoundError with fields
case errors.As(err, &nerr): // net.Error
	// timeout handling
default:
	// fallback
}
```

Backend, rate-limit and transient errors

- BackendError wraps external backend failures and exposes Retryable() when transient.
- RateLimitError carries a suggested RetryAfter duration and is treated as retryable.
- TransientError marks network/temporary conditions and exposes both Temporary() and Retryable().

Helper constructors and convenience predicates

- The package exposes helper constructors, for example:

  - NewNodeNotFoundError(id NodeID)
  - NewConflictError(id NodeID, expect, got int, cause error)
  - NewBackendError(backend, op string, status int, cause error, transient bool)
  - NewRateLimitError(retryAfter time.Duration, msg string, cause error)
  - NewTransientError(cause error)

- Convenience predicate helpers:
  - IsNotFound(err error) bool — true if err represents a node-not-found condition (matches sentinels and typed errors).
  - IsPermissionDenied(err error) bool
  - IsConflict(err error) bool
  - IsRetryable(err error) bool — searches the chain for a Retryable() implementation and returns its result.
  - IsTemporary(err error) bool — searches the chain for a Temporary() implementation and returns its result.

Examples (using helpers):

```go
if keg.IsNotFound(err) {
	// handle not-found
}

if keg.IsRetryable(err) {
	// schedule a retry
}

if keg.IsTemporary(err) {
	// treat as transient
}
```

Multiple errors

- Go 1.20+ supports errors.Join to combine multiple errors. errors.Is will match any element; errors.As will search through the joined chain as expected.

Documentation guidance (API design)

- Document in package-level docs:
  - Which sentinel variables are exported and what they represent (e.g., ErrNodeNotFound).
  - Which concrete error types are returned and their exported fields.
  - Any behavior interfaces the package may return (Temporary, Retryable).
  - Mention provided constructors and convenience predicates (NewFooError, IsFoo).

Tests

- Add unit tests asserting that callers can match errors through wrapping:
  - Assert errors.Is(original, sentinel)
  - Assert errors.As(err, &typed)
  - Assert convenience predicates return expected values
- Example test skeleton (package keg):

```go
package keg_test

import (
	"errors"
	"testing"

	"github.com/yourorg/yourrepo/pkg/keg"
)

func TestGetNode_NotFound(t *testing.T) {
	_, err := keg.GetNodeSimple(keg.NodeID(99))
	if !errors.Is(err, keg.ErrNodeNotFound) {
		t.Fatalf("expected ErrNodeNotFound")
	}

	_, err = keg.GetNodeDetailed(keg.NodeID(99))
	var nf *keg.NodeNotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("expected typed NodeNotFoundError")
	}
	if nf.ID != keg.NodeID(99) {
		t.Fatalf("unexpected id %s", nf.ID.Path())
	}

	if !keg.IsNotFound(err) {
		t.Fatalf("IsNotFound should be true for typed not-found")
	}
}
```

Anti-patterns (avoid these)

- Matching on error strings (fragile, fails with wrapping or localization).
- Returning unexported concrete types when callers need to match them — either export the type or provide a sentinel or interface.
- Swallowing or discarding the original error when returning a new error; use %w to wrap to preserve matchability.

Checklist for library authors

- [ ] Export sentinel errors for simple conditions (e.g., ErrNodeNotFound, ErrMetaNotFound).
- [ ] Export concrete types if callers need structured info (or provide accessors/helpers).
- [ ] Provide small interfaces for common behaviors (Temporary, Retryable).
- [ ] Wrap errors with %w so errors.Is/errors.As traverse the chain.
- [ ] Document how callers should match your package’s errors and list available constructors/predicates.
- [ ] Provide and test convenience predicates (IsNotFound, IsRetryable, IsTemporary, IsConflict).
