package keg

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"gopkg.in/yaml.v3"
)

// MetaCodec is an abstraction over meta serialization formats. It mirrors the
// ContentExtractor pattern used for content parsing: multiple codecs can be
// provided and attempted in a deterministic order.
type MetaCodec interface {
	// Parse parses bytes into a generic map representation.
	Parse(data []byte) (map[string]any, error)
	// Marshal serializes the map back to bytes in this codec's format.
	Marshal(m map[string]any) ([]byte, error)
	// Name returns a short format name (e.g., "yaml", "json").
	Name() string
}

type yamlCodec struct{}

var _ MetaCodec = (*yamlCodec)(nil)

// yamlCodec.Parse parses YAML bytes into a map[string]any.
func (yamlCodec) Parse(data []byte) (map[string]any, error) {
	var out map[string]any
	if err := yaml.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// yamlCodec.Marshal serializes a map[string]any to YAML bytes.
func (yamlCodec) Marshal(m map[string]any) ([]byte, error) {
	return yaml.Marshal(m)
}

// yamlCodec.Name returns the codec name "yaml".
func (yamlCodec) Name() string { return "yaml" }

type jsonCodec struct{}

var _ MetaCodec = (*jsonCodec)(nil)

// jsonCodec.Parse parses JSON bytes into a map[string]any.
func (jsonCodec) Parse(data []byte) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// jsonCodec.Marshal serializes a map[string]any to JSON bytes.
func (jsonCodec) Marshal(m map[string]any) ([]byte, error) {
	return json.Marshal(m)
}

// jsonCodec.Name returns the codec name "json".
func (jsonCodec) Name() string { return "json" }

// Meta is a simple wrapper around the raw YAML/JSON meta mapping.
// It preserves unknown fields and provides small helpers for:
// - reading/writing top-level keys (path Get/Set/Delete)
// - reading/updating canonical top-level "tags" only
// - parsing/setting the updated timestamp
//
// NOTE: This implementation attempts to preserve the original bytes supplied
// to ParseMeta when no modifications are made. ParseMeta detects whether the
// input was JSON or YAML and records the original format. ToYAML and ToJSON
// will return the original bytes verbatim only when the Meta is unmodified and
// the requested serialization matches the original format. Mutating operations
// mark the Meta modified; subsequent serializations will emit a canonical
// representation (comments are only preserved for unmodified YAML inputs).
type Meta struct {
	Data map[string]any

	// rawBytes holds the original bytes supplied to ParseMeta. It is returned
	// verbatim by ToYAML or ToJSON when no changes have been made and the
	// requested format matches the original format.
	rawBytes []byte

	// rawFormat records the detected input format: "yaml" or "json".
	rawFormat string

	// modified indicates whether this Meta has been changed since it was parsed
	// from rawBytes. NewMetaFromRaw-created metas are treated as modified.
	modified bool

	deps *Deps
}

// NewMeta returns a new Meta initialized with an empty map and marked as modified.
func NewMeta(deps *Deps) *Meta {
	return &Meta{
		Data:     make(map[string]any),
		modified: true,
		deps:     deps,
	}
}

// NewMetaFromRaw returns a Meta wrapping the provided raw map (or an empty one).
// The returned Meta is marked modified to indicate it did not originate from a
// verbatim parsed byte slice.
func NewMetaFromRaw(raw map[string]any, deps *Deps) *Meta {
	if raw == nil {
		raw = make(map[string]any)
	}
	return &Meta{
		Data:     raw,
		modified: true,
		deps:     deps,
	}
}

// ParseMeta parses YAML or JSON bytes into a Meta. If data is empty/whitespace, returns an empty Meta.
//
// Parsing uses a codec attempt order: if the trimmed content starts with '{' or '['
// we try JSON first then YAML; otherwise YAML first then JSON. The function
// preserves the original bytes and detected format so unmodified metas can be
// round-tripped verbatim.
func ParseMeta(data []byte, deps *Deps) (*Meta, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return NewMeta(deps), nil
	}

	trim := bytes.TrimSpace(data)
	tryJSONFirst := len(trim) > 0 && (trim[0] == '{' || trim[0] == '[')

	var codecs []MetaCodec
	if tryJSONFirst {
		codecs = []MetaCodec{jsonCodec{}, yamlCodec{}}
	} else {
		codecs = []MetaCodec{yamlCodec{}, jsonCodec{}}
	}

	var raw map[string]any
	var format string
	var lastErr error
	for _, c := range codecs {
		if m, err := c.Parse(data); err == nil {
			raw = m
			format = c.Name()
			lastErr = nil
			break
		} else {
			// remember last error for reporting if all fail
			lastErr = err
		}
	}
	if raw == nil {
		if lastErr != nil {
			return nil, fmt.Errorf("parse meta: %w", lastErr)
		}
		return nil, fmt.Errorf("parse meta: unknown format")
	}

	m := NewMetaFromRaw(raw, deps)
	// Preserve original bytes and format for potential verbatim round-trip when unmodified.
	m.rawBytes = append([]byte(nil), data...)
	m.rawFormat = format
	m.modified = false
	return m, nil
}

