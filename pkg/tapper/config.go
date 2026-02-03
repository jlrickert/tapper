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

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/cli-toolkit/mylog"
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
// All fields are private. Use getter methods to read values and setter methods
// to update them. Setter methods sync both the struct and the YAML node to
// ensure comments and formatting are preserved when writing.
type Config struct {
	// parsed data.
	data *configDTO

	// Node holds the original parsed YAML document root (document node).
	// When present, we edit it directly to preserve comments and layout.
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

// SetDefaultKeg sets the default keg alias and updates the node.
func (cfg *Config) SetDefaultKeg(keg string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultKeg = keg
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode != nil && rootNode.Kind == yaml.MappingNode {
			updateMapEntry(rootNode, "defaultKeg", keg)
		}
	}
	return nil
}

// SetUserRepoPath sets the user repository path and updates the node.
func (cfg *Config) SetUserRepoPath(path string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.UserRepoPath = path
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode != nil && rootNode.Kind == yaml.MappingNode {
			updateMapEntry(rootNode, "userRepoPath", path)
		}
	}
	return nil
}

// SetDefaultRegistry sets the default registry and updates the node.
func (cfg *Config) SetDefaultRegistry(ctx context.Context, registry string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultRegistry = registry
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode != nil && rootNode.Kind == yaml.MappingNode {
			updateMapEntry(rootNode, "defaultRegistry", registry)
		}
	}
	return nil
}

// SetLogFile sets the log file path and updates the node.
func (cfg *Config) SetLogFile(ctx context.Context, path string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.LogFile = path
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode != nil && rootNode.Kind == yaml.MappingNode {
			updateMapEntry(rootNode, "logFile", path)
		}
	}
	return nil
}

// SetLogLevel sets the log level and updates the node.
func (cfg *Config) SetLogLevel(ctx context.Context, level string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.LogLevel = level
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode != nil && rootNode.Kind == yaml.MappingNode {
			updateMapEntry(rootNode, "logLevel", level)
		}
	}
	return nil
}

// Clone produces a deep copy of the Config including the underlying yaml
// node so callers can safely mutate the clone without affecting the original.
//
// When possible, the clone preserves the original document node, so comment
// preserving edits remain possible on the returned value.
func (cfg *Config) Clone(ctx context.Context) *Config {
	if cfg == nil {
		return nil
	}
	data, _ := cfg.ToYAML(ctx)
	uCfg, _ := ParseConfig(ctx, data)
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
func (cfg *Config) LookupAlias(ctx context.Context, projectRoot string) string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
		return ""
	}
	// Expand path and make absolute/clean to compare reliably.
	val := toolkit.ExpandEnv(ctx, projectRoot)
	abs, err := toolkit.ExpandPath(ctx, val)
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
		pattern := toolkit.ExpandEnv(ctx, m.PathRegex)
		pattern, _ = toolkit.ExpandPath(ctx, pattern)
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
		pref := toolkit.ExpandEnv(ctx, m.PathPrefix)
		pref, _ = toolkit.ExpandPath(ctx, pref)
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
func (cfg *Config) ResolveKegMap(ctx context.Context, projectRoot string) (*kegurl.Target, error) {
	alias := cfg.LookupAlias(ctx, projectRoot)
	return cfg.ResolveAlias(alias)
}

func (cfg *Config) ResolveDefault(ctx context.Context) (*kegurl.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
		return nil, nil
	}
	alias := toolkit.ExpandEnv(ctx, cfg.DefaultKeg())
	return cfg.ResolveAlias(alias)

}

// ParseConfig parses raw YAML into a Config while preserving the
// underlying yaml.Node for comment-preserving edits.
//
// The function attempts to decode the document root mapping into the struct.
// If the document root is missing, the returned Config is a zero value with
// KegMap and Kegs set to nil.
func ParseConfig(ctx context.Context, raw []byte) (*Config, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	uc := &Config{node: &doc}
	var tmpCfg configDTO
	err := yaml.Unmarshal(raw, &tmpCfg)
	uc.data = &tmpCfg
	return uc, err
}

