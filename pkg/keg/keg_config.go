package keg

// Package config provides versioned configuration management for the KEG
// application. It supports loading, parsing, converting, and accessing
// configuration data with environment variable expansion and version
// migration.

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	KegConfigSchemaURL      = "https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/keg-config.json"
	kegConfigSchemaModeline = "# yaml-language-server: $schema=" + KegConfigSchemaURL + "\n"
)

// ConfigV1 KegConfigV1 represents the initial version of the KEG configuration
// specification.
type ConfigV1 struct {
	// Kegv is the version of the specification.
	Kegv string `yaml:"kegv"`

	// Updated indicates when the keg was last indexed.
	Updated string `yaml:"updated,omitempty"`

	// Title is the title of the KEG worklog or project.
	Title string `yaml:"title,omitempty"`

	// URL is the main URL where the KEG can be found.
	URL string `yaml:"url,omitempty"`

	// Creator is the URL or identifier of the creator of the KEG.
	Creator string `yaml:"creator,omitempty"`

	// State indicates the current state of the KEG (e.g., living, archived).
	State string `yaml:"state,omitempty"`

	// Summary provides a brief description or summary of the KEG content.
	Summary string `yaml:"summary,omitempty"`

	// Indexes is a list of index entries that link to related files or nodes.
	Indexes []IndexEntry `yaml:"indexes,omitempty"`

	path string
}

