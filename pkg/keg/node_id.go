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

// NodeId is the stable numeric identifier for a KEG node. The ID field is the
// canonical non-negative integer identifier. The optional Code field is a
// zero-padded 4-digit numeric suffix used to represent an uncommitted or
// temporary variant of the node.
type NodeId struct {
	ID    int
	Alias string
	// Code is an additional random identifier used to signify an uncommitted node.
	Code string
}

func RandomCode(context.Context) string {
	// generate a 0..9999 number using crypto/rand, fallback to nanotime if rand
	// fails
	b := make([]byte, 2)
	var num int
	if _, err := crand.Read(b); err == nil {
		num = (int(b[0])<<8 | int(b[1])) % 10000
	} else {
		num = int(time.Now().UnixNano() % 10000)
	}
	code := fmt.Sprintf("%04d", num)
	return code
}

// NewTempNode creates a new NodeId using the provided base id string and a
// 4-digit numeric code. The function attempts to parse the base id via
// ParseNode; if that fails it will try to parse the string as a non-negative
// integer. If the id is empty or cannot be parsed as a non-negative integer
// the returned NodeId will have ID set to 0.
//
// The Code is generated with crypto/rand when available and falls back to the
// current nanotime if random bytes cannot be obtained. The code is returned
// as a zero-padded 4-digit string.
//
// The context parameter is accepted to allow future callers to pass context
// without changing the signature. It is not used by the current
// implementation.
func NewTempNode(ctx context.Context, id string) *NodeId {
	_ = ctx
	baseID := 0
	alias := ""
	if id != "" {
		if n, err := ParseNode(id); err == nil && n != nil {
			baseID = n.ID
			alias = n.Alias
		} else if v, err := strconv.Atoi(id); err == nil && v >= 0 {
			baseID = v
		}
	}

	code := RandomCode(ctx)

	return &NodeId{ID: baseID, Code: code, Alias: alias}
}

// Path returns the path component for this NodeId suitable for use in file
// names or URLs.
//
// Examples:
//
//	NodeId{ID:42, Code:""}      -> "42"
//	NodeId{ID:42, Code:"0001"}  -> "42-0001"
//	NodeId{ID:42, Keg:"work"} -> "keg:work/42"
func (id NodeId) Path() string {
	if id.Alias != "" {
		if id.Code != "" {
			return "keg:" + id.Alias + "/" + strconv.Itoa(id.ID) + "-" + id.Code
		}
		return "keg:" + id.Alias + "/" + strconv.Itoa(id.ID)
	}
	if id.Code != "" {
		return strconv.Itoa(id.ID) + "-" + id.Code
	}
	return strconv.Itoa(id.ID)
}

func (id NodeId) String() string { return id.Path() }

// ParseNode converts a string into a *NodeId.
//
// Accepted forms:
//
//   - "0" or a non-negative integer without leading zeros (for example "1",
//     "23")
//
//   - "<id>-<code>" where <id> follows the rules above and <code> is exactly
//     4 digits
//
//   - "keg:<alias>/<id>" or "keg:<alias>/<id>-<code>" to include an alias.
//
// Examples:
//
//	"42"               -> &NodeId{ID:42, Code:""}, nil
//	"42-0001"          -> &NodeId{ID:42, Code:"0001"}, nil
//	"keg:work/23"      -> &NodeId{ID:23, Keg:"work"}, nil
//	"keg:work/23-0001" -> &NodeId{ID:23, Keg:"work", Code:"0001"}, nil
//	"0023"             -> nil, error (leading zeros not allowed)
//	""                 -> nil, error
func ParseNode(s string) (*NodeId, error) {
	if s == "" {
		return nil, fmt.Errorf("parse node id: empty")
	}

	// handle optional keg alias prefix "keg:<alias>/..."
	alias := ""
	if strings.HasPrefix(s, "keg:") {
		rest := s[len("keg:"):]
		// expect alias followed by '/' then id
		slash := strings.IndexByte(rest, '/')
		if slash < 0 {
			return nil, fmt.Errorf("parse node id %q: missing slash after alias", s)
		}
		alias = rest[:slash]
		if alias == "" {
			return nil, fmt.Errorf("parse node id %q: empty alias", s)
		}
		s = rest[slash+1:]
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
		for j := 0; j < 4; j++ {
			c := codePart[j]
			if c < '0' || c > '9' {
				return nil, fmt.Errorf("parse node id %q: code must be numeric", s)
			}
		}
		return &NodeId{ID: n, Code: codePart, Alias: alias}, nil
	}

	return &NodeId{ID: n, Alias: alias}, nil
}

// Valid reports whether the NodeId ID is a non-negative integer.
func (id NodeId) Valid() bool { return id.ID >= 0 }

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

// Equals reports whether two Nodes are identical in ID and Code.
func (n NodeId) Equals(other NodeId) bool {
	return n.ID == other.ID && n.Code == other.Code && n.Alias == other.Alias
}

// Lt reports whether n is strictly less than other using ID then Code.
func (n NodeId) Lt(other NodeId) bool {
	if n.ID == other.ID {
		if n.Code == other.Code {
			return n.Alias < other.Alias
		}
		return n.Code < other.Code
	}
	return n.ID < other.ID
}

// Gt reports whether n is strictly greater than other using ID then Code.
func (n NodeId) Gt(other NodeId) bool {
	if n.ID == other.ID {
		if n.Code == other.Code {
			return n.Alias > other.Alias
		}
		return n.Code > other.Code
	}
	return n.ID > other.ID
}

// Gte reports whether n is greater than or equal to other.
func (n NodeId) Gte(other NodeId) bool {
	return n.Gt(other) || n.Equals(other)
}

// Lte reports whether n is less than or equal to other.
func (n NodeId) Lte(other NodeId) bool {
	return n.Lt(other) || n.Equals(other)
}

// Compare returns -1 if n < other, 1 if n > other, and 0 if they are equal.
func (n NodeId) Compare(other NodeId) int {
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
	if n.Alias < other.Alias {
		return -1
	}
	if n.Alias > other.Alias {
		return 1
	}
	return 0
}

// Increment returns a new NodeId with the ID value increased by one while
// preserving the Code.
func (n NodeId) Increment() NodeId {
	return NodeId{ID: n.ID + 1, Code: n.Code, Alias: n.Alias}
}
