package keg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrOrientationStale           = errors.New("orientation stale")
	ErrOrientationDenied          = errors.New("orientation denied")
	ErrOrientationUnavailable     = errors.New("orientation unavailable")
	ErrOrientationRootUnavailable = errors.New("orientation root unavailable")
)

// OrientationHeaderName carries trusted Tapper session state between Tapper's
// RemoteKeg client and a Hub. It is internal protocol state, never a model tool
// argument and never authorization by itself.
const OrientationHeaderName = "Tapper-Orientation"

// OrientationState is the minimum state a Hub needs to recompute current
// authority for a governed request.
type OrientationState struct {
	Root     string `json:"root"`
	Active   string `json:"active"`
	Revision string `json:"revision"`
}

type orientationStateContextKey struct{}
type orientationValidatorContextKey struct{}

// WithOrientationState binds trusted session orientation to an internal call
// context. RemoteKeg serializes it into OrientationHeaderName.
func WithOrientationState(ctx context.Context, state OrientationState) context.Context {
	return context.WithValue(ctx, orientationStateContextKey{}, state)
}

// OrientationStateFromContext returns trusted orientation state, when present.
func OrientationStateFromContext(ctx context.Context) (OrientationState, bool) {
	state, ok := ctx.Value(orientationStateContextKey{}).(OrientationState)
	return state, ok && state.Root != "" && state.Active != "" && state.Revision != ""
}

// EncodeOrientationState returns the versioned header value.
func EncodeOrientationState(state OrientationState) (string, error) {
	if state.Root == "" || state.Active == "" || state.Revision == "" {
		return "", errors.New("orientation root, active flight, and revision are required")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode orientation state: %w", err)
	}
	return "v1." + base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeOrientationState parses a versioned orientation header. The result is
// untrusted until the Hub authenticates the caller and recomputes Revision.
func DecodeOrientationState(value string) (OrientationState, error) {
	value = strings.TrimSpace(value)
	encoded, ok := strings.CutPrefix(value, "v1.")
	if !ok || encoded == "" {
		return OrientationState{}, errors.New("unsupported orientation header")
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return OrientationState{}, fmt.Errorf("decode orientation header: %w", err)
	}
	var state OrientationState
	if err := json.Unmarshal(raw, &state); err != nil {
		return OrientationState{}, fmt.Errorf("parse orientation header: %w", err)
	}
	if state.Root == "" || state.Active == "" || state.Revision == "" {
		return OrientationState{}, errors.New("incomplete orientation header")
	}
	return state, nil
}

// OrientationHeaderValue returns the header for trusted context state.
func OrientationHeaderValue(ctx context.Context) (string, bool) {
	state, ok := OrientationStateFromContext(ctx)
	if !ok {
		return "", false
	}
	value, err := EncodeOrientationState(state)
	return value, err == nil
}

// OrientationValidator recomputes authority at an operation boundary.
type OrientationValidator func(context.Context) error

// WithOrientationValidator installs the Hub-side validation callback used by
// durable mutation transactions after acquiring their locks.
func WithOrientationValidator(ctx context.Context, validate OrientationValidator) context.Context {
	if validate == nil {
		return ctx
	}
	return context.WithValue(ctx, orientationValidatorContextKey{}, validate)
}

// ValidateOrientation runs the Hub-side validator, when one is installed.
func ValidateOrientation(ctx context.Context) error {
	validate, _ := ctx.Value(orientationValidatorContextKey{}).(OrientationValidator)
	if validate == nil {
		return nil
	}
	return validate(ctx)
}
