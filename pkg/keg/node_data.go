package keg

import (
	"context"
	"time"
)

// NodeData is a high-level representation of a KEG node. Implementations may
// compose this from repository pieces such as meta, content, and ancillary
// items.
type NodeData struct {
	// ID is the node identifier as a string (for example "42" or "42-0001").
	// Keep this lightweight while other fields are exposed via accessors.
	ID      NodeId
	Content *NodeContent
	Meta    *NodeMeta
	Stats   *NodeStats

	// Ancillary names (attachments and images). Implementations may populate these
	// from the repository.
	Items  []string
	Images []string
}

// ContentHash returns the content hash if content is present, otherwise the
// empty string.
func (n *NodeData) ContentHash() string {
	if n == nil || n.Content == nil {
		return ""
	}
	return n.Content.Hash
}

// MetaHash returns the stored programmatic hash when available.
func (n *NodeData) MetaHash() string {
	if n == nil || n.Stats == nil {
		return ""
	}
	return n.Stats.Hash()
}

// NodeContent has previously changed
func (n *NodeData) ContentChanged() bool {
	return n.ContentHash() != n.MetaHash()
}

// Title returns the canonical title for the node. Prefer stats title and fall
// back to parsed content title when available.
func (n *NodeData) Title() string {
	if n == nil {
		return ""
	}
	if n.Stats != nil {
		if t := n.Stats.Title(); t != "" {
			return t
		}
	}
	if n.Content != nil {
		return n.Content.Title
	}
	return ""
}

// Lead returns the short lead/summary for the node. Prefer stats then content.
func (n *NodeData) Lead() string {
	if n == nil {
		return ""
	}
	if n.Stats != nil {
		if l := n.Stats.Lead(); l != "" {
			return l
		}
	}
	if n.Content != nil {
		return n.Content.Lead
	}
	return ""
}

// Links returns the outgoing links discovered for the node. Prefer stats and
// fall back to parsed content links when stats are unavailable.
func (n *NodeData) Links() []NodeId {
	if n == nil {
		return nil
	}
	if n.Stats != nil {
		links := n.Stats.Links()
		return links
	}
	if n.Content != nil && len(n.Content.Links) > 0 {
		links := make([]NodeId, len(n.Content.Links))
		copy(links, n.Content.Links)
		return links
	}
	return nil
}

// Format returns the content format hint (for example "markdown" or "rst").
func (n *NodeData) Format() string {
	if n == nil || n.Content == nil {
		return ""
	}
	return n.Content.Format
}

// Updated returns the updated timestamp from stats when available.
func (n *NodeData) Updated() time.Time {
	if n == nil || n.Stats == nil {
		return time.Time{}
	}
	return n.Stats.Updated()
}

// Created returns the created timestamp from stats when available.
func (n *NodeData) Created() time.Time {
	if n == nil || n.Stats == nil {
		return time.Time{}
	}
	return n.Stats.Created()
}

// Accessed returns the accessed timestamp from stats when available.
func (n *NodeData) Accessed() time.Time {
	if n == nil || n.Stats == nil {
		return time.Time{}
	}
	return n.Stats.Accessed()
}

// Tags returns a copy of the normalized tag list from metadata or nil if not set.
func (n *NodeData) Tags() []string {
	if n == nil {
		return nil
	}
	if n.Meta != nil {
		tags := n.Meta.Tags()
		if len(tags) == 0 {
			return nil
		}
		out := make([]string, len(tags))
		copy(out, tags)
		return out
	}
	if n.Content == nil || n.Content.Frontmatter == nil {
		return nil
	}
	raw, ok := n.Content.Frontmatter["tags"]
	if !ok {
		return nil
	}
	tags := parseMetaTags(raw)
	if len(tags) == 0 {
		return nil
	}
	out := make([]string, len(tags))
	copy(out, tags)
	return out
}

// Ref builds a NodeIndexEntry from the NodeData. If the NodeData.ID is
// malformed ParseNode may fail and the function will fall back to a zero NodeId.
func (n *NodeData) Ref() NodeIndexEntry {
	return NodeIndexEntry{
		ID:      n.ID.Path(),
		Title:   n.Title(),
		Updated: n.Updated(),
	}
}

func (n *NodeData) UpdateMeta(ctx context.Context, now *time.Time) error {
	if n == nil || n.Content == nil {
		return nil
	}
	if n.Meta == nil {
		n.Meta = NewMeta(ctx, time.Time{})
	}
	if n.Stats == nil {
		n.Stats = &NodeStats{}
	}
	err := n.Meta.SetAttrs(ctx, n.Content.Frontmatter)
	n.Stats.UpdateFromContent(n.Content, now)
	return err
}

func (n *NodeData) Touch(ctx context.Context, now *time.Time) {
	_ = ctx
	if n == nil || now == nil {
		return
	}
	if n.Stats == nil {
		n.Stats = &NodeStats{}
	}
	n.Stats.SetAccessed(*now)
	n.Stats.IncrementAccessCount()
}
