package tapper

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	terrs "github.com/jlrickert/tapper/pkg/errors"
	"github.com/jlrickert/tapper/pkg/internal"
	"gopkg.in/yaml.v3"
)

// Simple types and helpers to read/write the small tapper repo-local and user
// config formats described in the docs. This file intentionally provides a
// focused, testable subset: read precedence (env, git config, .tapper/local.yaml,
// project keg file, user mappings) and atomic write helpers for repo-local and
// user config files.
//
// This file also supports:
//  - reading a user-level config (~/.config/tapper/config.yaml) that can
//    include mappings to map repositories to keg targets based on match criteria
//  - resolving a repo -> keg mapping by consulting those mappings (in addition
//    to aliased targets and other precedence sources)

// KegTarget describes the repo-local "keg" hint shape.
type KegTarget struct {
	// Value typically comes from env or git config. Ambiguous between an alias,
	// url, or a filesystem path at this point.
	Value string
	// Source indicates where the chosen value originated. Typical values:
	//   - "env"      : KEG_CURRENT environment variable
	//   - "git"      : git local config key `tap.keg`
	//   - "local"    : repo-local file `.tapper/local.yaml`
	//   - "keg-file" : project keg file (e.g., `docs/keg` or `./keg`)
	//   - "mapping"  : user-config mapping (~/.config/tapper/config.yaml)
	//   - "alias"    : alias from cli
	//   - "path"     : path from cli
	//   - "fallback" : fallback/default (no explicit source found)
	Source string

	Alias       string `yaml:"alias,omitempty"`
	URL         string `yaml:"url,omitempty"`
	Path        string `yaml:"path,omitempty"`
	PreferLocal bool   `yaml:"prefer_local,omitempty"`
}

// LocalConfig is the structure for .tapper/local.yaml (repo-local visible override).
type LocalConfig struct {
	Updated string    `yaml:"updated,omitempty"`
	Keg     KegTarget `yaml:"keg,omitempty"`
}

// UserConfig is the optional higher-level tapper config (~/.config/tapper/config.yaml).
// It may contain multiple mappings that match repositories to keg targets.
type UserConfig struct {
	DefaultKeg KegTarget            `yaml:"default_keg,omitempty"`
	Updated    string               `yaml:"updated,omitempty"`
	Mappings   []Mapping            `yaml:"mappings,omitempty"`
	Aliases    map[string]KegTarget `yaml:"aliases,omitempty"`
}

// MappingMatch lists the possible match criteria for a mapping entry.
// Empty fields are ignored; a mapping matches only if at least one provided
// criterion succeeds.
type MappingMatch struct {
	PathPrefix string `yaml:"prefix,omitempty"`
	PathRegex  string `yaml:"regex,omitempty"`
}

// Mapping is a single mapping entry in the user config.
type Mapping struct {
	Name     string       `yaml:"name,omitempty"`
	Match    MappingMatch `yaml:"match,omitempty"`
	Keg      KegTarget    `yaml:"keg,omitempty"`
	Priority int          `yaml:"priority,omitempty"`
}

func (target *KegTarget) TargetType() string {
	if target.Alias != "" {
		return "alias"
	}
	if target.URL != "" {
		if target.PreferLocal && target.Path != "" {
			return "path"
		}
		return "url"
	}
	if target.Path != "" {
		return "path"
	}
	return "unknown"
}

