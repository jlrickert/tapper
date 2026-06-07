package keg

import (
	"fmt"
	"regexp"
	"strings"
)

// refSegmentPattern restricts the namespace and keg-name segments of a
// qualified node reference to a portable, filesystem-safe shape. It is the same
// shape tapper enforces for aliases and namespaces; the absence of a dot keeps
// reserved sentinels such as flights.d from ever appearing as a namespace.
var refSegmentPattern = regexp.MustCompile(`^[a-z0-9_-]+$`)

// RefForm enumerates the three shapes a node reference may take.
type RefForm int

const (
	// RefLocal is a bare "<id>" or "<id>-<code>" resolving against the current keg.
	RefLocal RefForm = iota
	// RefAlias is "keg:<alias>/<id>[-<code>]" — the alias resolves against the
	// current keg's Links table (then the tap-config kegs map).
	RefAlias
	// RefQualified is "keg:@<namespace>/<keg>/<id>[-<code>]" — fully qualified;
	// the hub is implied from the current keg's hub.
	RefQualified
)

// NodeRef is a parsed node reference. Node always carries the numeric id and
// optional 4-digit code. Form selects how the owning keg is addressed:
//
//   - RefLocal:     Node only; resolves against the current keg.
//   - RefAlias:     Alias set; resolves against the current keg's Links table
//     then the tap-config kegs map. Node.Alias mirrors Alias.
//   - RefQualified: Namespace+KegName set; the hub is implied from context.
type NodeRef struct {
	Form      RefForm
	Node      NodeId
	Alias     string // RefAlias only
	Namespace string // RefQualified only, "@" sigil stripped
	KegName   string // RefQualified only
}

// ParseNodeRef parses any of the three node-reference forms. It is a superset of
// ParseNode: bare ids and "keg:<alias>/<id>" parse identically, and the
// qualified "keg:@<namespace>/<keg>/<id>" form is additionally recognized.
//
// The forms are disambiguated purely textually: a leading "keg:@" with two
// slashes is qualified; "keg:" with one slash is an alias; anything else is a
// bare local id.
func ParseNodeRef(s string) (*NodeRef, error) {
	if s == "" {
		return nil, fmt.Errorf("parse node ref: empty")
	}

	if !strings.HasPrefix(s, "keg:") {
		id, code, err := parseIdCode(s)
		if err != nil {
			return nil, err
		}
		return &NodeRef{Form: RefLocal, Node: NodeId{ID: id, Code: code}}, nil
	}

	body := s[len("keg:"):]

	if strings.HasPrefix(body, "@") {
		// Qualified: @<namespace>/<keg>/<id>[-<code>]
		parts := strings.SplitN(body[1:], "/", 3)
		if len(parts) != 3 {
			return nil, fmt.Errorf("parse node ref %q: qualified form needs @<namespace>/<keg>/<id>", s)
		}
		ns, kegName, idStr := parts[0], parts[1], parts[2]
		if !refSegmentPattern.MatchString(ns) {
			return nil, fmt.Errorf("parse node ref %q: invalid namespace %q: must match %s", s, ns, refSegmentPattern.String())
		}
		if !refSegmentPattern.MatchString(kegName) {
			return nil, fmt.Errorf("parse node ref %q: invalid keg name %q: must match %s", s, kegName, refSegmentPattern.String())
		}
		id, code, err := parseIdCode(idStr)
		if err != nil {
			return nil, err
		}
		return &NodeRef{
			Form:      RefQualified,
			Node:      NodeId{ID: id, Code: code},
			Namespace: ns,
			KegName:   kegName,
		}, nil
	}

	// Alias: <alias>/<id>[-<code>]
	slash := strings.IndexByte(body, '/')
	if slash < 0 {
		return nil, fmt.Errorf("parse node ref %q: missing slash after alias", s)
	}
	alias := body[:slash]
	if alias == "" {
		return nil, fmt.Errorf("parse node ref %q: empty alias", s)
	}
	if !refSegmentPattern.MatchString(alias) {
		return nil, fmt.Errorf("parse node ref %q: invalid alias %q: must match %s", s, alias, refSegmentPattern.String())
	}
	id, code, err := parseIdCode(body[slash+1:])
	if err != nil {
		return nil, err
	}
	return &NodeRef{
		Form:  RefAlias,
		Node:  NodeId{ID: id, Code: code, Alias: alias},
		Alias: alias,
	}, nil
}

// String renders the canonical text form, the inverse of ParseNodeRef.
func (r NodeRef) String() string {
	switch r.Form {
	case RefAlias:
		return "keg:" + r.Alias + "/" + r.Node.PathNumeric()
	case RefQualified:
		return "keg:@" + r.Namespace + "/" + r.KegName + "/" + r.Node.PathNumeric()
	default:
		return r.Node.PathNumeric()
	}
}
