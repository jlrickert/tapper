package keg

// Package keg defines common sentinel errors, typed error types, and helper
// constructors/predicates used across the KEG project for consistent error
// handling and matching (errors.Is / errors.As compatible).
import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors are exported values intended for simple equality-style checks.
// Callers should use errors.Is(err, ErrX) to detect these conditions. When
// returning higher-level errors, wrap underlying errors with %w or return typed
// errors that implement Unwrap/Is so these sentinels remain matchable.
//
// Keep these sentinel identities stable (do not change them lightly). Clients
// must rely on errors.Is (identity), not error string matching.
var (
	ErrKegExists        = errors.New("keg: keg already exists")
	ErrNodeNotFound     = errors.New("keg: node not found")
	ErrNodeExists       = errors.New("keg: node exists")
	ErrContentNotFound  = errors.New("keg: node content not found")
	ErrMetaNotFound     = errors.New("keg: node meta not found")
	ErrNotFound         = errors.New("keg: item not found")
	ErrParser           = errors.New("keg: unable to parse")
	ErrKegNotFound      = errors.New("keg: keg not found")
	ErrDexNotFound      = errors.New("keg: dex not found")
	ErrPermissionDenied = errors.New("keg: permission denied")
	ErrInvalidMeta      = errors.New("keg: invalid meta")
	ErrConflict         = errors.New("keg: conflict")
	ErrQuotaExceeded    = errors.New("keg: quota exceeded")
	ErrRateLimited      = errors.New("keg: rate limited")

	// ErrDestinationExists is returned when a move/rename cannot proceed because
	// the destination node id already exists. Prefer returning a typed
	// DestinationExistsError that unwraps to this sentinel when callers may need
	// structured information.
	ErrDestinationExists = errors.New("keg: destination already exists")

	// ErrLockTimeout indicates acquiring a repository or node lock timed out or
	// was canceled. Lock-acquiring helpers should wrap context/cancellation
	// information while preserving this sentinel for callers that need to detect
	// timeout semantics via errors.Is.
	ErrLockTimeout = errors.New("keg: lock acquire timeout")

	// ErrLock indicates a generic failure to acquire a repository or node
	// lock. Use errors.Is(err, ErrLock) to detect non-timeout lock acquisition
	// failures.
	ErrLock = errors.New("keg: cannot acquire lock")
)

// Behavior interfaces used when inspecting error chains via errors.As.
// These are intentionally unexported; predicates expose the behavior to callers.
type temporary interface{ Temporary() bool }
type retryable interface{ Retryable() bool }

// NodeNotFoundError is a typed error that carries the missing NodeID. It
// implements Is/Unwrap so callers can match either the typed error (via
// errors.As) or the sentinel ErrNodeNotFound (via errors.Is).
type NodeNotFoundError struct {
	ID NodeID
}

func (e *NodeNotFoundError) Error() string {
	return fmt.Sprintf("node not found: %s", e.ID.Path())
}

// Is allows errors.Is(err, ErrNodeNotFound) to succeed when the chain contains
// a *NodeNotFoundError.
func (e *NodeNotFoundError) Is(target error) bool {
	return target == ErrNodeNotFound
}

// Unwrap returns the sentinel so errors.Is can traverse to the sentinel value.
func (e *NodeNotFoundError) Unwrap() error { return ErrNodeNotFound }

// DestinationExistsError indicates the destination id for a move already exists.
// It implements Is/Unwrap to match ErrDestinationExists.
type DestinationExistsError struct {
	ID NodeID
}

func (e *DestinationExistsError) Error() string {
	return fmt.Sprintf("destination exists: %s", e.ID.Path())
}

func (e *DestinationExistsError) Is(target error) bool { return target == ErrDestinationExists }
func (e *DestinationExistsError) Unwrap() error        { return ErrDestinationExists }

// ConflictError represents a write/version conflict for a node. It may include
// optional expected/got version values and a Cause that is returned by Unwrap.
type ConflictError struct {
	ID            NodeID
	Expected, Got int // optional version numbers or similar
	Cause         error
}

func (e *ConflictError) Error() string {
	if e.Expected == 0 && e.Got == 0 {
		return fmt.Sprintf("conflict for node %s", e.ID.Path())
	}
	return fmt.Sprintf("conflict for node %s: expected=%d got=%d", e.ID.Path(), e.Expected, e.Got)
}

// Unwrap returns the underlying cause (if any).
func (e *ConflictError) Unwrap() error { return e.Cause }

