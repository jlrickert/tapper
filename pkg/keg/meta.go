package keg

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Meta holds parsed node metadata and provides helpers to read and update it.
//
// The type keeps common meta fields unexported and exposes accessor and mutator
// methods. When parsing succeeds the original YAML document node is preserved
// so that writes can retain comments and formatting when possible.
type Meta struct {
	// primary meta fields are unexported to encapsulate access behind methods
	title string
	hash  string
	tags  []string

	// Content timestamps. Marshaled form is RFC 3339.
	updated  time.Time
	created  time.Time
	accessed time.Time

	// lead is the first paragraph or short summary extracted from content.
	lead string
	// links are outgoing node links discovered in the content.
	links []NodeId

	// node holds the parsed YAML document node when available. When present we
	// prefer writing this node back to disk to preserve comments and layout.
	node *yaml.Node
}

type metaYAML struct {
	Title    string    `yaml:"title,omitempty"`
	Hash     string    `yaml:"hash,omitempty"`
	Tags     []string  `yaml:"tags,omitempty"`
	Updated  time.Time `yaml:"updated,omitempty"`
	Created  time.Time `yaml:"created,omitempty"`
	Accessed time.Time `yaml:"accessed,omitempty"`
	Lead     string    `yaml:"lead,omitempty"`
	Links    []string  `yaml:"links,omitempty"`
}

// NewMeta constructs a Meta prepopulated with sensible timestamps derived from
// the clock in ctx. Use this when creating a new meta value for a node.
func NewMeta(ctx context.Context, now time.Time) *Meta {
	return &Meta{
		updated: now,
		created: now,
	}
}

// ParseMeta parses raw YAML bytes into a Meta. If the input is empty or only
// whitespace a zero-value Meta is returned.
//
// When parsing succeeds the original yaml.NodeId is preserved so callers can
// perform comment-preserving edits. The YAML is decoded into a temporary
// struct for mapping and the resulting values are normalized before populating
// the Meta.
func ParseMeta(ctx context.Context, raw []byte) (*Meta, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		// empty meta => return zero meta (no node)
		return &Meta{
			tags: nil,
		}, nil
	}

	// Parse into a document node so we can preserve comments and formatting.
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse meta yaml: %w", err)
	}

	var tmp metaYAML
	// If doc.Content has a root mapping node, decode that into tmp; otherwise
	// try decoding the whole document as a fallback.
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
		lead:     tmp.Lead,
		// preserve the parsed document for ToYAML when writing
		node: &doc,
	}

	// Ensure Tags is non-nil for callers expecting a slice, normalize tags.
	if len(tmp.Tags) == 0 {
		m.tags = []string{}
	} else {
		m.tags = NormalizeTags(tmp.Tags)
	}

	// Parse Links strings into NodeId values when possible.
	if len(tmp.Links) == 0 {
		m.links = []NodeId{}
	} else {
		var lnks []NodeId
		for _, s := range tmp.Links {
			n, err := ParseNode(s)
			if err != nil || n == nil {
				// tolerate malformed entries by skipping
				continue
			}
			lnks = append(lnks, *n)
		}
		m.links = lnks
	}

	return m, nil
}

// ToYAML serializes the Meta to a YAML string.
//
// If the Meta preserves the original parsed yaml.NodeId we encode that node to
// retain comments and formatting. In that case we also try to normalize the
// "tags" sequence in-place so tags are emitted in ascending order. When no
// parsed node is available we marshal a temporary struct. An empty meta yields
// an empty string.
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

	// convert links to their string path representations
	links := make([]string, 0, len(m.links))
	for _, n := range m.links {
		links = append(links, n.Path())
	}

	t := metaYAML{
		Title:    m.title,
		Hash:     m.hash,
		Tags:     tags,
		Updated:  m.updated,
		Created:  m.created,
		Accessed: m.accessed,
		Lead:     m.lead,
		Links:    links,
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

// SetTitle updates the title and reflects the change in the parsed node when
// present.
func (m *Meta) SetTitle(ctx context.Context, t string) {
	if m == nil {
		return
	}
	m.title = t
	// reflect change in parsed node when present
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			setScalarInMapping(root, "title", t)
		}
	}
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
// hash changes. The parsed node is updated when present.
func (m *Meta) SetHash(ctx context.Context, h string, now *time.Time) {
	if m == nil {
		return
	}
	if m.hash != h && now != nil {
		m.hash = h
		m.updated = *now
	} else {
		m.hash = h
	}
	// reflect change in parsed node when present
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			setScalarInMapping(root, "hash", h)
		}
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