// ResolveKegTargetForRepo resolves which KEG to use for a repository following
// the precedence rules used by tapper. Implementations consult multiple
// candidate sources in order and return the first applicable resolution.
//
// Precedence (highest -> lowest) implemented here:
// 1. KEG_CURRENT environment variable (returned as-is)
// 2. git local config key `tap.keg` (if repoRoot is inside a git repo)
// 3. repo-local file: <repoRoot>/.tapper/local.yaml
// 4. project keg file: docs/keg or ./keg (returns the keg file path as a Path)
// 5. user config mappings (~/.config/tapper/config.yaml) — best matching mapping
// 6. user-config DefaultKeg if present
// 7. fallback: an empty target with Source="fallback"
//
// If nothing is found this returns a KegTarget with Source "fallback".
func ResolveKegTargetForRepo(repoRoot string, cfg *UserConfig) (*KegTarget, error) {
	// 1) env
	if v := os.Getenv("KEG_CURRENT"); v != "" {
		return &KegTarget{Value: v, Source: "env"}, nil
	}

	// 2) git config --local tap.keg
	if repoRoot != "" {
		if val, err := LocalGitConfigData(repoRoot, "tap.keg"); err == nil && len(val) > 0 {
			return &KegTarget{Value: string(val), Source: "git"}, nil
		}
	}

	// 3) repo-local .tapper/local.yaml
	localPath := filepath.Join(repoRoot, DefaultLocalTapperDir, "local.yaml")
	if _, err := os.Stat(localPath); err == nil {
		lf, _ := ReadLocalFile(localPath)
		if lf != nil && !lf.Keg.IsEmpty() {
			lf.Keg.Source = "local"
			return &lf.Keg, nil
		}
	}

	// 4) project keg file (docs/keg or ./keg)
	candidates := []string{
		filepath.Join(repoRoot, "docs", "keg"),
		filepath.Join(repoRoot, "keg"),
		filepath.Join(repoRoot, "docs", "keg.yaml"),
		filepath.Join(repoRoot, "keg.yaml"),
	}
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			// return the path to the keg file as the resolution (tooling can ReadConfig)
			return &KegTarget{Path: p, Source: "keg-file", PreferLocal: true}, nil
		}
	}

	// 5) user config mappings (config.yaml)
	if cfg != nil {
		best, ok := cfg.findBestMapping(repoRoot)
		if ok {
			best.Keg.Source = "mapping"
			return &best.Keg, nil
		}
	}

	// 6) user-config default keg
	if cfg != nil && !cfg.DefaultKeg.IsEmpty() {
		d := cfg.DefaultKeg
		d.Source = "alias"
		return &d, nil
	}

	// 7) fallback
	return &KegTarget{Source: "fallback"}, nil
}

// ResolveKegAlias looks up an alias in the user's Tapper configuration and
// returns the resolved KegTarget. Behavior:
//
//   - Prefers an exact key match against cfg.Aliases.
//   - Falls back to a case-insensitive match if no exact key is found.
//   - If no user config is available or the alias cannot be found, a typed
//     AliasNotFoundError is returned.
//
// Note: This function only reads and validates the alias entry. It does not
// perform further resolution (for example preferring local paths or expanding
// other alias tokens); callers that need that behavior should load the full
// UserConfig and use ResolveKegTargetForRepo as appropriate.
func (cfg *UserConfig) ResolveKegAlias(alias string) (KegTarget, error) {
	// Try exact/key lookup first.
	if target, ok := cfg.Aliases[alias]; ok {
		return target, nil
	}

	// Case-insensitive fallback.
	if target, ok := cfg.Aliases[strings.ToLower(alias)]; ok {
		return target, nil
	}

	return KegTarget{}, terrs.NewAliasNotFoundError(alias)
}

func ResolveKegAlias(alias string) (KegTarget, error) {
	if alias == "" {
		return KegTarget{}, terrs.NewAliasNotFoundError(alias)
	}

	cfg, err := ReadUserConfig(ConfigAppName)
	if err != nil || cfg == nil {
		// No user config — treat as alias not found.
		return KegTarget{}, terrs.NewAliasNotFoundError(alias)
	}
	return cfg.ResolveKegAlias(alias)
}

// ReadLocalFile reads and parses a .tapper/local.yaml file into LocalConfig.
func ReadLocalFile(path string) (*LocalConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var lf LocalConfig
	if err := yaml.Unmarshal(b, &lf); err != nil {
		return nil, err
	}
	return &lf, nil
}

