package keg

import (
	"bytes"
	"crypto/md5"
	"fmt"

	"github.com/jlrickert/tapper/pkg/internal"
)

// Deps holds optional dependency collaborators that can be injected into
// higher-level helpers and services. Use applyDefaults or applyKegOptions to
// produce a Deps value with any desired defaults applied.
type Deps struct {
	// Resolver resolves "keg:" tokens into concrete URLs. It is optional and may
	// be nil if callers prefer repository-based resolution.
	Resolver LinkResolver

	// Clock is used for deterministic timestamping in tests and production.
	Clock internal.Clock

	// Hasher is used to compute stable content hashes when needed. If nil, a
	// default hasher may be provided by callers (or via applyDefaults).
	Hasher Hasher
}

// Hasher computes a deterministic short hash for a byte slice. Implementations
// should return a textual representation suitable for inclusion in meta fields.
type Hasher interface {
	Hash(data []byte) string
}

// applyDefaults populates conservative defaults on the receiver without
// constructing heavy-weight external resources. It is safe to call on a nil
// receiver (no-op) and is intended for lightweight fallback wiring.
func (deps *Deps) applyDefaults() {
	if deps == nil {
		return
	}
	if deps.Resolver == nil {
		// Intentionally left as a no-op here. Callers may supply a LinkResolver
		// via WithLinkResolver or construct one explicitly. A package-level
		// default resolver could be assigned here if desired.
		// deps.Resolver = defaultLinkResolver
	}
	if deps.Clock == nil {
		deps.Clock = internal.RealClock{}
	}
}

// KegOption is a functional option type for configuring a Deps value.
type KegOption = func(*Deps)

// WithLinkResolver returns a KegOption that injects the provided LinkResolver.
func WithLinkResolver(resolver LinkResolver) KegOption {
	return func(dep *Deps) {
		dep.Resolver = resolver
	}
}

// WithClock returns a KegOption that injects the provided Clock.
func WithClock(clock internal.Clock) KegOption {
	return func(dep *Deps) {
		dep.Clock = clock
	}
}

// applyKegOptions constructs a Deps value from the provided functional options.
// Nil options are ignored. The returned Deps is a shallow, zero-initialized
// object with the options applied; callers may call applyDefaults afterwards if
// they want conservative defaults filled in.
func applyKegOptions(opts ...KegOption) *Deps {
	deps := &Deps{}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(deps)
	}
	return deps
}

// MD5Hasher is a simple Hasher implementation that returns an MD5 hex digest.
//
// Note: MD5 is used here for deterministic, compact hashes only and is not
// intended for cryptographic integrity protection.
type MD5Hasher struct{}

// Hash implements Hasher by returning the lowercase hex MD5 of the trimmed
// input bytes.
func (m *MD5Hasher) Hash(data []byte) string {
	sum := md5.Sum(bytes.TrimSpace(data))
	return fmt.Sprintf("%x", sum[:])
}

var _ Hasher = (*MD5Hasher)(nil)
