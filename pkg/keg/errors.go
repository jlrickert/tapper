package keg

import (
	"errors"
	"fmt"
	"time"
)

// Sentinel errors used for simple equality-style checks.
var (
	ErrAliasNotFound    = errors.New("alias not found")
	ErrInvalidConfig    = errors.New("invalid config")
	ErrKegExists        = errors.New("keg already exists")
	ErrNodeNotFound     = errors.New("node not found")
	ErrNodeExists       = errors.New("node exists")
	ErrContentNotFound  = errors.New("node content not found")
	ErrMetaNotFound     = errors.New("node meta not found")
	ErrNotFound         = errors.New("item not found")
	ErrParser           = errors.New("unable to parse")
	ErrKegNotFound      = errors.New("keg not found")
	ErrDexNotFound      = errors.New("dex not found")
	ErrPermissionDenied = errors.New("permission denied")
	ErrInvalidMeta      = errors.New("invalid meta")
	ErrConflict         = errors.New("conflict")
	ErrQuotaExceeded    = errors.New("quota exceeded")
	ErrRateLimited      = errors.New("rate limited")

	// ErrDestinationExists is returned when a move/rename cannot proceed because
	// the destination node id already exists. Prefer returning a typed
	// DestinationExistsError that unwraps to this sentinel when callers may need
	// structured information.
	ErrDestinationExists = errors.New("destination already exists")

	// ErrLockTimeout indicates acquiring a repository or node lock timed out or
	// was canceled. Lock-acquiring helpers should wrap context/cancellation
	// information while preserving this sentinel for callers that need to detect
	// timeout semantics via errors.Is.
	ErrLockTimeout = errors.New("lock acquire timeout")

	// ErrLock indicates a generic failure to acquire a repository or node
	// lock. Use errors.Is(err, ErrLock) to detect non-timeout lock acquisition
	// failures.
	ErrLock = errors.New("cannot acquire lock")
)

// AliasNotFoundError is a typed error that carries the missing alias for callers
// that need richer diagnostic information.
type AliasNotFoundError struct {
	Alias string
}

func (e *AliasNotFoundError) Error() string { return fmt.Sprintf("alias not found: %q", e.Alias) }

func (e *AliasNotFoundError) Is(target error) bool {
	return target == ErrAliasNotFound
}

func (e *AliasNotFoundError) Unwrap() error { return ErrAliasNotFound }

// NewAliasNotFoundError constructs a typed AliasNotFoundError.
func NewAliasNotFoundError(alias string) error {
	return &AliasNotFoundError{Alias: alias}
}

// IsAliasNotFound reports whether err is (or wraps) an alias-not-found condition.
func IsAliasNotFound(err error) bool {
	return errors.Is(err, ErrAliasNotFound)
}

// InvalidConfigError represents a validation or parse failure for tapper config.
type InvalidConfigError struct {
	Msg string
}

func (e *InvalidConfigError) Error() string {
	if e.Msg == "" {
		return "invalid tapper config"
	}
	return fmt.Sprintf("invalid tapper config: %s", e.Msg)
}

func (e *InvalidConfigError) Is(target error) bool {
	return target == ErrInvalidConfig
}

func (e *InvalidConfigError) Unwrap() error { return ErrInvalidConfig }

// NewInvalidConfigError creates an InvalidConfigError with a human message.
func NewInvalidConfigError(msg string) error {
	return &InvalidConfigError{Msg: msg}
}

// IsInvalidConfig reports whether err is (or wraps) an invalid-config condition.
func IsInvalidConfig(err error) bool {
	return errors.Is(err, ErrInvalidConfig)
}

// Behavior interfaces used when inspecting error chains via errors.As.
// These are intentionally unexported; predicates expose the behavior to callers.
type temporary interface{ Temporary() bool }
type retryable interface{ Retryable() bool }

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
