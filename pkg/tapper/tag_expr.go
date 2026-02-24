package tapper

import "github.com/jlrickert/tapper/pkg/keg"

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
