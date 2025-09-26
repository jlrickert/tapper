package tap

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/keg"
	"gopkg.in/yaml.v3"
)

// UserConfig represents the user's tapper configuration.
//
// We keep both a deserialized Go-friendly view (for quick access) and the
// original yaml.Node for in-place edits so comments and formatting are
// preserved when writing.
type UserConfig struct {
	DefaultKeg string            `yaml:"defaultKeg,omitempty"`
	KegMap     []KegMapEntry     `yaml:"kegMap"`
	Kegs       map[string]KegUrl `yaml:"kegs"`

	// node holds the original parsed YAML document root (document node). When
	// present we edit it directly to preserve comments and layout.
	node *yaml.Node
}

type KegMapEntry struct {
	Alias      string `yaml:"alias,omitempty"`
	PathPrefix string `yaml:"pathPrefix,omitempty"`
	PathRegex  string `yaml:"pathRegex,omitempty"`
}

// KegUrl represents a keg reference.
//
// YAML may supply either a plain scalar (URL string) or a mapping with fields.
// UnmarshalYAML and MarshalYAML support both forms so the field is flexible.
type KegUrl struct {
	URL      string `yaml:"url,omitempty"`
	Readonly bool   `yaml:"readonly,omitempty"`
	User     string `yaml:"user,omitempty"`
	Password string `yaml:"password,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`
}

// UnmarshalYAML accepts either a scalar string (the URL) or a mapping node that
// decodes into the full KegUrl struct.
func (k *KegUrl) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.ScalarNode:
		var s string
		if err := node.Decode(&s); err != nil {
			return fmt.Errorf("decode keg url scalar: %w", err)
		}
		k.URL = s
		k.Readonly = false
		k.User = ""
		k.Password = ""
		k.Token = ""
		k.TokenEnv = ""
		return nil
	case yaml.MappingNode:
		// decode into a temporary alias to avoid recursion issues
		type tmp KegUrl
		var t tmp
		if err := node.Decode(&t); err != nil {
			return fmt.Errorf("decode keg url mapping: %w", err)
		}
		*k = KegUrl(t)
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for KegUrl", node.Kind)
	}
}

// MarshalYAML emits a scalar string when only the URL is set and all other
// fields are zero values. Otherwise it emits a mapping with fields.
func (k KegUrl) MarshalYAML() (any, error) {
	onlyURL := k.URL != "" &&
		!k.Readonly &&
		k.User == "" &&
		k.Password == "" &&
		k.Token == "" &&
		k.TokenEnv == ""
	if onlyURL {
		return k.URL, nil
	}
	// return struct mapping
	return struct {
		URL      string `yaml:"url,omitempty"`
		Readonly bool   `yaml:"readonly,omitempty"`
		User     string `yaml:"user,omitempty"`
		Password string `yaml:"password,omitempty"`
		Token    string `yaml:"token,omitempty"`
		TokenEnv string `yaml:"tokenEnv,omitempty"`
	}{
		URL:      k.URL,
		Readonly: k.Readonly,
		User:     k.User,
		Password: k.Password,
		Token:    k.Token,
		TokenEnv: k.TokenEnv,
	}, nil
}

// ResolvePaths expands environment variables and tildes in basePath and
// reports an error only when expansion fails.
//
// This helper normalizes input used elsewhere for path matching.
func ResolvePaths(ctx context.Context, basePath string) error {
	// Use std helpers to ensure consistent behavior with other code paths.
	_, err := std.ExpandPath(ctx, basePath)
	if err != nil {
		return fmt.Errorf("expand path: %w", err)
	}
	// No stateful changes; caller can use the expanded path if needed.
	return nil
}

// Clone produces a deep copy of the UserConfig including the underlying yaml
// node so callers can safely mutate the clone without affecting the original.
func (uc *UserConfig) Clone(ctx context.Context) *UserConfig {
	if uc == nil {
		return nil
	}
	// Marshal the existing node (preserves comments) and parse into a new one.
	var buf bytes.Buffer
	if uc.node != nil {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		// encode the whole document node
		_ = enc.Encode(uc.node)
		_ = enc.Close()
	} else {
		// Fall back to struct marshal (loses comments but still clones)
		b, _ := yaml.Marshal(uc)
		buf.Write(b)
	}
	nuc, _ := ParseUserConfig(ctx, buf.Bytes())
	if nuc == nil {
		// As a final fallback, do a shallow copy
		out := *uc
		return &out
	}
	return nuc
}