// ReadConfig reads the YAML file at path and returns a parsed Config.
//
// When the file does not exist the function returns a Config value and an
// error that wraps keg.ErrNotExist so callers can detect no-config cases.
func ReadConfig(ctx context.Context, path string) (*Config, error) {
	b, err := toolkit.ReadFile(ctx, path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("unable read user config: %w", keg.ErrNotExist)
		}
		return nil, err
	}
	return ParseConfig(ctx, b)
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
//
// When possible the original parsed document node is emitted to preserve
// comments and formatting; otherwise the struct form is encoded.
func (cfg *Config) ToYAML(ctx context.Context) ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("no user config")
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)

	// Prefer writing the original node to keep comments. If absent, write struct.
	if cfg.node != nil {
		// Ensure we encode the document node as-is.
		if err := enc.Encode(cfg.node); err != nil {
			_ = enc.Close()
			return nil, fmt.Errorf("encode yaml node: %w", err)
		}
	} else {
		if err := enc.Encode(cfg.data); err != nil {
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
func (cfg *Config) Write(ctx context.Context, path string) error {
	data, err := cfg.ToYAML(ctx)
	if err != nil {
		return fmt.Errorf("unable to write user config: %w", err)
	}

	if err := toolkit.AtomicWriteFile(ctx, path, data, 0o644); err != nil {
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
//   - If any input carries a parsed yaml.Node, the node from the last non-nil
//     config is cloned and used to preserve comments when possible.
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

	var lastNode *yaml.Node

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

		for alias, target := range c.data.Kegs {
			out.AddKeg(alias, target)
		}

		// Merge KegMap entries. Preserve order but override by alias when provided.
		for _, e := range c.data.KegMap {
			out.AddKegMap(e)
		}

		// Remember last non-nil node so we can preserve comments if present.
		if c.node != nil {
			lastNode = c.node
		}
	}

	// If we found a lastNode, clone it by using ParseConfig on its YAML
	// rendering so the returned config has a node suitable for ToYAML edits.
	if lastNode != nil {
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		_ = enc.Encode(lastNode)
		_ = enc.Close()

		if cloned, err := ParseConfig(context.Background(), buf.Bytes()); err == nil && cloned != nil {
			// Use the cloned node but keep the merged struct fields from out.
			out.node = cloned.node
		}
	}

	return out
}

// Touch updates the Updated timestamp on the Config using the context clock.
func (cfg *Config) Touch(ctx context.Context) {
	clk := clock.ClockFromContext(ctx)
	cfg.data.Updated = clk.Now()
}

// AddKeg adds or updates a keg entry in the Config.
//
// Updates both the struct's Kegs map and the YAML node (if present) to preserve
// comments and formatting. If the node exists, the alias entry is added/updated
// within the kegs mapping while preserving document structure.
func (cfg *Config) AddKeg(alias string, target kegurl.Target) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if alias == "" {
		return fmt.Errorf("alias is required")
	}

	// Add/update in struct
	if cfg.data.Kegs == nil {
		cfg.data.Kegs = make(map[string]kegurl.Target)
	}
	cfg.data.Kegs[alias] = target

	// Update node if present to preserve comments in file
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode == nil || rootNode.Kind != yaml.MappingNode {
			return nil
		}

		// Find or create the "kegs" mapping in the root
		kegsNode := findOrCreateMapKey(rootNode, "kegs")
		if kegsNode != nil {
			// Add or update the alias entry within kegs
			updateMapEntry(kegsNode, alias, target.String())
		}
	}

	return nil
}

