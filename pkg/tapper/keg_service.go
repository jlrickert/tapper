package tapper

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	appCtx "github.com/jlrickert/cli-toolkit/appctx"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// nodeQueryResolver evaluates a single query term against a node's data.
// For key=value terms, it checks the node's meta.yaml attributes.
// For plain terms, it checks the node's tag set.
// This bridges the gap between pkg/tapper's resolveQueryTerm (which needs a
// Keg and Dex) and pkg/keg's per-node callback signature.
func nodeQueryResolver(term string, data *keg.NodeData) bool {
	if data == nil {
		return false
	}
	idx := strings.IndexByte(term, '=')
	if idx < 0 {
		// Plain tag — check node's tag set.
		for _, t := range data.Tags() {
			if t == term {
				return true
			}
		}
		return false
	}
	// Attribute predicate: key=value — check node's meta.
	key := term[:idx]
	val := term[idx+1:]
	if data.Meta == nil {
		return false
	}
	got, ok := data.Meta.Get(key)
	return ok && got == val
}

// KegService resolves keg targets from config, project paths, and explicit filesystem locations.
type KegService struct {
	// Runtime provides filesystem and environment access used to resolve kegs.
	Runtime *toolkit.Runtime

	// ConfigService resolves configured keg aliases and targets.
	ConfigService *ConfigService

	// cacheMu guards kegCache for concurrent access.
	cacheMu sync.Mutex
	// kegCache memoizes resolved kegs by alias or file-derived cache key.
	kegCache map[string]*keg.Keg

	// authStoreOnce guards the lazy load of authStore. We only touch the
	// auth file on first remote-keg resolution so local-only workflows
	// never pay for a disk read they don't need.
	authStoreOnce sync.Once
	// authStore is the loaded auth store, or nil when the file is missing
	// or failed to parse. Nil is valid: the resolver short-circuits to "".
	authStore *AuthStore
	// authStorePath is the path authStore was loaded from, handed to the
	// resolver so it can persist a refreshed token back to disk.
	authStorePath string
}

// ResolveKegOptions controls how KegService resolves a keg target.
type ResolveKegOptions struct {
	// Root is the base path used for project and fallback resolution.
	Root string
	// Keg is the explicit keg alias to resolve.
	Keg string
	// Project resolves a keg from project-local locations.
	Project bool
	// Cwd limits project resolution to the current working directory.
	Cwd bool
	// Path resolves a keg from an explicit filesystem path.
	Path string
	// NoCache disables in-memory keg caching for this resolution.
	NoCache bool
}

// ensureCache initializes the in-memory keg cache when needed.
func (s *KegService) ensureCache() {
	if s.kegCache == nil {
		s.kegCache = map[string]*keg.Keg{}
	}
}

// injectDexOpts installs the standard set of extra DexOptions on a resolved
// keg. This provides the query resolver that enables key=value attribute
// predicates in config-driven custom indexes (e.g. query: "favorite=true").
func (s *KegService) injectDexOpts(k *keg.Keg) {
	if k == nil {
		return
	}
	k.SetExtraDexOpts(keg.WithQueryResolver(nodeQueryResolver))
}

// tokenResolver returns a keg.TokenResolver backed by the service's lazily
// loaded AuthStore. Load failures are swallowed (logged at debug) and yield
// a nil-backed resolver that always returns "" — a missing or corrupt auth
// file must never block keg resolution for local or token-pinned targets.
func (s *KegService) tokenResolver() keg.TokenResolver {
	s.authStoreOnce.Do(func() {
		if s.ConfigService == nil || s.ConfigService.PathService == nil {
			return
		}
		path := s.ConfigService.PathService.AuthStorePath()
		store, err := LoadAuthStore(context.Background(), s.Runtime, path)
		if err != nil {
			if logger := s.Runtime.Logger(); logger != nil {
				logger.Debug("auth store load failed", "path", path, "err", err)
			}
			return
		}
		s.authStore = store
		s.authStorePath = path
	})
	return NewAuthStoreTokenResolver(s.authStore, s.Runtime, s.authStorePath)
}

