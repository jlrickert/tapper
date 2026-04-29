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

	// Links holds a list of LinkEntry objects representing related links or
	// references in the configuration.
	Links []LinkEntry `yaml:"links,omitempty"`

	// Indexes is a list of index entries that link to related files or nodes.
	Indexes []IndexEntry `yaml:"indexes,omitempty"`

	Entities map[string]EntityEntry `yaml:"entities,omitempty"`

	Tags map[string]string `yaml:"tags,omitempty"`

	// Timezone is the IANA timezone for resolving ambiguous timestamps
	// within this keg (e.g. "America/Chicago"). Defaults to "UTC".
	Timezone string `yaml:"timezone,omitempty"`

	// Doctor holds `tap doctor` check configuration.
	Doctor *DoctorConfig `yaml:"doctor,omitempty"`

	// Site holds static site generation defaults for `tap site`.
	Site *SiteConfig `yaml:"site,omitempty"`

	path string
}

// SiteConfig holds static site generation defaults stored in the keg config.
type SiteConfig struct {
	// Output is the default output directory.
	Output string `yaml:"output,omitempty" json:"output,omitempty"`
	// Title is the site title.
	Title string `yaml:"title,omitempty" json:"title,omitempty"`
	// BaseURL is the base URL prefix for absolute links.
	BaseURL string `yaml:"baseUrl,omitempty" json:"baseUrl,omitempty"`
	// Search enables or disables Pagefind search indexing.
	Search *bool `yaml:"search,omitempty" json:"search,omitempty"`
}

// DoctorConfig holds options that control which checks `tap doctor` performs.
type DoctorConfig struct {
	// EntityCheck enables per-node entity attribute validation.
	// When true, doctor reports nodes that lack an `entity` attribute in meta.
	EntityCheck bool `yaml:"entityCheck,omitempty" json:"entityCheck,omitempty"`

	// TagCheck enables per-node tag validation against the keg config's tag map.
	// When true, doctor warns about tags used in node metadata that are not
	// documented in the keg config.
	TagCheck bool `yaml:"tagCheck,omitempty" json:"tagCheck,omitempty"`
}

// LinkEntry represents a named link in the KEG configuration.
type LinkEntry struct {
	Alias string `json:"alias"` // Alias for the link
	URL   string `json:"url"`   // URL of the link
}

// IndexEntry represents an entry in the indexes list in the KEG configuration.
//
// File is the bare filename of the generated index artifact, e.g. "backlinks"
// or "concepts.md". The on-disk path is always under the keg's dex/ directory
// (the prefix is implicit and applied at write time). For backward
// compatibility, a leading "dex/" in the parsed YAML is stripped during
// ParseKegConfig and applyDefaults so callers consistently see the bare form.
//
// The Query field holds a boolean query expression used to filter index
// contents (tag names, key=value attribute predicates, boolean operators).
// The deprecated Tags field is accepted for backward compatibility; Query
// takes precedence when both are present.
type IndexEntry struct {
	File    string `yaml:"file"`
	Summary string `yaml:"summary"`
	Query   string `yaml:"query,omitempty"` // boolean query expression; omit for core/unfiltered indexes
	Tags    string `yaml:"tags,omitempty"`  // deprecated: use query instead
	Sort    string `yaml:"sort,omitempty"`  // sort order for query-filtered indexes: "updated" (default), "id", "created", "accessed"
}

// normalizeIndexFile returns the bare filename for an index entry, stripping
// a leading "dex/" prefix if present. This canonicalizes both the legacy
// prefixed form ("dex/backlinks") and the new bare form ("backlinks") to a
// single representation in the in-memory struct.
func normalizeIndexFile(file string) string {
	return strings.TrimPrefix(file, "dex/")
}

// normalizeIndexEntries canonicalizes the File field of each entry by
// stripping any leading "dex/" prefix.
func normalizeIndexEntries(entries []IndexEntry) {
	for i := range entries {
		entries[i].File = normalizeIndexFile(entries[i].File)
	}
}

