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
	"github.com/jlrickert/tapper/pkg/schemas"
	"gopkg.in/yaml.v3"
)

const (
	TapConfigSchemaURL = schemas.TapConfigURL

	// tapConfigSchemaModeline is what ToYAML emits by default: the published
	// URL, which is meaningful anywhere. Write paths that hold a runtime swap
	// it for the materialized local copy via schemas.ReplaceModeline.
	tapConfigSchemaModeline = schemas.ModelinePrefix + TapConfigSchemaURL + "\n"

	// DefaultHubName is the compiled-in name of the default remote hub.
	DefaultHubName = "atlas"

	// DefaultHubURL is the compiled-in fallback hub used by ResolveLoginHubURL
	// and Config.Hub when no explicit hub, defaultHub, fallbackHub, or single
	// configured hub is available and the implicit default has not been
	// disabled. The constant exists so the fallback target is auditable: a
	// deployment that needs to prove no implicit network calls happen sets
	// disableAtlasHub: true (or TAP_DISABLE_ATLAS_HUB=1) and the chain
	// errors out instead of falling through here.
	DefaultHubURL = "https://atlas.foldwise.ai"

	// DefaultHubTokenEnv is the environment variable the default remote hub
	// reads its bearer token from when no explicit credential is configured.
	DefaultHubTokenEnv = "ATLAS_API_KEY"
)

// Hub kinds describe how a hub's (namespace, name) pairs are backed.
const (
	// HubKindRemote is a read-write HTTP hub (the default, e.g. atlas).
	HubKindRemote = "remote"
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

	// defaultKeg is the keg reference used when no explicit keg is provided. It
	// is a remote keg selector (a bare name, @namespace/name, or keg:...),
	// resolved through the namespace-centric ResolveRef chain.
	DefaultKeg string `yaml:"defaultKeg,omitempty"`

	// fallbackKeg is a last-resort keg reference when no default keg resolves.
	// Same selector forms as defaultKeg.
	FallbackKeg string `yaml:"fallbackKeg,omitempty"`

	// flight is the flight context applied when no --flight flag is given. It is
	// a flight reference (@namespace/+slug, +slug, or a bare slug) and is
	// may be set as a user baseline by bootstrap or overridden in project config;
	// TAP_FLIGHT and an explicit --flight have higher precedence. Agent entries
	// never participate in flight selection.
	Flight string `yaml:"flight,omitempty"`

	// agent names the entry in agents{} driving this process. It serves two
	// directions: `tap launch` exports the agent it resolved here as TAP_AGENT
	// so the child can report its own identity, and `tap launch` reads it as the
	// default when --agent is omitted (mirroring flight/TAP_FLIGHT). It selects
	// a model and supplies telemetry only; flight selection is independent and
	// TAP_FLIGHT pins a launch root.
	Agent string `yaml:"agent,omitempty"`

	// kegMap maps a project path or pattern to a keg reference.
	KegMap []KegMapEntry `yaml:"kegMap"`

	// namespaces maps a namespace name to the hub that hosts it. Its role is to
	// disambiguate which hub a namespace lives on when the same namespace could
	// exist on more than one configured hub. An entry here wins over the
	// defaultHub / fallbackHub chain during namespace→hub resolution.
	Namespaces map[string]NamespaceRef `yaml:"namespaces,omitempty"`

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

	// disableAtlasHub turns off the synthesized built-in atlas hub. When true the
	// compiled-in atlas hub is not synthesized in Hub(), is omitted from hub
	// listings, and is skipped in hub resolution — hub-dependent commands fail
	// with a clear error instead of silently reaching out to the default hub. An
	// explicit atlas entry in hubs{} is unaffected (explicit always wins).
	DisableAtlasHub bool `yaml:"disableAtlasHub,omitempty"`

	// disableTelemetry opts this user out of privacy-minimized invocation
	// reporting to their authenticated remote hub. Reporting is enabled when
	// this field is unset or false.
	DisableTelemetry bool `yaml:"disableTelemetry,omitempty"`

	// hubs describes configured hubs available to the user, keyed by name.
	Hubs hubMap `yaml:"hubs,omitempty"`

	// agents names model definitions for `tap launch`, keyed by alias.
	// Experimental and undocumented; see tap_launch.go.
	Agents map[string]AgentEntry `yaml:"agents,omitempty"`
}

