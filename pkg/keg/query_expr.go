package keg

import (
	"fmt"
	"strings"
	"unicode"
)

// StatsFieldNames lists the dot-prefix stats field names recognized by the
// query expression parser. These correspond to fields in stats.json and
// NodeIndexEntry.
var StatsFieldNames = []string{
	"updated",
	"created",
	"accessed",
	"hash",
	"accessCount",
	"lead",
	"omega",
}

// QueryExpr is an opaque compiled tag boolean expression. Callers obtain one via
// ParseQueryExpression and pass it to EvaluateQueryExpression. The underlying AST
// is unexported; external packages cannot inspect or implement it.
type QueryExpr struct {
	root queryExprNode
}

// ParseQueryExpression compiles raw into a QueryExpr that can be evaluated with
// EvaluateQueryExpression. Returns an error if raw is empty or syntactically
// invalid.
func ParseQueryExpression(raw string) (QueryExpr, error) {
	node, err := parseQueryExpression(raw)
	if err != nil {
		return QueryExpr{}, err
	}
	return QueryExpr{root: node}, nil
}

// CompareResolver is an optional callback for evaluating comparison
// predicates (e.g., ".created>2026-01-01" or "entity!=plan"). When set,
// the evaluator calls it with dotPrefix, field, op, and value and expects
// the set of matching identifiers. dotPrefix is true for dot-prefix stats
// fields (e.g., ".created>2026-01-01") and false for plain attribute
// comparisons (e.g., "entity!=plan"). When nil, comparisons match nothing.
type CompareResolver func(dotPrefix bool, field, op, value string) map[string]struct{}

// EvaluateQueryExpression evaluates expr against a universe of string identifiers.
// universe is the full candidate set (e.g. node paths). resolve maps a tag
// name to the subset of universe that carries that tag. Returns the subset of
// universe that satisfies the expression.
func EvaluateQueryExpression(
	expr QueryExpr,
	universe map[string]struct{},
	resolve func(tag string) map[string]struct{},
) map[string]struct{} {
	return EvaluateQueryExpressionWithCompare(expr, universe, resolve, nil)
}

// EvaluateQueryExpressionWithCompare evaluates expr with full support for
// dot-prefix stats comparisons. resolveCompare handles ".field op value"
// predicates. When resolveCompare is nil, dot-prefix comparisons match nothing.
func EvaluateQueryExpressionWithCompare(
	expr QueryExpr,
	universe map[string]struct{},
	resolve func(tag string) map[string]struct{},
	resolveCompare CompareResolver,
) map[string]struct{} {
	if expr.root == nil {
		return map[string]struct{}{}
	}
	ctx := &queryEvalCtx{
		resolve:        resolve,
		resolveCompare: resolveCompare,
		universe:       copySet(universe),
	}
	return expr.root.run(ctx)
}

// --------------------------------------------------------------------------
// Internal AST and parser (unexported)
// --------------------------------------------------------------------------

type queryExprNode interface {
	run(ctx *queryEvalCtx) map[string]struct{}
}

type queryEvalCtx struct {
	resolve        func(tag string) map[string]struct{}
	resolveCompare CompareResolver
	universe       map[string]struct{}
}

type queryLiteralNode struct {
	tag string
}

func (n *queryLiteralNode) run(ctx *queryEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || ctx.resolve == nil {
		return map[string]struct{}{}
	}
	return copySet(ctx.resolve(n.tag))
}

type queryNotNode struct {
	node queryExprNode
}

func (n *queryNotNode) run(ctx *queryEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || n.node == nil {
		return map[string]struct{}{}
	}
	return complementSet(ctx.universe, n.node.run(ctx))
}

type queryAndNode struct {
	left  queryExprNode
	right queryExprNode
}

func (n *queryAndNode) run(ctx *queryEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || n.left == nil || n.right == nil {
		return map[string]struct{}{}
	}
	return intersectSets(n.left.run(ctx), n.right.run(ctx))
}

type queryOrNode struct {
	left  queryExprNode
	right queryExprNode
}

func (n *queryOrNode) run(ctx *queryEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil {
		return map[string]struct{}{}
	}
	if n.left == nil && n.right == nil {
		return map[string]struct{}{}
	}
	if n.left == nil {
		return n.right.run(ctx)
	}
	if n.right == nil {
		return n.left.run(ctx)
	}
	return unionSets(n.left.run(ctx), n.right.run(ctx))
}

