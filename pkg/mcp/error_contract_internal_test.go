package mcp

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
)

// TestErrorResultsAlwaysCarryTheRecoveryContract is the guard that keeps the
// error contract from decaying back into bare prose.
//
// An agent that gets only a sentence cannot tell whether its write landed, and
// in field testing that gap produced invented rules: a tester concluded `edit`
// required `schema` and that `expected_hash` validation was self-contradictory,
// because errors said what was wrong without saying where or what to do next.
//
// Every error a tool can return must therefore carry a code, a next step, and
// an explicit statement about whether state changed.
func TestErrorResultsAlwaysCarryTheRecoveryContract(t *testing.T) {
	t.Parallel()

	sentinels := []struct {
		name string
		err  error
	}{
		{"not found", keg.ErrNotExist},
		{"already exists", keg.ErrExist},
		{"destination exists", keg.ErrDestinationExists},
		{"invalid", keg.ErrInvalid},
		{"schema invalid", keg.ErrSchemaInvalid},
		{"invalid image", keg.ErrInvalidImage},
		{"lock mismatch", keg.ErrLockTokenMismatch},
		{"not locked", keg.ErrNotLocked},
		{"lock", keg.ErrLock},
		{"lock timeout", keg.ErrLockTimeout},
		{"not supported", keg.ErrNotSupported},
		{"precondition required", keg.ErrPreconditionRequired},
		{"precondition conflict", &keg.PreconditionConflictError{Resource: "node 1", CurrentHash: "abc"}},
		{"unauthorized", keg.ErrUnauthorized},
		{"forbidden", keg.ErrForbidden},
		{"orientation denied", ErrOrientationDenied},
		{"orientation unavailable", ErrOrientationUnavailable},
		{"orientation root unavailable", ErrOrientationRootUnavailable},
		{"unclassified", fmt.Errorf("some backend blew up")},
	}

	for _, tc := range sentinels {
		t.Run(tc.name, func(t *testing.T) {
			res := errorResult(tc.err)
			require.True(t, res.IsError, "result is not marked as an error")
			require.NotNil(t, res.StructuredContent, "error carries no structured content")

			raw, err := json.Marshal(res.StructuredContent)
			require.NoError(t, err)
			var got map[string]any
			require.NoError(t, json.Unmarshal(raw, &got))

			code, _ := got["code"].(string)
			require.NotEmpty(t, code, "error carries no code")

			action, _ := got["action"].(string)
			require.NotEmpty(t, action, "error carries no action; an agent cannot know what to do next")
			require.Greater(t, len(action), 25, "action %q is too terse to be actionable", action)

			performed, present := got["operationPerformed"]
			require.True(t, present, "error does not say whether state changed")
			if performed != nil {
				require.IsType(t, false, performed,
					"operationPerformed must be a bool or null (null meaning genuinely unknown)")
			}

			// The action must tell the caller what to do, not restate the code.
			require.NotEqual(t, strings.ToLower(code), strings.ToLower(action))
		})
	}
}

// TestUnclassifiedErrorsDoNotClaimNothingHappened pins the one case where
// honesty beats a definite answer. An unrecognised failure may have been raised
// after a partial write, so reporting operationPerformed:false would be a guess
// — and the field is only useful if an agent can trust it.
func TestUnclassifiedErrorsDoNotClaimNothingHappened(t *testing.T) {
	t.Parallel()

	raw, err := json.Marshal(errorResult(fmt.Errorf("backend exploded")).StructuredContent)
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(raw, &got))

	performed, present := got["operationPerformed"]
	require.True(t, present)
	require.Nil(t, performed, "an unclassified failure must report unknown, not false")
	require.Contains(t, got["action"], "cat", "the action must tell the agent how to establish current state")
}