// ConfigV2 KegConfigV2 represents the second (current) version of the KEG configuration
// specification. It extends V1 with additional fields such as Links.
type ConfigV2 struct {
	// Kegv is the version of the specification.
	Kegv string `yaml:"kegv" json:"kegv"`

	// Updated indicates when the keg was last indexed.
	Updated string `yaml:"updated,omitempty" json:"updated,omitempty"`

	// Title is the title of the KEG worklog or project.
	Title string `yaml:"title,omitempty" json:"title,omitempty"`

	// URL is the main URL where the KEG can be found.
	URL string `yaml:"url,omitempty" json:"url,omitempty"`

	// Creator is the URL or identifier of the creator of the KEG.
	Creator string `yaml:"creator,omitempty" json:"creator,omitempty"`

	// State indicates the current state of the KEG (e.g., living, archived).
	State string `yaml:"state,omitempty" json:"state,omitempty"`

	// Summary provides a brief description or summary of the KEG content.
	Summary string `yaml:"summary,omitempty" json:"summary,omitempty"`

	// Instructions are KEG-level guidance shown to agents when orienting to
	// this keg.
	Instructions string `yaml:"instructions,omitempty" json:"instructions,omitempty"`

	// Links holds a list of LinkEntry objects representing related links or
	// references in the configuration.
	Links []LinkEntry `yaml:"links,omitempty" json:"links,omitempty"`

	// Indexes is a list of index entries that link to related files or nodes.
	Indexes []IndexEntry `yaml:"indexes,omitempty" json:"indexes,omitempty"`

	// Timezone is the IANA timezone for resolving ambiguous timestamps
	// within this keg (e.g. "America/Chicago"). Defaults to "UTC".
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`

	// Snapshots controls automatic snapshot behavior for this keg.
	Snapshots *SnapshotConfig `yaml:"snapshots,omitempty" json:"snapshots,omitempty"`

	// SchemaPolicy controls whether schema validation warns, blocks, or is
	// disabled for different write actors. When omitted, human writes warn while
	// API/agent/import/restore writes block.
	SchemaPolicy *SchemaPolicy `yaml:"schemaPolicy,omitempty" json:"schemaPolicy,omitempty"`

	path string
}

const (
	SnapshotModeAuto = "auto"
	SnapshotModeOff  = "off"

	DefaultSnapshotIdleAfter = time.Hour
)

// SnapshotConfig holds per-keg automatic snapshot policy settings.
type SnapshotConfig struct {
	// Mode controls whether the hub should create idle snapshots automatically.
	// Supported values are "auto" and "off".
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	// IdleAfter is a Go-style duration string. Nodes become eligible for auto
	// snapshots only after their last edit has been idle for at least this long.
	IdleAfter string `yaml:"idleAfter,omitempty" json:"idleAfter,omitempty"`
}

func DefaultSnapshotConfig() *SnapshotConfig {
	return &SnapshotConfig{
		Mode:      SnapshotModeAuto,
		IdleAfter: formatSnapshotDuration(DefaultSnapshotIdleAfter),
	}
}

// SnapshotPolicy resolves the effective snapshot policy for this config.
func (kc *Config) SnapshotPolicy() (mode string, idleAfter time.Duration, err error) {
	if kc == nil {
		return SnapshotModeAuto, DefaultSnapshotIdleAfter, nil
	}
	cfg := kc.Snapshots
	if cfg == nil {
		cfg = DefaultSnapshotConfig()
	}
	return cfg.policy()
}

func (sc *SnapshotConfig) policy() (string, time.Duration, error) {
	mode := SnapshotModeAuto
	idleAfter := formatSnapshotDuration(DefaultSnapshotIdleAfter)
	if sc != nil {
		if strings.TrimSpace(sc.Mode) != "" {
			mode = strings.TrimSpace(sc.Mode)
		}
		if strings.TrimSpace(sc.IdleAfter) != "" {
			idleAfter = strings.TrimSpace(sc.IdleAfter)
		}
	}
	if mode != SnapshotModeAuto && mode != SnapshotModeOff {
		return "", 0, fmt.Errorf("invalid snapshots.mode %q: expected %q or %q", mode, SnapshotModeAuto, SnapshotModeOff)
	}
	duration, err := time.ParseDuration(idleAfter)
	if err != nil {
		return "", 0, fmt.Errorf("invalid snapshots.idleAfter %q: %w", idleAfter, err)
	}
	if duration <= 0 {
		return "", 0, fmt.Errorf("snapshots.idleAfter must be greater than zero")
	}
	return mode, duration, nil
}

func formatSnapshotDuration(d time.Duration) string {
	if d%time.Hour == 0 {
		return fmt.Sprintf("%dh", int64(d/time.Hour))
	}
	if d%time.Minute == 0 {
		return fmt.Sprintf("%dm", int64(d/time.Minute))
	}
	if d%time.Second == 0 {
		return fmt.Sprintf("%ds", int64(d/time.Second))
	}
	return d.String()
}

// LinkEntry represents a named link in the KEG configuration.
type LinkEntry struct {
	Alias string `yaml:"alias" json:"alias"` // Alias for the link
	URL   string `yaml:"url" json:"url"`     // URL of the link
}

// IndexEntry represents an entry in the indexes list in the KEG configuration.
//
// File is the bare filename of the generated index artifact, e.g. "backlinks"
// or "concepts.md". The on-disk path is always under the keg's dex/ directory
// (the prefix is implicit and applied at write time).
//
// The Query field holds a boolean query expression used to filter index
// contents (tag names, key=value attribute predicates, boolean operators).
type IndexEntry struct {
	File    string `yaml:"file" json:"file"`
	Summary string `yaml:"summary" json:"summary"`
	Query   string `yaml:"query,omitempty" json:"query,omitempty"` // boolean query expression; omit for core/unfiltered indexes
	Sort    string `yaml:"sort,omitempty" json:"sort,omitempty"`   // sort order for query-filtered indexes: "updated" (default), "id", "created", "accessed"
}

// Config KegConfig is an alias for the latest configuration version. Update this alias
// when introducing a newer configuration version.
type Config = ConfigV2

// toV2 converts a ConfigV1 value to the ConfigV2 representation.
func (c *ConfigV1) toV2() *ConfigV2 {
	return &ConfigV2{
		Kegv:      ConfigV2VersionString,
		Updated:   c.Updated,
		Title:     c.Title,
		URL:       c.URL,
		Creator:   c.Creator,
		State:     c.State,
		Summary:   c.Summary,
		Links:     nil, // No links in v1, so leave as nil
		Indexes:   c.Indexes,
		Snapshots: DefaultSnapshotConfig(),
		path:      "",
	}
}

type ConfigOption = func(cfg *Config)

func NewConfig(options ...ConfigOption) *Config {
	cfg := &Config{
		Kegv:    ConfigV2VersionString,
		Updated: "2025-08-19 12:54:28Z",
		Title:   "My KEG",
		URL:     "git@github.com:YOU/keg.git",
		Creator: "git@github.com:YOU/YOU.git",
		State:   "living",
		Summary: `A Knowledge Exchange Graph (KEG). Each numbered directory is a node
	containing a README.md (content), meta.yaml (metadata), and stats.json
	(programmatic stats).

	Getting started:
	- Edit this summary to describe your keg's purpose.
	- Update the url and creator fields to point to your keg's repo and
	  your profile.
		- The zero node (0/) is a placeholder for planned content.
		- Indices under dex/ are generated automatically by keg tooling.
		- Use tags in node meta.yaml to organize and filter content.`,
		Timezone:  "UTC",
		Snapshots: DefaultSnapshotConfig(),
		Indexes:   SystemIndexEntries(),
	}
	for _, f := range options {
		f(cfg)
	}
	cfg.materializeSystemIndexes()
	return cfg
}

// ParseKegConfig parses raw YAML config data into the latest Config version.
// It detects the "kegv" version field and performs migration from earlier
// versions when necessary.
func ParseKegConfig(data []byte) (*Config, error) {
	return parseKegConfig(data, false)
}

// ParseKegConfigStrict parses raw user-supplied config data for persistence.
// It rejects user-defined index entries that collide with required system
// indexes or duplicate another user index.
func ParseKegConfigStrict(data []byte) (*Config, error) {
	return parseKegConfig(data, true)
}

func parseKegConfig(data []byte, strict bool) (*Config, error) {
	var configV2 ConfigV2

	// Detect version by unmarshaling into a generic map
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return &configV2, fmt.Errorf("failed to parse keg data: %w", errors.Join(ErrParse, err))
	}

	// Check for "kegv" version field
	version, ok := raw["kegv"].(string)
	if !ok {
		return &configV2, fmt.Errorf("missing or invalid kegv version field")
	}

	switch version {
	case ConfigV1VersionString:
		var configV1 ConfigV1
		if err := yaml.Unmarshal(data, &configV1); err != nil {
			return &configV2, err
		}
		cfg := configV1.toV2()
		cfg.applyDefaults()
		if err := cfg.validateSnapshots(); err != nil {
			return cfg, err
		}
		if err := cfg.normalizeIndexes(strict); err != nil {
			return cfg, err
		}
		return cfg, nil
	case ConfigV2VersionString:
		if err := yaml.Unmarshal(data, &configV2); err != nil {
			return &configV2, err
		}
	default:
		return &configV2, fmt.Errorf("unsupported config version: %s", version)
	}

	configV2.applyDefaults()
	if err := configV2.validateSnapshots(); err != nil {
		return &configV2, err
	}
	if err := configV2.normalizeIndexes(strict); err != nil {
		return &configV2, err
	}
	return &configV2, nil
}

// applyDefaults fills in zero-value fields with their documented defaults.
func (kc *ConfigV2) applyDefaults() {
	if kc.Timezone == "" {
		kc.Timezone = "UTC"
	}
	if kc.Snapshots == nil {
		kc.Snapshots = DefaultSnapshotConfig()
		return
	}
	if strings.TrimSpace(kc.Snapshots.Mode) == "" {
		kc.Snapshots.Mode = SnapshotModeAuto
	} else {
		kc.Snapshots.Mode = strings.TrimSpace(kc.Snapshots.Mode)
	}
	if strings.TrimSpace(kc.Snapshots.IdleAfter) == "" {
		kc.Snapshots.IdleAfter = formatSnapshotDuration(DefaultSnapshotIdleAfter)
	} else {
		kc.Snapshots.IdleAfter = strings.TrimSpace(kc.Snapshots.IdleAfter)
	}
}

func (kc *ConfigV2) validateSnapshots() error {
	if kc == nil {
		return nil
	}
	_, _, err := kc.SnapshotPolicy()
	return err
}

func (kc *ConfigV2) normalizeIndexes(strict bool) error {
	if kc == nil {
		return nil
	}
	user, err := userIndexEntries(kc.Indexes, strict)
	if err != nil {
		return err
	}
	kc.Indexes = append(SystemIndexEntries(), user...)
	return nil
}

func (kc *ConfigV2) materializeSystemIndexes() {
	_ = kc.normalizeIndexes(false)
}

// MaterializeSystemIndexes ensures required system indexes are present in the
// runtime config view and removes any legacy persisted declarations of those
// indexes.
func (kc *ConfigV2) MaterializeSystemIndexes() {
	kc.materializeSystemIndexes()
}

func (kc *ConfigV2) persistedCopy() (*ConfigV2, error) {
	if kc == nil {
		return nil, fmt.Errorf("config is nil")
	}
	out := *kc
	out.applyDefaults()
	user, err := userIndexEntries(kc.Indexes, false)
	if err != nil {
		return nil, err
	}
	out.Indexes = user
	return &out, nil
}

func userIndexEntries(entries []IndexEntry, strict bool) ([]IndexEntry, error) {
	out := make([]IndexEntry, 0, len(entries))
	seen := map[string]struct{}{}
	for _, entry := range entries {
		name := strings.TrimSpace(entry.File)
		entry.File = name
		if name == "" {
			continue
		}
		if IsSystemIndex(name) {
			if strict {
				return nil, fmt.Errorf("index %q is a required system index and cannot be configured", name)
			}
			continue
		}
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("duplicate user index %q", name)
		}
		seen[name] = struct{}{}
		out = append(out, entry)
	}
	return out, nil
}

// SystemIndexEntries returns the required indexes that every keg has at
// runtime. Callers receive a fresh slice so entries can be appended safely.
func SystemIndexEntries() []IndexEntry {
	return []IndexEntry{
		{File: "nodes.tsv", Summary: "all nodes by id"},
		{File: "changes.md", Summary: "latest changes"},
		{File: "tags", Summary: "all tags"},
		{File: "links", Summary: "all outgoing links"},
		{File: "backlinks", Summary: "all incoming links"},
		{File: "timeline", Summary: "snapshot timeline"},
		{File: "dirty", Summary: "nodes changed since latest snapshot"},
	}
}

// IsSystemIndex reports whether name is a required generated index.
func IsSystemIndex(name string) bool {
	switch strings.TrimSpace(name) {
	case "nodes.tsv", "changes.md", "tags", "links", "backlinks", "timeline", "dirty":
		return true
	default:
		return false
	}
}

// UserIndexEntries returns the user-defined indexes from a runtime config.
func (kc *Config) UserIndexEntries() []IndexEntry {
	if kc == nil {
		return nil
	}
	user, _ := userIndexEntries(kc.Indexes, false)
	return user
}

// Location returns the *time.Location for the configured Timezone.
// It returns time.UTC if the Timezone field is empty or invalid.
func (kc *Config) Location() *time.Location {
	tz := kc.Timezone
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

func (kc *Config) ResolveAlias(alias string) (*Target, error) {
	for _, entry := range kc.Links {
		if alias == entry.Alias {
			kt, err := Parse(entry.URL)
			if err != nil {
				return nil, fmt.Errorf("could resolve alias: %w", err)
			}
			return kt, nil
		}
	}
	return nil, fmt.Errorf("alias %s not found: %w", alias, ErrNotExist)
}

// ToYAML serializes the Config to YAML.
func (kc *Config) ToYAML() ([]byte, error) {
	persisted, err := kc.persistedCopy()
	if err != nil {
		return nil, err
	}
	body, err := yaml.Marshal(persisted)
	if err != nil {
		return nil, err
	}
	return append([]byte(kegConfigSchemaModeline), body...), nil
}

// ToJSON serializes the Config to JSON.
func (kc *Config) ToJSON() ([]byte, error) {
	persisted, err := kc.persistedCopy()
	if err != nil {
		return nil, err
	}
	return json.Marshal(persisted)
}

func (kc *Config) String() string {
	out, _ := kc.ToYAML()
	return string(out)
}

func (kc *Config) Touch(t time.Time) {
	kc.Updated = t.Format(time.RFC3339)
}
