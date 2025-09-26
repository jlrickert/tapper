package keg

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	std "github.com/jlrickert/go-std/pkg"
	"gopkg.in/yaml.v3"
)

type Meta struct {
	// keep primary meta fields unexported to encapsulate access behind methods
	title string
	hash  string
	tags  []string
	// Content last updated timestamp. Marshaled content is RFC 3339
	updated time.Time
	// Content created timestamp. Marshaled content is RFC 3339
	created time.Time
	// Content last accessed timestamp. Marshaled content is RFC 3339
	accessed time.Time

	// node holds the parsed YAML document node when available. When present we
	// prefer writing this node back to disk to preserve comments and formatting.
	node *yaml.Node
}

type metaYAML struct {
	Title    string    `yaml:"title,omitempty"`
	Hash     string    `yaml:"hash,omitempty"`
	Tags     []string  `yaml:"tags,omitempty"`
	Updated  time.Time `yaml:"updated,omitempty"`
	Created  time.Time `yaml:"created,omitempty"`
	Accessed time.Time `yaml:"accessed,omitempty"`
}

func NewMeta(ctx context.Context) *Meta {
	clock := std.ClockFromContext(ctx)
	now := clock.Now()
	return &Meta{
		tags:    []string{},
		updated: now,
		created: now,
	}
}

// ParseMeta parses raw YAML bytes into a Meta. If the input is empty or only
// whitespace, a zero-value Meta is returned.
//
// This implementation preserves the underlying yaml.Node when possible so that
// comment-preserving edits/writes are possible. The yaml is decoded into a
// temporary exported struct for mapping, and the values are normalized before
// populating the returned Meta.
func ParseMeta(ctx context.Context, raw []byte) (*Meta, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		// empty meta => return zero meta (no node)
		return &Meta{
			tags: nil,
		}, nil
	}

	// Parse into a document node so we can preserve comments/formatting.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse meta yaml: %w", err)
	}

	var tmp metaYAML
	// If doc.Content has a root mapping node, decode that into tmp; otherwise
	// try decoding the whole doc as a fallback.
	if len(doc.Content) > 0 {
		if err := doc.Content[0].Decode(&tmp); err != nil {
			// fallback to decoding the whole document
			if err2 := doc.Decode(&tmp); err2 != nil {
				return nil, fmt.Errorf("failed to decode meta into struct: %w", err)
			}
		}
	}

	m := &Meta{
		title:    tmp.Title,
		hash:     tmp.Hash,
		updated:  tmp.Updated,
		created:  tmp.Created,
		accessed: tmp.Accessed,
		// we'll preserve the parsed document for ToYAML when writing
		node: &doc,
	}

	// Ensure Tags is non-nil for callers expecting a slice, and normalize tags.
	if len(tmp.Tags) == 0 {
		m.tags = []string{}
	} else {
		m.tags = NormalizeTags(tmp.Tags)
	}

	return m, nil
}

