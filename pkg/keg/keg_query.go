package keg

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Query evaluates a boolean query expression against the keg's index entries
// and returns the matching entries in dex order. The expression grammar
// supports plain tag names, key=value attribute predicates, attribute
// comparisons (omega>=0.5), and .field stats predicates (.updated>2026-01-01);
// see ParseQueryExpression.
func (k *LocalKeg) Query(ctx context.Context, opts QueryOptions) ([]NodeIndexEntry, error) {
	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to read dex: %w", err)
	}
	entries := dex.Nodes(ctx)

	matched, err := k.evalQueryExpr(ctx, dex, entries, opts.Expr)
	if err != nil {
		return nil, err
	}

	out := make([]NodeIndexEntry, 0, len(matched))
	for _, entry := range entries {
		if _, ok := matched[entry.ID]; ok {
			out = append(out, entry)
			continue
		}
		if id, parseErr := ParseNode(entry.ID); parseErr == nil && id != nil {
			if _, ok := matched[id.Path()]; ok {
				out = append(out, entry)
			}
		}
	}
	return out, nil
}

// Grep scans node content for a regular expression and returns per-node line
// matches in dex order. Nodes without matches are omitted.
func (k *LocalKeg) Grep(ctx context.Context, opts GrepOptions) ([]GrepMatch, error) {
	pattern := opts.Pattern
	if opts.IgnoreCase {
		pattern = "(?i)" + pattern
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid grep pattern %q: %w", opts.Pattern, err)
	}

	dex, err := k.Dex(ctx)
	if err != nil {
		return nil, fmt.Errorf("unable to read dex: %w", err)
	}

	matches := make([]GrepMatch, 0)
	for _, entry := range dex.Nodes(ctx) {
		id, parseErr := ParseNode(entry.ID)
		if parseErr != nil || id == nil {
			continue
		}
		raw, readErr := k.Repo.ReadContent(ctx, *id)
		if readErr != nil {
			if errors.Is(readErr, ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("unable to read node content: %w", readErr)
		}
		lines := grepContentLineMatches(re, raw)
		if opts.MaxLines > 0 && len(lines) > opts.MaxLines {
			lines = lines[:opts.MaxLines]
		}
		if len(lines) > 0 {
			matches = append(matches, GrepMatch{Entry: entry, Lines: lines})
		}
	}
	return matches, nil
}

// grepContentLineMatches returns "lineno:text" lines of raw content matching re.
func grepContentLineMatches(re *regexp.Regexp, raw []byte) []string {
	if len(raw) == 0 {
		return nil
	}
	content := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parts := strings.Split(content, "\n")
	lines := make([]string, 0)
	for i, part := range parts {
		line := strings.TrimRight(part, "\r")
		if re.MatchString(line) {
			lines = append(lines, fmt.Sprintf("%d:%s", i+1, line))
		}
	}
	return lines
}

// evalQueryExpr parses expr and evaluates it against the provided universe of
// node index entries. Returns the matched set of node path strings.
func (k *LocalKeg) evalQueryExpr(
	ctx context.Context,
	d *Dex,
	entries []NodeIndexEntry,
	expr string,
) (map[string]struct{}, error) {
	parsed, err := ParseQueryExpression(expr)
	if err != nil {
		return nil, err
	}

	universe := make(map[string]struct{}, len(entries)*2)
	for _, entry := range entries {
		universe[entry.ID] = struct{}{}
		id, parseErr := ParseNode(entry.ID)
		if parseErr == nil && id != nil {
			universe[id.Path()] = struct{}{}
		}
	}

	resolveTag := func(term string) map[string]struct{} {
		return k.resolveQueryTerm(ctx, d, entries, term)
	}

	resolveCompare := func(dotPrefix bool, field, op, value string) map[string]struct{} {
		if dotPrefix {
			return k.resolveStatsCompare(ctx, entries, field, op, value)
		}
		return k.resolveAttrCompare(ctx, entries, field, op, value)
	}

	return EvaluateQueryExpressionWithCompare(parsed, universe, resolveTag, resolveCompare), nil
}

// setFromNodeIDs converts a slice of NodeId to a set of path strings.
func setFromNodeIDs(ids []NodeId) map[string]struct{} {
	if len(ids) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id.Path()] = struct{}{}
	}
	return out
}

