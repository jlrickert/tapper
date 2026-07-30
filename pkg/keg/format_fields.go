package keg

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Listing formats and query expressions name node fields with one vocabulary.
// A selector appears in two syntactically distinct positions, and a bare word
// means something different in each:
//
//   - Predicate position (query expressions): a bare word is a TAG, because a
//     metadata key is always written as key=value there.
//   - Field position (listing formats): a bare word is a METADATA KEY, because
//     there is no value to compare against and a tag is not a field.
//
// Statistics fields carry a leading dot in both positions, so the one selector
// that appears in both means the same thing in both.

// FieldKind classifies a listing field selector by where its value comes from.
// The kind determines whether rendering a selector costs any I/O: intrinsics
// and index timestamps are served from the node index entry already in hand,
// while metadata and the remaining statistics fields require a per-node read.
type FieldKind int

const (
	// FieldUnknown is the zero value and names no field.
	FieldUnknown FieldKind = iota
	// FieldID is the intrinsic node id, from NodeIndexEntry.
	FieldID
	// FieldTitle is the intrinsic node title, from NodeIndexEntry.
	FieldTitle
	// FieldIndexTime is a timestamp served from NodeIndexEntry without I/O.
	FieldIndexTime
	// FieldStat is a statistics field requiring a NodeStats read.
	FieldStat
	// FieldTags is the reserved tag-list selector, requiring a NodeMeta read.
	FieldTags
	// FieldMetaKey is an arbitrary metadata key, requiring a NodeMeta read.
	FieldMetaKey
)

// FieldSelector is one parsed listing field selector. Text is the selector as
// written; Key is the bare lookup name with any leading dot stripped.
type FieldSelector struct {
	Text string
	Kind FieldKind
	Key  string
}

// NeedsMeta reports whether rendering this selector requires the node's
// metadata, which costs one read per node.
func (f FieldSelector) NeedsMeta() bool {
	return f.Kind == FieldTags || f.Kind == FieldMetaKey
}

// NeedsStats reports whether rendering this selector requires the node's
// statistics, which costs one read per node. Index timestamps do not, even
// though they are spelled like statistics fields.
func (f FieldSelector) NeedsStats() bool {
	return f.Kind == FieldStat
}

// IndexTimeFieldNames lists the statistics fields that resolve from the node
// index rather than from stats.json. These mirror resolveStatsCompare's
// no-I/O branch so a displayed value always agrees with the same predicate in
// a query expression.
var IndexTimeFieldNames = []string{"updated", "created", "accessed"}

// ReservedFieldNames lists the bare words that do not name a metadata key.
// A node carrying metadata under one of these keys cannot address it in
// field position; the intrinsic wins.
var ReservedFieldNames = []string{"id", "title", "tags"}

// LegacyFormatVerbs maps the historical single-letter format verbs onto
// selector text. These remain supported as aliases; no new letters are added,
// because a single letter cannot address an arbitrary metadata key.
var LegacyFormatVerbs = map[byte]string{
	'i': "id",
	't': "title",
	'd': ".updated",
	'c': ".created",
	'a': ".accessed",
}

// FormatVocabularyDescription is the one-line summary of the listing field
// vocabulary. It is duplicated as a literal in the MCP tool schemas, which
// require a constant struct tag; a test holds the two in agreement.
const FormatVocabularyDescription = "output format template. Legacy verbs %i (id), %t (title), %d (updated), %c (created), %a (accessed); %% is a literal percent. Named selectors use %{...}: a bare word names a metadata key such as %{type} or %{status}, a leading dot names a statistics field such as %{.accessCount} or %{.omega}, and %{tags} is the node's tag list. Selectors other than id, title, and the three dates read one file per node."

func isIndexTimeField(name string) bool {
	return slices.Contains(IndexTimeFieldNames, name)
}

func isStatsField(name string) bool {
	return slices.Contains(StatsFieldNames, name)
}

// ParseFieldSelector classifies raw as a listing field selector. Surrounding
// ASCII spaces are ignored so "%{ type }" behaves like "%{type}"; interior
// spaces are preserved because a YAML key may contain them.
//
// A bare word is always accepted, because metadata keys are open-ended and
// cannot be validated against a fixed list. A dotted name is rejected unless
// it is a known statistics field, since that vocabulary is closed.
func ParseFieldSelector(raw string) (FieldSelector, error) {
	text := strings.Trim(raw, " \t")
	if text == "" {
		return FieldSelector{}, fmt.Errorf("empty selector")
	}
	// Predicate syntax in field position is the likely result of pasting a
	// query expression, so name that specifically rather than failing as a
	// generic bad character.
	if strings.ContainsAny(text, "=<>!") {
		return FieldSelector{}, fmt.Errorf("selector %q is a field name, not a predicate", text)
	}
	if strings.ContainsAny(text, "%{}") {
		return FieldSelector{}, fmt.Errorf("invalid selector %q", text)
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 0x20 || text[i] == 0x7f {
			return FieldSelector{}, fmt.Errorf("invalid selector %q", text)
		}
	}

	if strings.HasPrefix(text, ".") {
		name := text[1:]
		if !isStatsField(name) {
			return FieldSelector{}, fmt.Errorf(
				"unknown stats field %q (valid: %s)", text, strings.Join(StatsFieldNames, ", "))
		}
		kind := FieldStat
		if isIndexTimeField(name) {
			kind = FieldIndexTime
		}
		return FieldSelector{Text: text, Kind: kind, Key: name}, nil
	}

	switch text {
	case "id":
		return FieldSelector{Text: text, Kind: FieldID, Key: text}, nil
	case "title":
		return FieldSelector{Text: text, Kind: FieldTitle, Key: text}, nil
	case "tags":
		return FieldSelector{Text: text, Kind: FieldTags, Key: text}, nil
	}
	return FieldSelector{Text: text, Kind: FieldMetaKey, Key: text}, nil
}

