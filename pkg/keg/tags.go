package keg

import (
	"sort"
	"strings"
	"unicode"
)

// NormalizeTags accepts a tag string in several common shapes and returns a
// normalized, deduplicated, sorted slice of tag tokens.
// Normalization rules:
//   - Trim whitespace around tokens.
//   - Lowercase all tokens.
//   - When the input contains explicit separators (commas/semicolons/newlines),
//     those are used to split tags. Within each separated token internal
//     whitespace is converted to hyphens (e.g. "My Tag" -> "my-tag").
//   - Otherwise (no commas/semicolons) the input is split on whitespace.
//   - Collapse runs of non-alphanumeric characters into a single '-' except
//     allow ':' and '-' to remain (for namespaces like "pkg:zeke").
//   - Strip leading/trailing '-' characters.
//   - Deduplicate tokens and return them in lexicographic order.
func NormalizeTags(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	// Decide splitting strategy:
	// - If the input contains commas/semicolons/newlines, treat those as primary
	//   separators and preserve internal whitespace inside each separated token
	//   (converted to hyphens).
	// - Otherwise split on whitespace.
	useExplicitSep := strings.ContainsAny(raw, ",;\n")

	var parts []string
	if useExplicitSep {
		// Split on commas/semicolons/newlines and treat each segment as a token
		parts = strings.FieldsFunc(raw, func(r rune) bool {
			return r == ',' || r == ';' || r == '\n' || r == '\r'
		})
	} else {
		// No explicit separators, split on whitespace
		parts = strings.Fields(raw)
	}

	seen := make(map[string]struct{}, len(parts))
	out := make([]string, 0, len(parts))

	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}

		// If we split by explicit separators, convert internal whitespace to hyphens
		if useExplicitSep {
			// replace runs of whitespace with single space so the normalization
			// below will convert spaces into hyphens consistently.
			p = strings.Join(strings.Fields(p), " ")
		}

		// Lowercase for canonical form
		p = strings.ToLower(p)

		// Build normalized token: allow letters, numbers, ':' and '-' as-is.
		// Treat other characters (including spaces when present) as hyphen separators,
		// but avoid emitting repeated hyphens.
		var b strings.Builder
		lastWasHyphen := false
		for _, r := range p {
			switch {
			case unicode.IsLetter(r) || unicode.IsNumber(r) || r == ':' || r == '-':
				b.WriteRune(r)
				lastWasHyphen = false
			case unicode.IsSpace(r) || r == '_' || r == '/' || r == '.' || r == '+' || r == ',' || r == ';':
				if !lastWasHyphen {
					b.WriteRune('-')
					lastWasHyphen = true
				}
			default:
				// For other punctuation, convert to hyphen as a safe default.
				if !lastWasHyphen {
					b.WriteRune('-')
					lastWasHyphen = true
				}
			}
		}

		token := strings.Trim(b.String(), "-")
		if token == "" {
			continue
		}

		if _, ok := seen[token]; ok {
			continue
		}
		seen[token] = struct{}{}
		out = append(out, token)
	}

	if len(out) == 0 {
		return nil
	}

	sort.Strings(out)
	return out
}
