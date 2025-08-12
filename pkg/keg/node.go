package keg

import (
	"fmt"
	"strconv"
	"time"
)

// NodeID is the stable numeric identifier for a KEG node.
type NodeID int

// Path returns the disk/URL path component for this NodeID (just the decimal id).
// Example: NodeID(42).Path() -> "42"
func (id NodeID) Path() string { return strconv.Itoa(int(id)) }

func (id NodeID) String() string { return id.Path() }

// ParseNodeID converts a decimal string into a NodeID. Returns an error for
// non-integer or negative values.
func ParseNodeID(s string) (NodeID, error) {
	if s == "" {
		return 0, fmt.Errorf("parse node id: empty")
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("parse node id %q: %w", s, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("parse node id %q: negative id", s)
	}
	return NodeID(n), nil
}

// Valid reports whether the NodeID is a non-negative (>= 0) value.
func (id NodeID) Valid() bool { return id >= 0 }

// NodeRef is a small descriptor for a node used by repository listings and
// indices. It contains the node id, a human-friendly title, and the last
// updated timestamp.
type NodeRef struct {
	ID      NodeID    `json:"id" yaml:"id"`
	Title   string    `json:"title" yaml:"title"`
	Updated time.Time `json:"updated" yaml:"updated"`
}

// NodeStats groups commonly used node timestamps returned by repository
// implementations and composed from Meta.GetStats.
type NodeStats struct {
	// Updated is the node's last meaningful modification time.
	Updated time.Time
	// Created is the node's creation time (when available).
	Created time.Time
	// Access is the last-read/access time.
	Access time.Time
}

// Node is a high-level representation of a KEG node, composed from repository
// pieces (meta, content, ancillary items).
type Node struct {
	ID      NodeID   // numeric identifier
	Ref     NodeRef  // small descriptor (id, title, updated)
	Meta    *Meta    // parsed metadata (may be nil if absent)
	Content *Content // parsed content (may be nil if absent)

	// Ancillary names (attachments/images). Implementations may populate these
	// from the repository.
	Items  []string
	Images []string
}

// NodeCreateOptions contains optional parameters used when creating a node.
type NodeCreateOptions struct {
	// Optional explicit ID to attempt to allocate. If zero, repository may
	// choose the next available ID.
	ID NodeID

	// Initial metadata. If nil, a default/new meta map is used.
	Meta *Meta

	// Initial content bytes (README.md). May be nil.
	Content []byte

	// If true, persist atomically and set created/updated timestamps.
	EnsureTimestamps bool
}