// ResolveAlias looks up the keg by alias and returns a parsed KegTarget.
//
// Returns nil + error when not found or parse fails.
func (uc *UserConfig) ResolveAlias(ctx context.Context, alias string) (*keg.KegTarget, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	u, ok := uc.Kegs[alias]
	if !ok {
		return nil, fmt.Errorf("keg alias not found: %s", alias)
	}
	if u.URL == "" {
		return nil, fmt.Errorf("keg alias %s has empty url", alias)
	}
	return keg.ParseKegTarget(ctx, u.URL)
}

// ResolveKegMap chooses the appropriate keg (via alias) based on path.
//
// Regex entries have precedence over pathPrefix. When multiple pathPrefix
// entries match, the longest prefix wins.
func (uc *UserConfig) ResolveKegMap(ctx context.Context, path string) (*keg.KegTarget, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	// Expand path and make absolute/clean to compare reliably.
	val := std.ExpandEnv(ctx, path)
	abs, err := std.ExpandPath(ctx, val)
	if err != nil {
		// still try with expanded env
		abs = val
	}
	abs = filepath.Clean(abs)

	// First check regex entries (highest precedence).
	for _, m := range uc.KegMap {
		if m.PathRegex == "" {
			continue
		}
		pattern := std.ExpandEnv(ctx, m.PathRegex)
		pattern, _ = std.ExpandPath(ctx, pattern)
		ok, _ := regexp.MatchString(pattern, abs)
		if ok {
			return uc.ResolveAlias(ctx, m.Alias)
		}
	}

	// Collect prefix matches and choose the longest matching prefix.
	type match struct {
		entry KegMapEntry
		len   int
	}
	var matches []match
	for _, m := range uc.KegMap {
		if m.PathPrefix == "" {
			continue
		}
		pref := std.ExpandEnv(ctx, m.PathPrefix)
		pref, _ = std.ExpandPath(ctx, pref)
		pref = filepath.Clean(pref)
		if strings.HasPrefix(abs, pref) {
			matches = append(matches, match{entry: m, len: len(pref)})
		}
	}
	if len(matches) > 0 {
		// choose longest prefix
		sort.Slice(matches, func(i, j int) bool { return matches[i].len > matches[j].len })
		return uc.ResolveAlias(ctx, matches[0].entry.Alias)
	}

	// fallback to defaultKeg if set
	if uc.DefaultKeg != "" {
		return uc.ResolveAlias(ctx, uc.DefaultKeg)
	}

	return nil, fmt.Errorf("no keg map entry matched path: %s", path)
}

// ParseUserConfig parses raw YAML into a UserConfig while preserving the
// underlying yaml.Node for comment-preserving edits.
func ParseUserConfig(ctx context.Context, raw []byte) (*UserConfig, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	uc := &UserConfig{node: &doc}
	// doc.Content[0] should be the document's root mapping node if present.
	if len(doc.Content) > 0 {
		if err := doc.Content[0].Decode(uc); err != nil {
			// Try decoding the whole doc as a fallback.
			if err2 := doc.Decode(uc); err2 != nil {
				return nil, fmt.Errorf("failed to decode config into struct: %w", err)
			}
		}
	} else {
		// empty doc -> zero value config
		uc.KegMap = nil
		uc.Kegs = nil
	}
	return uc, nil
}

// ReadUserConfig reads the YAML file at path and returns a parsed UserConfig.
func ReadUserConfig(ctx context.Context, path string) (*UserConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read user config: %w", err)
	}
	return ParseUserConfig(ctx, b)
}

func (uc *UserConfig) ToYAML(ctx context.Context) ([]byte, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	// Prefer writing the original node (keeps comments). If absent, write struct.
	if uc.node != nil {
		// Ensure we encode the document node as-is.
		if err := enc.Encode(uc.node); err != nil {
			_ = enc.Close()
			return nil, fmt.Errorf("encode yaml node: %w", err)
		}
	} else {
		if err := enc.Encode(uc); err != nil {
			_ = enc.Close()
			return nil, fmt.Errorf("encode yaml struct: %w", err)
		}
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("close encoder: %w", err)
	}
	return buf.Bytes(), nil
}

// WriteUserConfig writes the UserConfig back to path, preserving comments and
// formatting when possible. Uses AtomicWriteFile from std.
func (uc *UserConfig) WriteUserConfig(ctx context.Context, path string) error {
	data, err := uc.ToYAML(ctx)
	if err != nil {
		return fmt.Errorf("unable to write user config: %w", err)
	}

	// Ensure parent directory exists.
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdirall %s: %w", dir, err)
	}

	if err := std.AtomicWriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("atomic write: %w", err)
	}
	return nil
}