// Config represents the user's tapper configuration.
//
// Config keeps a typed model alongside its parsed YAML document. Tapper-owned
// rewrites overlay known values so extension fields and comments survive.
type Config struct {
	// parsed data.
	data *configDTO
	// doc retains the parsed YAML document so Tapper-owned rewrites can overlay
	// known fields without discarding extension fields it does not understand.
	doc *yaml.Node
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
// is empty) or "readonly" (read-only HTTP).
type HubEntry struct {
	Kind string `yaml:"kind,omitempty"`
	// DefaultNamespace is this hub's default namespace, used when a reference
	// resolved against this hub omits its namespace. A hub hosts many namespaces;
	// this is only the default. The "@" sigil is implied — store the bare value.
	DefaultNamespace string `yaml:"defaultNamespace,omitempty"`
	URL              string `yaml:"url,omitempty"`
	Token            string `yaml:"token,omitempty"`
	TokenEnv         string `yaml:"tokenEnv,omitempty"`
}

// AgentEntry is an alias for a model plus how to reach and
// authenticate against that model, keyed by name in the agents map and consumed
// by `tap launch`.
//
// Model is provider-qualified ("anthropic/claude-opus-4", "ollama/qwen3.6:35b")
// so the launcher knows which protocol the harness must speak. Launch roots
// come from the top-level flight cascade; legacy per-agent flight keys are
// ignored and preserved as unknown extension data when configuration is
// rewritten.
//
// BaseURL overrides the provider's endpoint. One value serves both protocols:
// the launcher adds or removes the /v1 suffix to suit whichever the harness
// speaks. It defaults to the local Ollama server for ollama models and is empty
// for hosted providers, leaving the harness on its own endpoint.
//
// Auth selects where credentials come from — inherit (default), subscription,
// or apiKey. APIKeyEnv names the environment variable holding the key; the
// name is configured, never the secret, mirroring HubEntry.TokenEnv. Agents
// therefore hold no secrets, so unlike hubs they need no trust-boundary strip
// and a project config may safely define them.
//
// Experimental: this shape is expected to change when agents move to the hub.
// ContextWindow caps the working context in tokens. Harnesses express this
// differently — Codex as model metadata, Claude Code as an auto-compact
// threshold — so the launcher translates it per harness rather than passing a
// raw flag, and reports rather than drops it where there is no equivalent.
type AgentEntry struct {
	Model         string   `yaml:"model,omitempty"`
	BaseURL       string   `yaml:"baseUrl,omitempty"`
	Auth          string   `yaml:"auth,omitempty"`
	APIKeyEnv     string   `yaml:"apiKeyEnv,omitempty"`
	ContextWindow int      `yaml:"contextWindow,omitempty"`
	Args          []string `yaml:"args,omitempty"`
}

// KegRef is the (hub, namespace, name) triple a keg alias resolves to. An empty
// Hub falls back to defaultHub/fallbackHub; an empty Namespace falls back to
// defaultNamespace/fallbackNamespace (see Config.ResolveRef).
type KegRef struct {
	Hub       string `yaml:"hub,omitempty"`
	Namespace string `yaml:"namespace,omitempty"`
	Name      string `yaml:"name,omitempty"`
}

// UnmarshalYAML accepts the canonical mapping form ({hub, namespace, name}) and
// remote scalar shorthand. The canonical shorthand "keg:@ns/name" sets
// {namespace, name}; the hub is resolved from the namespace and is never
// encoded. To pin a hub, use the mapping form's "hub" field. Writes always
// serialize the canonical mapping form.
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
		}
		var x rawRef
		if err := node.Decode(&x); err != nil {
			return fmt.Errorf("decode keg ref mapping: %w", err)
		}
		r.Hub = strings.TrimSpace(x.Hub)
		r.Namespace = strings.TrimSpace(x.Namespace)
		r.Name = strings.TrimSpace(x.Name)
		return nil
	case yaml.ScalarNode:
		s := strings.TrimSpace(node.Value)
		if s == "" {
			return nil
		}
		ref := parseKegRef(s)
		if ref.Name == "" || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.HasPrefix(s, ".") || strings.HasPrefix(s, "file://") {
			_, err := keg.Parse(s)
			return fmt.Errorf("decode keg ref scalar %q: %w", s, err)
		}
		*r = ref
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for keg ref", node.Kind)
	}
}

