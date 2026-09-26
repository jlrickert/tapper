package relaycontract

import (
	"regexp"
	"strings"
)

// MaxCanonicalLength bounds a canonical model name.
const MaxCanonicalLength = 128

var (
	canonicalName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	// packagingToken matches a trailing name part that says how weights are
	// packaged or quantized rather than which model they are.
	packagingToken = regexp.MustCompile(`^(mlx|gguf|exl2|awq|gptq|dwq|i?q\d+(_[0-9a-z]+)*|\d+bit|(mx|nv)?fp\d+|bf16|f16|f32|int\d+)$`)
	nonNameChars   = regexp.MustCompile(`[^a-z0-9._]+`)
)

// CanonicalModel derives the provider-neutral name a model pools under from
// the provider's own model id, so the same weights share one name wherever
// they run: Ollama's "qwen3.6:35b-mlx" and LM Studio's "qwen/qwen3.6-35b"
// both become "qwen3.6-35b".
//
// It lowercases, drops any source prefix up to the last '/', drops Ollama's
// ":latest" tag, and strips trailing format and quantization parts ("mlx",
// "q4_k_m", "4bit", "fp16", ...). A name made only of those parts keeps them.
func CanonicalModel(id string) string {
	s := strings.ToLower(strings.TrimSpace(id))
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	s = strings.TrimSuffix(s, ":latest")
	s = nonNameChars.ReplaceAllString(s, "-")
	var parts []string
	for _, p := range strings.Split(s, "-") {
		if p != "" {
			parts = append(parts, p)
		}
	}
	whole := parts
	for len(parts) > 1 && packagingToken.MatchString(parts[len(parts)-1]) {
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		parts = whole
	}
	out := strings.Trim(strings.Join(parts, "-"), "._-")
	if len(out) > MaxCanonicalLength {
		out = strings.Trim(out[:MaxCanonicalLength], "._-")
	}
	return out
}

// ValidCanonical reports whether name is usable as a canonical model name:
// lowercase letters, digits, '.', '_' and '-', starting with a letter or
// digit, at most MaxCanonicalLength long.
func ValidCanonical(name string) bool {
	return len(name) <= MaxCanonicalLength && canonicalName.MatchString(name)
}