// queryCompareNode represents a comparison predicate. When dotPrefix is true
// the field refers to a stats.json / index field (e.g., ".created>2026-01-01").
// When dotPrefix is false the field refers to a meta.yaml attribute (e.g.,
// "entity!=plan", "omega>=0.5"). When op and value are empty it acts as a
// boolean existence check (field is non-empty / non-zero).
type queryCompareNode struct {
	dotPrefix bool   // true for dot-prefix stats fields, false for meta attributes
	field     string // e.g., "created", "hash", "entity", "omega"
	op        string // e.g., ">", "<", ">=", "<=", "=", "!=" — empty for boolean
	value     string // comparison value; empty for boolean check
}

func (n *queryCompareNode) run(ctx *queryEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil {
		return map[string]struct{}{}
	}
	if ctx.resolveCompare == nil {
		return map[string]struct{}{}
	}
	return ctx.resolveCompare(n.dotPrefix, n.field, n.op, n.value)
}

type queryTokenType int

const (
	queryTokenEOF queryTokenType = iota
	queryTokenIdent
	queryTokenAnd
	queryTokenOr
	queryTokenNot
	queryTokenLParen
	queryTokenRParen
	queryTokenDotIdent // ".fieldname" or ".fieldname>=value"
)

type queryToken struct {
	typ   queryTokenType
	value string
	pos   int
}

type queryExprParser struct {
	tokens []queryToken
	index  int
}

func parseQueryExpression(raw string) (queryExprNode, error) {
	tokens, err := tokenizeQueryExpression(raw)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("expression is empty")
	}

	p := &queryExprParser{
		tokens: tokens,
	}

	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	tok := p.peek()
	if tok.typ != queryTokenEOF {
		return nil, fmt.Errorf("unexpected token %q at position %d", tok.value, tok.pos+1)
	}

	return root, nil
}

