package keg

import (
	"sort"
	"strings"
	"unicode"
)

// NormalizeTag normalizeTag lowercases, trims, and tokenizes a tag string into a hyphen-separated token.
func NormalizeTag(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if unicode.IsSpace(r) || r == ',' {
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
			continue
		}
		// allow a-z, 0-9, hyphen, underscore
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			prevHyphen = (r == '-')
		} else {
			// replace other runes with hyphen (single)
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func NormalizeTags(tags []string) []string {
	if len(tags) == 0 {
		return []string{}
	}

	set := make(map[string]struct{}, len(tags))
	for _, raw := range tags {
		if raw == "" {
			continue
		}

		var parts []string
		if strings.ContainsAny(raw, ",;\n\r") {
			parts = strings.FieldsFunc(raw, func(r rune) bool {
				return r == ',' || r == ';' || r == '\n' || r == '\r'
			})
		} else {
			parts = []string{raw}
		}

		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n := NormalizeTag(p)
			if n == "" {
				continue
			}
			set[n] = struct{}{}
		}
	}

	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	return out
}

// ParseTags accepts a comma/semicolon/newline separated list of tags (or a
// whitespace-separated string when no explicit separators are present) and
// returns a normalized, deduplicated, sorted slice of tags.
//
// Behavior:
// - Trims whitespace around tokens.
// - Lowercases tokens and converts internal whitespace to hyphens via NormalizeTag.
// - Splits on commas, semicolons, CR/LF, or newlines when present; otherwise splits on whitespace.
// - Deduplicates tokens and returns them in lexicographic order.
func ParseTags(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return []string{}
	}

	var parts []string
	if strings.ContainsAny(raw, ",;\n\r") {
		parts = strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		})
	} else {
		parts = strings.Fields(raw)
	}

	set := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		t := NormalizeTag(p)
		if t == "" {
			continue
		}
		set[t] = struct{}{}
	}

	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}
