package tapper

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/jlrickert/cli-toolkit/cfgcascade"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

// ErrNotBootstrapped is returned by hub/namespace-dependent operations on the
// full `tap` surface when no user config exists yet — i.e. `tap bootstrap` has
// not been run. Explicit filesystem destinations (--path/--project/--cwd) and
// the pruned `keg` binary are exempt, and callers that resolve a keg by an
// explicit filesystem path are too.
var ErrNotBootstrapped = errors.New("tapper is not set up on this machine; run `tap bootstrap` to get started")

// ConfigLoadWarning represents a non-fatal issue encountered while loading config.
type ConfigLoadWarning struct {
	Source  string // "user config" or "project config"
	Path    string // file path that caused the issue
	Message string // human-readable description
	Err     error  // underlying error
}

// ConfigService loads, merges, and resolves tapper configuration state.
//
// Configuration is read once and then fixed for the life of the process. A
// `tap` command therefore runs against one consistent snapshot, and a
// long-lived `tap mcp` session picks up an external edit at its next orient,
// which is the one place Reload is called. Nothing inside a session can write
// configuration — the `config` tool is read-only — so "edit the file, then
// reorient" is the whole update story.
//
// The snapshot is immutable once published, so concurrent readers need no
// coordination beyond the mutex guarding the pointer itself. That matters
// because the MCP SDK dispatches every call except initialize asynchronously.
//
// Flight authority is not affected by a reload: the MCP session gate snapshots
// its resolved flight separately, so it stays stable across a reload either way.
type ConfigService struct {
	Runtime *toolkit.Runtime

	PathService *PathService

	// ConfigPath is the path to the config file.
	ConfigPath string

	// mu guards snap. The snapshot it points at is never mutated after being
	// published, so readers may use it after releasing the lock.
	mu   sync.Mutex
	snap *resolved
}

// resolved is one complete read of the configuration cascade. Every field is
// populated together by load and read-only thereafter. Tier errors are captured
// alongside their values so UserConfig and ProjectConfig report exactly what a
// direct read would have.
type resolved struct {
	merged     *Config
	user       *Config
	userErr    error
	project    *Config
	projectErr error
	// env is the env-var layer in isolation. The merged config cannot answer
	// "did TAP_FLIGHT set this?", and agent resolution has to know: a direct
	// TAP_FLIGHT outranks the flight an agent points at, while a flight coming
	// from a file layer does not.
	env      *Config
	warnings []ConfigLoadWarning
}

// NewConfigService builds a ConfigService rooted at root.
func NewConfigService(root string, rt *toolkit.Runtime) (*ConfigService, error) {
	pathService, err := NewPathService(rt, root)
	if err != nil {
		return nil, err
	}
	return &ConfigService{
		Runtime:     rt,
		PathService: pathService,
	}, nil
}

// Reload discards the snapshot so the next read re-reads from disk. Orientation
// is its only caller: it is the point at which a session re-establishes the
// context it is operating under.
func (s *ConfigService) Reload() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.snap = nil
}

// snapshot returns the process-wide configuration read, performing it on first
// use. The load runs under the lock so a burst of concurrent first calls
// resolves once rather than racing.
func (s *ConfigService) snapshot() (*resolved, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snap != nil {
		return s.snap, nil
	}
	snap, err := s.load()
	if err != nil {
		return nil, err
	}
	s.snap = snap
	return snap, nil
}

// Load returns the merged configuration together with the non-fatal issues
// found while reading it. Missing files are not warnings (graceful
// degradation); corrupt YAML and permission errors are.
func (s *ConfigService) Load() (*Config, []ConfigLoadWarning, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, nil, err
	}
	return snap.merged, snap.warnings, nil
}

// UserConfigExists reports whether a user config file is present — the signal
// that `tap bootstrap` has been run. When an explicit config path is set
// (--config) it checks that file; otherwise the standard user config path. It
// inspects the filesystem only; it never parses, so a corrupt-but-present config
// still counts as bootstrapped.
func (s *ConfigService) UserConfigExists() bool {
	path := s.ConfigPath
	if path == "" {
		path = filepath.Join(s.PathService.ConfigRoot, "config.yaml")
	}
	if _, err := s.Runtime.Stat(path, false); err == nil {
		return true
	}
	return false
}

// UserConfig returns the global user configuration from the process snapshot.
func (s *ConfigService) UserConfig() (*Config, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return snap.user, snap.userErr
}

// ReadUserConfigFile reads the user configuration straight from disk, bypassing
// the snapshot. Use it when about to modify and rewrite that file, so the
// read-modify-write cycle starts from what is actually on disk.
func (s *ConfigService) ReadUserConfigFile() (*Config, error) {
	return ReadConfig(s.Runtime, filepath.Join(s.PathService.ConfigRoot, "config.yaml"))
}

