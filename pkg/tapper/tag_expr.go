package tapper

import (
	"context"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
)

// parseTagExpression compiles a boolean tag expression string.
// This is a thin wrapper around keg.ParseTagExpression.
func parseTagExpression(raw string) (keg.TagExpr, error) {
	return keg.ParseTagExpression(raw)
}

// evaluateTagExpression evaluates a compiled tag expression against a universe
// of identifiers. This is a thin wrapper around keg.EvaluateTagExpression.
func evaluateTagExpression(
	expr keg.TagExpr,
	universe map[string]struct{},
	resolve func(tag string) map[string]struct{},
) map[string]struct{} {
	return keg.EvaluateTagExpression(expr, universe, resolve)
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

// evalQueryExpr parses expr as a boolean expression that supports both plain
// tag names and key=value attribute predicates, then evaluates it against the
// provided universe of node index entries.
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
	parsed, err := parseTagExpression(expr)
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

	matched := evaluateTagExpression(parsed, universe, func(term string) map[string]struct{} {
		return resolveQueryTerm(ctx, k, d, entries, term)
	})
	return matched, nil
}
