package keg

import (
	"fmt"
	"strings"
)

// LinkResolver is an optional helper interface that knows how to resolve keg
// link tokens into concrete URLs given a repo-level Config. It is split out so
// higher-level services can accept injected resolution strategies for testing.
type LinkResolver interface {
	// Resolve takes a token (e.g., "repo", "keg:alias/123") and returns a URL.
	Resolve(cfg Config, token string) (string, error)
}

// BasicLinkResolver is a simple implementation of LinkResolver that resolves
// tokens using the repository Config.Links slice. It supports:
//   - plain aliases (e.g. "repo")
//   - keg:alias/node forms (e.g. "keg:jlrickert/123") where it will try to map
//     alias -> configured alias URL and append "/docs/<node>" as a conservative
//     node path.
//
// A consumer can provide an optional Fallback function via NewBasicLinkResolver
// to handle alias/node resolution when no alias alias is present in cfg.Links.
type BasicLinkResolver struct {
	// Fallback is called when resolving "alias/node" and no owner alias is found.
	// If nil, alias resolution will fail with an error.
	Fallback func(alias, node string) (string, error)
}

// NewBasicLinkResolver constructs a BasicLinkResolver with an optional fallback.
func NewBasicLinkResolver(fallback func(alias, node string) (string, error)) *BasicLinkResolver {
	return &BasicLinkResolver{Fallback: fallback}
}

// Resolve implements LinkResolver.
//
// Behavior:
//   - trims the token and accepts optional "keg:" prefix
//   - looks up aliases from cfg.Links case-insensitively
//   - for "alias/node" forms, if alias maps to a base URL, returns base+"/"+node
//   - if alias not found and a fallback is provided, calls the fallback
//   - rejects resolved URLs that embed credentials (user:pass@host)
func (r *BasicLinkResolver) Resolve(cfg Config, token string) (string, error) {
	tok := strings.TrimSpace(token)
	if tok == "" {
		return "", fmt.Errorf("empty token")
	}
	if after, ok := strings.CutPrefix(tok, "keg:"); ok {
		tok = after
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return "", fmt.Errorf("empty token after keg: prefix")
		}
	}

	// build alias map (lowercase alias -> url)
	aliasMap := make(map[string]string, len(cfg.Links))
	for _, le := range cfg.Links {
		if le.Alias == "" || strings.TrimSpace(le.URL) == "" {
			continue
		}
		aliasMap[strings.ToLower(strings.TrimSpace(le.Alias))] = strings.TrimSpace(le.URL)
	}

	// alias/node form
	if strings.Contains(tok, "/") {
		parts := strings.SplitN(tok, "/", 2)
		alias := strings.ToLower(strings.TrimSpace(parts[0]))
		nodePart := strings.TrimSpace(parts[1])
		if nodePart == "" {
			return "", fmt.Errorf("invalid alias/node token: %q", token)
		}
		if base, ok := aliasMap[alias]; ok && base != "" {
			base = strings.TrimRight(base, "/")
			res := base + "/" + nodePart
			return res, nil
		}
		// try fallback if provided
		if r != nil && r.Fallback != nil {
			res, err := r.Fallback(alias, nodePart)
			if err != nil {
				return "", fmt.Errorf("fallback resolution failed: %w", err)
			}
			return res, nil
		}
		return "", fmt.Errorf("alias alias not found: %s", alias)
	}

	// plain alias lookup
	l := strings.ToLower(strings.TrimSpace(tok))
	if u, ok := aliasMap[l]; ok && u != "" {
		return u, nil
	}

	return "", fmt.Errorf("alias not found: %s", token)
}
