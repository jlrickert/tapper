package tapper

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// KegService resolves configured remote KEGs.
type KegService struct {
	Runtime       *toolkit.Runtime
	ConfigService *ConfigService

	cacheMu  sync.Mutex
	kegCache map[string]keg.Keg

	authStoreOnce sync.Once
	authStore     *AuthStore
	authStorePath string
	authResolver  keg.TokenResolver
}

// ResolveKegOptions controls remote KEG resolution.
type ResolveKegOptions struct {
	// Root is used only for workspace kegMap matching.
	Root      string
	Keg       string
	Namespace string
	Hub       string

	RequireBootstrap bool
	NoCache          bool
}

func (s *KegService) ensureCache() {
	if s.kegCache == nil {
		s.kegCache = map[string]keg.Keg{}
	}
}

// ReloadAuthStore drops the cached credential store so the next resolution
// reads it back from disk.
//
// The store is otherwise loaded once per process. A `tap auth login` run in a
// separate shell writes a new token to disk that a long-lived MCP server would
// never see, so telling an agent to log in and reorient was advice that could
// not work (tapper#87). Orientation is already the reload boundary for
// configuration; this puts credentials on the same boundary.
func (s *KegService) ReloadAuthStore() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.authStoreOnce = sync.Once{}
	s.authStore = nil
	s.authStorePath = ""
	s.authResolver = nil
	// Kegs resolved earlier captured the old resolver, so dropping the store
	// alone would leave them holding the stale credential.
	s.kegCache = nil
}

func (s *KegService) tokenResolver() keg.TokenResolver {
	s.authStoreOnce.Do(func() {
		defer func() {
			s.authResolver = NewAuthStoreTokenResolver(s.authStore, s.Runtime, s.authStorePath)
		}()
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
	return s.authResolver
}

// Resolve returns a RemoteKeg selected by an explicit reference or config.
func (s *KegService) Resolve(ctx context.Context, options ResolveKegOptions) (keg.Keg, error) {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	s.ensureCache()

	if options.RequireBootstrap && !s.ConfigService.UserConfigExists() {
		return nil, ErrNotBootstrapped
	}
	root := strings.TrimSpace(options.Root)
	if root == "" {
		var err error
		root, err = s.Runtime.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}
	selector := strings.TrimSpace(options.Keg)
	if selector == "" {
		cfg, err := s.ConfigService.Config()
		if err != nil {
			return nil, fmt.Errorf("failed to resolve workspace config: %w", err)
		}
		selector = cfg.DefaultKeg()
		if selector == "" {
			selector = cfg.LookupAlias(s.Runtime, root)
		}
		if selector == "" {
			selector = cfg.FallbackKeg()
		}
	}
	if selector == "" {
		return nil, fmt.Errorf("no KEG configured")
	}

	cacheKey := selector + "\x00" + options.Namespace + "\x00" + options.Hub
	if !options.NoCache && s.kegCache[cacheKey] != nil {
		return s.kegCache[cacheKey], nil
	}
	target, err := s.ConfigService.ResolveTarget(selector, options.Namespace, options.Hub)
	if err != nil {
		return nil, err
	}
	resolved, err := keg.NewKegFromTarget(ctx, *target, s.Runtime, keg.WithTokenResolver(s.tokenResolver()))
	if err != nil {
		return nil, err
	}
	if !options.NoCache {
		s.kegCache[cacheKey] = resolved
	}
	return resolved, nil
}