// Raw returns a deep copy of the underlying raw map for safe inspection.
func (m *Meta) Raw() map[string]any {
	if m == nil {
		return nil
	}
	out, _ := deepCopyMap(m.Data)
	return out
}

// Clone returns a deep copy of Meta including raw bytes, format and modified flag.
func (m *Meta) Clone() *Meta {
	if m == nil {
		return nil
	}
	copyData, _ := deepCopyMap(m.Data)
	clone := NewMetaFromRaw(copyData, m.deps)
	// preserve rawBytes and rawFormat and modified flag so clones behave the same for ToYAML/ToJSON
	if m.rawBytes != nil {
		clone.rawBytes = append([]byte(nil), m.rawBytes...)
	}
	clone.rawFormat = m.rawFormat
	clone.modified = m.modified
	return clone
}

// GetHash returns the cached build/hash value stored in the meta map.
//
// This function is safe: it tolerates missing keys and a variety of underlying
// value types. It returns the string representation of the stored value or an
// empty string if no value is present.
func (m *Meta) GetHash() string {
	if m == nil {
		return ""
	}
	v, ok := m.Get("hash")
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case []byte:
		return string(t)
	default:
		// Best-effort stringification for unexpected types.
		return strings.TrimSpace(fmt.Sprint(t))
	}
}

// SetHash stores the given hash string into the meta map under the "hash" key.
// The call is a no-op for a nil receiver. The value is trimmed before storing.
func (m *Meta) SetHash(hash string) {
	if m == nil {
		return
	}
	_ = m.Set(strings.TrimSpace(hash), "hash")
}

// SetHashData computes a stable content hash for the supplied bytes and stores it
// in the meta map under the "hash" key.
//
// It prefers using an injected hasher available via m.deps if present. If no
// hasher is available it falls back to a simple MD5 hasher to ensure a useful
// value is still recorded. This function is nil-safe.
func (m *Meta) SetHashData(data []byte) {
	if m == nil {
		return
	}

	var h string
	// Use injected hasher if available; access to the unexported field is valid
	// within the same package. If not present, fall back to MD5Hasher.
	if m.deps != nil && m.deps.Hasher != nil {
		h = m.deps.Hasher.Hash(data)
	} else {
		h = (&MD5Hasher{}).Hash(data)
	}
	_ = m.Set(h, "hash")
}

// GetTitle returns the stored title from the meta map.
//
// It is defensive: missing keys or non-string stored values are handled safely.
// The returned string is trimmed. If no title exists an empty string is returned.
func (m *Meta) GetTitle() string {
	if m == nil {
		return ""
	}
	v, ok := m.Get("title")
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return strings.TrimSpace(s)
	}
	// Best-effort stringification for unexpected types.
	return strings.TrimSpace(fmt.Sprint(v))
}

// SetTitle writes the provided title into the meta map under the "title" key.
// The value is trimmed and the meta is marked modified. Nil receiver is a no-op.
func (m *Meta) SetTitle(title string) {
	if m == nil {
		return
	}
	_ = m.Set(strings.TrimSpace(title), "title")
}