// resolveQueryTerm resolves a single term from a query expression against the
// provided universe of node index entries.
//
// If term contains "=" it is treated as a key=value attribute predicate: each
// node's meta.yaml is read and the term matches when meta.Get(key) == value.
// Otherwise the term is treated as a tag name and resolved via the dex index.
func (k *LocalKeg) resolveQueryTerm(
	ctx context.Context,
	d *Dex,
	entries []NodeIndexEntry,
	term string,
) map[string]struct{} {
	idx := strings.IndexByte(term, '=')
	if idx < 0 {
		// Plain tag — use dex index.
		nodes, ok := d.TagNodes(ctx, term)
		if !ok || len(nodes) == 0 {
			return map[string]struct{}{}
		}
		return setFromNodeIDs(nodes)
	}

	// Attribute predicate: key=value — scan each node's meta.yaml.
	key := term[:idx]
	val := term[idx+1:]
	out := make(map[string]struct{})
	for _, entry := range entries {
		id, err := ParseNode(entry.ID)
		if err != nil || id == nil {
			continue
		}
		raw, err := k.Repo.ReadMeta(ctx, *id)
		if err != nil {
			continue
		}
		meta, err := ParseMeta(ctx, raw)
		if err != nil {
			continue
		}
		got, ok := meta.Get(key)
		if ok && got == val {
			out[id.Path()] = struct{}{}
			out[entry.ID] = struct{}{}
		}
	}
	return out
}

// resolveStatsCompare resolves a dot-prefix stats field comparison against the
// provided node index entries.
//
// Index-backed fields (.updated, .created, .accessed) are resolved from the
// NodeIndexEntry timestamps without additional I/O.
//
// Non-indexed fields (.hash, .accessCount, .lead) require reading stats.json
// per node.
//
// When op is empty, the predicate acts as a boolean check: the field must be
// non-empty (strings) or non-zero (numbers/times).
func (k *LocalKeg) resolveStatsCompare(
	ctx context.Context,
	entries []NodeIndexEntry,
	field, op, value string,
) map[string]struct{} {
	out := make(map[string]struct{})

	switch field {
	case "updated", "created", "accessed":
		resolveTimeField(entries, field, op, value, out)
	case "hash", "lead":
		k.resolveStringStatsField(ctx, entries, field, op, value, out)
	case "accessCount":
		k.resolveNumericStatsField(ctx, entries, field, op, value, out)
	default:
		// Unknown field: match nothing.
	}

	return out
}

// resolveTimeField handles .updated, .created, .accessed comparisons using
// the in-memory NodeIndexEntry timestamps (no I/O).
func resolveTimeField(
	entries []NodeIndexEntry,
	field, op, value string,
	out map[string]struct{},
) {
	var compareTime time.Time
	if value != "" {
		compareTime = ParseStatsTime(value)
		if compareTime.IsZero() {
			// Unparseable date value: match nothing for comparison ops.
			if op != "" {
				return
			}
		}
	}

	for _, entry := range entries {
		var entryTime time.Time
		switch field {
		case "updated":
			entryTime = entry.Updated
		case "created":
			entryTime = entry.Created
		case "accessed":
			entryTime = entry.Accessed
		}

		if matchTime(entryTime, op, compareTime) {
			id, err := ParseNode(entry.ID)
			if err == nil && id != nil {
				out[id.Path()] = struct{}{}
			}
			out[entry.ID] = struct{}{}
		}
	}
}

// resolveStringStatsField handles .hash and .lead comparisons by reading
// stats.json per node.
func (k *LocalKeg) resolveStringStatsField(
	ctx context.Context,
	entries []NodeIndexEntry,
	field, op, value string,
	out map[string]struct{},
) {
	for _, entry := range entries {
		id, err := ParseNode(entry.ID)
		if err != nil || id == nil {
			continue
		}

		stats, err := k.Repo.ReadStats(ctx, *id)
		if err != nil || stats == nil {
			continue
		}

		var fieldValue string
		switch field {
		case "hash":
			fieldValue = stats.Hash()
		case "lead":
			fieldValue = stats.Lead()
		}

		if matchString(fieldValue, op, value) {
			out[id.Path()] = struct{}{}
			out[entry.ID] = struct{}{}
		}
	}
}

// resolveNumericStatsField handles .accessCount comparisons by reading
// stats.json per node.
func (k *LocalKeg) resolveNumericStatsField(
	ctx context.Context,
	entries []NodeIndexEntry,
	field, op, value string,
	out map[string]struct{},
) {
	var compareNum int
	if value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			// Non-numeric value for a numeric field: match nothing for
			// comparison ops.
			if op != "" {
				return
			}
		}
		compareNum = n
	}

	for _, entry := range entries {
		id, err := ParseNode(entry.ID)
		if err != nil || id == nil {
			continue
		}

		stats, err := k.Repo.ReadStats(ctx, *id)
		if err != nil || stats == nil {
			continue
		}

		var fieldNum int
		switch field {
		case "accessCount":
			fieldNum = stats.AccessCount()
		}

		if matchInt(fieldNum, op, compareNum) {
			out[id.Path()] = struct{}{}
			out[entry.ID] = struct{}{}
		}
	}
}

