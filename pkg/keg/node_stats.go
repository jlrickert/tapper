package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type statsJSON struct {
	Title    string   `json:"title,omitempty"`
	Hash     string   `json:"hash,omitempty"`
	Updated  string   `json:"updated,omitempty"`
	Created  string   `json:"created,omitempty"`
	Accessed string   `json:"accessed,omitempty"`
	Lead     string   `json:"lead,omitempty"`
	Links    []string `json:"links,omitempty"`
}

// statsYAML is kept for compatibility with historical on-disk stats encodings.
type statsYAML struct {
	Title    string   `yaml:"title,omitempty"`
	Hash     string   `yaml:"hash,omitempty"`
	Updated  string   `yaml:"updated,omitempty"`
	Created  string   `yaml:"created,omitempty"`
	Accessed string   `yaml:"accessed,omitempty"`
	Lead     string   `yaml:"lead,omitempty"`
	Links    []string `yaml:"links,omitempty"`
}

// NodeStats contains programmatic node data derived by tooling.
type NodeStats struct {
	title    string
	hash     string
	updated  time.Time
	created  time.Time
	accessed time.Time
	lead     string
	links    []NodeId
}

func NewStats(now time.Time) *NodeStats {
	return &NodeStats{
		updated: now,
		created: now,
		links:   []NodeId{},
	}
}

// ParseStats extracts programmatic node stats from raw bytes.
// The canonical encoding is JSON; YAML is accepted as a compatibility fallback.
func ParseStats(ctx context.Context, raw []byte) (*NodeStats, error) {
	_ = ctx

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &NodeStats{links: []NodeId{}}, nil
	}

	var js statsJSON
	if err := json.Unmarshal(trimmed, &js); err == nil {
		return decodeStats(js.Title, js.Hash, js.Updated, js.Created, js.Accessed, js.Lead, js.Links), nil
	}

	// Compatibility path for legacy YAML stats payloads.
	var doc yaml.Node
	if err := yaml.Unmarshal(trimmed, &doc); err != nil {
		return nil, fmt.Errorf("failed to parse node stats json: %w", err)
	}
	var ys statsYAML
	if len(doc.Content) > 0 {
		if err := doc.Content[0].Decode(&ys); err != nil {
			if err2 := doc.Decode(&ys); err2 != nil {
				return nil, fmt.Errorf("failed to decode node stats yaml: %w", err)
			}
		}
	}
	return decodeStats(ys.Title, ys.Hash, ys.Updated, ys.Created, ys.Accessed, ys.Lead, ys.Links), nil
}

func decodeStats(title, hash, updated, created, accessed, lead string, rawLinks []string) *NodeStats {
	stats := &NodeStats{
		title:    title,
		hash:     hash,
		updated:  parseStatsTime(updated),
		created:  parseStatsTime(created),
		accessed: parseStatsTime(accessed),
		lead:     lead,
		links:    make([]NodeId, 0, len(rawLinks)),
	}

	for _, rawLink := range rawLinks {
		n, err := ParseNode(rawLink)
		if err != nil || n == nil {
			continue
		}
		stats.links = append(stats.links, *n)
	}
	stats.links = normalizeNodeIDList(stats.links)
	return stats
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

func (s *NodeStats) Title() string {
	if s == nil {
		return ""
	}
	return s.title
}

func (s *NodeStats) SetTitle(title string) {
	if s == nil {
		return
	}
	s.title = title
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
	s.SetTitle(content.Title)
	s.SetHash(content.Hash, now)
	s.SetLead(content.Lead)
	s.SetLinks(content.Links)
}

func (s *NodeStats) ToJSON() ([]byte, error) {
	if s == nil {
		s = &NodeStats{}
	}
	wire := statsJSON{
		Title: s.Title(),
		Hash:  s.Hash(),
		Lead:  s.Lead(),
	}
	if !s.Updated().IsZero() {
		wire.Updated = s.Updated().Format(time.RFC3339)
	}
	if !s.Created().IsZero() {
		wire.Created = s.Created().Format(time.RFC3339)
	}
	if !s.Accessed().IsZero() {
		wire.Accessed = s.Accessed().Format(time.RFC3339)
	}
	links := s.Links()
	if len(links) > 0 {
		wire.Links = make([]string, 0, len(links))
		for _, link := range links {
			wire.Links = append(wire.Links, link.Path())
		}
	}
	return json.Marshal(wire)
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