// Resolve returns a keg using explicit path, project, alias, or configured fallback resolution.
func (s *KegService) Resolve(ctx context.Context, opts ResolveKegOptions) (*keg.Keg, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.ensureCache()
	cache := !opts.NoCache

	alias := strings.TrimSpace(opts.Keg)
	explicitPath := strings.TrimSpace(opts.Path)

	if alias != "" && (opts.Project || opts.Cwd || explicitPath != "") {
		return nil, fmt.Errorf("--keg cannot be used with --project, --cwd, or --path")
	}
	if opts.Project && explicitPath != "" {
		return nil, fmt.Errorf("--project cannot be used with --path")
	}

	base := strings.TrimSpace(opts.Root)
	if base == "" {
		var err error
		base, err = s.Runtime.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	if explicitPath != "" {
		return s.resolveProjectTarget(ctx, explicitPath, cache)
	}
	if opts.Project || opts.Cwd {
		if !opts.Cwd {
			if gitRoot := appCtx.FindGitRoot(ctx, s.Runtime, base); gitRoot != "" {
				base = gitRoot
			}
		}
		return s.resolveProjectTarget(ctx, base, cache)
	}
	if alias != "" {
		return s.resolveKegAlias(ctx, alias, base, cache)
	}

	return s.resolvePath(ctx, base, cache)
}

// resolveProjectTarget resolves a filesystem-backed keg under known project keg locations.
func (s *KegService) resolveProjectTarget(ctx context.Context, base string, cache bool) (*keg.Keg, error) {
	rawBase := filepath.Clean(toolkit.ExpandEnv(s.Runtime, base))
	expandedBase := rawBase
	if p, err := toolkit.ExpandPath(s.Runtime, rawBase); err == nil {
		expandedBase = filepath.Clean(p)
	}

	// Check whether the base directory itself exists before searching for keg files.
	info, statErr := s.Runtime.Stat(expandedBase, false)
	if statErr != nil || !info.IsDir() {
		return nil, &PathNotFoundError{Path: base}
	}

	baseCandidates := []string{rawBase}
	if expandedBase != "" && expandedBase != rawBase {
		baseCandidates = append(baseCandidates, expandedBase)
	}

	var candidates []string
	seen := map[string]struct{}{}
	for _, b := range baseCandidates {
		if b == "" {
			continue
		}
		baseName := filepath.Base(filepath.Clean(b))
		for _, candidate := range []string{
			b,
			filepath.Join(b, "kegs", baseName),
			filepath.Join(b, "kegs", "project"),
			filepath.Join(b, "kegs", "tapper"),
		} {
			candidate = filepath.Clean(candidate)
			if _, ok := seen[candidate]; ok {
				continue
			}
			seen[candidate] = struct{}{}
			candidates = append(candidates, candidate)
		}

	}

	var checked []string
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		kegFile := filepath.Join(candidate, "keg")
		checked = append(checked, kegFile)
		info, statErr := s.Runtime.Stat(kegFile, false)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		return s.resolveFileKeg(ctx, candidate, cache)
	}

	return nil, newProjectKegNotFoundError(checked)
}

// resolveFileKeg resolves a keg from a filesystem root and caches it by normalized path.
// Symlinks are resolved before generating the cache key so that symlinks or
// mounts pointing to the same underlying directory share a single cache entry.
func (s *KegService) resolveFileKeg(ctx context.Context, root string, cache bool) (*keg.Keg, error) {
	cleanRoot := filepath.Clean(root)
	// Resolve symlinks so different paths that point to the same physical
	// directory produce identical cache keys.
	if resolved, err := filepath.EvalSymlinks(cleanRoot); err == nil {
		cleanRoot = resolved
	}
	key := "file:" + cleanRoot
	if cache && s.kegCache[key] != nil {
		return s.kegCache[key], nil
	}

	target := keg.NewFile(root)
	k, err := keg.NewKegFromTarget(ctx, target, s.Runtime, keg.WithTokenResolver(s.tokenResolver()))
	if err != nil {
		return nil, err
	}
	s.injectDexOpts(k)

	if cache {
		s.kegCache[key] = k
	}
	return k, nil
}