// QueryOrTags returns the effective query string for the index entry. It
// prefers Query when set, falling back to the deprecated Tags field.
func (ie *IndexEntry) QueryOrTags() string {
	if ie.Query != "" {
		return ie.Query
	}
	return ie.Tags
}

type EntityEntry struct {
	ID      int    `yaml:"id"`
	Summary string `yaml:"summary"`
}

// Config KegConfig is an alias for the latest configuration version. Update this alias
// when introducing a newer configuration version.
type Config = ConfigV2

// toV2 converts a ConfigV1 value to the ConfigV2 representation.
func (c *ConfigV1) toV2() *ConfigV2 {
	return &ConfigV2{
		Kegv:     ConfigV2VersionString,
		Updated:  c.Updated,
		Title:    c.Title,
		URL:      c.URL,
		Creator:  c.Creator,
		State:    c.State,
		Summary:  c.Summary,
		Links:    nil, // No links in v1, so leave as nil
		Indexes:  c.Indexes,
		Entities: nil,
		Tags:     nil,
		path:     "",
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
		Timezone: "UTC",
		Indexes: []IndexEntry{
			{
				File: "backlinks", Summary: "all incoming links",
			},
			{
				File: "changes.md", Summary: "latest changes",
			},
			{
				File: "links", Summary: "all outgoing links",
			},
			{
				File: "nodes.tsv", Summary: "all nodes by id",
			},
			{
				File: "tags", Summary: "all tags",
			},
		},
	}
	for _, f := range options {
		f(cfg)
	}
	return cfg
}

// ParseKegConfig parses raw YAML config data into the latest Config version.
// It detects the "kegv" version field and performs migration from earlier
// versions when necessary.
func ParseKegConfig(data []byte) (*Config, error) {
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
		return cfg, nil
	case ConfigV2VersionString:
		if err := yaml.Unmarshal(data, &configV2); err != nil {
			return &configV2, err
		}
	default:
		return &configV2, fmt.Errorf("unsupported config version: %s", version)
	}

	configV2.applyDefaults()
	return &configV2, nil
}

// applyDefaults fills in zero-value fields with their documented defaults
// and normalizes index entry filenames to the canonical bare form.
func (kc *ConfigV2) applyDefaults() {
	if kc.Timezone == "" {
		kc.Timezone = "UTC"
	}
	normalizeIndexEntries(kc.Indexes)
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
	body, err := yaml.Marshal(kc)
	if err != nil {
		return nil, err
	}
	return append([]byte(kegConfigSchemaModeline), body...), nil
}

// ToJSON serializes the Config to JSON.
func (kc *Config) ToJSON() ([]byte, error) {
	return json.Marshal(kc)
}

func (kc *Config) String() string {
	out, _ := kc.ToYAML()
	return string(out)
}

func (kc *Config) Touch(t time.Time) {
	kc.Updated = t.Format(time.RFC3339)
}

// AddEntity adds or updates an entity entry by entity name.
func (kc *Config) AddEntity(name string, id int, summary string) error {
	if kc == nil {
		return fmt.Errorf("config is nil")
	}

	name = strings.TrimSpace(name)
	summary = strings.TrimSpace(summary)
	if name == "" {
		return fmt.Errorf("entity name is required")
	}
	if id <= 0 {
		return fmt.Errorf("entity id must be greater than zero")
	}

	if kc.Entities == nil {
		kc.Entities = map[string]EntityEntry{}
	}
	kc.Entities[name] = EntityEntry{
		ID:      id,
		Summary: summary,
	}
	return nil
}

// AddTag adds or updates a tag summary by tag name.
func (kc *Config) AddTag(name, summary string) error {
	if kc == nil {
		return fmt.Errorf("config is nil")
	}
	name = strings.TrimSpace(name)
	summary = strings.TrimSpace(summary)
	if name == "" {
		return fmt.Errorf("tag name is required")
	}
	if summary == "" {
		return fmt.Errorf("tag summary is required")
	}

	if kc.Tags == nil {
		kc.Tags = map[string]string{}
	}
	kc.Tags[name] = summary
	return nil
}