// GetLinks extracts and returns normalized destination NodeIDs from the "links" key.
//
// It accepts multiple input shapes: arrays of numbers, arrays of strings, a single
// string of space/comma-separated ids, or mixed. Returned slice is deduplicated
// and sorted ascending.
func (m *Meta) GetLinks() []NodeID {
	if m == nil {
		return nil
	}
	raw, ok := m.Data["links"]
	if !ok || raw == nil {
		return nil
	}

	seen := make(map[int]struct{})

	addInt := func(v int) {
		if v < 0 {
			return
		}
		if _, ok := seen[v]; !ok {
			seen[v] = struct{}{}
		}
	}

	parseAny := func(x any) {
		switch v := x.(type) {
		case int:
			addInt(v)
		case int8:
			addInt(int(v))
		case int16:
			addInt(int(v))
		case int32:
			addInt(int(v))
		case int64:
			addInt(int(v))
		case uint:
			addInt(int(v))
		case uint8:
			addInt(int(v))
		case uint16:
			addInt(int(v))
		case uint32:
			addInt(int(v))
		case uint64:
			addInt(int(v))
		case float32:
			iv := int(v)
			// accept if it was integral or close enough
			if float32(iv) == v {
				addInt(iv)
			}
		case float64:
			iv := int(v)
			if float64(iv) == v {
				addInt(iv)
			}
		case string:
			// try to parse numeric id from string
			var id int
			if _, err := fmt.Sscan(strings.TrimSpace(v), &id); err == nil {
				addInt(id)
			}
		}
	}

	switch t := raw.(type) {
	case []any:
		for _, e := range t {
			parseAny(e)
		}
	case []string:
		for _, s := range t {
			parseAny(s)
		}
	case string:
		// allow space/comma-separated list of ids
		for _, part := range strings.FieldsFunc(t, func(r rune) bool {
			return r == ',' || unicode.IsSpace(r)
		}) {
			parseAny(part)
		}
	case map[string]any:
	case []int:
		for _, v := range t {
			addInt(v)
		}
	default:
		// unknown shape: try a best-effort single-value parse
		parseAny(t)
	}

	if len(seen) == 0 {
		return nil
	}

	ints := make([]int, 0, len(seen))
	for k := range seen {
		ints = append(ints, k)
	}
	sort.Ints(ints)
	out := make([]NodeID, 0, len(ints))
	for _, v := range ints {
		out = append(out, NodeID(v))
	}
	return out
}

// AddLink appends the given NodeID to the meta "links" list if not already present.
func (m *Meta) AddLink(link NodeID) {
	if m == nil {
		return
	}
	// Use GetLinks to obtain a normalized, deduped, sorted slice.
	existing := m.GetLinks()
	for _, l := range existing {
		if l == link {
			return // already present
		}
	}
	existing = append(existing, link)
	m.SetLinks(existing)
}

// SetLinks replaces the meta "links" value with the provided slice of NodeIDs.
// The stored representation is []int for predictable YAML/JSON output; the list
// is deduped and sorted before storing.
func (m *Meta) SetLinks(links []NodeID) {
	if m == nil {
		return
	}
	seen := make(map[int]struct{}, len(links))
	ints := make([]int, 0, len(links))
	for _, l := range links {
		li := int(l)
		if li < 0 {
			continue
		}
		if _, ok := seen[li]; ok {
			continue
		}
		seen[li] = struct{}{}
		ints = append(ints, li)
	}
	sort.Ints(ints)

	// Store as []int for predictable YAML/JSON output.
	// Use Set to mark modified and maintain path semantics.
	m.Set(ints, "links")
}

// GetSummary returns the "summary" string stored in meta or an empty string.
func (m *Meta) GetSummary() string {
	if m == nil {
		return ""
	}
	v, ok := m.Get("summary")
	if !ok || v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	// If it's some other scalar (rare), try to stringify safely.
	return fmt.Sprint(v)
}

// SetSummary stores the provided summary string under the "summary" key.
func (m *Meta) SetSummary(summary string) {
	if m == nil {
		return
	}
	_ = m.Set(summary, "summary")
}

// Get returns the value at the given path (variadic keys).
// Example: Get("zeke", "tags") or Get("tags").
// Returns (nil,false) if not found.
func (m *Meta) Get(path ...string) (any, bool) {
	if m == nil || len(path) == 0 {
		return nil, false
	}
	cur := any(m.Data)
	for _, key := range path {
		mp, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		v, exists := mp[key]
		if !exists {
			return nil, false
		}
		cur = v
	}
	return cur, true
}

