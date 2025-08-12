package keg

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Keg is a concrete high-level service backed by a KegRepository.
// It implements common node operations by delegating low-level storage to the repo.
type Keg struct {
	Repo KegRepository
	// optional injected link resolver for ResolveKegLink
	linkResolver LinkResolver

	mu sync.Mutex
}

// NewKeg returns a Keg service backed by the provided repository.
func NewKeg(repo KegRepository, resolver LinkResolver) *Keg {
	return &Keg{Repo: repo, linkResolver: resolver}
}

// BuildIndexes runs the supplied IndexBuilder implementations, writes each
// resulting index into the repository via WriteIndex and returns a combined
// error (errors.Join) if any builder or write fails. Each builder's Name()
// value is used as the index name to write.
func (k *Keg) BuildIndexes(ctx context.Context, builders []IndexBuilder) error {
	var errs []error
	for _, b := range builders {
		name := b.Name()
		data, err := b.Build(ctx, k.Repo)
		if err != nil {
			errs = append(errs, fmt.Errorf("build %s: %w", name, err))
			continue
		}
		if err := k.Repo.WriteIndex(name, data); err != nil {
			errs = append(errs, fmt.Errorf("write index %s: %w", name, err))
			continue
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

// BuildIndex runs a single IndexBuilder and writes its output to the
// repository.
func (k *Keg) BuildIndex(ctx context.Context, b IndexBuilder) error {
	data, err := b.Build(ctx, k.Repo)
	if err != nil {
		return fmt.Errorf("build %s: %w", b.Name(), err)
	}
	if err := k.Repo.WriteIndex(b.Name(), data); err != nil {
		return fmt.Errorf("write index %s: %w", b.Name(), err)
	}
	return nil
}

// ResolveLink resolves a token like "repo", "keg:owner/123", or "keg:alias" to
// a concrete URL. If a LinkResolver was injected at construction time it will
// be used. Otherwise a minimal resolver based on the repository Config.Links
// is used.
//
// Notes:
//   - The minimal resolver looks up aliases from the Config.Links slice
//     (case-insensitive).
//   - For tokens of the form "keg:owner/<nodeid>" it will attempt to find an
//     alias whose alias matches the owner and, if found, return baseURL +
//     "/docs/<nodeid>". This is a heuristic fallback and callers that need
//     precise behavior should inject a LinkResolver that implements the
//     desired mapping rules.
func (k *Keg) ResolveLink(ctx context.Context, token string) (string, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", fmt.Errorf("empty token")
	}

	// If a custom resolver was provided, prefer that.
	if k.linkResolver != nil {
		cfg, err := k.Repo.ReadConfig()
		if err != nil {
			return "", fmt.Errorf("read config: %w", err)
		}
		return k.linkResolver.Resolve(cfg, token)
	}

	// Minimal built-in resolver using repo config links.
	cfg, err := k.Repo.ReadConfig()
	if err != nil {
		return "", fmt.Errorf("read config: %w", err)
	}

	aliasMap := make(map[string]string)
	for _, le := range cfg.Links {
		if le.Alias == "" {
			continue
		}
		aliasMap[strings.ToLower(strings.TrimSpace(le.Alias))] = strings.TrimSpace(le.URL)
	}

	// Accept tokens with optional "keg:" prefix.
	if strings.HasPrefix(token, "keg:") {
		token = strings.TrimPrefix(token, "keg:")
	}

	// If token contains owner/node (owner/<nodeid>), try owner lookup first.
	if strings.Contains(token, "/") {
		parts := strings.SplitN(token, "/", 2)
		owner := strings.ToLower(strings.TrimSpace(parts[0]))
		nodePart := strings.TrimSpace(parts[1])
		if base, ok := aliasMap[owner]; ok && base != "" {
			// Heuristic construction: append a docs path for node ids. This is a
			// conservative, generic mapping. Callers that need different semantics
			// should provide a LinkResolver.
			base = strings.TrimRight(base, "/")
			return base + "/docs/" + nodePart, nil
		}
		// no alias for owner; fallthrough to alias-only lookup
	}

	// Plain alias lookup
	l := strings.ToLower(token)
	if u, ok := aliasMap[l]; ok && u != "" {
		return u, nil
	}

	return "", fmt.Errorf("alias not found: %s", token)
}
