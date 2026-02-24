package keg

import (
	"fmt"
	"strings"
	"unicode"
)

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

// EvaluateTagExpression evaluates expr against a universe of string identifiers.
// universe is the full candidate set (e.g. node paths). resolve maps a tag
// name to the subset of universe that carries that tag. Returns the subset of
// universe that satisfies the expression.
func EvaluateTagExpression(
	expr TagExpr,
	universe map[string]struct{},
	resolve func(tag string) map[string]struct{},
) map[string]struct{} {
	return evaluateTagExpression(expr.root, universe, resolve)
}

// --------------------------------------------------------------------------
// Internal AST and parser (unexported)
// --------------------------------------------------------------------------

type tagExprNode interface {
	eval(ctx *tagEvalContext) map[string]struct{}
}

type tagEvalContext struct {
	resolve  func(tag string) map[string]struct{}
	universe map[string]struct{}
}

type tagLiteralNode struct {
	tag string
}

func (n *tagLiteralNode) eval(ctx *tagEvalContext) map[string]struct{} {
	if n == nil || ctx == nil || ctx.resolve == nil {
		return map[string]struct{}{}
	}
	return copySet(ctx.resolve(n.tag))
}

type tagNotNode struct {
	node tagExprNode
}

func (n *tagNotNode) eval(ctx *tagEvalContext) map[string]struct{} {
	if n == nil || ctx == nil || n.node == nil {
		return map[string]struct{}{}
	}
	return complementSet(ctx.universe, n.node.eval(ctx))
}

type tagAndNode struct {
	left  tagExprNode
	right tagExprNode
}

func (n *tagAndNode) eval(ctx *tagEvalContext) map[string]struct{} {
	if n == nil || ctx == nil || n.left == nil || n.right == nil {
		return map[string]struct{}{}
	}
	return intersectSets(n.left.eval(ctx), n.right.eval(ctx))
}

type tagOrNode struct {
	left  tagExprNode
	right tagExprNode
}

func (n *tagOrNode) eval(ctx *tagEvalContext) map[string]struct{} {
	if n == nil || ctx == nil || n.left == nil || n.right == nil {
		return map[string]struct{}{}
	}
	return unionSets(n.left.eval(ctx), n.right.eval(ctx))
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

func evaluateTagExpression(root tagExprNode, universe map[string]struct{}, resolve func(tag string) map[string]struct{}) map[string]struct{} {
	if root == nil {
		return map[string]struct{}{}
	}
	ctx := &tagEvalContext{
		resolve:  resolve,
		universe: copySet(universe),
	}
	return root.eval(ctx)
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
		case '\'', '"':
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
