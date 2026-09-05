package mcp

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// TestErrorResult_AuthFailuresCarryGuidance covers tapper#87: an auth failure
// used to fall through to bare error text with no structured content, leaving
// an agent no indication that reorienting is the refresh boundary.
func TestErrorResult_AuthFailuresCarryGuidance(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantCode     string
		wantReorient bool
	}{
		{
			name:         "unauthorized",
			err:          fmt.Errorf("hub call: %w", keg.ErrUnauthorized),
			wantCode:     "UNAUTHORIZED",
			wantReorient: true,
		},
		{
			name:         "token rejected",
			err:          fmt.Errorf("hub: %w (401 Unauthorized)", tapper.ErrTokenRejected),
			wantCode:     "UNAUTHORIZED",
			wantReorient: true,
		},
		{
			// A grant the user does not have is not fixed by logging in, so
			// this must not send the agent into a login/reorient loop.
			name:         "forbidden",
			err:          fmt.Errorf("hub call: %w", keg.ErrForbidden),
			wantCode:     "FORBIDDEN",
			wantReorient: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res := errorResult(tc.err)
			if !res.IsError {
				t.Fatalf("IsError = false, want true")
			}
			structured, ok := res.StructuredContent.(map[string]any)
			if !ok {
				t.Fatalf("StructuredContent = %T, want map[string]any", res.StructuredContent)
			}
			if got := structured["code"]; got != tc.wantCode {
				t.Fatalf("code = %v, want %v", got, tc.wantCode)
			}
			if got := structured["reorientRequired"]; got != tc.wantReorient {
				t.Fatalf("reorientRequired = %v, want %v", got, tc.wantReorient)
			}
			if got := structured["operationPerformed"]; got != false {
				t.Fatalf("operationPerformed = %v, want false", got)
			}
			action, _ := structured["action"].(string)
			if strings.TrimSpace(action) == "" {
				t.Fatalf("action is empty; the agent gets no actionable guidance")
			}
			if tc.wantReorient {
				if !strings.Contains(action, "tap auth login") || !strings.Contains(action, "reorient") {
					t.Fatalf("action %q does not mention logging in and reorienting", action)
				}
				// The issue explicitly asks that guidance never tell a caller
				// to kill a host-owned process.
				for _, banned := range []string{"restart the host", "kill", "restart your"} {
					if strings.Contains(strings.ToLower(action), banned) &&
						!strings.Contains(action, "restarting the host is not required") {
						t.Fatalf("action %q tells the caller to restart or kill the host", action)
					}
				}
			}
		})
	}
}

// TestErrorResult_AuthFailuresLeakNoCredentials guards the acceptance criterion
// that no credential detail reaches the agent transcript.
func TestErrorResult_AuthFailuresLeakNoCredentials(t *testing.T) {
	const secret = "thub_supersecrettokenvalue"
	res := errorResult(fmt.Errorf("request with token %s: %w", secret, keg.ErrUnauthorized))

	structured, _ := res.StructuredContent.(map[string]any)
	if action, _ := structured["action"].(string); strings.Contains(action, secret) {
		t.Fatalf("action leaked the credential")
	}
	if code, _ := structured["code"].(string); strings.Contains(code, secret) {
		t.Fatalf("code leaked the credential")
	}
}

// TestErrorResult_NonAuthErrorsAreUnchanged confirms the auth branch did not
// swallow the precondition paths that sit next to it.
//
// A plain error used to return nil StructuredContent. It no longer does: every
// error now carries the recovery contract, and an unclassified one reports its
// outcome as unknown. See TestErrorResultsAlwaysCarryTheRecoveryContract.
func TestErrorResult_NonAuthErrorsAreUnchanged(t *testing.T) {
	res := errorResult(errors.New("something ordinary went wrong"))
	plain, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("plain error lost its structured content")
	}
	if plain["code"] != keg.RemoteCodeInternal {
		t.Fatalf("code = %v, want %v", plain["code"], keg.RemoteCodeInternal)
	}
	if plain["operationPerformed"] != nil {
		t.Fatalf("operationPerformed = %v, want nil (unknown) for an unclassified error", plain["operationPerformed"])
	}

	res = errorResult(fmt.Errorf("write: %w", keg.ErrPreconditionRequired))
	structured, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("precondition error lost its structured content")
	}
	if structured["code"] != keg.RemoteCodePreconditionRequired {
		t.Fatalf("code = %v, want %v", structured["code"], keg.RemoteCodePreconditionRequired)
	}
}
