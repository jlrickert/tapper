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
}

// TagExpr is an opaque compiled tag boolean expression. Callers obtain one via
// ParseTagExpression and pass it to EvaluateTagExpression. The underlying AST
// is unexported; external packages cannot inspect or implement it.
type TagExpr struct {
	root tagExprNode
}

// ParseTagExpression compiles raw into a TagExpr that can be evaluated with
// EvaluateTagExpression. Returns an error if raw is empty or syntactically
// invalid.
func ParseTagExpression(raw string) (TagExpr, error) {
	node, err := parseTagExpression(raw)
	if err != nil {
		return TagExpr{}, err
	}
	return TagExpr{root: node}, nil
}

// CompareResolver is an optional callback for evaluating dot-prefix stats
// comparisons (e.g., ".created>2026-01-01"). When set, the evaluator calls
// it with field, op, and value and expects the set of matching identifiers.
// When nil, dot-prefix comparisons match nothing.
type CompareResolver func(field, op, value string) map[string]struct{}

// EvaluateTagExpression evaluates expr against a universe of string identifiers.
// universe is the full candidate set (e.g. node paths). resolve maps a tag
// name to the subset of universe that carries that tag. Returns the subset of
// universe that satisfies the expression.
func EvaluateTagExpression(
	expr TagExpr,
	universe map[string]struct{},
	resolve func(tag string) map[string]struct{},
) map[string]struct{} {
	return EvaluateTagExpressionWithCompare(expr, universe, resolve, nil)
}

// EvaluateTagExpressionWithCompare evaluates expr with full support for
// dot-prefix stats comparisons. resolveCompare handles ".field op value"
// predicates. When resolveCompare is nil, dot-prefix comparisons match nothing.
func EvaluateTagExpressionWithCompare(
	expr TagExpr,
	universe map[string]struct{},
	resolve func(tag string) map[string]struct{},
	resolveCompare CompareResolver,
) map[string]struct{} {
	if expr.root == nil {
		return map[string]struct{}{}
	}
	ctx := &tagEvalCtx{
		resolve:        resolve,
		resolveCompare: resolveCompare,
		universe:       copySet(universe),
	}
	return expr.root.run(ctx)
}

// --------------------------------------------------------------------------
// Internal AST and parser (unexported)
// --------------------------------------------------------------------------

type tagExprNode interface {
	run(ctx *tagEvalCtx) map[string]struct{}
}

type tagEvalCtx struct {
	resolve        func(tag string) map[string]struct{}
	resolveCompare CompareResolver
	universe       map[string]struct{}
}

type tagLiteralNode struct {
	tag string
}

func (n *tagLiteralNode) run(ctx *tagEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || ctx.resolve == nil {
		return map[string]struct{}{}
	}
	return copySet(ctx.resolve(n.tag))
}

type tagNotNode struct {
	node tagExprNode
}

func (n *tagNotNode) run(ctx *tagEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || n.node == nil {
		return map[string]struct{}{}
	}
	return complementSet(ctx.universe, n.node.run(ctx))
}

type tagAndNode struct {
	left  tagExprNode
	right tagExprNode
}

func (n *tagAndNode) run(ctx *tagEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || n.left == nil || n.right == nil {
		return map[string]struct{}{}
	}
	return intersectSets(n.left.run(ctx), n.right.run(ctx))
}

type tagOrNode struct {
	left  tagExprNode
	right tagExprNode
}

func (n *tagOrNode) run(ctx *tagEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil || n.left == nil || n.right == nil {
		return map[string]struct{}{}
	}
	return unionSets(n.left.run(ctx), n.right.run(ctx))
}

// tagCompareNode represents a dot-prefix stats field predicate such as
// ".created>2026-01-01" or ".hash=abc123". When op and value are empty,
// it acts as a boolean existence check (field is non-empty / non-zero).
type tagCompareNode struct {
	field string // e.g., "created", "hash", "accessCount"
	op    string // e.g., ">", "<", ">=", "<=", "=", "!=" — empty for boolean
	value string // comparison value; empty for boolean check
}

func (n *tagCompareNode) run(ctx *tagEvalCtx) map[string]struct{} {
	if n == nil || ctx == nil {
		return map[string]struct{}{}
	}
	if ctx.resolveCompare == nil {
		return map[string]struct{}{}
	}
	return ctx.resolveCompare(n.field, n.op, n.value)
}

type tagTokenType int

const (
	tagTokenEOF tagTokenType = iota
	tagTokenIdent
	tagTokenAnd
	tagTokenOr
	tagTokenNot
	tagTokenLParen
	tagTokenRParen
	tagTokenDotIdent // ".fieldname" or ".fieldname>=value"
)

type tagToken struct {
	typ   tagTokenType
	value string
	pos   int
}

type tagExprParser struct {
	tokens []tagToken
	index  int
}