// matchTime compares entryTime against compareTime using op. For boolean
// checks (op == ""), returns true if entryTime is non-zero.
func matchTime(entryTime time.Time, op string, compareTime time.Time) bool {
	switch op {
	case "":
		return !entryTime.IsZero()
	case ">":
		return entryTime.After(compareTime)
	case ">=":
		return !entryTime.Before(compareTime)
	case "<":
		return entryTime.Before(compareTime)
	case "<=":
		return !entryTime.After(compareTime)
	case "=":
		return entryTime.Equal(compareTime)
	case "!=":
		return !entryTime.Equal(compareTime)
	}
	return false
}

// matchString compares fieldValue against value using op. For boolean
// checks (op == ""), returns true if fieldValue is non-empty.
func matchString(fieldValue, op, value string) bool {
	switch op {
	case "":
		return fieldValue != ""
	case "=":
		return fieldValue == value
	case "!=":
		return fieldValue != value
	case ">":
		return fieldValue > value
	case ">=":
		return fieldValue >= value
	case "<":
		return fieldValue < value
	case "<=":
		return fieldValue <= value
	}
	return false
}

// matchInt compares fieldNum against compareNum using op. For boolean
// checks (op == ""), returns true if fieldNum is non-zero.
func matchInt(fieldNum int, op string, compareNum int) bool {
	switch op {
	case "":
		return fieldNum != 0
	case ">":
		return fieldNum > compareNum
	case ">=":
		return fieldNum >= compareNum
	case "<":
		return fieldNum < compareNum
	case "<=":
		return fieldNum <= compareNum
	case "=":
		return fieldNum == compareNum
	case "!=":
		return fieldNum != compareNum
	}
	return false
}

// resolveAttrCompare resolves a meta.yaml attribute comparison against the
// provided node index entries (e.g., "entity!=plan", "omega>=0.5").
//
// Type detection uses try-parse: date first (most specific), then float,
// then string fallback. Both the predicate value and the stored meta value
// are parsed the same way to ensure consistent comparison.
func (k *LocalKeg) resolveAttrCompare(
	ctx context.Context,
	entries []NodeIndexEntry,
	field, op, value string,
) map[string]struct{} {
	out := make(map[string]struct{})

	for _, entry := range entries {
		id, err := ParseNode(entry.ID)
		if err != nil || id == nil {
			continue
		}
		raw, err := k.Repo.ReadMeta(ctx, *id)
		if err != nil {
			continue
		}
		meta, err := ParseMeta(ctx, raw)
		if err != nil {
			continue
		}
		got, ok := meta.Get(field)
		if !ok {
			// Field not present: only matches boolean false (op == "")
			// and != comparisons (missing != "anything" is true).
			if op == "!=" {
				out[id.Path()] = struct{}{}
				out[entry.ID] = struct{}{}
			}
			continue
		}

		if op == "" {
			// Boolean: field exists and is non-empty.
			if got != "" {
				out[id.Path()] = struct{}{}
				out[entry.ID] = struct{}{}
			}
			continue
		}

		if compareAttrValues(got, op, value) {
			out[id.Path()] = struct{}{}
			out[entry.ID] = struct{}{}
		}
	}

	return out
}

// compareAttrValues compares two attribute values using try-parse type
// detection: date first, then float, then string fallback.
func compareAttrValues(got, op, value string) bool {
	// Try date comparison first (most specific format).
	gotTime := ParseStatsTime(got)
	valTime := ParseStatsTime(value)
	if !gotTime.IsZero() && !valTime.IsZero() {
		return matchTime(gotTime, op, valTime)
	}

	// Try float comparison.
	gotFloat, gotErr := strconv.ParseFloat(got, 64)
	valFloat, valErr := strconv.ParseFloat(value, 64)
	if gotErr == nil && valErr == nil {
		return matchFloat(gotFloat, op, valFloat)
	}

	// Fall back to string comparison.
	return matchString(got, op, value)
}

// matchFloat compares two float64 values using op.
func matchFloat(fieldVal float64, op string, compareVal float64) bool {
	switch op {
	case "=":
		return fieldVal == compareVal
	case "!=":
		return fieldVal != compareVal
	case ">":
		return fieldVal > compareVal
	case ">=":
		return fieldVal >= compareVal
	case "<":
		return fieldVal < compareVal
	case "<=":
		return fieldVal <= compareVal
	}
	return false
}
