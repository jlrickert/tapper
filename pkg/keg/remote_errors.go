package keg

import (
	"errors"
	"net/http"
)

// Remote error codes carried in the hub's JSON error envelope
// ({"error": msg, "code": CODE}). The table below is the single source of
// truth for both directions: the hub maps sentinel errors to (code, status)
// when writing a response, and RemoteKeg maps (code, status) back to the
// sentinel when decoding one. Keep the two sides symmetric.
const (
	RemoteCodeNotFound     = "NOT_FOUND"
	RemoteCodeExist        = "EXIST"
	RemoteCodeDestExists   = "DEST_EXISTS"
	RemoteCodeConflict     = "CONFLICT"
	RemoteCodeInvalid      = "INVALID"
	RemoteCodeLockMismatch = "LOCK_MISMATCH"
	RemoteCodeNotLocked    = "NOT_LOCKED"
	RemoteCodeLock         = "LOCK"
	RemoteCodeLockTimeout  = "LOCK_TIMEOUT"
	RemoteCodeNotSupported = "NOT_SUPPORTED"
	RemoteCodeUnauthorized = "UNAUTHORIZED"
	RemoteCodeForbidden    = "FORBIDDEN"
	RemoteCodeBadRequest   = "BAD_REQUEST"
	RemoteCodeInternal     = "INTERNAL"
)

// remoteCodeTable pairs each sentinel with its wire code and HTTP status.
var remoteCodeTable = []struct {
	err    error
	code   string
	status int
}{
	{ErrNotExist, RemoteCodeNotFound, http.StatusNotFound},
	{ErrDestinationExists, RemoteCodeDestExists, http.StatusConflict},
	{ErrExist, RemoteCodeExist, http.StatusConflict},
	{ErrConflict, RemoteCodeConflict, http.StatusConflict},
	{ErrInvalid, RemoteCodeInvalid, http.StatusBadRequest},
	{ErrLockTokenMismatch, RemoteCodeLockMismatch, http.StatusLocked},
	{ErrNotLocked, RemoteCodeNotLocked, http.StatusConflict},
	{ErrLockTimeout, RemoteCodeLockTimeout, http.StatusConflict},
	{ErrLock, RemoteCodeLock, http.StatusConflict},
	{ErrNotSupported, RemoteCodeNotSupported, http.StatusNotImplemented},
}

// RemoteErrorCode maps err to its wire (code, status). Unrecognized errors
// map to (INTERNAL, 500).
func RemoteErrorCode(err error) (code string, status int) {
	for _, entry := range remoteCodeTable {
		if errors.Is(err, entry.err) {
			return entry.code, entry.status
		}
	}
	return RemoteCodeInternal, http.StatusInternalServerError
}

// RemoteErrorFromCode maps a wire (code, status, message) back to an error
// wrapping the matching sentinel. Codes without a sentinel (UNAUTHORIZED,
// FORBIDDEN, BAD_REQUEST, INTERNAL, unknown) map by status: 401→
// ErrUnauthorized, 403→ErrForbidden, 429→RateLimitError, 5xx→ transient
// BackendError, anything else → a plain error with the message.
func RemoteErrorFromCode(code string, status int, msg string) error {
	for _, entry := range remoteCodeTable {
		if entry.code == code {
			if msg == "" {
				return entry.err
			}
			return joinedRemoteError{msg: msg, sentinel: entry.err}
		}
	}
	switch {
	case status == http.StatusUnauthorized:
		return joinedRemoteError{msg: msg, sentinel: ErrUnauthorized}
	case status == http.StatusForbidden:
		return joinedRemoteError{msg: msg, sentinel: ErrForbidden}
	case status >= 500:
		return NewBackendError("hub", "request", status, errors.New(msg), true)
	default:
		return errors.New(msg)
	}
}

// joinedRemoteError carries the server-provided message while remaining
// errors.Is-matchable against the sentinel.
type joinedRemoteError struct {
	msg      string
	sentinel error
}

func (e joinedRemoteError) Error() string {
	if e.msg != "" {
		return e.msg
	}
	return e.sentinel.Error()
}

func (e joinedRemoteError) Unwrap() error { return e.sentinel }
