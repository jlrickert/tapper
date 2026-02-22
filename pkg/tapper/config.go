package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
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

type configDTO struct {
	LogFile  string `yaml:"logFile,omitempty"`
	LogLevel string `yaml:"logLevel,omitempty"`

	// updated is a timestamp.
	Updated time.Time `yaml:"updated,omitempty"`

	// defaultKeg is the alias of the default keg to use.
	DefaultKeg string `yaml:"defaultKeg,omitempty"`

	// kegMap maps a project path or pattern to a keg alias.
	KegMap []KegMapEntry `yaml:"kegMap"`

	// kegs maps an alias to a keg Target.
	Kegs map[string]kegurl.Target `yaml:"kegs"`

	// defaultRegistry is the named registry used by default when creating
	// API style kegs. The CLI default value is "knut".
	DefaultRegistry string `yaml:"defaultRegistry"`

	// userRepoPath is the path to discover KEGs on local file system.
	UserRepoPath string `yaml:"userRepoPath"`

	// registries describes configured registries available to the user.
	Registries []KegRegistry `yaml:"registries,omitempty"`
}

// Config represents the user's tapper configuration.
//
// Config is a data-only model. We do not preserve YAML comments or original
// document formatting.
type Config struct {
	// parsed data.
	data *configDTO
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

// --- Getter Methods ---

// DefaultKeg returns the alias of the default keg to use.
func (cfg *Config) DefaultKeg() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DefaultKeg
}

// UserRepoPath returns the path to discover KEGs on the local file system.
func (cfg *Config) UserRepoPath() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.UserRepoPath
}

// Kegs returns a map of keg aliases to their targets.
func (cfg *Config) Kegs() map[string]kegurl.Target {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Kegs == nil {

		return map[string]kegurl.Target{}
	}
	return cfg.data.Kegs
}

// DefaultRegistry returns the default registry name.
func (cfg *Config) DefaultRegistry() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DefaultRegistry
}

// KegMap returns the list of path/regex to keg alias mappings.
func (cfg *Config) KegMap() []KegMapEntry {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.KegMap == nil {
		return []KegMapEntry{}
	}
	return cfg.data.KegMap
}

// Registries return the list of configured registries.
func (cfg *Config) Registries() []KegRegistry {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Registries == nil {
		return []KegRegistry{}
	}
	return cfg.data.Registries
}

// LogFile returns the log file path.
func (cfg *Config) LogFile() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.LogFile
}

// LogLevel returns the log level.
func (cfg *Config) LogLevel() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.LogLevel
}

// Updated returns the last update timestamp.
func (cfg *Config) Updated() time.Time {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.Updated
}

// --- Setter Methods ---

// SetDefaultKeg sets the default keg alias.
func (cfg *Config) SetDefaultKeg(keg string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultKeg = keg
	return nil
}

// SetUserRepoPath sets the user repository path.
func (cfg *Config) SetUserRepoPath(path string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.UserRepoPath = path
	return nil
}

// SetDefaultRegistry sets the default registry.
func (cfg *Config) SetDefaultRegistry(_ context.Context, registry string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultRegistry = registry
	return nil
}

// SetLogFile sets the log file path.
func (cfg *Config) SetLogFile(_ context.Context, path string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.LogFile = path
	return nil
}

// SetLogLevel sets the log level.
func (cfg *Config) SetLogLevel(level string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.LogLevel = level
	return nil
}

// Clone produces a deep copy of the Config.
func (cfg *Config) Clone() *Config {
	if cfg == nil {
		return nil
	}
	data, err := cfg.ToYAML()
	if err != nil {
		return nil
	}
	uCfg, err := ParseConfig(data)
	if err != nil {
		return nil
	}
	return uCfg
}

// ResolveAlias looks up the keg by alias and returns a parsed Target.
//
// Returns (nil, error) when not found or parse fails.
func (cfg *Config) ResolveAlias(alias string) (*kegurl.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Kegs == nil {
		cfg.data.Kegs = map[string]kegurl.Target{}
	}
	u, ok := cfg.data.Kegs[alias]
	if !ok {
		return nil, fmt.Errorf("keg alias not found: %s", alias)
	}
	return kegurl.Parse(u.String())
}

