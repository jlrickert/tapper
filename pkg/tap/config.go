package tap

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"gopkg.in/yaml.v3"
)

// Config represents the user's tapper configuration.
//
// We keep both a deserialized Go-friendly view (for quick access) and the
// original yaml.Node for in-place edits so comments and formatting are
// preserved when writing.
type Config struct {
	LogFile  string `yaml:"logFile,omitempty"`
	LogLevel string `yaml:"logLevel,omitempty"`

	// Updated is a timestamp
	Updated time.Time `yaml:"updated,omitempty"`

	// Default keg to use. value is an alias
	DefaultKeg string `yaml:"defaultKeg,omitempty"`

	// KegMap maps a context to the keg to use
	KegMap []KegMapEntry `yaml:"kegMap"`

	// kegs maps an alias to a keg
	Kegs map[string]kegurl.Target `yaml:"kegs"`

	DefaultRegistry string        `yaml:"defaultRegistry"`
	Registries      []KegRegistry `yaml:"registries,omitempty"`

	// node holds the original parsed YAML document root (document node). When
	// present we edit it directly to preserve comments and layout.
	node *yaml.Node
}

type KegMapEntry struct {
	Alias      string `yaml:"alias,omitempty"`
	PathPrefix string `yaml:"pathPrefix,omitempty"`
	PathRegex  string `yaml:"pathRegex,omitempty"`
}

type KegRegistry struct {
	Name     string `yaml:"name,omitempty"`
	Url      string `yaml:"url,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`
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
func (uc *Config) Clone(ctx context.Context) *Config {
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
func (uc *Config) ResolveAlias(ctx context.Context, alias string) (*kegurl.Target, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	u, ok := uc.Kegs[alias]
	if !ok {
		return nil, fmt.Errorf("keg alias not found: %s", alias)
	}
	if u.String() == "" {
		return nil, fmt.Errorf("keg alias %s has empty url", alias)
	}
	return kegurl.Parse(u.String())
}

// ResolveProjectKeg chooses the appropriate keg (via alias) based on path.
//
// Regex entries have precedence over pathPrefix. When multiple pathPrefix
// entries match, the longest prefix wins.
func (uc *Config) ResolveProjectKeg(ctx context.Context, path string) (*kegurl.Target, error) {
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
func ParseUserConfig(ctx context.Context, raw []byte) (*Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(raw), &doc); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	uc := &Config{node: &doc}
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

// ReadConfig reads the YAML file at path and returns a parsed UserConfig.
func ReadConfig(ctx context.Context, path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, fmt.Errorf("unable read user config: %w", keg.ErrNotExist)
		}
	}
	return ParseUserConfig(ctx, b)
}

func DefaultUserConfig(ctx context.Context) *Config {
	path, _ := std.UserDataPath(ctx)
	return &Config{
		DefaultRegistry: "knut",
		KegMap:          []KegMapEntry{},
		DefaultKeg:      "local",
		Kegs: map[string]kegurl.Target{
			"local": kegurl.NewFile(path),
		},
		Registries: []KegRegistry{
			{
				Name:     "knut",
				Url:      "keg.jlrickert.me",
				TokenEnv: "KNUT_API_KEY",
			},
		},
	}
}

func DefaultDataConfig(ctx context.Context) *Config {
	return &Config{
		DefaultRegistry: "knut",
		Registries: []KegRegistry{
			{
				Name:     "knut",
				Url:      "keg.jlrickert.me",
				TokenEnv: "KNUT_API_KEY",
			},
		},
	}
}

func (uc *Config) ToYAML(ctx context.Context) ([]byte, error) {
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
func (uc *Config) Write(ctx context.Context, path string) error {
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

// MergeConfig merges multiple UserConfig values into a single configuration.
//
// Merge semantics:
//   - Later configs in the argument list override earlier values for the same
//     keys (DefaultKeg and Kegs).
//   - KegMap entries are appended in order, but entries with the same Alias are
//     overridden by later entries (the later entry replaces the earlier one).
//   - The returned UserConfig will have a Kegs map and a KegMap slice.
//   - If any input carries a parsed yaml.Node, the node from the last non-nil
//     config is cloned and used to preserve comments when possible.
func MergeConfig(cfgs ...*Config) *Config {
	if len(cfgs) == 0 {
		return nil
	}

	out := &Config{
		Kegs:   make(map[string]kegurl.Target),
		KegMap: make([]KegMapEntry, 0),
	}

	// map to track alias -> index in out.KegMap so newer entries override older
	aliasIndex := make(map[string]int)

	var lastNode *yaml.Node

	for _, c := range cfgs {
		if c == nil {
			continue
		}
		// DefaultKeg: later wins when non-empty.
		if c.DefaultKeg != "" {
			out.DefaultKeg = c.DefaultKeg
		}

		// Merge Kegs: later entries override earlier entries for same alias.
		if c.Kegs != nil {
			if out.Kegs == nil {
				out.Kegs = make(map[string]kegurl.Target)
			}
			maps.Copy(out.Kegs, c.Kegs)
		}

		// Merge KegMap entries. Preserve order but override by alias when provided.
		for _, e := range c.KegMap {
			if e.Alias == "" {
				// no alias to dedupe by; always append
				out.KegMap = append(out.KegMap, e)
				continue
			}
			if idx, ok := aliasIndex[e.Alias]; ok {
				// replace existing entry at idx with the new one
				out.KegMap[idx] = e
			} else {
				aliasIndex[e.Alias] = len(out.KegMap)
				out.KegMap = append(out.KegMap, e)
			}
		}

		// remember last non-nil node so we can preserve comments if present
		if c.node != nil {
			lastNode = c.node
		}
	}

	// If we found a lastNode, clone it by using ParseUserConfig on its YAML
	// rendering so the returned config has a node suitable for ToYAML edits.
	if lastNode != nil {
		// Encode lastNode into bytes
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		_ = enc.Encode(lastNode)
		_ = enc.Close()

		if cloned, err := ParseUserConfig(context.Background(), buf.Bytes()); err == nil && cloned != nil {
			// Use the cloned node but keep the merged struct fields from out.
			out.node = cloned.node
		}
	}

	return out
}

func (uc *Config) Touch(ctx context.Context) {
	clock := std.ClockFromContext(ctx)
	uc.Updated = clock.Now()
}

// LocalGitData attempts to run `git -C repoRoot config --local --get key`.
// If git isn't present or the command fails it returns an error.
func LocalGitData(ctx context.Context, projectPath, key string) ([]byte, error) {
	lg := std.LoggerFromContext(ctx)
	// check git exists
	if _, err := exec.LookPath("git"); err != nil {
		lg.Warn("git executable not found", "projectPath", projectPath, "err", err)
		return []byte{}, fmt.Errorf("git not available: %w", err)
	}
	cmd := exec.CommandContext(ctx, "git", "-C", projectPath, "config", "--local", "--get", key)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		lg.Error("local git data not read", "projectPath", projectPath, "err", err)
		return []byte{}, fmt.Errorf("local git data not read: %w", err)
	}
	data := bytes.TrimSpace(out.Bytes())
	lg.Debug("git data read", "projectPath", projectPath, "data", data)
	return bytes.TrimSpace(out.Bytes()), nil
}