// Set writes value at the given path, creating intermediate maps as needed.
//
// Behavior summary:
//   - Path is a sequence of keys (one or more). The final key in path is where the
//     value is written.
//   - If value is non-nil, it is stored at the final key. Intermediate maps are
//     created if they do not exist.
//   - If value is nil, the final key is deleted from the containing map (no error
//     if the key does not exist).
//   - If an intermediate path component exists but is not a map[string]any, Set
//     returns an error rather than overwriting it. To replace a non-map
//     intermediate component, delete it first (m.Delete(...)) or set the parent
//     key to a map explicitly.
//
// Notes:
//   - The method mutates m.Data in-place and preserves other keys not on the
//     affected path.
//   - It is not safe for concurrent use; callers should synchronize access if
//     multiple goroutines may modify the Meta concurrently.
func (m *Meta) Set(value any, path ...string) error {
	if m == nil || len(path) == 0 {
		return errors.New("invalid path")
	}
	cur := m.Data
	for _, key := range path[:len(path)-1] {
		if next, ok := cur[key]; ok {
			if mp, ok2 := next.(map[string]any); ok2 {
				cur = mp
			} else {
				return fmt.Errorf("path component %q is not a map", key)
			}
		} else {
			newMap := make(map[string]any)
			cur[key] = newMap
			cur = newMap
		}
	}
	last := path[len(path)-1]
	if value == nil {
		delete(cur, last)
	} else {
		cur[last] = value
	}
	// Mark as modified so ToYAML/ToJSON will emit updated serialization.
	m.modified = true
	return nil
}

// Delete removes the key at path.
func (m *Meta) Delete(path ...string) error {
	return m.Set(nil, path...)
}

// Tags returns the canonical top-level tags as a normalized, deduplicated,
// sorted slice. It only looks at meta["tags"].
func (m *Meta) Tags() []string {
	if m == nil {
		return nil
	}
	raw, ok := m.Data["tags"]
	if !ok || raw == nil {
		return nil
	}
	var collected []string
	switch v := raw.(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				if n := normalizeTag(s); n != "" {
					collected = append(collected, n)
				}
			}
		}
	case []string:
		for _, s := range v {
			if n := normalizeTag(s); n != "" {
				collected = append(collected, n)
			}
		}
	case string:
		for _, s := range splitAndNormalizeTags(v) {
			collected = append(collected, s)
		}
	default:
		// unknown shape: ignore
		return nil
	}
	// dedupe & sort
	set := map[string]struct{}{}
	for _, t := range collected {
		set[t] = struct{}{}
	}
	return setToSortedSlice(set)
}

// AddTag adds the normalized tag to the canonical top-level tags (creates tags
// slice if missing). No-op if tag normalizes to empty or already present.
func (m *Meta) AddTag(tag string) error {
	if m == nil {
		return errors.New("meta nil")
	}
	n := normalizeTag(tag)
	if n == "" {
		return fmt.Errorf("invalid tag %q", tag)
	}
	// build canonical slice from existing value (preserve other keys)
	raw, ok := m.Data["tags"]
	var canon []string
	if ok && raw != nil {
		switch v := raw.(type) {
		case []any:
			for _, e := range v {
				if s, ok := e.(string); ok {
					if ns := normalizeTag(s); ns != "" {
						canon = append(canon, ns)
					}
				}
			}
		case []string:
			for _, s := range v {
				if ns := normalizeTag(s); ns != "" {
					canon = append(canon, ns)
				}
			}
		case string:
			for _, s := range splitAndNormalizeTags(v) {
				canon = append(canon, s)
			}
		default:
			// unknown shape, overwrite with new slice
			canon = []string{}
		}
	}
	set := map[string]struct{}{}
	for _, t := range canon {
		set[t] = struct{}{}
	}
	set[n] = struct{}{}
	m.Data["tags"] = setToSortedSlice(set)
	m.modified = true
	return nil
}

// RemoveTag removes the tag from the canonical top-level tags (no-op if not
// present).
func (m *Meta) RemoveTag(tag string) error {
	if m == nil {
		return errors.New("meta nil")
	}
	n := normalizeTag(tag)
	raw, ok := m.Data["tags"]
	if !ok || raw == nil {
		return nil
	}
	var canon []string
	switch v := raw.(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				if ns := normalizeTag(s); ns != "" {
					canon = append(canon, ns)
				}
			}
		}
	case []string:
		for _, s := range v {
			if ns := normalizeTag(s); ns != "" {
				canon = append(canon, ns)
			}
		}
	case string:
		for _, s := range splitAndNormalizeTags(v) {
			canon = append(canon, s)
		}
	default:
		// unknown shape: nothing to remove
		return nil
	}
	set := map[string]struct{}{}
	for _, t := range canon {
		if t != n {
			set[t] = struct{}{}
		}
	}
	// keep deterministic empty slice rather than deleting key
	m.Data["tags"] = setToSortedSlice(set)
	m.modified = true
	return nil
}