// LookupAlias returns the keg alias matching the given project root path.
// It first checks regex patterns in KegMap entries, then prefix matches.
// For multiple prefix matches, the longest matching prefix wins.
// Returns empty string if no match is found or config data is nil.
func (cfg *Config) LookupAlias(rt *toolkit.Runtime, projectRoot string) string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
		return ""
	}
	// Expand path and make absolute/clean to compare reliably.
	val := toolkit.ExpandEnv(rt, projectRoot)
	abs, err := toolkit.ExpandPath(rt, val)
	if err != nil {
		// Still try with expanded env when ExpandPath fails.
		abs = val
	}
	abs = filepath.Clean(abs)

	// First check regex entries (highest precedence).
	for _, m := range cfg.data.KegMap {
		if m.PathRegex == "" {
			continue
		}
		pattern := toolkit.ExpandEnv(rt, m.PathRegex)
		pattern, _ = toolkit.ExpandPath(rt, pattern)
		ok, _ := regexp.MatchString(pattern, abs)
		if ok {
			return m.Alias
		}
	}

	// Collect prefix matches and choose the longest matching prefix.
	type match struct {
		entry KegMapEntry
		len   int
	}
	var matches []match
	for _, m := range cfg.data.KegMap {
		if m.PathPrefix == "" {
			continue
		}
		pref := toolkit.ExpandEnv(rt, m.PathPrefix)
		pref, _ = toolkit.ExpandPath(rt, pref)
		pref = filepath.Clean(pref)
		if strings.HasPrefix(abs, pref) {
			matches = append(matches, match{entry: m, len: len(pref)})
		}
	}

	if len(matches) > 0 {
		// Choose longest prefix.
		sort.Slice(matches, func(i, j int) bool { return matches[i].len > matches[j].len })
		return matches[0].entry.Alias
	}

	return ""
}

// ResolveKegMap chooses the appropriate keg (via alias) based on path.
//
// Precedence rules:
//  1. Regex entries in KegMap have the highest precedence.
//  2. PathPrefix entries are considered next; when multiple prefixes match the
//     longest prefix wins.
//  3. If no entry matches, the DefaultKeg is used if set.
//
// The function expands env vars and tildes prior to comparisons, so stored
// prefixes and patterns may contain ~ or $VAR values.
func (cfg *Config) ResolveKegMap(rt *toolkit.Runtime, projectRoot string) (*kegurl.Target, error) {
	alias := cfg.LookupAlias(rt, projectRoot)
	return cfg.ResolveAlias(alias)
}

func (cfg *Config) ResolveDefault(rt *toolkit.Runtime) (*kegurl.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
		return nil, nil
	}
	alias := toolkit.ExpandEnv(rt, cfg.DefaultKeg())
	return cfg.ResolveAlias(alias)

}

// ParseConfig parses raw YAML into a Config data model.
func ParseConfig(raw []byte) (*Config, error) {
	uc := &Config{data: &configDTO{}}
	if err := yaml.Unmarshal(raw, uc.data); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	return uc, nil
}

// ReadConfig reads the YAML file at path and returns a parsed Config.
//
// When the file does not exist the function returns a Config value and an
// error that wraps keg.ErrNotExist so callers can detect no-config cases.
func ReadConfig(rt *toolkit.Runtime, path string) (*Config, error) {
	b, err := rt.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("unable read user config: %w", keg.ErrNotExist)
		}
		return nil, err
	}
	return ParseConfig(b)
}

// DefaultUserConfig returns a sensible default Config for a new user.
//
// The returned Config is a fully populated in-memory config suitable as a
// starting point when no on-disk config is available. The DefaultRegistry is
// set to "knut" and a local file based keg pointing at the user data path is
// provided under the alias "local".
func DefaultUserConfig(name string, userRepos string) *Config {
	return &Config{
		data: &configDTO{
			DefaultRegistry: "knut",
			KegMap:          []KegMapEntry{},
			DefaultKeg:      name,
			Kegs: map[string]kegurl.Target{
				name: kegurl.NewFile(filepath.Join(userRepos, name)),
			},
			UserRepoPath: filepath.Join(userRepos),
			Registries: []KegRegistry{
				{
					Name:     "knut",
					Url:      "keg.jlrickert.me",
					TokenEnv: "KNUT_API_KEY",
				},
			},
		},
	}
}

func DefaultProjectConfig(user, userKegRepo string) *Config {
	return &Config{
		data: &configDTO{
			DefaultRegistry: "knut",
			KegMap:          []KegMapEntry{},
			DefaultKeg:      "local",
			Kegs: map[string]kegurl.Target{
				user: kegurl.NewFile(filepath.Join(userKegRepo, "docs")),
			},
			UserRepoPath: filepath.Join(userKegRepo),
			Registries: []KegRegistry{
				{
					Name:     "knut",
					Url:      "keg.jlrickert.me",
					TokenEnv: "KNUT_API_KEY",
				},
			},
		},
	}
}

