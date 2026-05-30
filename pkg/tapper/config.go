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
	"gopkg.in/yaml.v3"
)

const (
	TapConfigSchemaURL      = "https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/tap-config.json"
	tapConfigSchemaModeline = "# yaml-language-server: $schema=" + TapConfigSchemaURL + "\n"

	// DefaultHubName is the compiled-in name of the default remote hub.
	DefaultHubName = "atlas"

	// DefaultHubURL is the compiled-in fallback hub used by ResolveLoginHubURL
	// and Config.Hub when no explicit hub, defaultHub, fallbackHub, or single
	// configured hub is available and the implicit default has not been
	// disabled. The constant exists so the fallback target is auditable: a
	// deployment that needs to prove no implicit network calls happen sets
	// disableDefaultHub: true (or TAP_DISABLE_DEFAULT_HUB=1) and the chain
	// errors out instead of falling through here.
	DefaultHubURL = "https://atlas.foldwise.ai"

	// DefaultHubTokenEnv is the environment variable the default remote hub
	// reads its bearer token from when no explicit credential is configured.
	DefaultHubTokenEnv = "ATLAS_API_KEY"

	// LocalHubName is the reserved name of the built-in filesystem hub. Kegs
	// addressed at the "local" hub (or with namespace "local" and no hub) live
	// on disk under the hub's basePath rather than on a remote service.
	LocalHubName = "local"
)

// Hub kinds describe how a hub's (namespace, name) pairs are backed.
const (
	// HubKindRemote is a read-write HTTP hub (the default, e.g. atlas).
	HubKindRemote = "remote"
	// HubKindLocal is a filesystem-backed hub on this machine.
	HubKindLocal = "local"
	// HubKindReadonly is a read-only HTTP hub. TODO: enforce read-only writes
	// in the API repository; today the kind only sets Target.Readonly.
	HubKindReadonly = "readonly"
)

// Package tapper provides helpers for the tapper CLI related to user and
// project configuration, keg resolution, and small utilities used by commands.