// ToYAML serializes the Meta to a YAML string. When the Meta carries an original
// parsed yaml.Node we prefer encoding that node to preserve comments and layout.
// Otherwise we marshal a temporary exported struct.
//
// This function also ensures tag lists are normalized and emitted in ascending
// order.
func (m *Meta) ToYAML() string {
	if m == nil {
		return ""
	}

	// Prefer writing the original node to preserve comments and formatting.
	if m.node != nil {
		// Attempt to normalize the "tags" sequence inside the parsed node so the
		// emitted YAML has tags in ascending order while preserving comments.
		if len(m.node.Content) > 0 {
			root := m.node.Content[0]
			// root expected to be a mapping node for typical document structure.
			if root != nil && root.Kind == yaml.MappingNode {
				for i := 0; i+1 < len(root.Content); i += 2 {
					key := root.Content[i]
					val := root.Content[i+1]
					if key != nil && key.Kind == yaml.ScalarNode &&
						key.Value == "tags" && val != nil &&
						val.Kind == yaml.SequenceNode {
						// collect scalar values
						var toks []string
						for _, n := range val.Content {
							if n != nil && n.Kind == yaml.ScalarNode {
								toks = append(toks, n.Value)
							}
						}
						// normalize and sort tokens
						toks = NormalizeTags(toks)
						// rebuild sequence node with sorted scalars
						seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
						for _, s := range toks {
							seq.Content = append(seq.Content,
								&yaml.Node{Kind: yaml.ScalarNode, Value: s, Tag: "!!str"})
						}
						root.Content[i+1] = seq
					}
				}
			}
		}

		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		_ = enc.Encode(m.node)
		_ = enc.Close()
		out := buf.String()
		if out == "{}\n" || strings.TrimSpace(out) == "{}" {
			return ""
		}
		return out
	}

	// Fallback to marshaling an exported struct. Ensure tags are sorted.
	tags := make([]string, len(m.tags))
	copy(tags, m.tags)
	sort.Strings(tags)

	t := metaYAML{
		Title:    m.title,
		Hash:     m.hash,
		Tags:     tags,
		Updated:  m.updated,
		Created:  m.created,
		Accessed: m.accessed,
	}
	b, _ := yaml.Marshal(t)
	out := string(b)
	if out == "{}\n" || strings.TrimSpace(out) == "{}" {
		return ""
	}
	return out
}

// Title returns the meta title.
func (m *Meta) Title() string {
	if m == nil {
		return ""
	}
	return m.title
}

// SetTitle updates the title.
func (m *Meta) SetTitle(ctx context.Context, t string) {
	if m == nil {
		return
	}
	m.title = t
}

// AddTag adds a tag to the Meta if it is not already present. The tag list is
// deduplicated and kept in lexicographic order.
func (m *Meta) AddTag(tag string) {
	if m == nil {
		return
	}
	t := strings.TrimSpace(tag)
	if t == "" {
		return
	}

	// normalize tag using shared logic from pkg/tap
	t = NormalizeTag(t)
	if t == "" {
		return
	}

	// simple dedupe
	if slices.Contains(m.tags, t) {
		return
	}
	m.tags = append(m.tags, t)
	sort.Strings(m.tags)
}

// RmTag removes a tag from the Meta if present.
func (m *Meta) RmTag(tag string) {
	if m == nil {
		return
	}

	// normalize input tag
	t := NormalizeTag(tag)
	if t == "" {
		return
	}

	changed := false
	newTags := make([]string, 0, len(m.tags))
	for _, existing := range m.tags {
		if existing == t {
			changed = true
			continue
		}
		newTags = append(newTags, existing)
	}
	if !changed {
		return
	}
	sort.Strings(newTags)
	m.tags = newTags
}

// Hash returns the stored content hash.
func (m *Meta) Hash() string {
	if m == nil {
		return ""
	}
	return m.hash
}

// SetHash sets the content hash and updates the updated timestamp when the
// hash changes.
func (m *Meta) SetHash(ctx context.Context, h string, updateTime bool) {
	if m == nil {
		return
	}
	if m.hash != h && updateTime {
		clock := std.ClockFromContext(ctx)
		now := clock.Now()
		m.hash = h
		m.updated = now
	}
}

// Tags returns a copy of the tags slice.
func (m *Meta) Tags() []string {
	if m == nil {
		return nil
	}
	out := make([]string, len(m.tags))
	copy(out, m.tags)
	// ensure callers always see tags in ascending order
	sort.Strings(out)
	return out
}

// SetTags replaces the tag list (normalizing and deduping).
func (m *Meta) SetTags(ctx context.Context, tags []string) {
	if m == nil {
		return
	}
	toks := NormalizeTags(tags)
	m.tags = toks
}

// Updated returns the updated timestamp.
func (m *Meta) Updated() time.Time {
	if m == nil {
		return time.Time{}
	}
	return m.updated
}

// SetUpdated sets the updated timestamp.
func (m *Meta) SetUpdated(ctx context.Context, t time.Time) {
	if m == nil {
		return
	}
	m.updated = t
}