// WalkConfigsUp returns the absolute paths of every existing rel file found by
// walking from start up to the filesystem root, ordered DEEPEST-FIRST (nearest
// to start first). Missing candidates are skipped. The user-global config is
// not included; callers layer it underneath the returned project configs.
func WalkConfigsUp(rt *toolkit.Runtime, start, rel string) []string {
	var out []string
	seen := map[string]bool{}
	p := filepath.Clean(start)
	for {
		candidate := filepath.Join(p, rel)
		if _, err := rt.Stat(candidate, false); err == nil && !seen[candidate] {
			out = append(out, candidate)
			seen[candidate] = true
		}
		parent := filepath.Dir(p)
		if parent == p {
			break
		}
		p = parent
	}
	return out
}

// ProjectConfig returns the merged project-level configuration with optional
// caching. It walks from the workspace root up to the filesystem root,
// collecting every .tapper/config.yaml, and merges them so a deeper directory
// overrides a shallower one. Hub definitions and credentials are stripped from
// project layers — only the user config may define them — and each strip is
// recorded as a load warning surfaced by Config(). Returns keg.ErrNotExist when
// no project config exists.
func (s *ConfigService) ProjectConfig() (*Config, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return snap.project, snap.projectErr
}

// ReadProjectConfigFile walks and merges the project configs straight from
// disk, bypassing the snapshot. Same purpose as ReadUserConfigFile.
func (s *ConfigService) ReadProjectConfigFile() (*Config, error) {
	cfg, _, err := s.readProjectConfig()
	return cfg, err
}

// readProjectConfig performs the project walk and returns the merged result
// along with the trust-boundary warnings it produced.
func (s *ConfigService) readProjectConfig() (*Config, []ConfigLoadWarning, error) {
	relDir := filepath.Base(s.PathService.LocalConfigRoot) // ".tapper"
	rel := filepath.Join(relDir, "config.yaml")
	paths := WalkConfigsUp(s.Runtime, s.PathService.Root, rel)

	var (
		merged   *Config
		warnings []ConfigLoadWarning
	)
	// paths is deepest-first; merge shallowest→deepest so a deeper directory's
	// values win.
	for i := len(paths) - 1; i >= 0; i-- {
		p := paths[i]
		cfg, err := ReadConfig(s.Runtime, p)
		if err != nil {
			if errors.Is(err, keg.ErrNotExist) {
				continue
			}
			warnings = append(warnings, ConfigLoadWarning{
				Source:  "project config",
				Path:    p,
				Message: fmt.Sprintf("failed to load project config at %s: %v", p, err),
				Err:     err,
			})
			continue
		}
		for _, field := range stripUntrustedFields(cfg) {
			warnings = append(warnings, ConfigLoadWarning{
				Source:  "project config",
				Path:    p,
				Message: fmt.Sprintf("ignored %s in project config at %s (hubs and credentials may only be set in the user config)", field, p),
			})
		}
		if merged == nil {
			merged = cfg
		} else {
			merged = MergeConfig(merged, cfg)
		}
	}

	if merged == nil {
		return nil, warnings, keg.ErrNotExist
	}
	return merged, warnings, nil
}

// Config returns the merged user, project, and environment configuration from
// the process snapshot.
func (s *ConfigService) Config() (*Config, error) {
	snap, err := s.snapshot()
	if err != nil {
		return nil, err
	}
	return snap.merged, nil
}