// NamespaceRef names the hub that hosts a namespace. It is the conflict
// resolver for namespace→hub: when a namespace could live on more than one
// configured hub, an entry here pins which one. An empty Hub falls back to the
// hub precedence chain (see Config.resolveHubForNamespace).
type NamespaceRef struct {
	Hub string `yaml:"hub,omitempty"`
}

// UnmarshalYAML accepts the canonical mapping form ({hub: name}) and the scalar
// shorthand (a bare hub name), so both `jlrickert: atlas` and `jlrickert: {hub:
// atlas}` load. Writes always serialize the mapping form.
func (r *NamespaceRef) UnmarshalYAML(node *yaml.Node) error {
	if node == nil {
		return nil
	}
	switch node.Kind {
	case yaml.MappingNode:
		type rawRef struct {
			Hub string `yaml:"hub"`
		}
		var x rawRef
		if err := node.Decode(&x); err != nil {
			return fmt.Errorf("decode namespace ref mapping: %w", err)
		}
		r.Hub = strings.TrimSpace(x.Hub)
		return nil
	case yaml.ScalarNode:
		r.Hub = strings.TrimSpace(node.Value)
		return nil
	default:
		return fmt.Errorf("unsupported yaml node kind %d for namespace ref", node.Kind)
	}
}

// hubMap is a name-keyed map of hubs.
type hubMap map[string]HubEntry

// UnmarshalYAML decodes the canonical mapping form ({name: {url, token, ...}}).
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

// Flight returns the persisted flight reference applied when no --flight flag
// is given. On a merged config this may have come from the active agent rather
// than from any file — see ConfigService.load.
func (cfg *Config) Flight() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.Flight
}

// AgentName returns the name of the agent driving this process, or "" when none
// is selected. It indexes Agents; it is not itself an agent definition.
func (cfg *Config) AgentName() string {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return strings.TrimSpace(cfg.data.Agent)
}

// LookupAliasForTarget previously reverse-mapped a resolved target back to its
// configured keg alias. The namespace-centric model has no alias table, so a
// target no longer carries a short alias; callers fall back to the canonical
// @namespace/name label. Retained as a no-op so those callers need no change.
func (cfg *Config) LookupAliasForTarget(_ *toolkit.Runtime, _ string) string {
	return ""
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

// DisableAtlasHub returns true when the synthesized built-in atlas hub is
// suppressed.
func (cfg *Config) DisableAtlasHub() bool {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DisableAtlasHub
}

// DisableTelemetry returns true when remote invocation reporting is disabled.
func (cfg *Config) DisableTelemetry() bool {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	return cfg.data.DisableTelemetry
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

// Agents returns the configured `tap launch` agents keyed by alias.
func (cfg *Config) Agents() map[string]AgentEntry {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Agents == nil {
		return map[string]AgentEntry{}
	}
	return cfg.data.Agents
}

// Agent returns the named agent entry. Unlike Hub there are no synthesized
// built-ins: an agent exists only if configured.
func (cfg *Config) Agent(name string) (AgentEntry, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return AgentEntry{}, false
	}
	e, ok := cfg.Agents()[name]
	return e, ok
}

// Hub returns the named hub entry. The built-in atlas remote hub is synthesized
// when it is not explicitly configured unless disableAtlasHub is set.
func (cfg *Config) Hub(name string) (HubEntry, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return HubEntry{}, false
	}
	if e, ok := cfg.Hubs()[name]; ok {
		return e, true
	}
	switch name {
	case DefaultHubName:
		if cfg.DisableAtlasHub() {
			return HubEntry{}, false
		}
		return HubEntry{Kind: HubKindRemote, URL: DefaultHubURL, TokenEnv: DefaultHubTokenEnv}, true
	}
	return HubEntry{}, false
}

// Namespaces returns the map of namespace name to hosting hub reference.
func (cfg *Config) Namespaces() map[string]NamespaceRef {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Namespaces == nil {
		return map[string]NamespaceRef{}
	}
	return cfg.data.Namespaces
}