// SetTags replaces the tag list. Input tags are normalized and deduped. When a
// parsed node is present the YAML node is updated to reflect the new tags.
func (m *Meta) SetTags(ctx context.Context, tags []string) {
	if m == nil {
		return
	}
	toks := NormalizeTags(tags)
	m.tags = toks
	// reflect change in parsed node when present
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			// build sequence node
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, s := range toks {
				seq.Content = append(seq.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: s, Tag: "!!str"})
			}
			setNodeInMapping(root, "tags", seq)
		}
	}
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

// Lead returns the meta lead (short summary).
func (m *Meta) Lead() string {
	if m == nil {
		return ""
	}
	return m.lead
}

// SetLead sets the meta lead and updates the parsed node when present.
func (m *Meta) SetLead(ctx context.Context, l string) {
	if m == nil {
		return
	}
	m.lead = l
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			setScalarInMapping(root, "lead", l)
		}
	}
}

// Links returns a copy of outgoing links as a slice of NodeId.
func (m *Meta) Links() []NodeId {
	if m == nil {
		return nil
	}
	out := make([]NodeId, len(m.links))
	copy(out, m.links)
	return out
}

// SetLinks replaces the outgoing links and updates the parsed node when
// present.
func (m *Meta) SetLinks(ctx context.Context, links []NodeId) {
	if m == nil {
		return
	}
	lnks := make([]NodeId, len(links))
	copy(lnks, links)
	m.links = lnks

	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, n := range links {
				seq.Content = append(seq.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Value: n.Path(), Tag: "!!str"})
			}
			setNodeInMapping(root, "links", seq)
		}
	}
}

// Get retrieves well-known meta fields (hash, tags, updated, created,
// accessed, lead). The boolean return indicates whether a value was found.
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
	case "lead":
		if m.lead == "" {
			return "", false
		}
		return m.lead, true
	default:
		return "", false
	}
}

// Set sets or updates a well-known key/value pair in the Meta.
//
// Supported keys:
//   - "hash": sets Meta.hash (string)
//   - "tags": accepts []string or string (space/comma separated) and normalizes
//     tags
//   - "updated","created","accessed": accept time.Time or RFC3339 string
//   - "lead": sets the lead string
//   - "links": accepts []NodeId, []string or string (space/comma separated) and
//     normalizes into a []NodeId slice
//
// For other keys, the function will write the key/value into the preserved
// yaml.NodeId when available so comment-preserving writes include them.
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
		m.SetHash(ctx, sval, nil)
		return nil

	case "tags":
		if val == nil {
			m.SetTags(ctx, []string{})
			return nil
		}
		switch v := val.(type) {
		case []string:
			m.SetTags(ctx, v)
			return nil
		case string:
			parsed := ParseTags(v)
			m.SetTags(ctx, parsed)
		default:
			parsed := ParseTags(fmt.Sprint(v))
			m.SetTags(ctx, parsed)
		}
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

	case "lead":
		if val == nil {
			m.lead = ""
			return nil
		}
		m.lead = fmt.Sprint(val)
		return nil

	case "links":
		if val == nil {
			m.links = []NodeId{}
			return nil
		}
		switch v := val.(type) {
		case []NodeId:
			lnks := make([]NodeId, len(v))
			copy(lnks, v)
			m.links = lnks
		case []string:
			var lnks []NodeId
			for _, s := range v {
				n, err := ParseNode(s)
				if err != nil || n == nil {
					continue
				}
				lnks = append(lnks, *n)
			}
			m.links = lnks
		case string:
			parts := strings.FieldsFunc(v, func(r rune) bool {
				return r == ',' || r == ';' || r == ' ' || r == '\n' ||
					r == '\t'
			})
			var lnks []NodeId
			for _, s := range parts {
				if s == "" {
					continue
				}
				n, err := ParseNode(s)
				if err != nil || n == nil {
					continue
				}
				lnks = append(lnks, *n)
			}
			m.links = lnks
		default:
			// attempt to coerce via fmt
			parsed := strings.Fields(fmt.Sprint(v))
			var lnks []NodeId
			for _, s := range parsed {
				if s == "" {
					continue
				}
				n, err := ParseNode(s)
				if err != nil || n == nil {
					continue
				}
				lnks = append(lnks, *n)
			}
			m.links = lnks
		}
		return nil

	default:
		if m.node == nil {
			hackyM, _ := ParseMeta(ctx, []byte(m.ToYAML()))
			m.node = hackyM.node
		}
		// For unknown keys, write the key/value into the preserved yaml node
		// when possible so comment-preserving writes will include them.
		if m.node != nil && len(m.node.Content) > 0 {
			root := m.node.Content[0]
			if root != nil && root.Kind == yaml.MappingNode {
				node := valueToYAMLNode(val)
				setNodeInMapping(root, key, node)
			}
		}
		return nil
	}
}

