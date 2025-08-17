package tapper

import (
	"errors"
	"fmt"
)

// Sentinel errors used for simple equality-style checks.
var (
	// ErrAliasNotFound indicates a requested alias/key was not found.
	ErrAliasNotFound = errors.New("tapper: alias not found")

	// ErrInvalidConfig indicates the configuration is invalid or fails validation.
	ErrInvalidConfig = errors.New("tapper: invalid config")

	// ErrCredentialInURL indicates a detected URL embeds credentials (user:pass@host).
	ErrCredentialInURL = errors.New("tapper: credentials embedded in URL")

	// ErrLockTimeout indicates acquiring a repository or config lock timed out.
	ErrLockTimeout = errors.New("tapper: lock acquire timeout")
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

// CredentialError indicates a URL contained embedded credentials which we reject.
type CredentialError struct {
	URL string
}

func (e *CredentialError) Error() string {
	if e.URL == "" {
		return "credentials embedded in URL"
	}
	return fmt.Sprintf("credentials embedded in URL: %s", e.URL)
}

func (e *CredentialError) Is(target error) bool {
	return target == ErrCredentialInURL
}

func (e *CredentialError) Unwrap() error { return ErrCredentialInURL }

// NewCredentialError constructs a CredentialError for the given URL.
func NewCredentialError(url string) error {
	return &CredentialError{URL: url}
}

// IsCredentialError reports whether err is (or wraps) a credentials-in-url error.
func IsCredentialError(err error) bool {
	return errors.Is(err, ErrCredentialInURL)
}