// AddKegMap adds or updates a keg map entry in the Config.
//
// Updates both the struct's KegMap slice and the YAML node (if present) to preserve
// comments and formatting. If an entry with the same alias exists, it is replaced;
// otherwise a new entry is appended.
func (cfg *Config) AddKegMap(entry KegMapEntry) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if entry.Alias == "" {
		return fmt.Errorf("alias is required")
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

	// Update node if present to preserve comments in file
	if cfg.node != nil && len(cfg.node.Content) > 0 {
		rootNode := cfg.node.Content[0]
		if rootNode == nil || rootNode.Kind != yaml.MappingNode {
			return nil
		}

		// Find or create the "kegMap" sequence in the root
		kegMapNode := findOrCreateMapKey(rootNode, "kegMap")
		if kegMapNode != nil {
			// Rebuild the kegMap sequence from the updated slice
			updateKegMapSequence(kegMapNode, cfg.data.KegMap)
		}
	}

	return nil
}

// updateKegMapSequence updates a YAML sequence node to match the provided KegMapEntry slice.
func updateKegMapSequence(seqNode *yaml.Node, entries []KegMapEntry) {
	if seqNode == nil || seqNode.Kind != yaml.SequenceNode {
		return
	}

	// Clear existing content
	seqNode.Content = []*yaml.Node{}

	// Add each entry as a mapping node
	for _, e := range entries {
		entryNode := &yaml.Node{
			Kind:    yaml.MappingNode,
			Content: []*yaml.Node{},
		}

		// Add alias key-value
		if e.Alias != "" {
			entryNode.Content = append(entryNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "alias"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: e.Alias},
			)
		}

		// Add pathPrefix key-value if set
		if e.PathPrefix != "" {
			entryNode.Content = append(entryNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "pathPrefix"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: e.PathPrefix},
			)
		}

		// Add pathRegex key-value if set
		if e.PathRegex != "" {
			entryNode.Content = append(entryNode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: "pathRegex"},
				&yaml.Node{Kind: yaml.ScalarNode, Value: e.PathRegex},
			)
		}

		seqNode.Content = append(seqNode.Content, entryNode)
	}
}

// findOrCreateMapKey finds a mapping key's value in a YAML mapping node or creates
// it if missing. Returns the value node for the given key.
func findOrCreateMapKey(mapNode *yaml.Node, key string) *yaml.Node {
	if mapNode == nil || mapNode.Kind != yaml.MappingNode {
		return nil
	}

	// Search for existing key in key-value pairs
	for i := 0; i < len(mapNode.Content); i += 2 {
		if mapNode.Content[i].Value == key {
			if i+1 < len(mapNode.Content) {
				return mapNode.Content[i+1]
			}
		}
	}

	// Not found, create new entry with empty mapping
	keyNode := &yaml.Node{Kind: yaml.ScalarNode, Value: key}
	valueNode := &yaml.Node{Kind: yaml.MappingNode, Content: []*yaml.Node{}}
	mapNode.Content = append(mapNode.Content, keyNode, valueNode)
	return valueNode
}

// updateMapEntry adds or updates an entry within a YAML mapping node.
// If the key exists, its value is updated; otherwise a new key-value pair is added.
func updateMapEntry(mapNode *yaml.Node, key, value string) {
	if mapNode == nil || mapNode.Kind != yaml.MappingNode {
		return
	}

	// Search for existing entry
	for i := 0; i < len(mapNode.Content); i += 2 {
		if mapNode.Content[i].Value == key {
			if i+1 < len(mapNode.Content) {
				mapNode.Content[i+1] = &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: value,
				}
			}
			return
		}
	}

	// Not found, add new entry
	mapNode.Content = append(mapNode.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Value: value},
	)
}

// LocalGitData attempts to run `git -C projectPath config --local --get key`.
//
// If git is not present or the command fails it returns an error. The returned
// bytes are trimmed of surrounding whitespace. The function logs diagnostic
// messages using the logger present in ctx.
func LocalGitData(ctx context.Context, projectPath, key string) ([]byte, error) {
	lg := mylog.LoggerFromContext(ctx)
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
