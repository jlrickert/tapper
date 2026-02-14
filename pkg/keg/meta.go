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

// NodeMeta holds manually edited node metadata and helpers to read/update it.
//
// Programmatic fields (hash/timestamps/lead/links) are represented by NodeStats.
// NodeMeta focuses on human-editable yaml data and comment-preserving writes.
type NodeMeta struct {
	title string
	tags  []string

	// node preserves the parsed yaml document to retain comments/layout when
	// serializing back to yaml.
	node *yaml.Node
}

type metaYAML struct {
	Title string   `yaml:"title,omitempty"`
	Tags  []string `yaml:"tags,omitempty"`
}

type metaWithStatsYAML struct {
	Title    string    `yaml:"title,omitempty"`
	Tags     []string  `yaml:"tags,omitempty"`
	Hash     string    `yaml:"hash,omitempty"`
	Updated  time.Time `yaml:"updated,omitempty"`
	Created  time.Time `yaml:"created,omitempty"`
	Accessed time.Time `yaml:"accessed,omitempty"`
	Lead     string    `yaml:"lead,omitempty"`
	Links    []string  `yaml:"links,omitempty"`
}

// NewMeta constructs an empty NodeMeta.
func NewMeta(ctx context.Context, now time.Time) *NodeMeta {
	_ = ctx
	_ = now
	return &NodeMeta{}
}

// ParseMeta parses raw yaml bytes into NodeMeta. Empty input returns an empty
// NodeMeta.
func ParseMeta(ctx context.Context, raw []byte) (*NodeMeta, error) {
	_ = ctx
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &NodeMeta{tags: []string{}}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse meta yaml: %w", err)
	}

	var tmp metaYAML
	if len(doc.Content) > 0 {
		if err := doc.Content[0].Decode(&tmp); err != nil {
			if err2 := doc.Decode(&tmp); err2 != nil {
				return nil, fmt.Errorf("failed to decode meta yaml: %w", err)
			}
		}
	}

	m := &NodeMeta{
		title: tmp.Title,
		node:  &doc,
	}
	if len(tmp.Tags) == 0 {
		m.tags = []string{}
	} else {
		m.tags = NormalizeTags(tmp.Tags)
	}

	return m, nil
}

// ToYAML serializes only manually edited metadata fields.
func (m *NodeMeta) ToYAML() string {
	return m.ToYAMLWithStats(nil)
}

// ToYAMLWithStats serializes metadata while optionally merging programmatic
// NodeStats fields into the emitted yaml.
func (m *NodeMeta) ToYAMLWithStats(stats *NodeStats) string {
	if m == nil {
		return ""
	}

	if m.node != nil {
		if len(m.node.Content) > 0 {
			root := m.node.Content[0]
			if root != nil && root.Kind == yaml.MappingNode {
				if m.title == "" {
					removeFromMapping(root, "title")
				} else {
					setScalarInMapping(root, "title", m.title)
				}
				rewriteTagsInMapping(root, m.tags)
				if stats != nil {
					applyStatsToMapping(root, stats)
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

	tags := make([]string, len(m.tags))
	copy(tags, m.tags)
	sort.Strings(tags)

	data := metaWithStatsYAML{
		Title: m.title,
		Tags:  tags,
	}
	if stats != nil {
		data.Hash = stats.Hash()
		data.Updated = stats.Updated()
		data.Created = stats.Created()
		data.Accessed = stats.Accessed()
		data.Lead = stats.Lead()
		links := stats.Links()
		if len(links) > 0 {
			data.Links = make([]string, 0, len(links))
			for _, link := range links {
				data.Links = append(data.Links, link.Path())
			}
		}
	}

	b, _ := yaml.Marshal(data)
	out := string(b)
	if out == "{}\n" || strings.TrimSpace(out) == "{}" {
		return ""
	}
	return out
}

func (m *NodeMeta) Title() string {
	if m == nil {
		return ""
	}
	return m.title
}

func (m *NodeMeta) SetTitle(ctx context.Context, title string) {
	_ = ctx
	if m == nil {
		return
	}
	m.title = title
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			if title == "" {
				removeFromMapping(root, "title")
			} else {
				setScalarInMapping(root, "title", title)
			}
		}
	}
}

func (m *NodeMeta) AddTag(tag string) {
	if m == nil {
		return
	}
	t := NormalizeTag(strings.TrimSpace(tag))
	if t == "" {
		return
	}
	if slices.Contains(m.tags, t) {
		return
	}
	m.tags = append(m.tags, t)
	sort.Strings(m.tags)
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			rewriteTagsInMapping(root, m.tags)
		}
	}
}

func (m *NodeMeta) RmTag(tag string) {
	if m == nil {
		return
	}
	t := NormalizeTag(tag)
	if t == "" {
		return
	}

	out := make([]string, 0, len(m.tags))
	changed := false
	for _, existing := range m.tags {
		if existing == t {
			changed = true
			continue
		}
		out = append(out, existing)
	}
	if !changed {
		return
	}
	m.tags = out
	sort.Strings(m.tags)

	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			rewriteTagsInMapping(root, m.tags)
		}
	}
}

func (m *NodeMeta) Tags() []string {
	if m == nil {
		return nil
	}
	out := make([]string, len(m.tags))
	copy(out, m.tags)
	sort.Strings(out)
	return out
}

