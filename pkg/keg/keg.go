package keg

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

// LocalKeg is the concrete high-level service providing KEG node operations
// backed by a Repository. It abstracts storage implementation details, allowing
// operations over nodes to work uniformly across memory and filesystem backends.
// LocalKeg delegates low-level storage operations to its underlying repository and
// maintains an in-memory dex for indexing.
type LocalKeg struct {
	// target is the keg URL/location (nil for memory-backed kegs)
	target *Target
	// Repo is the storage backend implementation
	Repo Repository
	// Runtime provides clock/hash/fs helpers used by high-level keg operations.
	Runtime *toolkit.Runtime

	// dexMu guards lazy initialization of dex and the mtime/gen fields below.
	dexMu sync.Mutex
	// dex is an optional in-memory index of nodes, lazily loaded from repo
	dex *Dex
	// dexWriteGen is a monotonic counter bumped after every successful
	// Dex.Write by this process. Used for diagnostics.
	dexWriteGen uint64
	// dexLoadMtime records the ModTime of dex/nodes.tsv at the time the
	// cached dex was last loaded from disk. Used by dexStale() to detect
	// whether another process has modified the index files since this
	// process last read them.
	dexLoadMtime time.Time
	// dexLoadGeneration records a repository-owned in-process generation when
	// the backend exposes one (MemoryRepo). It keeps caches in separate
	// LocalKeg instances coherent even though filesystem mtimes do not apply.
	dexLoadGeneration uint64

	// configMu guards the read-modify-write cycle in UpdateConfig.
	configMu sync.Mutex

	// kegExistsVerified is set to true after the first successful
	// checkKegExists call. Once a keg is confirmed to exist, it won't
	// un-exist, so subsequent checks are skipped.
	kegExistsVerified atomic.Bool
}

// Option is a functional option for configuring LocalKeg behavior
type Option func(*LocalKeg)

// TokenResolver supplies bearer tokens for remote targets when no token is
// configured inline on the target or via TokenEnv. Implementations typically
// look up credentials in a persistent auth store keyed by the target's hub
// root. A resolver returns "" when no credential is available; a nil
// TokenResolver is legal and means "no fallback".
type TokenResolver interface {
	// ResolveToken returns the bearer token for target, or an empty string when
	// no credential is available.
	ResolveToken(target *Target) string
}

// KegOption customises NewKegFromTarget without breaking existing callers.
// Variadic options keep the common case (no resolver, no extras) a zero-cost
// invocation.
type KegOption func(*kegOptions)

type kegOptions struct {
	resolver TokenResolver
}

// WithTokenResolver installs a TokenResolver consulted as the third and final
// fallback when a remote target has neither a TokenEnv-sourced value nor an
// inline Token. Pass a nil resolver to explicitly opt out of fallback; the
// option itself is a no-op in that case.
func WithTokenResolver(r TokenResolver) KegOption {
	return func(o *kegOptions) {
		o.resolver = r
	}
}

// NewKegFromTarget constructs a Keg implementation from a Target. It automatically
// selects the appropriate repository implementation based on the target's scheme:
//   - memory:// targets use an in-memory repository
//   - file:// targets use a filesystem repository
//   - http:// and https:// targets use a RemoteKeg speaking the hub's
//     operation API
//   - hub targets use a RemoteKeg resolved from repo/user/keg fields
//
// Returns an error if the target scheme is not supported.
func NewKegFromTarget(ctx context.Context, target Target, rt *toolkit.Runtime, opts ...KegOption) (Keg, error) {
	var o kegOptions
	for _, apply := range opts {
		apply(&o)
	}
	switch target.Scheme() {
	case SchemeMemory:
		repo := NewMemoryRepo(rt)
		keg := LocalKeg{Repo: repo, Runtime: rt}
		return &keg, nil
	case SchemeFile:
		repo := FsRepo{
			Root:            filepath.Clean(target.Path()),
			ContentFilename: MarkdownContentFilename,
			MetaFilename:    YAMLMetaFilename,
			StatsFilename:   JSONStatsFilename,
			runtime:         rt,
		}
		keg := LocalKeg{target: &target, Repo: &repo, Runtime: rt}
		return &keg, nil
	case SchemeHTTP, SchemeHTTPs:
		token := resolveTargetToken(&target, rt, o.resolver)
		baseURL := strings.TrimRight(target.Url, "/")
		keg := NewRemoteKeg(baseURL, token, rt)
		keg.SetTarget(&target)
		installTokenFn(keg, &target, rt, o.resolver)
		return keg, nil
	case SchemeAlias:
		token := resolveTargetToken(&target, rt, o.resolver)
		// Build the API base URL from the resolved hub URL, namespace, and
		// keg-name fields. The hub routes per-keg endpoints under
		// <hubURL>/api/v1/@<namespace>/kegs/<kegName> — only the namespace
		// segment carries the @ sigil; keg aliases are bare (see tapper-hub
		// server.go mountKegRoutes).
		// HubURL is pushed down by the tapper layer from the configured hubs map
		// during resolution; a keg reference that reaches here without it was not
		// resolved against a hub.
		base := strings.TrimRight(target.HubURL, "/")
		if base == "" {
			return nil, fmt.Errorf("keg reference %q has no resolved hub url", target.String())
		}
		baseURL := fmt.Sprintf("%s/api/v1/@%s/kegs/%s",
			base, target.Namespace, target.KegName)
		keg := NewRemoteKeg(baseURL, token, rt)
		keg.SetTarget(&target)
		installTokenFn(keg, &target, rt, o.resolver)
		return keg, nil
	}
	return nil, fmt.Errorf("unsupported target scheme: %s", target.Scheme())
}