type configDTO struct {
	LogFile  string `yaml:"logFile,omitempty"`
	LogLevel string `yaml:"logLevel,omitempty"`

	// updated is a timestamp.
	Updated time.Time `yaml:"updated,omitempty"`

	// defaultKeg is the alias used when no explicit keg is provided.
	DefaultKeg string `yaml:"defaultKeg,omitempty"`

	// fallbackKeg is a last-resort alias when no mapped/default keg resolves.
	FallbackKeg string `yaml:"fallbackKeg,omitempty"`

	// kegMap maps a project path or pattern to a keg alias.
	KegMap []KegMapEntry `yaml:"kegMap"`

	// kegs maps an alias to a (hub, namespace, name) keg reference.
	Kegs map[string]KegRef `yaml:"kegs"`

	// defaultHub names the hub used when a keg reference omits its hub. It is
	// the high-precedence slot (the authoritative choice); set it in project
	// config. The value is looked up by name in Hubs.
	DefaultHub string `yaml:"defaultHub,omitempty"`

	// fallbackHub names the hub used when a keg reference omits its hub and no
	// defaultHub applies. It is the last-resort slot; set it in the global user
	// config so references need not specify a hub.
	FallbackHub string `yaml:"fallbackHub,omitempty"`

	// defaultNamespace is the high-precedence namespace used when a keg
	// reference omits its namespace; set it in project config.
	DefaultNamespace string `yaml:"defaultNamespace,omitempty"`

	// fallbackNamespace is the last-resort namespace used when a keg reference
	// omits its namespace and no defaultNamespace applies; set it in the global
	// user config.
	FallbackNamespace string `yaml:"fallbackNamespace,omitempty"`

	// disableDefaultHub turns off the compiled-in DefaultHubURL fallback. When
	// true and no other branch matches, hub-dependent commands fail with a
	// clear error instead of silently reaching out to the default hub.
	DisableDefaultHub bool `yaml:"disableDefaultHub,omitempty"`

	// hubs describes configured hubs available to the user, keyed by name.
	Hubs hubMap `yaml:"hubs,omitempty"`
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

// HubEntry describes a single configured hub, keyed by name in the hubs map.
//
// Kind selects the backend: "remote" (read-write HTTP, the default when Kind
// is empty), "local" (filesystem) or "readonly" (read-only HTTP). Remote and
// readonly hubs use URL; local hubs use BasePath as the filesystem root that
// holds @<namespace>/<name> keg directories.
type HubEntry struct {
	Kind     string `yaml:"kind,omitempty"`
	URL      string `yaml:"url,omitempty"`
	BasePath string `yaml:"basePath,omitempty"`
	Token    string `yaml:"token,omitempty"`
	TokenEnv string `yaml:"tokenEnv,omitempty"`
}

// KegRef is the (hub, namespace, name) triple a keg alias resolves to. An empty
// Hub falls back to defaultHub/fallbackHub; an empty Namespace falls back to
// defaultNamespace/fallbackNamespace (see Config.ResolveRef).
//
// Path is an explicit local-filesystem escape hatch used for legacy file-path
// kegs and direct local references. When set it takes precedence over the
// triple and resolves to a file target at that path. The triple is the
// canonical, encouraged form; Path exists for backward compatibility.
type KegRef struct {
	Hub       string `yaml:"hub,omitempty"`
	Namespace string `yaml:"namespace,omitempty"`
	Name      string `yaml:"name,omitempty"`
	Path      string `yaml:"path,omitempty"`
}

// UnmarshalYAML accepts the canonical mapping form ({hub, namespace, name}) and
// also tolerates legacy forms so old configs keep loading: the legacy "kegName"
// key is read as Name, and scalar shorthands are parsed via keg.Parse — hub
// shorthand "hub:@ns/name" becomes the triple, while bare file/url scalars map
// onto the built-in local hub (best effort; the directory component is not
// preserved). Writes always serialize the canonical mapping form.
func (r *KegRef) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		type rawRef struct {
			Hub       string `yaml:"hub"`
			Namespace string `yaml:"namespace"`
			Name      string `yaml:"name"`
			Path      string `yaml:"path"`
			KegName   string `yaml:"kegName"` // legacy alias for name
			File      string `yaml:"file"`    // legacy explicit file path
		}
		var x rawRef
		if err := node.Decode(&x); err != nil {
			return fmt.Errorf("decode keg ref mapping: %w", err)
		}
		r.Hub = strings.TrimSpace(x.Hub)
		r.Namespace = strings.TrimSpace(x.Namespace)
		r.Name = strings.TrimSpace(x.Name)
		if r.Name == "" {
			r.Name = strings.TrimSpace(x.KegName)
		}
		r.Path = strings.TrimSpace(x.Path)
		if r.Path == "" {
			r.Path = strings.TrimSpace(x.File)
		}
		return nil
	case yaml.ScalarNode:
		s := strings.TrimSpace(node.Value)
		if s == "" {
			return nil
		}
		t, err := keg.Parse(s)
		if err != nil {
			return fmt.Errorf("decode keg ref scalar %q: %w", s, err)
		}
		switch {
		case t.Hub != "":
			// Hub shorthand "hub:@ns/name" → canonical triple.
			r.Hub = t.Hub
			r.Namespace = t.Namespace
			r.Name = t.KegName
		case t.File != "":
			// Legacy file-path scalar → explicit local path.
			r.Path = t.File
		default:
			// Any other legacy scalar (e.g. a bare URL): keep verbatim.
			r.Path = s
		}
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for keg ref", node.Kind)
	}
}

// hubMap is a name-keyed map of hubs that also accepts the legacy sequence form
// (a list of {name, url, token, tokenEnv}) on load so old configs keep working.
type hubMap map[string]HubEntry

