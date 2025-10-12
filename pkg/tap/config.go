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

// Package tap provides helpers for the tapper CLI related to user and
// project configuration, keg resolution, and small utilities used by commands.
//
// The comments in this file document the behavior expected by the CLI. The
// task design for "tap init" is reflected in the semantics described for
// default registries and path handling.

// Config represents the user's tapper configuration.
//
// We keep both a deserialized, Go friendly view for quick access and the
// original yaml.Node for in-place edits so comments and formatting are
// preserved when writing.
type Config struct {
	LogFile  string `yaml:"logFile,omitempty"`
	LogLevel string `yaml:"logLevel,omitempty"`

	// Updated is a timestamp.
	Updated time.Time `yaml:"updated,omitempty"`

	// DefaultKeg is the alias of the default keg to use.
	DefaultKeg string `yaml:"defaultKeg,omitempty"`

	// KegMap maps a project path or pattern to a keg alias.
	KegMap []KegMapEntry `yaml:"kegMap"`

	// Kegs maps an alias to a keg Target.
	Kegs map[string]kegurl.Target `yaml:"kegs"`

	// DefaultRegistry is the named registry used by default when creating
	// API style kegs. The CLI default value is "knut".
	DefaultRegistry string `yaml:"defaultRegistry"`

	// Registries describes configured registries available to the user.
	Registries []KegRegistry `yaml:"registries,omitempty"`

	// node holds the original parsed YAML document root (document node).
	// When present we edit it directly to preserve comments and layout.
	node *yaml.Node
}

// KegMapEntry is an entry mapping a path prefix or regex to a keg alias.
type KegMapEntry struct {
	Alias      string `yaml:"alias,omitempty"`
	PathPrefix string `yaml:"pathPrefix,omitempty"`
	PathRegex  string `yaml:"pathRegex,omitempty"`
}

// KegRegistry describes a named registry configuration entry.
type KegRegistry struct {
	Name     string `yaml:"name,omitempty"`
	Url      string `yaml:"url,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`
}

// ResolvePaths expands environment variables and tildes in basePath and
// reports an error only when expansion fails.
//
// This helper normalizes input used elsewhere for path matching. It calls
// std.ExpandPath which expands a leading tilde and environment variables. The
// function does not modify any state.
func ResolvePaths(ctx context.Context, basePath string) error {
	// Use std helpers to ensure consistent behavior with other code paths.
	_, err := std.ExpandPath(ctx, basePath)
	if err != nil {
		return fmt.Errorf("expand path: %w", err)
	}
	// No stateful changes; caller can use the expanded path if needed.
	return nil
}

// Clone produces a deep copy of the Config including the underlying yaml
// node so callers can safely mutate the clone without affecting the original.
//
// When possible the clone preserves the original document node so comment
// preserving edits remain possible on the returned value.
func (uc *Config) Clone(ctx context.Context) *Config {
	if uc == nil {
		return nil
	}
	// Marshal the existing node (preserves comments) and parse into a new one.
	var buf bytes.Buffer
	if uc.node != nil {
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		// Encode the whole document node.
		_ = enc.Encode(uc.node)
		_ = enc.Close()
	} else {
		// Fall back to struct marshal (loses comments but still clones).
		b, _ := yaml.Marshal(uc)
		buf.Write(b)
	}
	nuc, _ := ParseUserConfig(ctx, buf.Bytes())
	if nuc == nil {
		// As a final fallback, do a shallow copy.
		out := *uc
		return &out
	}
	return nuc
}