// Is allows errors.Is(err, ErrConflict) to succeed when the chain contains a
// *ConflictError.
func (e *ConflictError) Is(target error) bool { return target == ErrConflict }

// BackendError wraps errors coming from an external backend (API, DB, object
// store). It exposes Retryable() to indicate transient failures.
type BackendError struct {
	Backend    string // e.g. "s3", "http", "postgres", "fs"
	Op         string // operation, e.g. "WriteContent", "GetMeta"
	StatusCode int    // optional HTTP / backend status
	Cause      error
	Transient  bool // whether this is a transient error (retryable)
}

func (e *BackendError) Error() string {
	if e.StatusCode != 0 {
		return fmt.Sprintf("%s %s: status=%d: %v", e.Backend, e.Op, e.StatusCode, e.Cause)
	}
	return fmt.Sprintf("%s %s: %v", e.Backend, e.Op, e.Cause)
}

// Unwrap returns the wrapped cause.
func (e *BackendError) Unwrap() error { return e.Cause }

// Retryable reports whether the backend error is transient.
func (e *BackendError) Retryable() bool { return e.Transient }

// RateLimitError represents a throttling response that includes a suggested
// RetryAfter duration and an optional message. It is always considered retryable.
type RateLimitError struct {
	RetryAfter time.Duration // suggested wait time
	Message    string
	Cause      error
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("rate limited: retry after %s: %s", e.RetryAfter, e.Message)
	}
	if e.Message != "" {
		return "rate limited: " + e.Message
	}
	return "rate limited"
}

func (e *RateLimitError) Unwrap() error   { return e.Cause }
func (e *RateLimitError) Retryable() bool { return true }

// TransientError marks a transient (retryable) failure, e.g. network timeout,
// DB deadlock. It implements both Temporary() and Retryable().
type TransientError struct {
	Cause error
}

func (e *TransientError) Error() string   { return e.Cause.Error() }
func (e *TransientError) Unwrap() error   { return e.Cause }
func (e *TransientError) Temporary() bool { return true }
func (e *TransientError) Retryable() bool { return true }

// Helper constructors

// NewNodeNotFoundError constructs a *NodeNotFoundError for the given id.
func NewNodeNotFoundError(id NodeID) error {
	return &NodeNotFoundError{ID: id}
}

// NewDestinationExistsError constructs a *DestinationExistsError for the given id.
func NewDestinationExistsError(id NodeID) error {
	return &DestinationExistsError{ID: id}
}

// NewConflictError constructs a *ConflictError with the provided details.
func NewConflictError(id NodeID, expect, got int, cause error) error {
	return &ConflictError{
		ID:       id,
		Expected: expect,
		Got:      got,
		Cause:    cause,
	}
}

// NewBackendError constructs a *BackendError describing an operation against a backend.
func NewBackendError(backend, op string, status int, cause error, transient bool) error {
	return &BackendError{
		Backend:    backend,
		Op:         op,
		StatusCode: status,
		Cause:      cause,
		Transient:  transient,
	}
}

// NewRateLimitError constructs a *RateLimitError with a suggested retry duration.
func NewRateLimitError(retryAfter time.Duration, msg string, cause error) error {
	return &RateLimitError{RetryAfter: retryAfter, Message: msg, Cause: cause}
}

// NewTransientError constructs a *TransientError wrapping the provided cause.
func NewTransientError(cause error) error {
	return &TransientError{Cause: cause}
}

// Convenience predicates

// IsNotFound returns true if err represents a node-not-found condition.
func IsNotFound(err error) bool {
	return errors.Is(err, ErrNodeNotFound)
}

// IsDestinationExists returns true if err represents a destination-exists condition.
func IsDestinationExists(err error) bool {
	return errors.Is(err, ErrDestinationExists)
}

// IsPermissionDenied returns true if err indicates a permission problem.
func IsPermissionDenied(err error) bool {
	return errors.Is(err, ErrPermissionDenied)
}

// IsConflict returns true if err is a conflict error.
func IsConflict(err error) bool {
	return errors.Is(err, ErrConflict)
}

// IsRetryable inspects the error chain for a Retryable() bool implementation and
// returns its result (false if none found).
func IsRetryable(err error) bool {
	if err == nil {
		return false
	}
	var r retryable
	if errors.As(err, &r) {
		return r.Retryable()
	}
	return false
}

// IsTemporary inspects the error chain for a Temporary() bool implementation and
// returns its result (false if none found).
func IsTemporary(err error) bool {
	if err == nil {
		return false
	}
	var t temporary
	if errors.As(err, &t) {
		return t.Temporary()
	}
	return false
}