// Created returns the created timestamp.
func (m *Meta) Created() time.Time {
	if m == nil {
		return time.Time{}
	}
	return m.created
}

// SetCreated sets the created timestamp.
func (m *Meta) SetCreated(ctx context.Context, t time.Time) {
	if m == nil {
		return
	}
	m.created = t
}

// Accessed returns the accessed timestamp.
func (m *Meta) Accessed() time.Time {
	if m == nil {
		return time.Time{}
	}
	return m.accessed
}

// SetAccessed sets the accessed timestamp.
func (m *Meta) SetAccessed(ctx context.Context, t time.Time) {
	if m == nil {
		return
	}
	m.accessed = t
}

// Get retrieves well-known meta fields (hash, tags, updated, created, accessed).
// The boolean return indicates whether a value was found.
func (m *Meta) Get(key string) (string, bool) {
	if m == nil {
		return "", false
	}

	switch key {
	case "hash":
		if m.hash == "" {
			return "", false
		}
		return m.hash, true
	case "tags":
		if len(m.tags) == 0 {
			return "", false
		}
		// return tags joined with single spaces, in ascending order
		toks := make([]string, len(m.tags))
		copy(toks, m.tags)
		sort.Strings(toks)
		return strings.Join(toks, " "), true
	case "updated":
		if m.updated.IsZero() {
			return "", false
		}
		return m.updated.Format(time.RFC3339), true
	case "created":
		if m.created.IsZero() {
			return "", false
		}
		return m.created.Format(time.RFC3339), true
	case "accessed":
		if m.accessed.IsZero() {
			return "", false
		}
		return m.accessed.Format(time.RFC3339), true
	default:
		return "", false
	}
}

// Set sets or updates a well-known key/value pair in the Meta. Supported keys:
//   - "hash": sets Meta.hash (string)
//   - "tags": accepts []string or string (space/comma separated) and normalizes tags
//   - "updated","created","accessed": accept time.Time or RFC3339 string
//
// Other keys are ignored (no-op). Comment-preserving edits are out of scope.
func (m *Meta) Set(ctx context.Context, key string, val any) error {
	if m == nil {
		return nil
	}

	switch key {
	case "hash":
		var sval string
		if val == nil {
			sval = ""
		} else {
			sval = fmt.Sprint(val)
		}
		m.hash = sval
		return nil

	case "tags":
		var toks []string
		if val == nil {
			toks = []string{}
		} else {
			switch v := val.(type) {
			case []string:
				toks = v
			case string:
				parsed := ParseTags(v)
				toks = parsed
			default:
				parsed := ParseTags(fmt.Sprint(v))
				toks = parsed
			}
		}
		toks = NormalizeTags(toks)
		m.tags = toks
		return nil

	case "updated":
		if val == nil {
			m.updated = time.Time{}
			return nil
		}
		var tt time.Time
		switch v := val.(type) {
		case time.Time:
			tt = v
		case string:
			tp, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("invalid time string for updated: %w", err)
			}
			tt = tp
		default:
			return fmt.Errorf("unsupported type for updated")
		}
		m.updated = tt
		return nil

	case "created":
		if val == nil {
			m.created = time.Time{}
			return nil
		}
		var tt time.Time
		switch v := val.(type) {
		case time.Time:
			tt = v
		case string:
			tp, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("invalid time string for created: %w", err)
			}
			tt = tp
		default:
			return fmt.Errorf("unsupported type for created")
		}
		m.created = tt
		return nil

	case "accessed":
		if val == nil {
			m.accessed = time.Time{}
			return nil
		}
		var tt time.Time
		switch v := val.(type) {
		case time.Time:
			tt = v
		case string:
			tp, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fmt.Errorf("invalid time string for accessed: %w", err)
			}
			tt = tp
		default:
			return fmt.Errorf("unsupported type for accessed")
		}
		m.accessed = tt
		return nil

	default:
		// Other keys: no-op in this refactor iteration.
		return nil
	}
}