func tokenizeQueryExpression(raw string) ([]queryToken, error) {
	in := strings.TrimSpace(raw)
	tokens := make([]queryToken, 0)
	if in == "" {
		return tokens, nil
	}

	pos := 0
	for pos < len(in) {
		r := rune(in[pos])
		if unicode.IsSpace(r) {
			pos++
			continue
		}

		switch in[pos] {
		case '(':
			tokens = append(tokens, queryToken{typ: queryTokenLParen, value: "(", pos: pos})
			pos++
			continue
		case ')':
			tokens = append(tokens, queryToken{typ: queryTokenRParen, value: ")", pos: pos})
			pos++
			continue
		case '!':
			tokens = append(tokens, queryToken{typ: queryTokenNot, value: "!", pos: pos})
			pos++
			continue
		case '&':
			if pos+1 < len(in) && in[pos+1] == '&' {
				tokens = append(tokens, queryToken{typ: queryTokenAnd, value: "&&", pos: pos})
				pos += 2
				continue
			}
			return nil, fmt.Errorf("unexpected token %q at position %d", string(in[pos]), pos+1)
		case '|':
			if pos+1 < len(in) && in[pos+1] == '|' {
				tokens = append(tokens, queryToken{typ: queryTokenOr, value: "||", pos: pos})
				pos += 2
				continue
			}
			return nil, fmt.Errorf("unexpected token %q at position %d", string(in[pos]), pos+1)
		case '.':
			// Dot-prefix stats field: ".fieldname" optionally followed by
			// a comparison operator and value, e.g. ".created>2026-01-01".
			// The entire ".field>=value" or ".lead!=deprecated" is consumed
			// as a single token.
			if pos+1 < len(in) && unicode.IsLetter(rune(in[pos+1])) {
				start := pos
				pos++ // skip the dot
				for pos < len(in) {
					c := rune(in[pos])
					if unicode.IsSpace(c) || c == '(' || c == ')' || c == '&' || c == '|' || c == '\'' || c == '"' {
						break
					}
					// '!' breaks the scan only if it is NOT part of "!="
					if c == '!' {
						if pos+1 < len(in) && in[pos+1] == '=' {
							// "!=" is a comparison operator inside the dot-ident;
							// continue scanning to include the value.
							pos += 2
							continue
						}
						break
					}
					pos++
				}
				tokens = append(tokens, queryToken{typ: queryTokenDotIdent, value: in[start:pos], pos: start})
				continue
			}
			// Lone dot — fall through to default word handling.
			fallthrough
		case '\'', '"':
			if in[pos] == '\'' || in[pos] == '"' {
				quote := in[pos]
				start := pos
				pos++
				var b strings.Builder
				for pos < len(in) {
					ch := in[pos]
					if ch == '\\' && pos+1 < len(in) {
						b.WriteByte(in[pos+1])
						pos += 2
						continue
					}
					if ch == quote {
						pos++
						tokens = append(tokens, queryToken{typ: queryTokenIdent, value: b.String(), pos: start})
						goto nextToken
					}
					b.WriteByte(ch)
					pos++
				}
				return nil, fmt.Errorf("unterminated quoted tag at position %d", start+1)
			}
			fallthrough
		default:
			start := pos
			for pos < len(in) {
				c := rune(in[pos])
				if unicode.IsSpace(c) {
					break
				}
				switch in[pos] {
				case '(', ')', '&', '|', '\'', '"':
					goto emitWord
				case '!':
					// '!' breaks the scan only if it is NOT part of "!="
					if pos+1 < len(in) && in[pos+1] == '=' {
						// "!=" is a comparison operator inside the ident;
						// continue scanning to include the value.
						pos += 2
						continue
					}
					goto emitWord
				}
				pos++
			}
		emitWord:
			word := strings.TrimSpace(in[start:pos])
			if word == "" {
				return nil, fmt.Errorf("unexpected token %q at position %d", string(in[start]), start+1)
			}
			lower := strings.ToLower(word)
			switch lower {
			case "and":
				tokens = append(tokens, queryToken{typ: queryTokenAnd, value: word, pos: start})
			case "or":
				tokens = append(tokens, queryToken{typ: queryTokenOr, value: word, pos: start})
			case "not":
				tokens = append(tokens, queryToken{typ: queryTokenNot, value: word, pos: start})
			default:
				tokens = append(tokens, queryToken{typ: queryTokenIdent, value: word, pos: start})
			}
		}
	nextToken:
	}

	tokens = append(tokens, queryToken{typ: queryTokenEOF, value: "", pos: len(in)})
	return tokens, nil
}

func (p *queryExprParser) peek() queryToken {
	if p.index >= len(p.tokens) {
		if len(p.tokens) == 0 {
			return queryToken{typ: queryTokenEOF, pos: 0}
		}
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.index]
}

func (p *queryExprParser) next() queryToken {
	tok := p.peek()
	if p.index < len(p.tokens) {
		p.index++
	}
	return tok
}

func (p *queryExprParser) parseOr() (queryExprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.typ != queryTokenOr {
			break
		}
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &queryOrNode{left: left, right: right}
	}
	return left, nil
}

func (p *queryExprParser) parseAnd() (queryExprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.typ != queryTokenAnd {
			break
		}
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &queryAndNode{left: left, right: right}
	}
	return left, nil
}

func (p *queryExprParser) parseUnary() (queryExprNode, error) {
	tok := p.peek()
	if tok.typ == queryTokenNot {
		p.next()
		node, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &queryNotNode{node: node}, nil
	}
	return p.parsePrimary()
}

func (p *queryExprParser) parsePrimary() (queryExprNode, error) {
	tok := p.peek()
	switch tok.typ {
	case queryTokenIdent:
		p.next()
		// Check if the ident contains a non-bare-= comparison operator,
		// indicating an attribute comparison (e.g., "entity!=plan",
		// "omega>=0.5", "omega>0.3"). Bare "key=value" is parsed through the
		// literal path so it resolves as a tag/attribute term.
		if node, ok := tryParseAttrCompare(tok.value); ok {
			return node, nil
		}
		return &queryLiteralNode{tag: tok.value}, nil
	case queryTokenDotIdent:
		p.next()
		return parseDotIdent(tok.value)
	case queryTokenLParen:
		p.next()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		closing := p.next()
		if closing.typ != queryTokenRParen {
			if closing.typ == queryTokenEOF {
				return nil, fmt.Errorf("expected ')' before end of expression")
			}
			return nil, fmt.Errorf("expected ')' before position %d", closing.pos+1)
		}
		return expr, nil
	case queryTokenEOF:
		return nil, fmt.Errorf("unexpected end of expression")
	default:
		return nil, fmt.Errorf("unexpected token %q at position %d", tok.value, tok.pos+1)
	}
}

