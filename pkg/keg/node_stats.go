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

type statsYAML struct {
	Hash     string   `yaml:"hash,omitempty"`
	Updated  string   `yaml:"updated,omitempty"`
	Created  string   `yaml:"created,omitempty"`
	Accessed string   `yaml:"accessed,omitempty"`
	Lead     string   `yaml:"lead,omitempty"`
	Links    []string `yaml:"links,omitempty"`
	Tags     any      `yaml:"tags,omitempty"`
}

// NodeStats contains programmatic node data derived by tooling.
type NodeStats struct {
	hash     string
	updated  time.Time
	created  time.Time
	accessed time.Time
	lead     string
	links    []NodeId
	tags     []string
}

func NewStats(now time.Time) *NodeStats {
	return &NodeStats{
		updated: now,
		created: now,
		links:   []NodeId{},
		tags:    []string{},
	}
}

// ParseStats extracts programmatic node stats from raw meta yaml bytes.
func ParseStats(ctx context.Context, raw []byte) (*NodeStats, error) {
	_ = ctx

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &NodeStats{links: []NodeId{}, tags: []string{}}, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse node stats yaml: %w", err)
	}

	var tmp statsYAML
	if len(doc.Content) > 0 {
		if err := doc.Content[0].Decode(&tmp); err != nil {
			if err2 := doc.Decode(&tmp); err2 != nil {
				return nil, fmt.Errorf("failed to decode node stats yaml: %w", err)
			}
		}
	}

	stats := &NodeStats{
		hash:     tmp.Hash,
		updated:  parseStatsTime(tmp.Updated),
		created:  parseStatsTime(tmp.Created),
		accessed: parseStatsTime(tmp.Accessed),
		lead:     tmp.Lead,
		links:    make([]NodeId, 0, len(tmp.Links)),
		tags:     parseStatsTags(tmp.Tags),
	}

	for _, rawLink := range tmp.Links {
		n, err := ParseNode(rawLink)
		if err != nil || n == nil {
			continue
		}
		stats.links = append(stats.links, *n)
	}
	stats.links = normalizeNodeIDList(stats.links)

	return stats, nil
}

func parseStatsTime(raw string) time.Time {
	value := strings.TrimSpace(raw)
	if value == "" {
		return time.Time{}
	}

	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
	}
	for _, layout := range layouts {
		t, err := time.Parse(layout, value)
		if err == nil {
			return t
		}
	}

	return time.Time{}
}

func (s *NodeStats) Hash() string {
	if s == nil {
		return ""
	}
	return s.hash
}

func (s *NodeStats) SetHash(hash string, now *time.Time) {
	if s == nil {
		return
	}
	if s.hash != hash && now != nil {
		s.updated = *now
	}
	s.hash = hash
}

func (s *NodeStats) Updated() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.updated
}

func (s *NodeStats) SetUpdated(t time.Time) {
	if s == nil {
		return
	}
	s.updated = t
}

func (s *NodeStats) Created() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.created
}

func (s *NodeStats) SetCreated(t time.Time) {
	if s == nil {
		return
	}
	s.created = t
}

func (s *NodeStats) Accessed() time.Time {
	if s == nil {
		return time.Time{}
	}
	return s.accessed
}

func (s *NodeStats) SetAccessed(t time.Time) {
	if s == nil {
		return
	}
	s.accessed = t
}

func (s *NodeStats) Lead() string {
	if s == nil {
		return ""
	}
	return s.lead
}

func (s *NodeStats) SetLead(lead string) {
	if s == nil {
		return
	}
	s.lead = lead
}

func (s *NodeStats) Links() []NodeId {
	if s == nil {
		return nil
	}
	out := make([]NodeId, len(s.links))
	copy(out, s.links)
	return out
}

func (s *NodeStats) Tags() []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s.tags))
	copy(out, s.tags)
	sort.Strings(out)
	return out
}

func (s *NodeStats) SetTags(tags []string) {
	if s == nil {
		return
	}
	s.tags = NormalizeTags(tags)
	sort.Strings(s.tags)
}

func (s *NodeStats) AddTag(tag string) {
	if s == nil {
		return
	}
	t := NormalizeTag(strings.TrimSpace(tag))
	if t == "" || slices.Contains(s.tags, t) {
		return
	}
	s.tags = append(s.tags, t)
	sort.Strings(s.tags)
}

func (s *NodeStats) RmTag(tag string) {
	if s == nil {
		return
	}
	t := NormalizeTag(strings.TrimSpace(tag))
	if t == "" {
		return
	}
	out := make([]string, 0, len(s.tags))
	for _, existing := range s.tags {
		if existing == t {
			continue
		}
		out = append(out, existing)
	}
	s.tags = out
	sort.Strings(s.tags)
}

func (s *NodeStats) SetLinks(links []NodeId) {
	if s == nil {
		return
	}
	s.links = normalizeNodeIDList(links)
}

func (s *NodeStats) EnsureTimes(now time.Time) {
	if s == nil {
		return
	}
	if s.updated.IsZero() {
		s.updated = now
	}
	if s.created.IsZero() {
		s.created = now
	}
	if s.accessed.IsZero() {
		s.accessed = now
	}
}

func (s *NodeStats) UpdateFromContent(content *NodeContent, now *time.Time) {
	if s == nil || content == nil {
		return
	}
	s.SetHash(content.Hash, now)
	s.SetLead(content.Lead)
	s.SetLinks(content.Links)
	if tags, ok := parseTagsFromFrontmatter(content.Frontmatter); ok {
		s.SetTags(tags)
	}
}

func normalizeNodeIDList(links []NodeId) []NodeId {
	if len(links) == 0 {
		return []NodeId{}
	}

	byPath := make(map[string]NodeId, len(links))
	for _, link := range links {
		byPath[link.Path()] = link
	}

	out := make([]NodeId, 0, len(byPath))
	for _, link := range byPath {
		out = append(out, link)
	}

	slices.SortFunc(out, func(a, b NodeId) int { return a.Compare(b) })
	return out
}

func parseTagsFromFrontmatter(frontmatter map[string]any) ([]string, bool) {
	if frontmatter == nil {
		return nil, false
	}
	raw, ok := frontmatter["tags"]
	if !ok {
		return nil, false
	}
	return parseStatsTags(raw), true
}

func parseStatsTags(raw any) []string {
	switch v := raw.(type) {
	case nil:
		return []string{}
	case []string:
		out := NormalizeTags(v)
		sort.Strings(out)
		return out
	case []any:
		values := make([]string, 0, len(v))
		for _, item := range v {
			switch t := item.(type) {
			case string:
				values = append(values, t)
			default:
				values = append(values, fmt.Sprint(t))
			}
		}
		out := NormalizeTags(values)
		sort.Strings(out)
		return out
	case string:
		out := ParseTags(v)
		sort.Strings(out)
		return out
	default:
		out := ParseTags(fmt.Sprint(v))
		sort.Strings(out)
		return out
	}
}
