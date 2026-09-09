package keg

import (
	"fmt"
	"regexp"
	"strings"
)

var relationshipRefRE = regexp.MustCompile(`^@[a-z0-9][a-z0-9-]{0,63}/[a-z0-9][a-z0-9-]{0,63}$`)

// RelationshipTarget returns the canonical same-Hub KEG reference of a
// settings relationship. Ordinary external URLs are metadata only.
func RelationshipTarget(raw string) (string, bool, error) {
	ref := strings.TrimPrefix(raw, "keg:")
	if !strings.HasPrefix(raw, "keg:") && !strings.HasPrefix(raw, "@") {
		return "", false, nil
	}
	if !relationshipRefRE.MatchString(ref) {
		return "", false, fmt.Errorf("invalid relationship target %q: use keg:@namespace/keg", raw)
	}
	return ref, true, nil
}

// ValidateRelationships requires unique aliases and canonical KEG targets.
func ValidateRelationships(links []LinkEntry) error {
	seen := map[string]bool{}
	for _, link := range links {
		if link.Alias != "" {
			if !refSegmentPattern.MatchString(link.Alias) {
				return fmt.Errorf("invalid settings alias %q", link.Alias)
			}
			if seen[link.Alias] {
				return fmt.Errorf("duplicate settings alias %q", link.Alias)
			}
			seen[link.Alias] = true
		}
		if _, _, err := RelationshipTarget(link.URL); err != nil {
			return err
		}
	}
	return nil
}

// ResolveRelationshipAlias resolves the explicit keg:~alias/node form using
// only the source KEG's settings, preserving canonical keg:@namespace/keg/node.
func ResolveRelationshipAlias(links []LinkEntry, alias string) (namespace, name string, err error) {
	if err = ValidateRelationships(links); err != nil {
		return
	}
	for _, link := range links {
		if link.Alias != alias {
			continue
		}
		ref, ok, e := RelationshipTarget(link.URL)
		if e != nil {
			return "", "", e
		}
		if !ok {
			return "", "", fmt.Errorf("settings alias %q does not name a same-Hub KEG", alias)
		}
		namespace, name, _ = strings.Cut(strings.TrimPrefix(ref, "@"), "/")
		return
	}
	return "", "", fmt.Errorf("settings alias %q: %w", alias, ErrNotExist)
}