// load performs one complete read of the cascade: both tiers, then the merge.
// It builds and returns a value without touching service state, which is what
// lets snapshot publish it as an immutable pointer.
//
// The merge resolves three providers in rank order — user config, project
// config, TAP_* env vars. When ConfigPath is set it reads that file instead and
// bypasses the cascade entirely.
func (s *ConfigService) load() (*resolved, error) {
	out := &resolved{}
	out.user, out.userErr = s.ReadUserConfigFile()

	var projectWarnings []ConfigLoadWarning
	out.project, projectWarnings, out.projectErr = s.readProjectConfig()

	if s.ConfigPath != "" {
		cfg, err := ReadConfig(s.Runtime, s.ConfigPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read config at %s: %w", s.ConfigPath, err)
		}
		if cfg == nil {
			cfg = &Config{}
		}
		out.merged = cfg
		return out, nil
	}

	userPath := filepath.Join(s.PathService.ConfigRoot, "config.yaml")
	projectPath := filepath.Join(s.PathService.LocalConfigRoot, "config.yaml")

	cascade := &cfgcascade.Cascade[*Config]{
		Layers: []cfgcascade.Layer[*Config]{
			{
				Rank: 1,
				Provider: &cfgcascade.FuncProvider[*Config]{
					ProviderName: "user config",
					Fn: func(_ func(string) string) (*Config, error) {
						if out.userErr != nil {
							if errors.Is(out.userErr, keg.ErrNotExist) {
								return nil, os.ErrNotExist
							}
							return nil, out.userErr
						}
						return out.user, nil
					},
				},
			},
			{
				Rank: 2,
				Provider: &cfgcascade.FuncProvider[*Config]{
					ProviderName: "project config",
					Fn: func(_ func(string) string) (*Config, error) {
						if out.projectErr != nil {
							if errors.Is(out.projectErr, keg.ErrNotExist) {
								return nil, os.ErrNotExist
							}
							return nil, out.projectErr
						}
						return out.project, nil
					},
				},
			},
			{
				Rank: 3,
				Provider: &cfgcascade.FuncProvider[*Config]{
					ProviderName: "env vars",
					Fn: func(getenv func(string) string) (*Config, error) {
						envProvider := &cfgcascade.EnvProvider{
							ProviderName: "env vars",
							Prefix:       tapEnvPrefix,
							Keys:         tapEnvVarKeys,
						}
						envMap, err := envProvider.Load(getenv)
						if err != nil {
							return nil, err
						}
						cfg := configFromEnvMap(envMap)
						out.env = cfg
						if cfg == nil {
							return nil, os.ErrNotExist
						}
						return cfg, nil
					},
				},
			},
		},
		MergeFn: func(base, overlay *Config) *Config {
			return MergeConfig(base, overlay)
		},
	}

	rv := cascade.Resolve(s.Runtime.Env().Get)

	// Surface trust-boundary / per-layer warnings accumulated by the project
	// config walk (the cascade only sees the merged result).
	out.warnings = append(out.warnings, projectWarnings...)

	// Map cascade provider errors to warnings.
	for _, pe := range rv.Errors {
		var path string
		switch pe.Name {
		case "user config":
			path = userPath
		case "project config":
			path = projectPath
		}
		out.warnings = append(out.warnings, ConfigLoadWarning{
			Source:  pe.Name,
			Path:    path,
			Message: fmt.Sprintf("failed to load %s at %s: %v", pe.Name, path, pe.Err),
			Err:     pe.Err,
		})
	}

	out.merged = rv.Value
	if out.merged == nil {
		out.merged = &Config{data: &configDTO{}}
	}
	if warning := applyAgentFlight(out.merged, out.env); warning != nil {
		out.warnings = append(out.warnings, *warning)
	}
	return out, nil
}

// applyAgentFlight resolves the active agent's flight into merged, and is why
// `tap launch` can export an agent name instead of a resolved flight. The agent
// is a reference, so the lookup happens on every load; a flight baked into the
// environment at launch would instead be frozen for the life of the process and
// no amount of reloading could move it.
//
// It sits between the env and project layers of the cascade rather than inside
// it, because the cascade merges whole Configs by rank and this rule needs two
// layers at once: the agents map comes from the file layers, while the decision
// to apply it at all depends on the env layer. Running here also means every
// consumer of ConfigService.Config sees one already-resolved flight.
//
// A returned warning means the selection named an agent that is not configured.
// That is reported rather than fatal: the session is still usable on whatever
// the file layers select, and a hard failure over a stale TAP_AGENT would brick
// a harness for a typo it cannot fix from the inside.
func applyAgentFlight(merged, env *Config) *ConfigLoadWarning {
	if merged == nil {
		return nil
	}
	name := merged.AgentName()
	if name == "" {
		return nil
	}
	// A direct TAP_FLIGHT outranks the agent's indirect one, so leave it be.
	if env != nil && strings.TrimSpace(env.Flight()) != "" {
		return nil
	}
	entry, ok := merged.Agent(name)
	if !ok {
		return &ConfigLoadWarning{
			Source: "agent",
			Message: fmt.Sprintf(
				"agent %q is selected but not configured, so its flight could not be applied; "+
					"the flight falls back to project and user configuration", name),
		}
	}
	// An agent without a flight selects no flight, matching a launch that had
	// none to export.
	if flight := strings.TrimSpace(entry.Flight); flight != "" {
		_ = merged.SetFlight(flight)
	}
	return nil
}

// ResolveTarget resolves a keg selector to a keg target. When the selector is
// empty it uses defaultKeg, then fallbackKeg. The selector is parsed as a keg
// reference and turned into a concrete target by Config.ResolveAlias (the
// namespace-centric ResolveRef chain and per-hub-kind backend mapping).
func (s *ConfigService) ResolveTarget(alias, nsOverride, hubOverride string) (*keg.Target, error) {
	cfg, err := s.Config()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve target: %w", err)
	}
	requestedAlias := alias
	if requestedAlias == "" {
		requestedAlias = cfg.DefaultKeg()
	}
	if requestedAlias == "" {
		requestedAlias = cfg.FallbackKeg()
	}
	if requestedAlias == "" {
		return nil, fmt.Errorf("no keg configured (set defaultKeg/fallbackKeg or use --keg)")
	}

	// Apply the --namespace / --hub overrides onto the parsed reference.
	ref, err := applyRefOverrides(parseKegRef(requestedAlias), nsOverride, hubOverride, requestedAlias)
	if err != nil {
		return nil, err
	}
	return cfg.ResolveRef(s.Runtime, ref)
}