// tryParseAttrCompare checks whether raw contains a non-bare-= comparison
// operator (!=, >=, <=, >, <) and, if so, parses it into a queryCompareNode
// with dotPrefix=false. Returns (node, true) on success. If raw contains
// only a bare = or no operator at all, returns (nil, false) so the caller
// can fall back to the literal path.
func tryParseAttrCompare(raw string) (*queryCompareNode, bool) {
	// Order matters: check two-char operators before single-char.
	for _, op := range []string{"!=", ">=", "<="} {
		if idx := strings.Index(raw, op); idx > 0 {
			field := raw[:idx]
			value := raw[idx+len(op):]
			if field != "" && value != "" {
				return &queryCompareNode{dotPrefix: false, field: field, op: op, value: value}, true
			}
		}
	}
	for _, op := range []string{">", "<"} {
		if idx := strings.Index(raw, op); idx > 0 {
			field := raw[:idx]
			value := raw[idx+len(op):]
			if field != "" && value != "" {
				return &queryCompareNode{dotPrefix: false, field: field, op: op, value: value}, true
			}
		}
	}
	return nil, false
}

// parseDotIdent parses a dot-ident token value like ".created>2026-01-01",
// ".accessCount>=5", ".hash=abc123", or ".created" (boolean check) into a
// queryCompareNode.
func parseDotIdent(raw string) (*queryCompareNode, error) {
	// Strip leading dot.
	s := raw[1:]

	// Find the operator boundary: first char that is one of > < = !
	opIdx := -1
	for i, c := range s {
		if c == '>' || c == '<' || c == '=' || c == '!' {
			opIdx = i
			break
		}
	}

	if opIdx < 0 {
		// No operator: boolean existence check.
		return &queryCompareNode{dotPrefix: true, field: s, op: "", value: ""}, nil
	}

	field := s[:opIdx]
	rest := s[opIdx:]

	// Extract operator (1 or 2 chars).
	var op string
	switch {
	case strings.HasPrefix(rest, ">="):
		op = ">="
	case strings.HasPrefix(rest, "<="):
		op = "<="
	case strings.HasPrefix(rest, "!="):
		op = "!="
	case strings.HasPrefix(rest, ">"):
		op = ">"
	case strings.HasPrefix(rest, "<"):
		op = "<"
	case strings.HasPrefix(rest, "="):
		op = "="
	default:
		return nil, fmt.Errorf("invalid comparison operator in %q", raw)
	}

	value := rest[len(op):]
	if value == "" {
		return nil, fmt.Errorf("missing value after operator in %q", raw)
	}

	return &queryCompareNode{dotPrefix: true, field: field, op: op, value: value}, nil
}

// --------------------------------------------------------------------------
// Set utilities (unexported)
// --------------------------------------------------------------------------

func copySet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(in))
	for key := range in {
		out[key] = struct{}{}
	}
	return out
}

func unionSets(a, b map[string]struct{}) map[string]struct{} {
	if len(a) == 0 && len(b) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(a)+len(b))
	for key := range a {
		out[key] = struct{}{}
	}
	for key := range b {
		out[key] = struct{}{}
	}
	return out
}

func intersectSets(a, b map[string]struct{}) map[string]struct{} {
	if len(a) == 0 || len(b) == 0 {
		return map[string]struct{}{}
	}
	if len(a) > len(b) {
		a, b = b, a
	}
	out := make(map[string]struct{}, len(a))
	for key := range a {
		if _, ok := b[key]; ok {
			out[key] = struct{}{}
		}
	}
	return out
}

func complementSet(universe, selected map[string]struct{}) map[string]struct{} {
	if len(universe) == 0 {
		return map[string]struct{}{}
	}
	out := make(map[string]struct{}, len(universe))
	for key := range universe {
		if _, ok := selected[key]; !ok {
			out[key] = struct{}{}
		}
	}
	return out
}
