package keg

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"time"

	"gopkg.in/yaml.v3"
)

type statsYAML struct {
	Hash     string    `yaml:"hash,omitempty"`
	Updated  time.Time `yaml:"updated,omitempty"`
	Created  time.Time `yaml:"created,omitempty"`
	Accessed time.Time `yaml:"accessed,omitempty"`
	Lead     string    `yaml:"lead,omitempty"`
	Links    []string  `yaml:"links,omitempty"`
}

// NodeStats contains programmatic node data derived by tooling.
type NodeStats struct {
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

// ParseStats extracts programmatic node stats from raw meta yaml bytes.
func ParseStats(ctx context.Context, raw []byte) (*NodeStats, error) {
	_ = ctx

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &NodeStats{links: []NodeId{}}, nil
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
		updated:  tmp.Updated,
		created:  tmp.Created,
		accessed: tmp.Accessed,
		lead:     tmp.Lead,
		links:    make([]NodeId, 0, len(tmp.Links)),
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
	s.SetHash(content.Hash, now)
	s.SetLead(content.Lead)
	s.SetLinks(content.Links)
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