// ResolveAlias looks up the keg by alias and returns a parsed Target.
//
// Returns (nil, error) when not found or parse fails. The function checks that
// the stored target has a non-empty string form before parsing.
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
// Precedence rules:
//  1. Regex entries in KegMap have the highest precedence.
//  2. PathPrefix entries are considered next; when multiple prefixes match the
//     longest prefix wins.
//  3. If no entry matches, the DefaultKeg is used if set.
//
// The function expands env vars and tildes prior to comparisons so stored
// prefixes and patterns may contain ~ or $VAR values.
func (uc *Config) ResolveProjectKeg(ctx context.Context, path string) (*kegurl.Target, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	// Expand path and make absolute/clean to compare reliably.
	val := std.ExpandEnv(ctx, path)
	abs, err := std.ExpandPath(ctx, val)
	if err != nil {
		// Still try with expanded env when ExpandPath fails.
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
		// Choose longest prefix.
		sort.Slice(matches, func(i, j int) bool { return matches[i].len > matches[j].len })
		return uc.ResolveAlias(ctx, matches[0].entry.Alias)
	}

	// Fallback to DefaultKeg if set.
	if uc.DefaultKeg != "" {
		return uc.ResolveAlias(ctx, uc.DefaultKeg)
	}

	return nil, fmt.Errorf("no keg map entry matched path: %s", path)
}

// ParseUserConfig parses raw YAML into a Config while preserving the
// underlying yaml.Node for comment-preserving edits.
//
// The function attempts to decode the document root mapping into the struct.
// If the document root is missing the returned Config is a zero value with
// KegMap and Kegs set to nil.
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
		// Empty doc -> zero value config.
		uc.KegMap = nil
		uc.Kegs = nil
	}
	return uc, nil
}

// ReadConfig reads the YAML file at path and returns a parsed Config.
//
// When the file does not exist the function returns a Config value and an
// error that wraps keg.ErrNotExist so callers can detect no-config cases.
func ReadConfig(ctx context.Context, path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Config{}, fmt.Errorf("unable read user config: %w", keg.ErrNotExist)
		}
		return nil, err
	}
	return ParseUserConfig(ctx, b)
}

// DefaultUserConfig returns a sensible default Config for a new user.
//
// The returned Config is a fully populated in-memory config suitable as a
// starting point when no on-disk config is available. The DefaultRegistry is
// set to "knut" and a local file based keg pointing at the user data path is
// provided under the alias "local".
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

// ToYAML serializes the Config to YAML bytes.
//
// When possible the original parsed document node is emitted to preserve
// comments and formatting; otherwise the struct form is encoded.
func (uc *Config) ToYAML(ctx context.Context) ([]byte, error) {
	if uc == nil {
		return nil, fmt.Errorf("no user config")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	// Prefer writing the original node to keep comments. If absent, write struct.
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

// Write writes the Config back to path, preserving comments and formatting
// when possible. Uses AtomicWriteFile from std.
func (uc *Config) Write(ctx context.Context, path string) error {
	data, err := uc.ToYAML(ctx)
	if err != nil {
		return fmt.Errorf("unable to write user config: %w", err)
	}

	if err := std.AtomicWriteFile(ctx, path, data, 0o644); err != nil {
		return fmt.Errorf("unable to write config: %w", err)
	}
	return nil
}

// MergeConfig merges multiple Config values into a single configuration.
//
// Merge semantics:
//   - Later configs override earlier values for the same keys.
//   - KegMap entries are appended in order, but entries with the same Alias
//     are overridden by later entries.
//   - The returned Config will have a Kegs map and a KegMap slice.
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
				// No alias to dedupe by; always append.
				out.KegMap = append(out.KegMap, e)
				continue
			}
			if idx, ok := aliasIndex[e.Alias]; ok {
				// Replace existing entry at idx with the new one.
				out.KegMap[idx] = e
			} else {
				aliasIndex[e.Alias] = len(out.KegMap)
				out.KegMap = append(out.KegMap, e)
			}
		}

		// Remember last non-nil node so we can preserve comments if present.
		if c.node != nil {
			lastNode = c.node
		}
	}

	// If we found a lastNode, clone it by using ParseUserConfig on its YAML
	// rendering so the returned config has a node suitable for ToYAML edits.
	if lastNode != nil {
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

// Touch updates the Updated timestamp on the Config using the context clock.
func (uc *Config) Touch(ctx context.Context) {
	clock := std.ClockFromContext(ctx)
	uc.Updated = clock.Now()
}

// LocalGitData attempts to run `git -C projectPath config --local --get key`.
//
// If git is not present or the command fails it returns an error. The returned
// bytes are trimmed of surrounding whitespace. The function logs diagnostic
// messages using the logger present in ctx.
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
	return data, nil
}
