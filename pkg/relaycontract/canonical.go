package relaycontract

import (
	"regexp"
	"strconv"
	"strings"
)

// MaxCanonicalLength bounds a canonical model name.
const MaxCanonicalLength = 128

var (
	canonicalName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
	// formatToken matches a trailing name part that says how weights are
	// packaged rather than which model they are.
	formatToken = regexp.MustCompile(`^(mlx|gguf|exl2|awq|gptq|dwq)$`)
	// precisionToken matches a trailing name part that says how weights are
	// quantized; its captured digits are the bits per weight.
	precisionToken = regexp.MustCompile(`^(?:i?q(\d+)(?:_[0-9a-z]+)*|(\d+)bit|(?:mx|nv)?fp(\d+)|b?f(16|32)|int(\d+))$`)
	nonNameChars   = regexp.MustCompile(`[^a-z0-9._]+`)
)

// CanonicalModel derives the provider-neutral name a model pools under from
// the provider's own model id, so the same weights at the same precision share
// one name wherever they run: Ollama's "qwen3.6:35b-mlx" and LM Studio's
// "qwen/qwen3.6-35b" both become "qwen3.6-35b", and "qwen3.6:35b-a3b-mxfp8"
// and "Qwen3.6-35B-A3B-8bit" both become "qwen3.6-35b-a3b-8bit".
//
// It lowercases, drops any source prefix up to the last '/', drops Ollama's
// ":latest" tag, strips trailing format parts ("mlx", "gguf", ...), and
// rewrites a trailing quantization part ("q4_k_m", "nvfp4", "bf16", ...) as
// its bits per weight ("4bit", "16bit"), so different quantizations of one
// model do not pool together. A name made only of those parts keeps them.
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
	precision := ""
	for len(parts) > 1 {
		last := parts[len(parts)-1]
		if bits, ok := precisionBits(last); ok {
			if precision == "" {
				precision = bits
			}
		} else if !formatToken.MatchString(last) {
			break
		}
		parts = parts[:len(parts)-1]
	}
	if len(parts) == 0 {
		parts = whole
	}
	out := strings.Trim(strings.Join(parts, "-"), "._-")
	suffix := ""
	if precision != "" && out != "" {
		suffix = "-" + precision
	}
	if len(out)+len(suffix) > MaxCanonicalLength {
		out = strings.Trim(out[:MaxCanonicalLength-len(suffix)], "._-")
	}
	return out + suffix
}

// precisionBits reports the "<n>bit" precision a quantization name part
// stands for.
func precisionBits(part string) (string, bool) {
	m := precisionToken.FindStringSubmatch(part)
	if m == nil {
		return "", false
	}
	for _, digits := range m[1:] {
		if n, err := strconv.Atoi(digits); err == nil && n > 0 {
			return strconv.Itoa(n) + "bit", true
		}
	}
	return "", false
}

// ValidCanonical reports whether name is usable as a canonical model name:
// lowercase letters, digits, '.', '_' and '-', starting with a letter or
// digit, at most MaxCanonicalLength long.
func ValidCanonical(name string) bool {
	return len(name) <= MaxCanonicalLength && canonicalName.MatchString(name)
}