// UnmarshalYAML accepts either the canonical mapping form or the legacy list
// form (entries keyed by their "name" field, defaulting to the remote kind).
func (h *hubMap) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		m := map[string]HubEntry{}
		if err := node.Decode(&m); err != nil {
			return fmt.Errorf("decode hubs mapping: %w", err)
		}
		*h = m
		return nil
	case yaml.SequenceNode:
		var list []struct {
			Name     string `yaml:"name"`
			Url      string `yaml:"url"`
			Token    string `yaml:"token"`
			TokenEnv string `yaml:"tokenEnv"`
		}
		if err := node.Decode(&list); err != nil {
			return fmt.Errorf("decode legacy hubs sequence: %w", err)
		}
		m := map[string]HubEntry{}
		for _, e := range list {
			name := strings.TrimSpace(e.Name)
			if name == "" {
				continue
			}
			m[name] = HubEntry{
				Kind:     HubKindRemote,
				URL:      e.Url,
				Token:    e.Token,
				TokenEnv: e.TokenEnv,
			}
		}
		*h = m
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for hubs", node.Kind)
	}
}

// --- Getter Methods ---

// DefaultKeg returns the alias to use when no explicit keg is provided.
func (cfg *Config) DefaultKeg() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DefaultKeg
}

// FallbackKeg returns the last-resort keg alias.
func (cfg *Config) FallbackKeg() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.FallbackKeg
}

// LookupAliasForTarget returns the alias whose configured keg reference
// resolves to the given target string. Returns empty string if no match is
// found. References are resolved to targets and compared by their canonical
// String() form (file paths are env/tilde-expanded and cleaned).
func (cfg *Config) LookupAliasForTarget(rt *toolkit.Runtime, target string) string {
	if cfg.data == nil || len(cfg.data.Kegs) == 0 || target == "" {
		return ""
	}
	norm := normalizeTargetString(rt, target)
	for alias, ref := range cfg.data.Kegs {
		t, err := cfg.ResolveRef(rt, ref)
		if err != nil || t == nil {
			continue
		}
		if normalizeTargetString(rt, t.String()) == norm {
			return alias
		}
	}
	return ""
}

// normalizeTargetString expands env vars and a leading tilde then cleans the
// result so target strings compare reliably regardless of how they were written.
func normalizeTargetString(rt *toolkit.Runtime, s string) string {
	norm := toolkit.ExpandEnv(rt, s)
	if expanded, err := toolkit.ExpandPath(rt, norm); err == nil {
		norm = filepath.Clean(expanded)
	}
	return norm
}

// Kegs returns a map of keg aliases to their references.
func (cfg *Config) Kegs() map[string]KegRef {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Kegs == nil {
		return map[string]KegRef{}
	}
	return cfg.data.Kegs
}

// DefaultHub returns the default hub name (high-precedence slot).
func (cfg *Config) DefaultHub() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DefaultHub
}

// FallbackHub returns the fallback hub name (last-resort slot).
func (cfg *Config) FallbackHub() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.FallbackHub
}

// DefaultNamespace returns the default namespace (high-precedence slot).
func (cfg *Config) DefaultNamespace() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DefaultNamespace
}

// FallbackNamespace returns the fallback namespace (last-resort slot).
func (cfg *Config) FallbackNamespace() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.FallbackNamespace
}

