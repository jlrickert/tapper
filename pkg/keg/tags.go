package keg

import "strings"

// NormalizeTags accepts a comma-separated tag string and returns a slice of
// trimmed tokens. Real implementation would perform normalization (lowercase,
// hyphenation, dedupe, sort). This helper keeps the behavior simple for stubs.
func NormalizeTags(raw string) []string {
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