// ToYAML serializes the Config to YAML bytes.
func (cfg *Config) ToYAML() ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return yaml.Marshal(cfg.data)
}

// Write writes the Config back to path using atomic replacement.
func (cfg *Config) Write(rt *toolkit.Runtime, path string) error {
	data, err := cfg.ToYAML()
	if err != nil {
		return fmt.Errorf("unable to write user config: %w", err)
	}

	if err := rt.AtomicWriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("unable to write config: %w", err)
	}
	return nil
}

// MergeConfig merges multiple Config values into a single configuration.
//
// Merge semantics:
//   - Later configs override earlier values for the same keys.
//   - KegMap entries are appended in order, but entries with the same Keg
//     are overridden by later entries.
//   - The returned Config will have a Kegs map and a KegMap slice.
func MergeConfig(cfgs ...*Config) *Config {
	if len(cfgs) == 0 {
		return nil
	}

	out := &Config{
		data: &configDTO{
			Kegs:   make(map[string]kegurl.Target),
			KegMap: make([]KegMapEntry, 0),
		},
	}

	for _, c := range cfgs {
		if c == nil || c.data == nil {
			continue
		}

		// DefaultKeg: later wins when non-empty.
		if c.data.DefaultKeg != "" {
			out.data.DefaultKeg = c.data.DefaultKeg
		}

		if c.data.UserRepoPath != "" {
			out.data.UserRepoPath = c.data.UserRepoPath
		}
		if c.data.LogFile != "" {
			out.data.LogFile = c.data.LogFile
		}
		if c.data.LogLevel != "" {
			out.data.LogLevel = c.data.LogLevel
		}
		if !c.data.Updated.IsZero() {
			out.data.Updated = c.data.Updated
		}
		if len(c.data.Registries) > 0 {
			out.data.Registries = append([]KegRegistry(nil), c.data.Registries...)
		}
		if c.data.DefaultRegistry != "" {
			out.data.DefaultRegistry = c.data.DefaultRegistry
		}

		for alias, target := range c.data.Kegs {
			out.AddKeg(alias, target)
		}

		// Merge KegMap entries. Preserve order but override by alias when provided.
		for _, e := range c.data.KegMap {
			out.AddKegMap(e)
		}
	}

	return out
}

// Touch updates the Updated timestamp on the Config using the runtime clock.
func (cfg *Config) Touch(rt *toolkit.Runtime) {
	clk := rt.Clock()
	cfg.data.Updated = clk.Now()
}

// AddKeg adds or updates a keg entry in the Config.
func (cfg *Config) AddKeg(alias string, target kegurl.Target) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if alias == "" {
		return fmt.Errorf("alias is required")
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}

	// Add/update in struct
	if cfg.data.Kegs == nil {
		cfg.data.Kegs = make(map[string]kegurl.Target)
	}
	cfg.data.Kegs[alias] = target

	return nil
}

// AddKegMap adds or updates a keg map entry in the Config.
func (cfg *Config) AddKegMap(entry KegMapEntry) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if entry.Alias == "" {
		return fmt.Errorf("alias is required")
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}

	// Find and update or append to struct
	found := false
	for i, e := range cfg.data.KegMap {
		if e.Alias == entry.Alias {
			cfg.data.KegMap[i] = entry
			found = true
			break
		}
	}
	if !found {
		cfg.data.KegMap = append(cfg.data.KegMap, entry)
	}

	return nil
}

// LocalGitData attempts to run `git -C projectPath config --local --get key`.
//
// If git is not present or the command fails it returns an error. The returned
// bytes are trimmed of surrounding whitespace. The function logs diagnostic
// messages using the logger from rt.
func LocalGitData(ctx context.Context, rt *toolkit.Runtime, projectPath, key string) ([]byte, error) {
	lg := rt.Logger()
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

// ListKegs returns a sorted slice of all keg names in the configuration.
// Returns an empty slice if the config or its data is nil.
func (cfg *Config) ListKegs() []string {
	if cfg == nil || cfg.data == nil {
		return []string{}
	}
	keys := make([]string, 0, len(cfg.data.Kegs))
	for k := range cfg.data.Kegs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