func parseTagExpression(raw string) (tagExprNode, error) {
	tokens, err := tokenizeTagExpression(raw)
	if err != nil {
		return nil, err
	}
	if len(tokens) == 0 {
		return nil, fmt.Errorf("expression is empty")
	}

	p := &tagExprParser{
		tokens: tokens,
	}

	root, err := p.parseOr()
	if err != nil {
		return nil, err
	}

	tok := p.peek()
	if tok.typ != tagTokenEOF {
		return nil, fmt.Errorf("unexpected token %q at position %d", tok.value, tok.pos+1)
	}

	return root, nil
}

func tokenizeTagExpression(raw string) ([]tagToken, error) {
	in := strings.TrimSpace(raw)
	tokens := make([]tagToken, 0)
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
			tokens = append(tokens, tagToken{typ: tagTokenLParen, value: "(", pos: pos})
			pos++
			continue
		case ')':
			tokens = append(tokens, tagToken{typ: tagTokenRParen, value: ")", pos: pos})
			pos++
			continue
		case '!':
			tokens = append(tokens, tagToken{typ: tagTokenNot, value: "!", pos: pos})
			pos++
			continue
		case '&':
			if pos+1 < len(in) && in[pos+1] == '&' {
				tokens = append(tokens, tagToken{typ: tagTokenAnd, value: "&&", pos: pos})
				pos += 2
				continue
			}
			return nil, fmt.Errorf("unexpected token %q at position %d", string(in[pos]), pos+1)
		case '|':
			if pos+1 < len(in) && in[pos+1] == '|' {
				tokens = append(tokens, tagToken{typ: tagTokenOr, value: "||", pos: pos})
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
				tokens = append(tokens, tagToken{typ: tagTokenDotIdent, value: in[start:pos], pos: start})
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
						tokens = append(tokens, tagToken{typ: tagTokenIdent, value: b.String(), pos: start})
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
				case '(', ')', '!', '&', '|', '\'', '"':
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
				tokens = append(tokens, tagToken{typ: tagTokenAnd, value: word, pos: start})
			case "or":
				tokens = append(tokens, tagToken{typ: tagTokenOr, value: word, pos: start})
			case "not":
				tokens = append(tokens, tagToken{typ: tagTokenNot, value: word, pos: start})
			default:
				tokens = append(tokens, tagToken{typ: tagTokenIdent, value: word, pos: start})
			}
		}
	nextToken:
	}

	tokens = append(tokens, tagToken{typ: tagTokenEOF, value: "", pos: len(in)})
	return tokens, nil
}

func (p *tagExprParser) peek() tagToken {
	if p.index >= len(p.tokens) {
		if len(p.tokens) == 0 {
			return tagToken{typ: tagTokenEOF, pos: 0}
		}
		return p.tokens[len(p.tokens)-1]
	}
	return p.tokens[p.index]
}

func (p *tagExprParser) next() tagToken {
	tok := p.peek()
	if p.index < len(p.tokens) {
		p.index++
	}
	return tok
}

func (p *tagExprParser) parseOr() (tagExprNode, error) {
	left, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.typ != tagTokenOr {
			break
		}
		p.next()
		right, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		left = &tagOrNode{left: left, right: right}
	}
	return left, nil
}

func (p *tagExprParser) parseAnd() (tagExprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for {
		tok := p.peek()
		if tok.typ != tagTokenAnd {
			break
		}
		p.next()
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = &tagAndNode{left: left, right: right}
	}
	return left, nil
}

func (p *tagExprParser) parseUnary() (tagExprNode, error) {
	tok := p.peek()
	if tok.typ == tagTokenNot {
		p.next()
		node, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &tagNotNode{node: node}, nil
	}
	return p.parsePrimary()
}

func (p *tagExprParser) parsePrimary() (tagExprNode, error) {
	tok := p.peek()
	switch tok.typ {
	case tagTokenIdent:
		p.next()
		return &tagLiteralNode{tag: tok.value}, nil
	case tagTokenDotIdent:
		p.next()
		return parseDotIdent(tok.value)
	case tagTokenLParen:
		p.next()
		expr, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		closing := p.next()
		if closing.typ != tagTokenRParen {
			if closing.typ == tagTokenEOF {
				return nil, fmt.Errorf("expected ')' before end of expression")
			}
			return nil, fmt.Errorf("expected ')' before position %d", closing.pos+1)
		}
		return expr, nil
	case tagTokenEOF:
		return nil, fmt.Errorf("unexpected end of expression")
	default:
		return nil, fmt.Errorf("unexpected token %q at position %d", tok.value, tok.pos+1)
	}
}

// parseDotIdent parses a dot-ident token value like ".created>2026-01-01",
// ".accessCount>=5", ".hash=abc123", or ".created" (boolean check) into a
// tagCompareNode.
func parseDotIdent(raw string) (*tagCompareNode, error) {
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
		return &tagCompareNode{field: s, op: "", value: ""}, nil
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

	return &tagCompareNode{field: field, op: op, value: value}, nil
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