// NormalizeTags normalizes canonical top-level tags in-place (lowercase,
// hyphenize, dedupe, sort).
func (m *Meta) NormalizeTags() {
	if m == nil {
		return
	}
	tags := m.Tags()
	if tags == nil {
		m.Data["tags"] = []string{}
		// treat normalization as a modification
		m.modified = true
		return
	}
	// Only update if different to avoid spurious modification flag changes.
	oldRaw, _ := m.Data["tags"]
	// Convert oldRaw to canonical slice for comparison
	var oldSlice []string
	switch v := oldRaw.(type) {
	case []any:
		for _, e := range v {
			if s, ok := e.(string); ok {
				oldSlice = append(oldSlice, normalizeTag(s))
			}
		}
	case []string:
		for _, s := range v {
			oldSlice = append(oldSlice, normalizeTag(s))
		}
	case string:
		oldSlice = splitAndNormalizeTags(v)
	default:
		oldSlice = nil
	}
	// compare
	equal := len(oldSlice) == len(tags)
	if equal {
		for i := range tags {
			if oldSlice[i] != tags[i] {
				equal = false
				break
			}
		}
	}
	if !equal {
		m.Data["tags"] = tags
		m.modified = true
	}
}

// ToYAML serializes Meta to YAML and returns bytes.
//
// If the Meta was parsed from YAML and has not been modified, the original raw
// bytes are returned verbatim to preserve comments and formatting.
func (m *Meta) ToYAML() ([]byte, error) {
	if m == nil {
		return []byte{}, nil
	}
	// If we parsed the meta and it has not been modified, and the original
	// format was YAML, return the original bytes verbatim to preserve comments
	// and formatting.
	if !m.modified && m.rawFormat == "yaml" && m.rawBytes != nil {
		return append([]byte(nil), m.rawBytes...), nil
	}
	m.NormalizeTags()
	// Ensure canonical tags normalized for stable output (may set modified).
	return yaml.Marshal(m.Data)
}

// ToJSON serializes Meta to JSON.
//
// If the Meta was parsed from JSON and has not been modified, the original raw
// bytes are returned verbatim to preserve formatting.
func (m *Meta) ToJSON() ([]byte, error) {
	if m == nil {
		return []byte{}, nil
	}
	// If we parsed the meta and it has not been modified, and the original
	// format was JSON, return the original bytes verbatim to preserve
	// formatting.
	if !m.modified && m.rawFormat == "json" && m.rawBytes != nil {
		return append([]byte(nil), m.rawBytes...), nil
	}
	m.NormalizeTags()
	return json.Marshal(m.Data)
}

// ToBytes returns a serialization matching the original detected format
// ("json" or "yaml"). If the format is unknown, an empty slice is returned.
func (m *Meta) ToBytes() ([]byte, error) {
	if m == nil {
		return []byte{}, nil
	}
	switch m.rawFormat {
	case "json":
		return m.ToJSON()
	case "yaml":
		return m.ToYAML()
	default:
		return []byte{}, nil
	}
}

// GetUpdated returns parsed "updated" timestamp if present and parseable; zero
// time otherwise.
func (m *Meta) GetUpdated() time.Time {
	if m == nil {
		return time.Time{}
	}
	if v, ok := m.Data["updated"]; ok && v != nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			if t, err := parseUpdatedTimestamp(s); err == nil {
				return t
			}
		}
		if tt, ok := v.(time.Time); ok {
			return tt
		}
	}
	return time.Time{}
}

// SetUpdated writes the updated timestamp as RFC3339 string.
func (m *Meta) SetUpdated(t time.Time) {
	if m == nil {
		return
	}
	m.Data["updated"] = t.UTC().Format(time.RFC3339)
	m.modified = true
}

// GetCreated returns parsed "created" timestamp if present and parseable; zero
// time otherwise.
func (m *Meta) GetCreated() time.Time {
	if m == nil {
		return time.Time{}
	}
	if v, ok := m.Data["created"]; ok && v != nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			if t, err := parseUpdatedTimestamp(s); err == nil {
				return t
			}
		}
		if tt, ok := v.(time.Time); ok {
			return tt
		}
	}
	return time.Time{}
}

