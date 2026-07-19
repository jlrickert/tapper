package cli

import (
	"errors"
	"strings"
)

// UserMessager is implemented by error types that can produce a user-facing
// message tailored to the current CLI context (e.g. log level). New error
// types should implement this interface instead of adding branches to
// renderUserError.
type UserMessager interface {
	// UserMessage returns the message appropriate for the caller's debug mode.
	UserMessage(debug bool) string
}

func renderUserError(err error, deps *Deps) string {
	if err == nil {
		return ""
	}

	debug := isDebugLogLevel(deps)

	// Walk the error chain looking for a UserMessager implementation.
	var um UserMessager
	if errors.As(err, &um) {
		return um.UserMessage(debug)
	}

	return err.Error()
}

func isDebugLogLevel(deps *Deps) bool {
	if deps == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(deps.LogLevel), "debug")
}