// resolvePath resolves the effective keg alias from config for the given path and returns its keg.
//
// Precedence: defaultKeg (authoritative, project-set) → kegMap (path-specific)
// → fallbackKeg (global-user last resort). The default* slots are meant for
// project config and win first; kegMap routes by path; fallback* are what
// `tap bootstrap` writes for the global user so anything more specific overrides.
func (s *KegService) resolvePath(ctx context.Context, path string, cache bool) (*keg.Keg, error) {
	s.ensureCache()
	cfg, err := s.ConfigService.Config(true)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path config: %w", err)
	}
	kegAlias := cfg.DefaultKeg()
	if kegAlias == "" {
		kegAlias = cfg.LookupAlias(s.Runtime, path)
	}
	if kegAlias == "" {
		kegAlias = cfg.FallbackKeg()
	}
	if kegAlias == "" {
		return nil, fmt.Errorf("no keg configured")
	}
	return s.resolveKegAlias(ctx, kegAlias, path, cache)
}

// resolveKegAlias resolves a keg selector from config and falls back to
// project-local resolution. The selector is a keg reference string (a bare
// name, @ns/name, keg:..., or a path), resolved via the namespace-centric
// chain in ConfigService.ResolveTarget. When that yields no target — e.g. a
// bare name with no namespace/hub configured — a project-local keg at
// <project>/kegs/<name> answers instead, so local project kegs work without
// any config (the documented last-resort tier).
func (s *KegService) resolveKegAlias(ctx context.Context, kegAlias string, projectRoot string, cache bool) (*keg.Keg, error) {
	s.ensureCache()
	if kegAlias == "" {
		return nil, fmt.Errorf("no keg configured")
	}
	if cache && s.kegCache[kegAlias] != nil {
		return s.kegCache[kegAlias], nil
	}

	target, err := s.ConfigService.ResolveTarget(kegAlias, cache)
	if err == nil && target != nil {
		k, kerr := keg.NewKegFromTarget(ctx, *target, s.Runtime, keg.WithTokenResolver(s.tokenResolver()))
		if kerr != nil {
			return k, kerr
		}
		if k != nil {
			s.injectDexOpts(k)
			s.kegCache[kegAlias] = k
		}
		return k, nil
	}

	// ResolveTarget could not turn the selector into a target. A bare keg name
	// (no namespace, hub, or path) may instead name a project-local keg at
	// <project>/kegs/<name> — resolve it so local project kegs work without
	// requiring any config entries.
	if ref := parseKegRef(kegAlias); ref.Name != "" && ref.Namespace == "" && ref.Hub == "" && ref.Path == "" {
		if projectKeg, found, projectErr := s.resolveProjectAlias(ctx, projectRoot, ref.Name, cache); projectErr != nil {
			return nil, projectErr
		} else if found {
			if cache && projectKeg != nil {
				s.kegCache[kegAlias] = projectKeg
			}
			return projectKeg, nil
		}
	}

	// ResolveTarget failed and no project-local fallback was found.
	if err != nil {
		return nil, err
	}

	return nil, fmt.Errorf("keg %q could not be resolved", kegAlias)
}

// resolveProjectAlias resolves a project-local alias at <project>/kegs/<alias>/keg when present.
func (s *KegService) resolveProjectAlias(ctx context.Context, base string, alias string, cache bool) (*keg.Keg, bool, error) {
	base = strings.TrimSpace(base)
	alias = strings.TrimSpace(alias)
	if base == "" || alias == "" {
		return nil, false, nil
	}

	searchBase := base
	if gitRoot := appCtx.FindGitRoot(ctx, s.Runtime, base); gitRoot != "" {
		searchBase = gitRoot
	}

	rawBase := filepath.Clean(toolkit.ExpandEnv(s.Runtime, searchBase))
	expandedBase := rawBase
	if p, err := toolkit.ExpandPath(s.Runtime, rawBase); err == nil {
		expandedBase = filepath.Clean(p)
	}

	baseCandidates := []string{rawBase}
	if expandedBase != "" && expandedBase != rawBase {
		baseCandidates = append(baseCandidates, expandedBase)
	}

	seen := map[string]struct{}{}
	for _, candidateBase := range baseCandidates {
		if candidateBase == "" {
			continue
		}
		projectKegRoot := filepath.Clean(filepath.Join(candidateBase, "kegs", alias))
		if _, ok := seen[projectKegRoot]; ok {
			continue
		}
		seen[projectKegRoot] = struct{}{}

		kegFile := filepath.Join(projectKegRoot, "keg")
		info, statErr := s.Runtime.Stat(kegFile, false)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}

		k, err := s.resolveFileKeg(ctx, projectKegRoot, cache)
		if err != nil {
			return nil, false, err
		}
		return k, true, nil
	}

	return nil, false, nil
}
