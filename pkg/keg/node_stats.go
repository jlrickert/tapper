package keg

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jlrickert/cli-toolkit/toolkit"
)

type statsJSON struct {
	Title    string   `json:"title,omitempty"`
	Hash     string   `json:"hash,omitempty"`
	Updated  string   `json:"updated,omitempty"`
	Created  string   `json:"created,omitempty"`
	Accessed string   `json:"accessed,omitempty"`
	Accesses int      `json:"access_count,omitempty"`
	Lead     string   `json:"lead,omitempty"`
	Omega    *float64 `json:"omega,omitempty"`
	Links    []string `json:"links,omitempty"`
}

// NodeStats contains programmatic node data derived by tooling.
type NodeStats struct {
	title    string
	hash     string
	updated  time.Time
	created  time.Time
	accessed time.Time
	accesses int
	lead     string
	omega    *float64
	links    []NodeId
}

func NewStats(now time.Time) *NodeStats {
	return &NodeStats{
		updated: now,
		created: now,
		links:   []NodeId{},
	}
}

// ParseStats extracts programmatic node stats from raw JSON bytes.
func ParseStats(ctx context.Context, raw []byte) (*NodeStats, error) {
	_ = ctx

	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return &NodeStats{links: []NodeId{}}, nil
	}

	var js statsJSON
	if err := json.Unmarshal(trimmed, &js); err != nil {
		return nil, fmt.Errorf("failed to parse node stats json: %w", err)
	}
	return decodeStats(js.Title, js.Hash, js.Updated, js.Created, js.Accessed, js.Accesses, js.Lead, js.Omega, js.Links), nil
}

func decodeStats(title, hash, updated, created, accessed string, accesses int, lead string, omega *float64, rawLinks []string) *NodeStats {
	if accesses < 0 {
		accesses = 0
	}
	var omegaCopy *float64
	if omega != nil {
		v := *omega
		omegaCopy = &v
	}

	stats := &NodeStats{
		title:    title,
		hash:     hash,
		updated:  ParseStatsTime(updated),
		created:  ParseStatsTime(created),
		accessed: ParseStatsTime(accessed),
		accesses: accesses,
		lead:     lead,
		omega:    omegaCopy,
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

// ParseStatsTime parses a time string using the same format layouts accepted
// by stats.json timestamps: RFC3339Nano, RFC3339, and several date-only and
// datetime variants. Returns the zero time if raw is empty or unparseable.
func ParseStatsTime(raw string) time.Time {
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

func (s *NodeStats) AccessCount() int {
	if s == nil {
		return 0
	}
	if s.accesses < 0 {
		return 0
	}
	return s.accesses
}

func (s *NodeStats) SetAccessCount(count int) {
	if s == nil {
		return
	}
	if count < 0 {
		count = 0
	}
	s.accesses = count
}

func (s *NodeStats) IncrementAccessCount() {
	if s == nil {
		return
	}
	s.accesses++
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

func (s *NodeStats) Omega() (float64, bool) {
	if s == nil || s.omega == nil {
		return 0, false
	}
	return *s.omega, true
}

func (s *NodeStats) SetOmega(omega float64) {
	if s == nil {
		return
	}
	s.omega = &omega
}

func (s *NodeStats) ClearOmega() {
	if s == nil {
		return
	}
	s.omega = nil
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
	s.UpdateFromSource(nil, content, nil, now)
}

func (s *NodeStats) UpdateFromSource(rt *toolkit.Runtime, content *NodeContent, meta *NodeMeta, now *time.Time) {
	if s == nil || content == nil {
		return
	}
	s.SetTitle(content.Title)
	s.SetHash(nodeStateHash(rt, content.Hash, meta), now)
	s.SetLead(content.Lead)
	s.SetLinks(content.Links)
}

func nodeStateHash(rt *toolkit.Runtime, contentHash string, meta *NodeMeta) string {
	hasher := toolkit.OrDefaultHasher(nil)
	if rt != nil {
		hasher = toolkit.OrDefaultHasher(rt.Hasher())
	}

	metaHash := ""
	if meta != nil {
		metaYAML := meta.ToYAML()
		if strings.TrimSpace(metaYAML) != "" {
			metaHash = hasher.Hash([]byte(metaYAML))
		}
	}
	if contentHash == "" && metaHash == "" {
		return ""
	}

	var buf bytes.Buffer
	writeNodeStateHashPart(&buf, "node-state-v2")
	writeNodeStateHashPart(&buf, contentHash)
	writeNodeStateHashPart(&buf, metaHash)
	return hasher.Hash(buf.Bytes())
}

func writeNodeStateHashPart(buf *bytes.Buffer, value string) {
	_, _ = fmt.Fprintf(buf, "%d:", len(value))
	buf.WriteString(value)
	buf.WriteByte('\n')
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
	if omega, ok := s.Omega(); ok {
		wire.Omega = &omega
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
	if s.AccessCount() > 0 {
		wire.Accesses = s.AccessCount()
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