// SetAttrs applies a map of attributes to the Meta. It now delegates all keys
// to Meta.Set so normalization, validation, and YAML preservation are handled
// in one place.
func (m *Meta) SetAttrs(ctx context.Context, attrs map[string]any) error {
	if m == nil || attrs == nil {
		return nil
	}

	for k, v := range attrs {
		if err := m.Set(ctx, k, v); err != nil {
			return err
		}
	}
	return nil
}

// Update refreshes meta fields based on parsed content. This updates title,
// hash, lead and links derived from the provided Content.
func (m *Meta) Update(ctx context.Context, content *Content, now *time.Time) {
	m.SetAttrs(ctx, content.Frontmatter)
	m.SetTitle(ctx, content.Title)
	// update hash and bump updated timestamp on change
	m.SetHash(ctx, content.Hash, now)
	// also update lead and links from parsed content
	m.SetLead(ctx, content.Lead)
	m.SetLinks(ctx, content.Links)
}

// Helpers to update yaml.NodeId mappings in-place when preserving parsed nodes.
// root is expected to be a mapping node (yaml.MappingNode).

// setScalarInMapping sets or appends a scalar value for the provided key.
func setScalarInMapping(root *yaml.Node, key, val string) {
	if root == nil || root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == key {
			root.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"}
			return
		}
	}
	// append key/value
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"},
	)
}

// setNodeInMapping sets or appends a node value for the provided key.
func setNodeInMapping(root *yaml.Node, key string, node *yaml.Node) {
	if root == nil || root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == key {
			root.Content[i+1] = node
			return
		}
	}
	// append key/value
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		node,
	)
}

// valueToYAMLNode converts a Go value into a yaml.NodeId. It handles common
// primitive types, slices, maps and time.Time.
func valueToYAMLNode(v any) *yaml.Node {
	if v == nil {
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: ""}
	}
	switch t := v.(type) {
	case string:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t}
	case bool:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: fmt.Sprint(t)}
	case int, int8, int16, int32, int64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(t)}
	case uint, uint8, uint16, uint32, uint64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(t)}
	case float32, float64:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(t)}
	case time.Time:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str",
			Value: t.Format(time.RFC3339)}
	case []string:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, s := range t {
			seq.Content = append(seq.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s})
		}
		return seq
	case []any:
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, e := range t {
			seq.Content = append(seq.Content, valueToYAMLNode(e))
		}
		return seq
	case map[string]any:
		mnode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		// preserve iteration order is not guaranteed; callers should not rely on
		for k, v2 := range t {
			mnode.Content = append(mnode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				valueToYAMLNode(v2))
		}
		return mnode
	default:
		// attempt to handle []interface{} and map[string]interface{}
		switch s := t.(type) {
		case []any:
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, e := range s {
				seq.Content = append(seq.Content, valueToYAMLNode(e))
			}
			return seq
		case map[string]any:
			mnode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for k, v2 := range s {
				mnode.Content = append(mnode.Content,
					&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
					valueToYAMLNode(v2))
			}
			return mnode
		default:
			// fallback to string scalar
			return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str",
				Value: fmt.Sprint(v)}
		}
	}
}
