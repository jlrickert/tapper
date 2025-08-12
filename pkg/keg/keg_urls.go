package keg

import (
	"fmt"
	"net/url"
	"strings"
)

// LinkResolver is an optional helper interface that knows how to resolve keg
// link tokens into concrete URLs given a repo-level Config. It is split out so
// higher-level services can accept injected resolution strategies for testing.
type LinkResolver interface {
	// Resolve takes a token (e.g., "repo", "keg:owner/123") and returns a URL.
	Resolve(cfg Config, token string) (string, error)
}

// BasicLinkResolver is a simple implementation of LinkResolver that resolves
// tokens using the repository Config.Links slice. It supports:
//   - plain aliases (e.g. "repo")
//   - keg:owner/node forms (e.g. "keg:jlrickert/123") where it will try to map
//     owner -> configured alias URL and append "/docs/<node>" as a conservative
//     node path.
//
// A consumer can provide an optional Fallback function via NewBasicLinkResolver
// to handle owner/node resolution when no owner alias is present in cfg.Links.
type BasicLinkResolver struct {
	// Fallback is called when resolving "owner/node" and no owner alias is found.
	// If nil, owner resolution will fail with an error.
	Fallback func(owner, node string) (string, error)
}

// NewBasicLinkResolver constructs a BasicLinkResolver with an optional fallback.
func NewBasicLinkResolver(fallback func(owner, node string) (string, error)) *BasicLinkResolver {
	return &BasicLinkResolver{Fallback: fallback}
}

// Resolve implements LinkResolver.
//
// Behavior:
//   - trims the token and accepts optional "keg:" prefix
//   - looks up aliases from cfg.Links case-insensitively
//   - for "owner/node" forms, if owner maps to a base URL, returns base+"/docs/"+node
//   - if owner not found and a fallback is provided, calls the fallback
//   - rejects resolved URLs that embed credentials (user:pass@host)
func (r *BasicLinkResolver) Resolve(cfg Config, token string) (string, error) {
	tok := strings.TrimSpace(token)
	if tok == "" {
		return "", fmt.Errorf("empty token")
	}
	if strings.HasPrefix(tok, "keg:") {
		tok = strings.TrimPrefix(tok, "keg:")
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

	// owner/node form
	if strings.Contains(tok, "/") {
		parts := strings.SplitN(tok, "/", 2)
		owner := strings.ToLower(strings.TrimSpace(parts[0]))
		nodePart := strings.TrimSpace(parts[1])
		if nodePart == "" {
			return "", fmt.Errorf("invalid owner/node token: %q", token)
		}
		if base, ok := aliasMap[owner]; ok && base != "" {
			base = strings.TrimRight(base, "/")
			res := base + "/docs/" + nodePart
			if err := validateNoCredentials(res); err != nil {
				return "", err
			}
			return res, nil
		}
		// try fallback if provided
		if r != nil && r.Fallback != nil {
			res, err := r.Fallback(owner, nodePart)
			if err != nil {
				return "", fmt.Errorf("fallback resolution failed: %w", err)
			}
			if err := validateNoCredentials(res); err != nil {
				return "", err
			}
			return res, nil
		}
		return "", fmt.Errorf("owner alias not found: %s", owner)
	}

	// plain alias lookup
	l := strings.ToLower(strings.TrimSpace(tok))
	if u, ok := aliasMap[l]; ok && u != "" {
		if err := validateNoCredentials(u); err != nil {
			return "", err
		}
		return u, nil
	}

	return "", fmt.Errorf("alias not found: %s", token)
}

// validateNoCredentials parses the URL and rejects URLs that embed user
// credentials (e.g., "https://user:pass@host"). Returns an error if the URL is
// invalid or contains credentials.
func validateNoCredentials(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		// If parsing fails, be conservative: return parse error rather than a
		// resolved-but-invalid URL.
		return fmt.Errorf("invalid resolved URL %q: %w", raw, err)
	}
	if u.User != nil {
		return fmt.Errorf("resolved URL contains embedded credentials: %q", raw)
	}
	return nil
}