// installTokenFn makes k re-run the target's token resolution chain on every
// request instead of pinning the token captured at construction. Kegs are
// memoized by callers (KegService's cache) and can live for hours in a
// long-running MCP server; the hub's access tokens expire in minutes. The
// resolver refreshes expired tokens as a side effect of ResolveToken, so
// re-resolving per request is what keeps a cached keg authenticated. Only
// installed when a resolver exists — a static inline/env token has nothing
// to re-resolve.
func installTokenFn(k *RemoteKeg, target *Target, rt *toolkit.Runtime, resolver TokenResolver) {
	if resolver == nil {
		return
	}
	k.SetTokenFn(func() string {
		return resolveTargetToken(target, rt, resolver)
	})
}

// resolveTargetToken extracts the bearer token from a Target. Precedence:
// TokenEnv (environment variable name) → literal Token → resolver fallback.
// A nil resolver skips the fallback. Returns an empty string when no
// credential is available.
func resolveTargetToken(target *Target, rt *toolkit.Runtime, r TokenResolver) string {
	if target.TokenEnv != "" {
		if v := rt.Get(target.TokenEnv); v != "" {
			return v
		}
	}
	if target.Token != "" {
		return target.Token
	}
	if r != nil {
		return r.ResolveToken(target)
	}
	return ""
}

// NewLocalKeg returns a LocalKeg service backed by the provided repository.
// Functional options can be provided to customize LocalKeg behavior.
func NewLocalKeg(repo Repository, rt *toolkit.Runtime, opts ...Option) *LocalKeg {
	keg := &LocalKeg{
		Repo:    repo,
		Runtime: rt,
	}
	for _, o := range opts {
		o(keg)
	}
	return keg
}

// RepoContainsKeg checks if a keg has been properly initialized within a repository.
// It verifies both that a keg config exists and that a zero node (node ID 0) is present.
// Returns true only if both conditions are met, indicating a fully initialized keg.
func RepoContainsKeg(ctx context.Context, repo Repository) (bool, error) {
	if repo == nil {
		return false, fmt.Errorf("no repository provided")
	}
	var exists bool
	err := repo.WithKegRead(ctx, func(readCtx context.Context) error {
		var err error
		exists, err = repoContainsKeg(readCtx, repo)
		return err
	})
	return exists, err
}

func repoContainsKeg(ctx context.Context, repo Repository) (bool, error) {

	var configExists bool

	// Check for a config. If it is missing, keg is not initialized.
	_, err := repo.ReadConfig(ctx)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			configExists = false
		} else {
			return false, fmt.Errorf("failed to check config existence: %w", err)
		}
	} else {
		configExists = true
	}

	var zeroNodeExists bool

	// Ensure a zero node exists by attempting to read content for ID 0.
	_, err = repo.ReadContent(ctx, NodeId{ID: 0})
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			zeroNodeExists = false
		} else {
			return false, fmt.Errorf("failed to check zero node existence: %w", err)
		}
	} else {
		zeroNodeExists = true
	}
	return configExists && zeroNodeExists, nil
}

// checkKegExists verifies that a keg is properly initialized in the repository.
// Returns an error if the keg is not found or if the repository is not configured.
func (k *LocalKeg) checkKegExists(ctx context.Context) error {
	if k == nil || k.Repo == nil {
		return fmt.Errorf("no repository configured")
	}

	if k.kegExistsVerified.Load() {
		return nil
	}

	exists, err := RepoContainsKeg(ctx, k.Repo)
	if err != nil {
		return fmt.Errorf("failed to check keg existence: %w", err)
	}
	if !exists {
		return fmt.Errorf("keg not initialized: %w", ErrNotExist)
	}
	k.kegExistsVerified.Store(true)
	return nil
}

// Target returns the keg's resolved location, or nil for anonymous
// (memory-backed) kegs.
func (k *LocalKeg) Target() *Target {
	if k == nil {
		return nil
	}
	return k.target
}

// SetTarget records the keg's resolved location. Construction paths that
// cannot pass the target through a literal (e.g. the hub opening a keg by
// namespace/alias) use this to label the keg after the fact.
func (k *LocalKeg) SetTarget(target *Target) {
	k.target = target
}

// LocalKeg implements the full Keg interface.
var _ Keg = (*LocalKeg)(nil)