// Namespace returns the namespace reference registered under name.
func (cfg *Config) Namespace(name string) (NamespaceRef, bool) {
	name = strings.TrimSpace(name)
	if name == "" {
		return NamespaceRef{}, false
	}
	ref, ok := cfg.Namespaces()[name]
	return ref, ok
}

// SetNamespace adds or replaces the hub mapping for a namespace.
func (cfg *Config) SetNamespace(name string, ref NamespaceRef) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("namespace name is required")
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if cfg.data.Namespaces == nil {
		cfg.data.Namespaces = map[string]NamespaceRef{}
	}
	cfg.data.Namespaces[name] = ref
	return nil
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

// SetFlight sets the persisted flight reference (or clears it when empty).
func (cfg *Config) SetFlight(flight string) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.Flight = flight
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

// SetDisableAtlasHub toggles the synthesized built-in atlas hub.
func (cfg *Config) SetDisableAtlasHub(disable bool) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DisableAtlasHub = disable
	return nil
}

// SetDisableTelemetry toggles privacy-minimized invocation reporting.
func (cfg *Config) SetDisableTelemetry(disable bool) error {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	cfg.data.DisableTelemetry = disable
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

// DeleteHub removes a hub entry by name, reporting whether it was present.
func (cfg *Config) DeleteHub(name string) (bool, error) {
	if cfg == nil {
		return false, fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false, fmt.Errorf("hub name is required")
	}
	if cfg.data == nil || cfg.data.Hubs == nil {
		return false, nil
	}
	if _, ok := cfg.data.Hubs[name]; !ok {
		return false, nil
	}
	delete(cfg.data.Hubs, name)
	return true, nil
}

// DeleteNamespace removes a namespace→hub mapping by name, reporting whether it
// was present. `tap hub remove` uses it to prune namespace pins that route to a
// hub being removed.
func (cfg *Config) DeleteNamespace(name string) bool {
	if cfg == nil || cfg.data == nil || cfg.data.Namespaces == nil {
		return false
	}
	name = strings.TrimSpace(name)
	if _, ok := cfg.data.Namespaces[name]; !ok {
		return false
	}
	delete(cfg.data.Namespaces, name)
	return true
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

// resolveNamespaceForName applies namespace precedence for a keg name in the
// namespace-centric model: defaultNamespace → fallbackNamespace. It returns ""
// when neither applies, leaving the per-hub default in ResolveRef to have the
// final say once the hub is known.
func (cfg *Config) resolveNamespaceForName() string {
	if ns := strings.TrimSpace(cfg.DefaultNamespace()); ns != "" {
		return ns
	}
	if ns := strings.TrimSpace(cfg.FallbackNamespace()); ns != "" {
		return ns
	}
	return ""
}

// resolveHubForNamespace applies hub precedence for a namespace: an explicit
// namespaces[ns].Hub mapping → the general hub precedence chain (defaultHub →
// fallbackHub → sole/alphabetically-first hub → the compiled-in default hub).
func (cfg *Config) resolveHubForNamespace(ns string) string {
	ns = strings.TrimSpace(ns)
	if ns != "" {
		if ref, ok := cfg.Namespaces()[ns]; ok {
			if h := strings.TrimSpace(ref.Hub); h != "" {
				return h
			}
		}
	}
	return cfg.resolveHubName()
}

// resolveHubName applies hub-name precedence: defaultHub → fallbackHub →
// the sole/alphabetically-first configured hub → the compiled-in default hub.
// The compiled-in atlas fallback is skipped when disableAtlasHub is set, in
// which case "" is returned and the caller surfaces a "no hub" error rather
// than routing to a disabled built-in.
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
	if cfg.DisableAtlasHub() {
		return ""
	}
	return DefaultHubName
}

// resolveNamespaceHub resolves the effective (namespace, hubName, entry) for a
// reference whose namespace and/or hub may be empty. It is the single source of
// truth for the namespace-centric chain, shared by ResolveRef (backend
// resolution), resolveIdentity (display) and resolveKegAdminRef (admin):
//
//	namespace: explicit → defaultNamespace → fallbackNamespace → the resolved
//	           hub's per-hub defaultNamespace
//	hub:       explicit → namespaces[ns].hub → defaultHub → fallbackHub →
//	           sole/alpha hub → compiled-in atlas (unless disabled)
//
// It returns an error when no hub is available (a disabled built-in with nothing
// else configured), the resolved hub is not configured, or the hub has no
// resolvable namespace. Callers wrap the error with reference context.
func (cfg *Config) resolveNamespaceHub(ns, hubName string) (string, string, HubEntry, error) {
	// Namespace first: explicit → default → fallback. It may still be empty
	// here; the per-hub default below gets the final say once the hub is known.
	ns = strings.TrimSpace(ns)
	if ns == "" {
		ns = cfg.resolveNamespaceForName()
	}

	// Hub from the namespace: explicit wins; otherwise the namespace→hub map
	// pins it, and finally the hub precedence chain applies.
	hubName = strings.TrimSpace(hubName)
	if hubName == "" {
		hubName = cfg.resolveHubForNamespace(ns)
	}
	if hubName == "" {
		return "", "", HubEntry{}, fmt.Errorf("no hub available (the built-in hub is disabled and no other hub is configured)")
	}

	entry, ok := cfg.Hub(hubName)
	if !ok {
		return "", "", HubEntry{}, fmt.Errorf("hub %q is not configured", hubName)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}

	// Last-resort namespace once the hub is known: the hub's own default
	// namespace (lower precedence than the default/fallback chain), else an error.
	if ns == "" {
		if strings.TrimSpace(entry.DefaultNamespace) != "" {
			ns = strings.TrimSpace(entry.DefaultNamespace)
		} else {
			return "", "", HubEntry{}, fmt.Errorf("no namespace and no per-hub, default, or fallback namespace is configured")
		}
	}
	if kind != HubKindRemote && kind != HubKindReadonly {
		return "", "", HubEntry{}, fmt.Errorf("hub %q has unsupported kind %q", hubName, kind)
	}

	return ns, hubName, entry, nil
}

// ResolveRef resolves a (hub, namespace, name) reference into a concrete
// keg.Target. It applies the namespace-centric chains via resolveNamespaceHub —
// namespace first (explicit → default/fallback), then the hub that hosts that
// namespace — and the per-kind backend mapping:
//
//   - remote:   <hub.url>/api/v1/@<namespace>/kegs/<name> as a hub target
//   - readonly: same URL as remote, with Target.Readonly set
func (cfg *Config) ResolveRef(_ *toolkit.Runtime, ref KegRef) (*keg.Target, error) {
	name := strings.TrimSpace(ref.Name)
	if name == "" {
		return nil, fmt.Errorf("keg reference is missing a name")
	}

	ns, hubName, entry, err := cfg.resolveNamespaceHub(ref.Namespace, ref.Hub)
	if err != nil {
		return nil, fmt.Errorf("keg reference %q: %w", name, err)
	}
	kind := strings.TrimSpace(entry.Kind)
	if kind == "" {
		kind = HubKindRemote
	}

	if err := ValidateNamespace(ns); err != nil {
		return nil, fmt.Errorf("keg reference %q: %w", name, err)
	}

	switch kind {
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

// parseKegRef turns a keg selector string (defaultKeg, fallbackKeg, a kegMap
// alias, or an explicit --keg value) into a KegRef for ResolveRef. There is no
// alias table anymore: a selector is the keg reference itself. Forms:
//
//   - "keg:@ns/name" / "keg:name" — the canonical keg scheme (parsed by
//     keg.Parse; the hub is resolved from the namespace, never encoded).
//   - "@ns/name"                  — a namespace-qualified reference.
//   - an HTTP(S) endpoint         — used directly as a remote target.
//   - "name"                      — a bare keg name; its namespace and hub are
//     supplied by ResolveRef's default/fallback chains.
//
// applyRefOverrides overlays --namespace / --hub overrides onto a parsed keg
// reference. The namespace override fills a bare reference but conflicts with an
// explicit @namespace/ already present (an error); the hub override always wins,
// since the hub is never part of the reference string. selector is the original
// reference text, used only for the conflict error message.
func applyRefOverrides(ref KegRef, nsOverride, hubOverride, selector string) (KegRef, error) {
	if ns := strings.TrimPrefix(strings.TrimSpace(nsOverride), "@"); ns != "" {
		if ref.Namespace != "" && ref.Namespace != ns {
			return ref, fmt.Errorf("--namespace %q conflicts with the namespace in keg reference %q", ns, selector)
		}
		ref.Namespace = ns
	}
	if hub := strings.TrimSpace(hubOverride); hub != "" {
		ref.Hub = hub
	}
	return ref, nil
}

func parseKegRef(s string) KegRef {
	s = strings.TrimSpace(s)
	if s == "" {
		return KegRef{}
	}
	// Canonical keg scheme: defer to the shared parser.
	if strings.HasPrefix(s, keg.SchemeAlias+":") {
		if t, err := keg.Parse(s); err == nil {
			if t.KegName != "" {
				return KegRef{Hub: t.Hub, Namespace: t.Namespace, Name: t.KegName}
			}
		}
		// Malformed keg: ref falls through to be treated as a bare name so
		// ResolveRef can report a coherent namespace/hub error.
	}
	// Namespace-qualified reference: @namespace/name.
	if rest, ok := strings.CutPrefix(s, "@"); ok {
		if ns, name, found := strings.Cut(rest, "/"); found {
			ns = strings.TrimSpace(ns)
			name = strings.TrimSpace(name)
			if ns != "" && name != "" {
				return KegRef{Namespace: ns, Name: name}
			}
		}
		// Malformed @-ref falls through to a bare name.
	}
	// Bare keg name: namespace and hub come from the default/fallback chains.
	return KegRef{Name: s}
}

// ResolveAlias resolves a keg selector string to a concrete Target. The
// selector is parsed as a keg reference (see parseKegRef) and resolved through
// the namespace-centric ResolveRef chain — there is no kegs alias table.
//
// Returns (nil, error) when the selector is empty or resolution fails.
func (cfg *Config) ResolveAlias(rt *toolkit.Runtime, alias string) (*keg.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	if strings.TrimSpace(alias) == "" {
		return nil, fmt.Errorf("no keg reference given")
	}
	if target, err := keg.Parse(alias); err == nil && (target.Scheme() == keg.SchemeHTTP || target.Scheme() == keg.SchemeHTTPs) {
		return target, nil
	} else if strings.HasPrefix(alias, "/") || strings.HasPrefix(alias, "~") || strings.HasPrefix(alias, ".") || strings.HasPrefix(alias, "file://") {
		return nil, err
	}
	return cfg.ResolveRef(rt, parseKegRef(alias))
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

// ResolveDefault resolves the current DefaultKeg reference to a target.
func (cfg *Config) ResolveDefault(rt *toolkit.Runtime) (*keg.Target, error) {
	if cfg.data == nil {
		cfg.data = &configDTO{}
		return nil, nil
	}
	alias := toolkit.ExpandEnv(rt, cfg.DefaultKeg())
	return cfg.ResolveAlias(rt, alias)
}

// ParseConfig parses raw YAML into a Config data model. Unknown keys are
// ignored by the decoder.
func ParseConfig(raw []byte) (*Config, error) {
	uc := &Config{data: &configDTO{}}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	if err := yaml.Unmarshal(raw, uc.data); err != nil {
		return nil, fmt.Errorf("failed to parse user config yaml: %w", err)
	}
	uc.doc = &doc
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

// stripUntrustedFields removes configuration a walked (project) config layer is
// not permitted to set. Hub definitions and their credentials are user-config
// only, so a repository you cd into cannot introduce a hub target or harvest a
// token environment variable. It returns a human-readable description of each
// removed field for surfacing as a load warning. Project layers may still set
// kegMap and the default*/fallback* selectors.
func stripUntrustedFields(cfg *Config) []string {
	if cfg == nil || cfg.data == nil {
		return nil
	}
	var removed []string
	if len(cfg.data.Hubs) > 0 {
		names := make([]string, 0, len(cfg.data.Hubs))
		for n := range cfg.data.Hubs {
			names = append(names, n)
		}
		sort.Strings(names)
		removed = append(removed, fmt.Sprintf("hubs (%s)", strings.Join(names, ", ")))
		cfg.data.Hubs = nil
	}
	return removed
}

// DefaultUserConfig returns a sensible default global/user Config.
//
// The global user config uses the FALLBACK hub (fallbackHub) so keg references
// need not specify a hub. The namespace is NOT pinned by a global
// fallbackNamespace — it comes from the resolved hub's own namespace field.
// `name` seeds the default remote hub's namespace.
func DefaultUserConfig(name string) *Config {
	return &Config{
		data: &configDTO{
			FallbackHub: DefaultHubName,
			KegMap:      []KegMapEntry{},
			Namespaces:  map[string]NamespaceRef{},
			Hubs: hubMap{
				DefaultHubName: {
					Kind:             HubKindRemote,
					DefaultNamespace: name,
					URL:              DefaultHubURL,
					TokenEnv:         DefaultHubTokenEnv,
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
		alias = "project"
	}
	return &Config{
		data: &configDTO{
			DefaultHub:       DefaultHubName,
			DefaultNamespace: alias,
			DefaultKeg:       alias,
			KegMap:           []KegMapEntry{},
			Namespaces:       map[string]NamespaceRef{},
			Hubs:             hubMap{},
		},
	}
}

// projectConfigTemplate returns a fully commented-out starter project config:
// the schema modeline, a short header, and the illustrative DefaultProjectConfig
// example with every field line commented. It is what `tap config edit` writes
// when no project config exists yet, so an abandoned or empty edit leaves an
// INERT file — none of the authoritative default* slots are active, so a stray
// project config can't silently override user-level keg/namespace/hub
// resolution. Parsing it yields an empty Config.
func projectConfigTemplate(rt *toolkit.Runtime) ([]byte, error) {
	example := DefaultProjectConfig("project", "kegs")
	body, err := yaml.Marshal(example.data)
	if err != nil {
		return nil, fmt.Errorf("render project config template: %w", err)
	}
	var b strings.Builder
	b.WriteString(schemas.Modeline(rt, schemas.TapConfig))
	b.WriteString("# Project config. Uncomment and edit fields below to override the user\n")
	b.WriteString("# config for this directory tree. While everything stays commented this\n")
	b.WriteString("# file is inert. Note: hubs and tokens may only live in the user config\n")
	b.WriteString("# (they are stripped from project config).\n")
	for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			b.WriteByte('\n')
			continue
		}
		b.WriteString("# ")
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return []byte(b.String()), nil
}

// ToYAML serializes the Config to YAML bytes.
func (cfg *Config) ToYAML() ([]byte, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if cfg.data == nil {
		cfg.data = &configDTO{}
	}
	doc, err := overlayConfigDocument(cfg.doc, cfg.data)
	if err != nil {
		return nil, err
	}
	body, err := yaml.Marshal(doc)
	if err != nil {
		return nil, err
	}
	if bytes.HasPrefix(body, []byte(schemas.ModelinePrefix)) {
		return body, nil
	}
	return append([]byte(tapConfigSchemaModeline), body...), nil
}

// Write writes the Config back to path using atomic replacement.
func (cfg *Config) Write(rt *toolkit.Runtime, path string) error {
	data, err := cfg.ToYAML()
	if err != nil {
		return fmt.Errorf("unable to write user config: %w", err)
	}
	// Point the modeline at the schema copy materialized from this binary so
	// an editor completes against the shape this build actually accepts.
	data = schemas.ReplaceModeline(data, schemas.Modeline(rt, schemas.TapConfig))

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
//   - Namespaces are merged by name; later entries override earlier ones.
//   - KegMap entries are appended in order, but entries with the same alias +
//     path pattern are replaced by later entries.
func MergeConfig(cfgs ...*Config) *Config {
	if len(cfgs) == 0 {
		return nil
	}

	out := &Config{
		data: &configDTO{
			Namespaces: make(map[string]NamespaceRef),
			KegMap:     make([]KegMapEntry, 0),
			Hubs:       make(hubMap),
			Agents:     make(map[string]AgentEntry),
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
		if c.data.Flight != "" {
			out.data.Flight = c.data.Flight
		}
		if c.data.Agent != "" {
			out.data.Agent = c.data.Agent
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
		// Disable flags: any tier that sets one to true wins (fail closed).
		if c.data.DisableAtlasHub {
			out.data.DisableAtlasHub = true
		}
		if c.data.DisableTelemetry {
			out.data.DisableTelemetry = true
		}

		for name, entry := range c.data.Hubs {
			out.data.Hubs[name] = entry
		}

		for name, entry := range c.data.Agents {
			out.data.Agents[name] = entry
		}

		for ns, ref := range c.data.Namespaces {
			out.data.Namespaces[ns] = ref
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