// WriteLocalFile writes LocalConfig to repoRoot/.tapper/local.yaml atomically.
// It will create the .tapper directory if needed.
func (lf *LocalConfig) WriteLocalFile(repoRoot string) error {
	if repoRoot == "" {
		return fmt.Errorf("repoRoot required")
	}
	dir := filepath.Join(repoRoot, DefaultLocalTapperDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	outPath := filepath.Join(dir, "local.yaml")
	// ensure updated timestamp if not provided
	if lf.Updated == "" {
		lf.Updated = time.Now().UTC().Format(time.RFC3339)
	}
	b, err := yaml.Marshal(lf)
	if err != nil {
		return err
	}
	// atomic write using temp file in same dir
	tmp := outPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	// best-effort fsync not implemented here (os.Rename is atomic on POSIX)
	if err := os.Rename(tmp, outPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (lf *LocalConfig) Touch() {
	lf.Updated = time.Now().UTC().Format(time.RFC3339)
}

// ReadUserConfig reads ~/.config/tapper/config.yaml (XDG) and returns parsed UserConfig.
// If the file doesn't exist or cannot be parsed, an error is returned.
func ReadUserConfig(appName string) (*UserConfig, error) {
	cfgDir, err := internal.GetConfigDir(appName)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(cfgDir, "config.yaml")
	return ReadUserConfigFrom(path)
}

func ReadUserConfigFrom(path string) (*UserConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var uc UserConfig
	if err := yaml.Unmarshal(b, &uc); err != nil {
		return nil, err
	}
	return &uc, nil
}

// WriteUserConfig writes the UserConfig to pathName atomically.
//
// Behavior:
//   - Validates receiver is non-nil and pathName is provided.
//   - Ensures the Updated field is set (RFC3339 UTC) if empty.
//   - Creates parent directory (os.MkdirAll) as needed.
//   - Marshals the config to YAML and writes it to a temporary file in the
//     same directory, then renames the temp file into place to provide an
//     atomic replacement on POSIX filesystems.
//   - Returns a wrapped error describing the failure (marshal, write, rename,
//     or directory creation). The temporary file is removed on failure when
//     possible.
//
// Note: This routine does not attempt an fsync of the directory after rename.
// Callers that require stronger durability guarantees should perform explicit
// fsyncs on the target filesystem where supported.
func (uc *UserConfig) WriteUserConfig(path string) error {
	if uc == nil {
		return fmt.Errorf("UserConfig is nil")
	}
	if path == "" {
		return fmt.Errorf("pathName required")
	}

	// ensure updated timestamp
	if uc.Updated == "" {
		uc.Updated = time.Now().UTC().Format(time.RFC3339)
	}

	dir := filepath.Dir(path)
	if dir == "" || dir == "." {
		// if caller provided a filename with no dir component, attempt to create the
		// current working directory (usually exists) — MkdirAll is a no-op if exists.
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("creating config dir %q: %w", dir, err)
	}

	b, err := yaml.Marshal(uc)
	if err != nil {
		return fmt.Errorf("marshal user config: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return fmt.Errorf("write temp config %q: %w", tmp, err)
	}

	// atomic replace
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %q -> %q: %w", tmp, path, err)
	}

	return nil
}

// findBestMapping evaluates all mappings in the user config against repoRoot and
// returns the best-matching mapping (highest priority, then most specific).
func (uc *UserConfig) findBestMapping(repoRoot string) (Mapping, bool) {
	var best Mapping

	bestScore := 0
	found := false

	for _, m := range uc.Mappings {
		ok := m.mappingMatches(repoRoot)
		if !ok {
			continue
		}
		if !found || m.Priority > bestScore {
			bestScore = m.Priority
			found = true
			best = m
		}
	}
	return best, found
}

// mappingMatches checks whether the mapping matches the given repository context.
// It requires at least one match criterion to be provided and returns true if
// any provided criterion matches. The function is intentionally conservative to
// avoid accidental global matches.
func (m *Mapping) mappingMatches(repoRoot string) bool {
	mm := m.Match
	criteriaProvided := mm.PathPrefix != "" || mm.PathRegex != ""
	if !criteriaProvided {
		return false
	}

	// path_prefix: treat as absolute if absolute, otherwise interpret relative to repoRoot.
	if mm.PathPrefix != "" {
		prefix := mm.PathPrefix
		if !filepath.IsAbs(prefix) {
			prefix = filepath.Join(repoRoot, prefix)
		}
		// Clean both sides for a stable prefix comparison.
		if strings.HasPrefix(filepath.Clean(repoRoot), filepath.Clean(prefix)) {
			return true
		}
	}

	// path_regex: treat the configured regex as matching against repoRoot.
	if mm.PathRegex != "" {
		pattern := mm.PathRegex
		ok, _ := regexp.MatchString(pattern, repoRoot)
		if ok {
			return true
		}
	}

	// No provided criteria matched.
	return false
}

// LocalGitConfigData attempts to run `git -C repoRoot config --local --get key`.
// If git isn't present or the command fails it returns an error.
func LocalGitConfigData(repoRoot, key string) ([]byte, error) {
	// check git exists
	if _, err := exec.LookPath("git"); err != nil {
		return []byte{}, err
	}
	cmd := exec.Command("git", "-C", repoRoot, "config", "--local", "--get", key)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return []byte{}, err
	}
	return bytes.TrimSpace(out.Bytes()), nil
}

// ExpandEnv expands environment variables in the resolved target fields.
// It replaces ${VAR} or $VAR occurrences in Target.URL, Target.Path and
// Target.Alias using os.ExpandEnv. If the receiver is nil this is a no-op.
func (target *KegTarget) ExpandEnv() {
	target.Alias = os.ExpandEnv(target.Alias)
	target.Path = os.ExpandEnv(target.Path)
	target.URL = os.ExpandEnv(target.URL)
}

func (target *KegTarget) IsEmpty() bool {
	if target.Alias != "" {
		return false
	}
	if target.Path != "" {
		return false
	}
	if target.Value != "" {
		return false
	}
	if target.URL != "" {
		return false
	}
	return true
}