// SetCreated writes the created timestamp as an RFC3339 UTC string into the
// meta map under the "created" key. If m is nil the call is a no-op.
func (m *Meta) SetCreated(t time.Time) {
	if m == nil {
		return
	}
	m.Data["created"] = t.UTC().Format(time.RFC3339)
	m.modified = true
}

// GetAccessed returns parsed "accessed" timestamp if present and parseable;
// zero time otherwise.
func (m *Meta) GetAccessed() time.Time {
	if m == nil {
		return time.Time{}
	}
	if v, ok := m.Data["accessed"]; ok && v != nil {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			if t, err := parseUpdatedTimestamp(s); err == nil {
				return t
			}
		}
		if tt, ok := v.(time.Time); ok {
			return tt
		}
	}
	return time.Time{}
}

// SetAccessed writes the accessed timestamp as an RFC3339 UTC string into the
// meta map under the "accessed" key.
func (m *Meta) SetAccessed(t time.Time) {
	if m == nil {
		return
	}
	m.Data["accessed"] = t.UTC().Format(time.RFC3339)
	m.modified = true
}

// Touch updates accessed/created/updated timestamps as appropriate and marks
// the meta modified.
func (m *Meta) Touch() {
	if m == nil {
		return
	}
	now := m.deps.Clock.Now().UTC()

	// Ensure created/updated timestamps exist (set to now if missing)
	if m.GetCreated().IsZero() {
		m.SetCreated(now)
	}
	if m.GetUpdated().IsZero() {
		m.SetUpdated(now)
	}

	// Always bump accessed to now
	m.SetAccessed(now)
	// SetAccessed already marks modified.
}

// GetStats composes a NodeStats from the meta timestamps.
func (m *Meta) GetStats() NodeStats {
	return NodeStats{
		Updated: m.GetUpdated(),
		Created: m.GetCreated(),
		Access:  m.GetAccessed(),
	}
}

// LoadContent updates meta fields from the provided Content (hash/title/lead/links)
// and sets timestamps as necessary.
func (m *Meta) LoadContent(content *Content) {
	if content == nil {
		return
	}

	now := m.deps.Clock.Now()
	if m.GetHash() != content.Hash {
		m.SetUpdated(now)
	}
	m.SetTitle(content.Title)
	m.SetSummary(content.Lead)
	m.SetLinks(content.Links)
	m.SetHash(content.Hash)
	if m.GetCreated().IsZero() {
		m.SetCreated(now)
	}
	if m.GetAccessed().IsZero() {
		m.SetAccessed(now)
	}
}

/* ---------------- helpers ---------------- */

// parseUpdatedTimestamp parses an RFC3339 timestamp or a fallback format.
func parseUpdatedTimestamp(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	const alt = "2006-01-02 15:04:05Z"
	return time.Parse(alt, s)
}

// normalizeTag lowercases, trims and tokenizes a tag string into a hyphen-separated token.
func normalizeTag(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.ToLower(s)
	var b strings.Builder
	prevHyphen := false
	for _, r := range s {
		if unicode.IsSpace(r) || r == ',' {
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
			continue
		}
		// allow a-z, 0-9, hyphen, underscore
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
			prevHyphen = (r == '-')
		} else {
			// replace other runes with hyphen (single)
			if !prevHyphen {
				b.WriteByte('-')
				prevHyphen = true
			}
		}
	}
	out := strings.Trim(b.String(), "-_")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

// splitAndNormalizeTags splits a comma/space separated tag string and normalizes each token.
func splitAndNormalizeTags(s string) []string {
	parts := strings.FieldsFunc(s, func(r rune) bool {
		return r == ',' || unicode.IsSpace(r)
	})
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if n := normalizeTag(p); n != "" {
			out = append(out, n)
		}
	}
	return out
}

// setToSortedSlice converts a set (map[string]struct{}) into a sorted slice.
func setToSortedSlice(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// deepCopyMap uses YAML round-trip to deep-copy heterogeneous map[string]any structures.
//
// It's simple and robust for metadata maps. If performance is a concern replace with faster logic.
func deepCopyMap(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	b, err := yaml.Marshal(m)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := yaml.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}
