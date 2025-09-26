package keg

import (
	"context"
	crand "crypto/rand"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Package keg defines the identifier type and related high-level node
// data structures used by KEG repository implementations.

// Node is the stable numeric identifier for a KEG node. The ID field is the
// canonical non-negative integer identifier. The optional Code field is a
// zero-padded 4-digit numeric suffix used to represent an uncommitted or
// temporary variant of the node.
type Node struct {
	ID int
	// Code is an additional random identifier used to signify an uncommitted node.
	Code string
}

// NewTempNodeID creates a new Node using the provided base id string and a
// 4-digit numeric code. The function attempts to parse the base id via
// ParseNode; if that fails it will try to parse the string as a non-negative
// integer. If the id is empty or cannot be parsed as a non-negative integer
// the returned Node will have ID set to 0.
//
// The Code is generated with crypto/rand when available and falls back to the
// current nanotime if random bytes cannot be obtained. The code is returned
// as a zero-padded 4-digit string.
//
// The context parameter is accepted to allow future callers to pass context
// without changing the signature. It is not used by the current
// implementation.
func NewTempNodeID(ctx context.Context, id string) *Node {
	_ = ctx
	baseID := 0
	if id != "" {
		if n, err := ParseNode(id); err == nil && n != nil {
			baseID = n.ID
		} else if v, err := strconv.Atoi(id); err == nil && v >= 0 {
			baseID = v
		}
	}

	// generate a 0..9999 number using crypto/rand, fallback to nanotime if rand fails
	b := make([]byte, 2)
	var num int
	if _, err := crand.Read(b); err == nil {
		num = (int(b[0])<<8 | int(b[1])) % 10000
	} else {
		num = int(time.Now().UnixNano() % 10000)
	}
	code := fmt.Sprintf("%04d", num)

	return &Node{ID: baseID, Code: code}
}

// Path returns the path component for this Node suitable for use in file
// names or URLs.
//
// Examples:
//
//	Node{ID:42, Code:""}      -> "42"
//	Node{ID:42, Code:"0001"}  -> "42-0001"
func (id Node) Path() string {
	if id.Code != "" {
		return strconv.Itoa(id.ID) + "-" + id.Code
	}
	return strconv.Itoa(id.ID)
}

func (id Node) String() string { return id.Path() }

// ParseNode converts a string into a *Node.
//
// Accepted forms:
//   - "0" or a non-negative integer without leading zeros (for example "1", "23")
//   - "<id>-<code>" where <id> follows the rules above and <code> is exactly 4 digits
//
// Examples:
//
//	"42"       -> &Node{ID:42, Code:""}, nil
//	"42-0001"  -> &Node{ID:42, Code:"0001"}, nil
//	"0023"     -> nil, error (leading zeros not allowed)
//	""         -> nil, error
func ParseNode(s string) (*Node, error) {
	if s == "" {
		return nil, fmt.Errorf("parse node id: empty")
	}

	// find hyphen if present
	i := strings.IndexByte(s, '-')
	var idPart, codePart string
	if i >= 0 {
		idPart = s[:i]
		codePart = s[i+1:]
	} else {
		idPart = s
		codePart = ""
	}

	// idPart must be digits
	if idPart == "" {
		return nil, fmt.Errorf("parse node id %q: empty id part", s)
	}
	for j := 0; j < len(idPart); j++ {
		c := idPart[j]
		if c < '0' || c > '9' {
			return nil, fmt.Errorf("parse node id %q: non-digit in id", s)
		}
	}
	// disallow leading zeros except for the single digit "0"
	if len(idPart) > 1 && idPart[0] == '0' {
		return nil, fmt.Errorf("parse node id %q: invalid leading zeros", s)
	}

	n, err := strconv.Atoi(idPart)
	if err != nil {
		return nil, fmt.Errorf("parse node id %q: %w", s, err)
	}
	if n < 0 {
		return nil, fmt.Errorf("parse node id %q: negative id", s)
	}

	// If there is a code part, validate it's exactly 4 digits.
	if codePart != "" {
		if len(codePart) != 4 {
			return nil, fmt.Errorf("parse node id %q: code must be 4 digits", s)
		}
		for j := range 4 {
			c := codePart[j]
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("parse node id %q: code must be numeric", s)
			}
		}
		return &Node{ID: n, Code: codePart}, nil
	}

	return &Node{ID: n}, nil
}

// Valid reports whether the Node ID is a non-negative integer.
func (id Node) Valid() bool { return id.ID >= 0 }

// NodeIndexEntry is a small descriptor for a node used by repository listings
// and indices. It contains the node id as a string, a human-friendly title,
// and the last updated timestamp.
type NodeIndexEntry struct {
	ID      string    `json:"id" yaml:"id"`
	Title   string    `json:"title" yaml:"title"`
	Updated time.Time `json:"updated" yaml:"updated"`
}

// NodeStats groups commonly used node timestamps returned by repository
// implementations and composed from Meta.GetStats.
type NodeStats struct {
	// Updated is the node's last meaningful modification time.
	Updated time.Time
	// Created is the node's creation time when available.
	Created time.Time
	// Access is the last-read or access time.
	Access time.Time
}

// NodeData is a high-level representation of a KEG node. Implementations may
// compose this from repository pieces such as meta, content, and ancillary
// items.
type NodeData struct {
	ID       string // identifier
	Hash     string
	Title    string
	Lead     string
	Links    []Node
	Format   string
	Updated  time.Time
	Created  time.Time
	Accessed time.Time
	Tags     []string

	// Ancillary names (attachments and images). Implementations may populate these
	// from the repository.
	Items  []string
	Images []string
}

// Equals reports whether two Nodes are identical in ID and Code.
func (n Node) Equals(other Node) bool {
	return n.ID == other.ID && n.Code == other.Code
}

// Lt reports whether n is strictly less than other using ID then Code.
func (n Node) Lt(other Node) bool {
	if n.ID == other.ID {
		return n.Code < other.Code
	}
	return n.ID < other.ID
}

// Gt reports whether n is strictly greater than other using ID then Code.
func (n Node) Gt(other Node) bool {
	if n.ID == other.ID {
		return n.Code > other.Code
	}
	return n.ID > other.ID
}

// Gte reports whether n is greater than or equal to other.
func (n Node) Gte(other Node) bool {
	return n.Gt(other) || n.Equals(other)
}

// Lte reports whether n is less than or equal to other.
func (n Node) Lte(other Node) bool {
	return n.Lt(other) || n.Equals(other)
}

// Compare returns -1 if n < other, 1 if n > other, and 0 if they are equal.
func (n Node) Compare(other Node) int {
	if n.ID < other.ID {
		return -1
	}
	if n.ID > other.ID {
		return 1
	}
	if n.Code < other.Code {
		return -1
	}
	if n.Code > other.Code {
		return 1
	}
	return 0
}

// Increment returns a new Node with the ID value increased by one while
// preserving the Code.
func (n Node) Increment() Node {
	return Node{ID: n.ID + 1, Code: n.Code}
}

// Ref builds a NodeIndexEntry from the NodeData. If the NodeData.ID is
// malformed ParseNode may fail and the function will fall back to a zero Node.
func (d *NodeData) Ref() *NodeIndexEntry {
	// ParseNode may fail for malformed IDs; fall back to a zero Node when that happens.
	n, err := ParseNode(d.ID)
	if err != nil || n == nil {
		n = &Node{ID: 0}
	}
	return &NodeIndexEntry{
		ID:      n.Path(),
		Title:   d.Title,
		Updated: d.Updated,
	}
}
