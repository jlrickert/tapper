package keg

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/schemas"
	"gopkg.in/yaml.v3"
)

// KegSettingsSchemaURL is the published JSON Schema for keg settings YAML.
// It is the $id of the schema and the fallback modeline target; the modeline
// itself is added only when keg settings are opened in an editor (see
// Tap.KegSettingsEdit), never by the serializers below — keg settings are
// persisted, and on a hub they are shared, so a modeline naming one machine's
// filesystem has no business in the stored document.
const KegSettingsSchemaURL = schemas.KegSettingsURL

// SettingsV1 represents the initial version of the KEG settings specification.
type SettingsV1 struct {
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

// SettingsV2 represents the second (current) version of the KEG settings
// specification. It extends V1 with additional fields such as Links.
type SettingsV2 struct {
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

	// ListFields are the field selectors a node listing shows by default, in
	// the vocabulary of ParseFieldSelector: a bare word names a metadata key
	// ("type", "subkind"), a leading dot names a statistics field (".omega"),
	// and "id", "title", and "tags" are reserved. One setting drives both the
	// default `tap list` format and the columns of the hosted node list, so a
	// keg presents the same shape everywhere. Empty means the built-in default.
	ListFields []string `yaml:"listFields,omitempty" json:"list_fields,omitempty"`

	// Timezone is the IANA timezone for resolving ambiguous timestamps
	// within this keg (e.g. "America/Chicago"). Defaults to "UTC".
	Timezone string `yaml:"timezone,omitempty" json:"timezone,omitempty"`

	// Snapshots controls automatic snapshot behavior for this keg.
	Snapshots *SnapshotSettings `yaml:"snapshots,omitempty" json:"snapshots,omitempty"`

	// SchemaPolicy controls actor validation modes. Strict adds explicit schema
	// selection to live nonzero-node writes whose resolved mode is block.
	SchemaPolicy *SchemaPolicy `yaml:"schemaPolicy,omitempty" json:"schemaPolicy,omitempty"`

	path string
	raw  []byte
	hash string
}

const (
	SnapshotModeAuto = "auto"
	SnapshotModeOff  = "off"

	DefaultSnapshotIdleAfter = time.Hour
)

// SnapshotSettings holds per-keg automatic snapshot policy settings.
type SnapshotSettings struct {
	// Mode controls whether the hub should create idle snapshots automatically.
	// Supported values are "auto" and "off".
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`

	// IdleAfter is a Go-style duration string. Nodes become eligible for auto
	// snapshots only after their last edit has been idle for at least this long.
	IdleAfter string `yaml:"idleAfter,omitempty" json:"idleAfter,omitempty"`
}

// DefaultSnapshotSettings returns the default automatic snapshot settings.
func DefaultSnapshotSettings() *SnapshotSettings {
	return &SnapshotSettings{
		Mode:      SnapshotModeAuto,
		IdleAfter: formatSnapshotDuration(DefaultSnapshotIdleAfter),
	}
}

// SnapshotPolicy resolves the effective snapshot policy for this settings.
func (kc *Settings) SnapshotPolicy() (mode string, idleAfter time.Duration, err error) {
	if kc == nil {
		return SnapshotModeAuto, DefaultSnapshotIdleAfter, nil
	}
	cfg := kc.Snapshots
	if cfg == nil {
		cfg = DefaultSnapshotSettings()
	}
	return cfg.policy()
}

func (sc *SnapshotSettings) policy() (string, time.Duration, error) {
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

// LinkEntry represents a named link in the KEG settings.
type LinkEntry struct {
	Alias string `yaml:"alias" json:"alias"` // Alias for the link
	URL   string `yaml:"url" json:"url"`     // URL of the link
}

// IndexEntry represents an entry in the indexes list in the KEG settings.
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

// Settings is the latest version of the keg settings document.
type Settings = SettingsV2

// toV2 converts a SettingsV1 value to the SettingsV2 representation.
func (c *SettingsV1) toV2() *SettingsV2 {
	return &SettingsV2{
		Kegv:      SettingsV2VersionString,
		Updated:   c.Updated,
		Title:     c.Title,
		URL:       c.URL,
		Creator:   c.Creator,
		State:     c.State,
		Summary:   c.Summary,
		Links:     nil, // No links in v1, so leave as nil
		Indexes:   c.Indexes,
		Snapshots: DefaultSnapshotSettings(),
		path:      "",
	}
}

type SettingsOption = func(cfg *Settings)

func NewSettings(options ...SettingsOption) *Settings {
	cfg := &Settings{
		Kegv:    SettingsV2VersionString,
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
		Timezone:     "UTC",
		Snapshots:    DefaultSnapshotSettings(),
		SchemaPolicy: &SchemaPolicy{Strict: true},
		Indexes:      SystemIndexEntries(),
	}
	for _, f := range options {
		f(cfg)
	}
	cfg.materializeSystemIndexes()
	return cfg
}

// ParseKegSettings parses raw YAML settings data into the latest Settings version.
// It detects the "kegv" version field and performs migration from earlier
// versions when necessary.
func ParseKegSettings(data []byte) (*Settings, error) {
	cfg, err := parseKegSettings(data, false)
	if cfg != nil {
		cfg.raw = append([]byte(nil), data...)
	}
	return cfg, err
}

// ParseKegSettingsStrict parses raw user-supplied settings data for persistence.
// It rejects user-defined index entries that collide with required system
// indexes or duplicate another user index.
func ParseKegSettingsStrict(data []byte) (*Settings, error) {
	cfg, err := parseKegSettings(data, true)
	if cfg != nil {
		cfg.raw = append([]byte(nil), data...)
	}
	return cfg, err
}

func parseKegSettings(data []byte, strict bool) (*Settings, error) {
	var settingsV2 SettingsV2

	// Detect version by unmarshaling into a generic map
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return &settingsV2, fmt.Errorf("failed to parse keg data: %w", errors.Join(ErrParse, err))
	}

	// Check for "kegv" version field
	version, ok := raw["kegv"].(string)
	if !ok {
		return &settingsV2, fmt.Errorf("missing or invalid kegv version field")
	}

	switch version {
	case SettingsV1VersionString:
		var settingsV1 SettingsV1
		if err := yaml.Unmarshal(data, &settingsV1); err != nil {
			return &settingsV2, err
		}
		cfg := settingsV1.toV2()
		cfg.applyDefaults()
		if err := cfg.validateSnapshots(); err != nil {
			return cfg, err
		}
		if err := cfg.validateListFields(); err != nil {
			return cfg, err
		}
		if err := cfg.normalizeIndexes(strict); err != nil {
			return cfg, err
		}
		return cfg, nil
	case SettingsV2VersionString:
		if err := yaml.Unmarshal(data, &settingsV2); err != nil {
			return &settingsV2, err
		}
	default:
		return &settingsV2, fmt.Errorf("unsupported settings version: %s", version)
	}

	settingsV2.applyDefaults()
	if err := settingsV2.validateSnapshots(); err != nil {
		return &settingsV2, err
	}
	if err := settingsV2.validateListFields(); err != nil {
		return &settingsV2, err
	}
	if err := settingsV2.normalizeIndexes(strict); err != nil {
		return &settingsV2, err
	}
	return &settingsV2, nil
}

// applyDefaults fills in zero-value fields with their documented defaults.
func (kc *SettingsV2) applyDefaults() {
	if kc.Timezone == "" {
		kc.Timezone = "UTC"
	}
	if kc.Snapshots == nil {
		kc.Snapshots = DefaultSnapshotSettings()
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

func (kc *SettingsV2) validateSnapshots() error {
	if kc == nil {
		return nil
	}
	_, _, err := kc.SnapshotPolicy()
	return err
}

// validateListFields rejects an unusable selector when the settings is parsed
// rather than when a listing is rendered, so a typo surfaces at the point of
// editing instead of silently blanking a column later.
func (kc *SettingsV2) validateListFields() error {
	if kc == nil || len(kc.ListFields) == 0 {
		return nil
	}
	if _, err := ParseFieldSelectors(kc.ListFields); err != nil {
		return fmt.Errorf("listFields: %w", err)
	}
	return nil
}

func (kc *SettingsV2) normalizeIndexes(strict bool) error {
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

func (kc *SettingsV2) materializeSystemIndexes() {
	_ = kc.normalizeIndexes(false)
}

// MaterializeSystemIndexes ensures required system indexes are present in the
// runtime settings view and removes any legacy persisted declarations of those
// indexes.
func (kc *SettingsV2) MaterializeSystemIndexes() {
	kc.materializeSystemIndexes()
}

func (kc *SettingsV2) persistedCopy() (*SettingsV2, error) {
	if kc == nil {
		return nil, fmt.Errorf("settings is nil")
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

// UserIndexEntries returns the user-defined indexes from a runtime settings.
func (kc *Settings) UserIndexEntries() []IndexEntry {
	if kc == nil {
		return nil
	}
	user, _ := userIndexEntries(kc.Indexes, false)
	return user
}

// Location returns the *time.Location for the configured Timezone.
// It returns time.UTC if the Timezone field is empty or invalid.
func (kc *Settings) Location() *time.Location {
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

func (kc *Settings) ResolveAlias(alias string) (*Target, error) {
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

// ToYAML serializes the Settings to YAML. The result is what gets persisted —
// to the on-disk `keg` file, or over the wire to a hub — so it carries no
// schema modeline. Editors get one added on open; see Tap.KegSettingsEdit.
func (kc *Settings) ToYAML() ([]byte, error) {
	persisted, err := kc.persistedCopy()
	if err != nil {
		return nil, err
	}
	return yaml.Marshal(persisted)
}

// Raw returns the exact document representation read from storage. Settings
// constructed in memory fall back to their canonical YAML representation.
func (kc *Settings) Raw() []byte {
	if kc == nil {
		return nil
	}
	if kc.raw != nil {
		return append([]byte(nil), kc.raw...)
	}
	out, _ := kc.ToYAML()
	return out
}

// Hash returns the optimistic-concurrency token associated with Raw.
func (kc *Settings) Hash() string {
	if kc == nil {
		return ""
	}
	return kc.hash
}

func (kc *Settings) setDocument(raw []byte, hash string) {
	kc.raw = append([]byte(nil), raw...)
	kc.hash = hash
}

// ToJSON serializes the Settings to JSON.
func (kc *Settings) ToJSON() ([]byte, error) {
	persisted, err := kc.persistedCopy()
	if err != nil {
		return nil, err
	}
	return json.Marshal(persisted)
}

func (kc *Settings) String() string {
	out, _ := kc.ToYAML()
	return string(out)
}

func (kc *Settings) Touch(t time.Time) {
	kc.Updated = t.Format(time.RFC3339)
}
