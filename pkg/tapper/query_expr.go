package tapper

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
)

// parseQueryExpression compiles a boolean query expression string.
// This is a thin wrapper around keg.ParseQueryExpression.
func parseQueryExpression(raw string) (keg.QueryExpr, error) {
	return keg.ParseQueryExpression(raw)
}

// setFromNodeIDs converts a slice of NodeId to a set of path strings.
func setFromNodeIDs(ids []keg.NodeId) map[string]struct{} {
	if len(ids) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id.Path()] = struct{}{}
	}
	return out
}

// resolveQueryTerm resolves a single term from a --query expression against the
// provided universe of node index entries.
//
// If term contains "=" it is treated as a key=value attribute predicate: each
// node's meta.yaml is read and the term matches when meta.Get(key) == value.
// Otherwise the term is treated as a tag name and resolved via the dex index.
func resolveQueryTerm(
	ctx context.Context,
	k *keg.Keg,
	d *keg.Dex,
	entries []keg.NodeIndexEntry,
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
		id, err := keg.ParseNode(entry.ID)
		if err != nil || id == nil {
			continue
		}
		raw, err := k.Repo.ReadMeta(ctx, *id)
		if err != nil {
			continue
		}
		meta, err := keg.ParseMeta(ctx, raw)
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
// per node via k.Repo.ReadStats.
//
// When op is empty, the predicate acts as a boolean check: the field must be
// non-empty (strings) or non-zero (numbers/times).
func resolveStatsCompare(
	ctx context.Context,
	k *keg.Keg,
	entries []keg.NodeIndexEntry,
	field, op, value string,
) map[string]struct{} {
	out := make(map[string]struct{})

	switch field {
	case "updated", "created", "accessed":
		resolveTimeField(entries, field, op, value, out)
	case "hash", "lead":
		resolveStringStatsField(ctx, k, entries, field, op, value, out)
	case "accessCount":
		resolveNumericStatsField(ctx, k, entries, field, op, value, out)
	default:
		// Unknown field: match nothing.
	}

	return out
}

// resolveTimeField handles .updated, .created, .accessed comparisons using
// the in-memory NodeIndexEntry timestamps (no I/O).
func resolveTimeField(
	entries []keg.NodeIndexEntry,
	field, op, value string,
	out map[string]struct{},
) {
	var compareTime time.Time
	if value != "" {
		compareTime = keg.ParseStatsTime(value)
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
			id, err := keg.ParseNode(entry.ID)
			if err == nil && id != nil {
				out[id.Path()] = struct{}{}
			}
			out[entry.ID] = struct{}{}
		}
	}
}

// resolveStringStatsField handles .hash and .lead comparisons by reading
// stats.json per node.
func resolveStringStatsField(
	ctx context.Context,
	k *keg.Keg,
	entries []keg.NodeIndexEntry,
	field, op, value string,
	out map[string]struct{},
) {
	for _, entry := range entries {
		id, err := keg.ParseNode(entry.ID)
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
func resolveNumericStatsField(
	ctx context.Context,
	k *keg.Keg,
	entries []keg.NodeIndexEntry,
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
		id, err := keg.ParseNode(entry.ID)
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
func resolveAttrCompare(
	ctx context.Context,
	k *keg.Keg,
	entries []keg.NodeIndexEntry,
	field, op, value string,
) map[string]struct{} {
	out := make(map[string]struct{})

	for _, entry := range entries {
		id, err := keg.ParseNode(entry.ID)
		if err != nil || id == nil {
			continue
		}
		raw, err := k.Repo.ReadMeta(ctx, *id)
		if err != nil {
			continue
		}
		meta, err := keg.ParseMeta(ctx, raw)
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
	gotTime := keg.ParseStatsTime(got)
	valTime := keg.ParseStatsTime(value)
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

// evalQueryExpr parses expr as a boolean expression that supports plain
// tag names, key=value attribute predicates, and .field{op}value stats
// predicates, then evaluates it against the provided universe of node index
// entries.
//
// Returns the matched set of node path strings, or an error if the expression
// cannot be parsed.
func evalQueryExpr(
	ctx context.Context,
	k *keg.Keg,
	d *keg.Dex,
	entries []keg.NodeIndexEntry,
	expr string,
) (map[string]struct{}, error) {
	parsed, err := parseQueryExpression(expr)
	if err != nil {
		return nil, err
	}

	universe := make(map[string]struct{}, len(entries)*2)
	for _, entry := range entries {
		universe[entry.ID] = struct{}{}
		id, parseErr := keg.ParseNode(entry.ID)
		if parseErr == nil && id != nil {
			universe[id.Path()] = struct{}{}
		}
	}

	resolveTag := func(term string) map[string]struct{} {
		return resolveQueryTerm(ctx, k, d, entries, term)
	}

	resolveCompare := func(dotPrefix bool, field, op, value string) map[string]struct{} {
		if dotPrefix {
			return resolveStatsCompare(ctx, k, entries, field, op, value)
		}
		return resolveAttrCompare(ctx, k, entries, field, op, value)
	}

	matched := keg.EvaluateQueryExpressionWithCompare(parsed, universe, resolveTag, resolveCompare)
	return matched, nil
}