// DisableDefaultHub returns true when the compiled-in DefaultHubURL fallback is
// suppressed.
func (cfg *Config) DisableDefaultHub() bool {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DisableDefaultHub
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

// Hubs returns the map of configured hubs keyed by name.
func (cfg *Config) Hubs() map[string]HubEntry {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Hubs == nil {
		return map[string]HubEntry{}
	}
	return cfg.data.Hubs
}

// Hub returns the named hub entry. Built-in hubs are synthesized when not
// explicitly configured: "local" (filesystem) and "atlas" (the default remote
// hub). An explicit configuration always wins over the built-in.
func (cfg *Config) Hub(name string) (HubEntry, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return HubEntry{}, false
	}
	if e, ok := cfg.Hubs()[name]; ok {
		return e, true
	}
	switch name {
	case LocalHubName:
		return HubEntry{Kind: HubKindLocal}, true
	case DefaultHubName:
		return HubEntry{Kind: HubKindRemote, URL: DefaultHubURL, TokenEnv: DefaultHubTokenEnv}, true
	}
	return HubEntry{}, false
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

// SetDefaultKeg sets the alias used when no explicit keg is provided.
func (cfg *Config) SetDefaultKeg(keg string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultKeg = keg
	return nil
}

// SetFallbackKeg sets the fallback keg alias.
func (cfg *Config) SetFallbackKeg(keg string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.FallbackKeg = keg
	return nil
}

// SetDefaultHub sets the default hub name.
func (cfg *Config) SetDefaultHub(_ context.Context, hub string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultHub = hub
	return nil
}

// SetFallbackHub sets the fallback hub name.
func (cfg *Config) SetFallbackHub(hub string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.FallbackHub = hub
	return nil
}

// SetDefaultNamespace sets the default namespace.
func (cfg *Config) SetDefaultNamespace(ns string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DefaultNamespace = ns
	return nil
}

// SetFallbackNamespace sets the fallback namespace.
func (cfg *Config) SetFallbackNamespace(ns string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.FallbackNamespace = ns
	return nil
}

// SetDisableDefaultHub toggles the compiled-in DefaultHubURL fallback.
func (cfg *Config) SetDisableDefaultHub(disable bool) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DisableDefaultHub = disable
	return nil
}

// SetHub adds or replaces a hub entry by name.
func (cfg *Config) SetHub(name string, entry HubEntry) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("hub name is required")
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Hubs == nil {
		cfg.data.Hubs = hubMap{}
	}
	cfg.data.Hubs[name] = entry
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

// resolveHubName applies hub-name precedence: defaultHub → fallbackHub →
// the sole/alphabetically-first configured hub → the compiled-in default hub.
func (cfg *Config) resolveHubName() string {
	if h := strings.TrimSpace(cfg.DefaultHub()); h != "" {
		return h
	}
	if h := strings.TrimSpace(cfg.FallbackHub()); h != "" {
		return h
	}
	hubs := cfg.Hubs()
	if len(hubs) > 0 {
		names := make([]string, 0, len(hubs))
		for n := range hubs {
			names = append(names, n)
		}
		sort.Strings(names)
		return names[0]
	}
	return DefaultHubName
}

// resolveNamespace applies namespace precedence for an omitted namespace:
// defaultNamespace → fallbackNamespace. (Auth-derived username and the @local
// default are applied by callers that have the resolved hub in hand.)
func (cfg *Config) resolveNamespace() string {
	if ns := strings.TrimSpace(cfg.DefaultNamespace()); ns != "" {
		return ns
	}
	return strings.TrimSpace(cfg.FallbackNamespace())
}

