package keg

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
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
	// AllowedTargets binds discovered KEGs to destination Hubs; nil is a Hub-local context.
	AllowedTargets []string `json:"-"`
	// RootHub is trusted local routing metadata and is never transported.
	RootHub  string `json:"-"`
	Root     string `json:"root"`
	Active   string `json:"active"`
	Revision string `json:"revision,omitempty"`
}

type orientationStateContextKey struct{}
type orientationValidatorContextKey struct{}

// WithOrientationState binds trusted session orientation to an internal call
// context. RemoteKeg serializes it into OrientationHeaderName.
func WithOrientationState(ctx context.Context, state OrientationState) context.Context {
	state.AllowedTargets = slices.Clone(state.AllowedTargets)
	return context.WithValue(ctx, orientationStateContextKey{}, state)
}

// OrientationStateFromContext returns trusted orientation state, when present.
func OrientationStateFromContext(ctx context.Context) (OrientationState, bool) {
	state, ok := ctx.Value(orientationStateContextKey{}).(OrientationState)
	return state, ok && state.Root != "" && state.Active != ""
}

// EncodeOrientationState returns the versioned header value.
func EncodeOrientationState(state OrientationState) (string, error) {
	if state.Root == "" || state.Active == "" {
		return "", errors.New("orientation root and active flight are required")
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return "", fmt.Errorf("encode orientation state: %w", err)
	}
	return "v1." + base64.RawURLEncoding.EncodeToString(raw), nil
}

// DecodeOrientationState parses a versioned orientation header. The result is
// untrusted until the Hub authenticates the caller and evaluates current permissions.
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
	if state.Root == "" || state.Active == "" {
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
	previous, _ := ctx.Value(orientationValidatorContextKey{}).(OrientationValidator)
	if previous != nil {
		next := validate
		validate = func(current context.Context) error {
			if err := previous(current); err != nil {
				return err
			}
			return next(current)
		}
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

// OrientationHeaderForURL sends a proof only to the Hub that can validate it.
func OrientationHeaderForURL(ctx context.Context, target string) (string, bool) {
	state, ok := OrientationStateFromContext(ctx)
	if !ok {
		return "", false
	}
	if state.RootHub != "" {
		root, err := url.Parse(state.RootHub)
		if err != nil {
			return "", false
		}
		destination, err := url.Parse(target)
		if err != nil || !strings.EqualFold(root.Scheme, destination.Scheme) || !strings.EqualFold(root.Host, destination.Host) || !strings.HasPrefix(destination.Path, strings.TrimRight(root.Path, "/")+"/api/v1/") {
			return "", false
		}
	}
	return OrientationHeaderValue(ctx)
}

// ValidateOrientationTarget refuses unresolved or mismatched routing before dispatch.
func ValidateOrientationTarget(ctx context.Context, target string) error {
	state, ok := OrientationStateFromContext(ctx)
	if !ok || state.RootHub == "" {
		return nil
	}
	root, err := url.Parse(state.RootHub)
	if err != nil || root.Host == "" || (root.Scheme != "http" && root.Scheme != "https") {
		return ErrOrientationUnavailable
	}
	destination, err := url.Parse(target)
	if err != nil || !strings.EqualFold(root.Scheme, destination.Scheme) || !strings.EqualFold(root.Host, destination.Host) || !strings.HasPrefix(destination.EscapedPath(), strings.TrimRight(root.EscapedPath(), "/")+"/api/v1/") {
		return fmt.Errorf("%w: target is outside the pinned Hub", ErrOrientationDenied)
	}
	if state.AllowedTargets == nil {
		return nil
	}
	for _, allowed := range state.AllowedTargets {
		if sameOrientationTarget(target, allowed) {
			return nil
		}
	}
	return fmt.Errorf("%w: target Hub routing is outside the resolved Flight projection", ErrOrientationDenied)
}

func sameOrientationTarget(left, right string) bool {
	a, err := url.Parse(left)
	if err != nil {
		return false
	}
	b, err := url.Parse(right)
	if err != nil {
		return false
	}
	return a.Host != "" && strings.EqualFold(a.Scheme, b.Scheme) && strings.EqualFold(a.Host, b.Host) && strings.TrimRight(a.EscapedPath(), "/") == strings.TrimRight(b.EscapedPath(), "/") && a.RawQuery == b.RawQuery
}