// StatsFieldValue renders the named statistics field for display. known
// reports whether name is a recognized statistics field; an empty value with
// known true means the field is absent or unset on this node.
//
// Absent values render empty rather than as a placeholder so a tabular format
// keeps a stable column count. accessCount is the one exception: it always
// renders its integer, including zero, because stats.json omits the key when
// it is zero and so absent and zero are indistinguishable on disk.
func StatsFieldValue(s *NodeStats, name string) (string, bool) {
	if !isStatsField(name) {
		return "", false
	}
	if s == nil {
		if name == "accessCount" {
			return "0", true
		}
		return "", true
	}

	switch name {
	case "updated":
		return formatStatsTime(s.Updated()), true
	case "created":
		return formatStatsTime(s.Created()), true
	case "accessed":
		return formatStatsTime(s.Accessed()), true
	case "hash":
		return s.Hash(), true
	case "lead":
		return s.Lead(), true
	case "accessCount":
		return strconv.Itoa(s.AccessCount()), true
	case "omega":
		omega, ok := s.Omega()
		if !ok {
			return "", true
		}
		return strconv.FormatFloat(omega, 'f', -1, 64), true
	}
	return "", false
}

// formatStatsTime renders a timestamp for display. A zero time renders empty
// rather than 0001-01-01T00:00:00Z, which parses as a real date and would
// silently corrupt downstream sorting and filtering.
func formatStatsTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// ParseFieldSelectors classifies a list of selectors, rejecting the whole list
// if any entry is invalid so a listing never silently drops a column.
func ParseFieldSelectors(raw []string) ([]FieldSelector, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	out := make([]FieldSelector, 0, len(raw))
	for _, text := range raw {
		sel, err := ParseFieldSelector(text)
		if err != nil {
			return nil, err
		}
		out = append(out, sel)
	}
	return out, nil
}

// ParseSortSelector classifies a sort key. An empty key yields the zero
// selector, meaning "leave the listing in its natural index order".
func ParseSortSelector(raw string) (FieldSelector, error) {
	if strings.TrimSpace(raw) == "" {
		return FieldSelector{}, nil
	}
	return ParseFieldSelector(raw)
}

// SelectorNeeds reports whether a set of selectors requires reading node
// metadata or statistics. Callers use it to skip per-node reads entirely for
// listings that name only intrinsics and index timestamps.
func SelectorNeeds(selectors []FieldSelector) (meta, stats bool) {
	for _, sel := range selectors {
		if sel.NeedsMeta() {
			meta = true
		}
		if sel.NeedsStats() {
			stats = true
		}
	}
	return meta, stats
}

// FieldValue resolves one selector against a node's index entry and, when the
// selector requires them, its metadata and statistics. meta and stats may be
// nil; an unresolvable value renders empty so a tabular listing keeps a stable
// column count.
//
// Intrinsics and index timestamps come from the entry, never from stats.json,
// so a displayed value always agrees with the same predicate in a query
// expression and the default listing stays free of per-node reads.
func FieldValue(sel FieldSelector, entry NodeIndexEntry, meta *NodeMeta, stats *NodeStats) string {
	switch sel.Kind {
	case FieldID:
		return entry.ID
	case FieldTitle:
		return entry.Title
	case FieldIndexTime:
		return formatStatsTime(entryTimeField(entry, sel.Key))
	case FieldStat:
		value, _ := StatsFieldValue(stats, sel.Key)
		return value
	case FieldTags, FieldMetaKey:
		if meta == nil {
			return ""
		}
		value, _ := meta.Get(sel.Key)
		return value
	}
	return ""
}

// entryTimeField returns the index timestamp named by an index-time selector.
func entryTimeField(entry NodeIndexEntry, name string) time.Time {
	switch name {
	case "updated":
		return entry.Updated
	case "created":
		return entry.Created
	case "accessed":
		return entry.Accessed
	}
	return time.Time{}
}

// sortNodeIndexEntriesByID orders entries by numeric node id, matching the
// natural dex order.
func sortNodeIndexEntriesByID(entries []NodeIndexEntry) {
	slices.SortStableFunc(entries, func(a, b NodeIndexEntry) int {
		left, lerr := ParseNode(a.ID)
		right, rerr := ParseNode(b.ID)
		if lerr != nil || rerr != nil || left == nil || right == nil {
			return strings.Compare(a.ID, b.ID)
		}
		switch {
		case left.Lt(*right):
			return -1
		case right.Lt(*left):
			return 1
		}
		return 0
	})
}

// FormatSelectorSuggestions returns the closed part of the field vocabulary as
// ready-to-type format tokens, for shell completion. Metadata keys are
// open-ended and therefore absent.
func FormatSelectorSuggestions() []string {
	out := make([]string, 0, len(ReservedFieldNames)+len(StatsFieldNames))
	for _, name := range ReservedFieldNames {
		out = append(out, "%{"+name+"}")
	}
	for _, name := range StatsFieldNames {
		out = append(out, "%{."+name+"}")
	}
	return out
}