// ResolveRef resolves a (hub, namespace, name) reference into a concrete
// keg.Target, applying hub and namespace default/fallback chains and the
// per-kind backend mapping:
//
//   - local:    <basePath>/@<namespace>/<name> as a file target
//   - remote:   <hub.url>/api/v1/kegs/@<namespace>/<name> as a hub target
//   - readonly: same URL as remote, with Target.Readonly set
func (cfg *Config) ResolveRef(rt *toolkit.Runtime, ref KegRef) (*keg.Target, error) {
	// Explicit local path takes precedence over the triple (legacy/escape hatch).
	if p := strings.TrimSpace(ref.Path); p != "" {
		p = toolkit.ExpandEnv(rt, p)
		if expanded, err := toolkit.ExpandPath(rt, p); err == nil {
			p = expanded
		}
		t := keg.NewFile(p)
		return &t, nil
	}

	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return nil, fmt.Errorf("keg reference is missing a name")
	}
	ns := strings.TrimSpace(ref.Namespace)
	if ns == "" {
		ns = cfg.resolveNamespace()
	}

	hubName := strings.TrimSpace(ref.Hub)
	if hubName == "" {
		if ns == LocalHubName {
			hubName = LocalHubName
		} else {
			hubName = cfg.resolveHubName()
		}
	}

	entry, ok := cfg.Hub(hubName)
	if !ok {
		return nil, fmt.Errorf("hub %q is not configured", hubName)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}

	if ns == "" {
		if kind == HubKindLocal {
			ns = LocalHubName
		} else {
			return nil, fmt.Errorf("keg reference %q has no namespace and no default/fallback namespace is configured", name)
		}
	}

	switch kind {
	case HubKindLocal:
		base := strings.TrimSpace(entry.BasePath)
		if base == "" {
			root, err := defaultUserKegRoot(rt)
			if err != nil {
				return nil, fmt.Errorf("local hub %q has no basePath and the platform default is unavailable: %w", hubName, err)
			}
			base = root
		}
		base = toolkit.ExpandEnv(rt, base)
		if expanded, err := toolkit.ExpandPath(rt, base); err == nil {
			base = expanded
		}
		path := filepath.Join(base, name)
		t := keg.NewFile(path)
		return &t, nil
	case HubKindRemote, HubKindReadonly:
		url := strings.TrimSpace(entry.URL)
		if url == "" {
			return nil, fmt.Errorf("hub %q has no url configured", hubName)
		}
		opts := []keg.TargetOption{keg.WithHubURL(url)}
		if kind == HubKindReadonly {
			opts = append(opts, keg.WithReadonly())
		}
		t := keg.NewApi(hubName, ns, name, opts...)
		t.Token = entry.Token
		t.TokenEnv = entry.TokenEnv
		return &t, nil
	default:
		return nil, fmt.Errorf("hub %q has unknown kind %q", hubName, kind)
	}
}

// ResolveAlias looks up the keg by alias and resolves it to a concrete Target.
//
// Returns (nil, error) when the alias is not configured or resolution fails.
func (cfg *Config) ResolveAlias(rt *toolkit.Runtime, alias string) (*keg.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	ref, ok := cfg.data.Kegs[alias]
	if !ok {
		return nil, fmt.Errorf("keg alias not found: %s", alias)
	}
	return cfg.ResolveRef(rt, ref)
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
//  3. If no entry matches, resolution returns an alias-not-found error.
//
// The function expands env vars and tildes prior to comparisons, so stored
// prefixes and patterns may contain ~ or $VAR values.
func (cfg *Config) ResolveKegMap(rt *toolkit.Runtime, projectRoot string) (*keg.Target, error) {
	alias := cfg.LookupAlias(rt, projectRoot)
	return cfg.ResolveAlias(rt, alias)
}

// ResolveDefault resolves the current DefaultKeg alias to a target.
func (cfg *Config) ResolveDefault(rt *toolkit.Runtime) (*keg.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
		return nil, nil
	}
	alias := toolkit.ExpandEnv(rt, cfg.DefaultKeg())
	return cfg.ResolveAlias(rt, alias)
}

// ParseConfig parses raw YAML into a Config data model. Unknown keys (such as
// the removed kegSearchPaths) are ignored, and legacy hubs/kegs shapes are
// upgraded by their tolerant UnmarshalYAML methods.
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

// DefaultUserConfig returns a sensible default global/user Config.
//
// The global user config uses FALLBACK slots (fallbackHub/fallbackNamespace) so
// keg references need not specify a hub or namespace. The default remote hub
// (atlas) and the built-in local hub are registered; localKegRoot seeds the
// local hub's basePath.
func DefaultUserConfig(name string, localKegRoot string) *Config {
	return &Config{
		data: &configDTO{
			FallbackHub:       DefaultHubName,
			FallbackNamespace: name,
			KegMap:            []KegMapEntry{},
			Kegs:              map[string]KegRef{},
			Hubs: hubMap{
				DefaultHubName: {
					Kind:     HubKindRemote,
					URL:      DefaultHubURL,
					TokenEnv: DefaultHubTokenEnv,
				},
				LocalHubName: {
					Kind:     HubKindLocal,
					BasePath: localKegRoot,
				},
			},
		},
	}
}