func (m *NodeMeta) SetTags(ctx context.Context, tags []string) {
	_ = ctx
	if m == nil {
		return
	}
	m.tags = NormalizeTags(tags)
	if m.node != nil && len(m.node.Content) > 0 {
		root := m.node.Content[0]
		if root != nil && root.Kind == yaml.MappingNode {
			rewriteTagsInMapping(root, m.tags)
		}
	}
}

// Get retrieves well-known meta fields managed by NodeMeta.
func (m *NodeMeta) Get(key string) (string, bool) {
	if m == nil {
		return "", false
	}
	switch key {
	case "title":
		if m.title == "" {
			return "", false
		}
		return m.title, true
	case "tags":
		if len(m.tags) == 0 {
			return "", false
		}
		toks := make([]string, len(m.tags))
		copy(toks, m.tags)
		sort.Strings(toks)
		return strings.Join(toks, " "), true
	default:
		return "", false
	}
}

// Set updates known NodeMeta keys (title, tags) and preserves unknown keys in
// the yaml node when available.
func (m *NodeMeta) Set(ctx context.Context, key string, val any) error {
	if m == nil {
		return nil
	}

	switch key {
	case "title":
		if val == nil {
			m.SetTitle(ctx, "")
			return nil
		}
		m.SetTitle(ctx, fmt.Sprint(val))
		return nil
	case "tags":
		if val == nil {
			m.SetTags(ctx, []string{})
			return nil
		}
		switch v := val.(type) {
		case []string:
			m.SetTags(ctx, v)
		case string:
			m.SetTags(ctx, ParseTags(v))
		default:
			m.SetTags(ctx, ParseTags(fmt.Sprint(v)))
		}
		return nil
	default:
		if m.node == nil {
			m.node = &yaml.Node{
				Kind: yaml.DocumentNode,
				Content: []*yaml.Node{
					{Kind: yaml.MappingNode, Tag: "!!map"},
				},
			}
			root := m.node.Content[0]
			if m.title != "" {
				setScalarInMapping(root, "title", m.title)
			}
			if len(m.tags) > 0 {
				rewriteTagsInMapping(root, m.tags)
			}
		}
		if m.node != nil && len(m.node.Content) > 0 {
			root := m.node.Content[0]
			if root != nil && root.Kind == yaml.MappingNode {
				if val == nil {
					removeFromMapping(root, key)
				} else {
					setNodeInMapping(root, key, valueToYAMLNode(val))
				}
			}
		}
		return nil
	}
}

func (m *NodeMeta) SetAttrs(ctx context.Context, attrs map[string]any) error {
	if m == nil || attrs == nil {
		return nil
	}
	for key, val := range attrs {
		if err := m.Set(ctx, key, val); err != nil {
			return err
		}
	}
	return nil
}

func applyStatsToMapping(root *yaml.Node, stats *NodeStats) {
	if root == nil || root.Kind != yaml.MappingNode || stats == nil {
		return
	}

	if stats.Hash() == "" {
		removeFromMapping(root, "hash")
	} else {
		setScalarInMapping(root, "hash", stats.Hash())
	}

	if stats.Updated().IsZero() {
		removeFromMapping(root, "updated")
	} else {
		setScalarInMapping(root, "updated", stats.Updated().Format(time.RFC3339))
	}

	if stats.Created().IsZero() {
		removeFromMapping(root, "created")
	} else {
		setScalarInMapping(root, "created", stats.Created().Format(time.RFC3339))
	}

	if stats.Accessed().IsZero() {
		removeFromMapping(root, "accessed")
	} else {
		setScalarInMapping(root, "accessed", stats.Accessed().Format(time.RFC3339))
	}

	if stats.Lead() == "" {
		removeFromMapping(root, "lead")
	} else {
		setScalarInMapping(root, "lead", stats.Lead())
	}

	links := stats.Links()
	if len(links) == 0 {
		removeFromMapping(root, "links")
	} else {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for _, link := range links {
			seq.Content = append(seq.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Value: link.Path(), Tag: "!!str"})
		}
		setNodeInMapping(root, "links", seq)
	}
}

func rewriteTagsInMapping(root *yaml.Node, tags []string) {
	if root == nil || root.Kind != yaml.MappingNode {
		return
	}
	if len(tags) == 0 {
		removeFromMapping(root, "tags")
		return
	}
	normalized := NormalizeTags(tags)
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, tag := range normalized {
		seq.Content = append(seq.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Value: tag, Tag: "!!str"})
	}
	setNodeInMapping(root, "tags", seq)
}

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
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		&yaml.Node{Kind: yaml.ScalarNode, Value: val, Tag: "!!str"},
	)
}

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
	root.Content = append(root.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Value: key, Tag: "!!str"},
		node,
	)
}

func removeFromMapping(root *yaml.Node, key string) {
	if root == nil || root.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k := root.Content[i]
		if k != nil && k.Kind == yaml.ScalarNode && k.Value == key {
			root.Content = append(root.Content[:i], root.Content[i+2:]...)
			return
		}
	}
}

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
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: t.Format(time.RFC3339)}
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
		for k, v2 := range t {
			mnode.Content = append(mnode.Content,
				&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: k},
				valueToYAMLNode(v2))
		}
		return mnode
	default:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: fmt.Sprint(v)}
	}
}