// DefaultProjectConfig returns a project-scoped config with sensible defaults.
//
// Project config uses DEFAULT slots (defaultHub/defaultNamespace) — the
// authoritative choice for the project, overriding the user-level fallbacks.
func DefaultProjectConfig(user, userKegRepo string) *Config {
	alias := strings.TrimSpace(user)
	if alias == "" {
		alias = LocalHubName
	}
	return &Config{
		data: &configDTO{
			DefaultHub:       DefaultHubName,
			DefaultNamespace: alias,
			DefaultKeg:       alias,
			KegMap:           []KegMapEntry{},
			Kegs: map[string]KegRef{
				alias: {Hub: LocalHubName, Namespace: LocalHubName, Name: alias},
			},
			Hubs: hubMap{},
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
	body, err := yaml.Marshal(cfg.data)
	if err != nil {
		return nil, err
	}
	return append([]byte(tapConfigSchemaModeline), body...), nil
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
//   - Later configs override earlier values for scalar keys.
//   - Hubs are merged by name; later entries override earlier ones.
//   - Kegs are merged by alias; later entries override earlier ones.
//   - KegMap entries are appended in order, but entries with the same alias +
//     path pattern are replaced by later entries.
func MergeConfig(cfgs ...*Config) *Config {
	if len(cfgs) == 0 {
		return nil
	}

	out := &Config{
		data: &configDTO{
			Kegs:   make(map[string]KegRef),
			KegMap: make([]KegMapEntry, 0),
			Hubs:   make(hubMap),
		},
	}

	for _, c := range cfgs {
		if c == nil || c.data == nil {
			continue
		}

		// Scalars: later wins when non-empty.
		if c.data.DefaultKeg != "" {
			out.data.DefaultKeg = c.data.DefaultKeg
		}
		if c.data.FallbackKeg != "" {
			out.data.FallbackKeg = c.data.FallbackKeg
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
		if c.data.DefaultHub != "" {
			out.data.DefaultHub = c.data.DefaultHub
		}
		if c.data.FallbackHub != "" {
			out.data.FallbackHub = c.data.FallbackHub
		}
		if c.data.DefaultNamespace != "" {
			out.data.DefaultNamespace = c.data.DefaultNamespace
		}
		if c.data.FallbackNamespace != "" {
			out.data.FallbackNamespace = c.data.FallbackNamespace
		}
		// DisableDefaultHub: any tier that sets it to true wins (fail closed).
		if c.data.DisableDefaultHub {
			out.data.DisableDefaultHub = true
		}

		for name, entry := range c.data.Hubs {
			out.data.Hubs[name] = entry
		}

		for alias, ref := range c.data.Kegs {
			out.data.Kegs[alias] = ref
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

// AddKeg adds or updates a keg reference in the Config under alias.
func (cfg *Config) AddKeg(alias string, ref KegRef) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if err := ValidateKegAlias(alias); err != nil {
		return err
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Kegs == nil {
		cfg.data.Kegs = make(map[string]KegRef)
	}
	cfg.data.Kegs[alias] = ref
	return nil
}

// RemoveKeg removes a keg entry from the Config by alias.
//
// Returns an error when the alias is not registered.
func (cfg *Config) RemoveKeg(alias string) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	if alias == "" {
		return fmt.Errorf("alias is required")
	}
	if cfg.data == nil || cfg.data.Kegs == nil {
		return fmt.Errorf("keg alias not found: %s", alias)
	}
	if _, ok := cfg.data.Kegs[alias]; !ok {
		return fmt.Errorf("keg alias not found: %s", alias)
	}
	delete(cfg.data.Kegs, alias)
	return nil
}

// AddKegMap adds or updates a keg map entry in the Config.
// Entries are matched by alias + pathPrefix + pathRegex. An entry with the same
// alias but a different path pattern is treated as a separate mapping.
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

	for i, e := range cfg.data.KegMap {
		if e.Alias == entry.Alias && e.PathPrefix == entry.PathPrefix && e.PathRegex == entry.PathRegex {
			cfg.data.KegMap[i] = entry
			return nil
		}
	}
	cfg.data.KegMap = append(cfg.data.KegMap, entry)
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

// ListKegs returns a sorted slice of all keg aliases in the configuration.
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
