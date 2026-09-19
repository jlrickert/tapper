package omegadsl

import (
	"fmt"
	"sort"
)

// reserved words may not be used as let-binding names.
var reserved = map[string]bool{
	"let": true, "omega": true, "match": true, "else": true, "weighted": true,
	"and": true, "or": true, "not": true, "true": true, "false": true,
	"meta": true, "stat": true, "out": true, "in": true, "bi": true, "self": true,
}

// knownFuncs is the built-in function set. setFuncs is the subset whose first
// argument must be a link set (out./in./bi.).
var knownFuncs = map[string]bool{
	"score": true, "clamp": true, "min": true, "max": true,
	"present": true, "missing": true,
	"count": true, "has": true, "any": true, "all": true,
	"frac": true, "avg": true, "sum": true,
}

var setFuncs = map[string]bool{
	"count": true, "has": true, "any": true, "all": true,
	"frac": true, "avg": true, "sum": true,
}

type parser struct {
	toks []token
	pos  int
	lets map[string]bool
	rels map[string]bool
}

func parse(src string) (*Program, error) {
	toks, err := lex(src)
	if err != nil {
		return nil, err
	}
	p := &parser{toks: toks, lets: map[string]bool{}, rels: map[string]bool{}}
	return p.parseProgram()
}

// --- token cursor helpers -------------------------------------------------

func (p *parser) peek() token { return p.toks[p.pos] }
func (p *parser) peekAt(n int) token {
	if p.pos+n >= len(p.toks) {
		return p.toks[len(p.toks)-1]
	}
	return p.toks[p.pos+n]
}
func (p *parser) next() token       { t := p.toks[p.pos]; p.pos++; return t }
func (p *parser) at(k tokKind) bool { return p.peek().kind == k }

func (p *parser) isKeyword(word string) bool {
	t := p.peek()
	return t.kind == tIdent && t.text == word
}

func (p *parser) expect(k tokKind, what string) (token, error) {
	t := p.peek()
	if t.kind != k {
		return t, fmt.Errorf("line %d: expected %s, found %s", t.line, what, t)
	}
	return p.next(), nil
}

// --- program & statements -------------------------------------------------

func (p *parser) parseProgram() (*Program, error) {
	var lets []letBinding
	var omega expr
	sawOmega := false

	for !p.at(tEOF) {
		switch {
		case p.isKeyword("let"):
			lb, err := p.parseLet()
			if err != nil {
				return nil, err
			}
			lets = append(lets, lb)
			p.lets[lb.name] = true
		case p.isKeyword("omega"):
			if sawOmega {
				return nil, fmt.Errorf("line %d: program assigns omega more than once", p.peek().line)
			}
			p.next()
			if _, err := p.expect(tAssign, "'=' after omega"); err != nil {
				return nil, err
			}
			e, err := p.parseExpr()
			if err != nil {
				return nil, err
			}
			omega = e
			sawOmega = true
		default:
			t := p.peek()
			return nil, fmt.Errorf("line %d: expected 'let' or 'omega', found %s", t.line, t)
		}
	}
	if !sawOmega {
		return nil, fmt.Errorf("program must assign omega (e.g. `omega = ...`)")
	}
	return &Program{lets: lets, omega: omega, rels: sortedKeys(p.rels)}, nil
}

func (p *parser) parseLet() (letBinding, error) {
	p.next() // 'let'
	name := p.peek()
	if name.kind != tIdent {
		return letBinding{}, fmt.Errorf("line %d: expected a name after 'let', found %s", name.line, name)
	}
	if reserved[name.text] {
		return letBinding{}, fmt.Errorf("line %d: %q is a reserved word and cannot be a binding name", name.line, name.text)
	}
	p.next()
	if _, err := p.expect(tAssign, fmt.Sprintf("'=' after 'let %s'", name.text)); err != nil {
		return letBinding{}, err
	}
	value, err := p.parseExpr()
	if err != nil {
		return letBinding{}, err
	}
	return letBinding{name: name.text, value: value}, nil
}

// --- expressions (precedence climbing) ------------------------------------

func (p *parser) parseExpr() (expr, error) { return p.parseOr() }

func (p *parser) parseOr() (expr, error) {
	l, err := p.parseAnd()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("or") {
		p.next()
		r, err := p.parseAnd()
		if err != nil {
			return nil, err
		}
		l = binaryExpr{op: "or", l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseAnd() (expr, error) {
	l, err := p.parseNot()
	if err != nil {
		return nil, err
	}
	for p.isKeyword("and") {
		p.next()
		r, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		l = binaryExpr{op: "and", l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseNot() (expr, error) {
	if p.isKeyword("not") {
		p.next()
		x, err := p.parseNot()
		if err != nil {
			return nil, err
		}
		return unaryExpr{op: "not", x: x}, nil
	}
	return p.parseComparison()
}

var compareOps = map[tokKind]string{
	tEq: "==", tNeq: "!=", tLt: "<", tLte: "<=", tGt: ">", tGte: ">=",
}

func (p *parser) parseComparison() (expr, error) {
	l, err := p.parseAdd()
	if err != nil {
		return nil, err
	}
	if op, ok := compareOps[p.peek().kind]; ok {
		p.next()
		r, err := p.parseAdd()
		if err != nil {
			return nil, err
		}
		return binaryExpr{op: op, l: l, r: r}, nil
	}
	return l, nil
}

func (p *parser) parseAdd() (expr, error) {
	l, err := p.parseMul()
	if err != nil {
		return nil, err
	}
	for p.at(tPlus) || p.at(tMinus) {
		op := p.next().text
		r, err := p.parseMul()
		if err != nil {
			return nil, err
		}
		l = binaryExpr{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseMul() (expr, error) {
	l, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for p.at(tStar) || p.at(tSlash) {
		op := p.next().text
		r, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		l = binaryExpr{op: op, l: l, r: r}
	}
	return l, nil
}

func (p *parser) parseUnary() (expr, error) {
	if p.at(tMinus) {
		p.next()
		x, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return unaryExpr{op: "-", x: x}, nil
	}
	return p.parsePrimary()
}

func (p *parser) parsePrimary() (expr, error) {
	t := p.peek()
	switch t.kind {
	case tNumber:
		p.next()
		return numberLit{val: t.num}, nil
	case tString:
		p.next()
		return stringLit{val: t.text}, nil
	case tLParen:
		p.next()
		e, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tRParen, "')'"); err != nil {
			return nil, err
		}
		return e, nil
	case tIdent:
		return p.parseIdentPrimary()
	default:
		return nil, fmt.Errorf("line %d: unexpected %s", t.line, t)
	}
}

func (p *parser) parseIdentPrimary() (expr, error) {
	t := p.peek()
	switch t.text {
	case "true":
		p.next()
		return boolLit{val: true}, nil
	case "false":
		p.next()
		return boolLit{val: false}, nil
	case "match":
		return p.parseMatch()
	case "weighted":
		return p.parseWeighted()
	case "meta", "stat":
		p.next()
		name, err := p.parseNameAfterDot(t.text)
		if err != nil {
			return nil, err
		}
		return p.makeFieldRef(t.text, name, false), nil
	case "out", "in", "bi":
		p.next()
		rel, err := p.parseNameAfterDot(t.text)
		if err != nil {
			return nil, err
		}
		p.rels[rel] = true
		return linkSetRef{dir: directionFor(t.text), rel: rel, line: t.line}, nil
	case "self":
		return p.parseSelfRef()
	case "and", "or", "not", "else", "let", "omega":
		return nil, fmt.Errorf("line %d: unexpected keyword %q", t.line, t.text)
	}

	// Function call?
	if p.peekAt(1).kind == tLParen {
		return p.parseCall()
	}

	// Otherwise a bare identifier — must be a defined let binding.
	if reserved[t.text] {
		return nil, fmt.Errorf("line %d: %q must be followed by a field, e.g. %s.<name>", t.line, t.text, t.text)
	}
	if !p.lets[t.text] {
		return nil, fmt.Errorf("line %d: unknown identifier %q (use meta.%s for metadata, stat.%s for a stat, or %q for a string literal)", t.line, t.text, t.text, t.text, t.text)
	}
	p.next()
	return letRef{name: t.text, line: t.line}, nil
}

func (p *parser) parseSelfRef() (expr, error) {
	self := p.next() // 'self'
	if _, err := p.expect(tDot, "'.' after 'self'"); err != nil {
		return nil, err
	}
	ns := p.peek()
	if ns.kind != tIdent || (ns.text != "meta" && ns.text != "stat") {
		return nil, fmt.Errorf("line %d: 'self.' must be followed by 'meta' or 'stat', found %s", ns.line, ns)
	}
	p.next()
	name, err := p.parseNameAfterDot("self." + ns.text)
	if err != nil {
		return nil, err
	}
	_ = self
	return p.makeFieldRef(ns.text, name, true), nil
}

func (p *parser) makeFieldRef(ns, name string, self bool) expr {
	if ns == "stat" {
		return statRef{name: name, self: self}
	}
	return metaRef{name: name, self: self}
}

// parseNameAfterDot consumes ".<name>" where name is an identifier or a
// backtick-quoted name (for hyphenated fields like meta.`due-date`).
func (p *parser) parseNameAfterDot(prefix string) (string, error) {
	if _, err := p.expect(tDot, fmt.Sprintf("'.' after %q", prefix)); err != nil {
		return "", err
	}
	name := p.peek()
	if name.kind != tIdent && name.kind != tQIdent {
		return "", fmt.Errorf("line %d: expected a name after %q, found %s", name.line, prefix+".", name)
	}
	p.next()
	return name.text, nil
}

func (p *parser) parseCall() (expr, error) {
	name := p.next() // function name
	if !knownFuncs[name.text] {
		return nil, fmt.Errorf("line %d: unknown function %q", name.line, name.text)
	}
	if _, err := p.expect(tLParen, fmt.Sprintf("'(' after %q", name.text)); err != nil {
		return nil, err
	}
	var args []callArg
	if !p.at(tRParen) {
		for {
			arg, err := p.parseArg()
			if err != nil {
				return nil, err
			}
			args = append(args, arg)
			if p.at(tComma) {
				p.next()
				continue
			}
			break
		}
	}
	if _, err := p.expect(tRParen, fmt.Sprintf("')' to close %q", name.text)); err != nil {
		return nil, err
	}
	return callExpr{name: name.text, args: args, line: name.line}, nil
}

func (p *parser) parseArg() (callArg, error) {
	// Named argument: (identifier | string) '=' expr  (used by score).
	first := p.peek()
	if (first.kind == tIdent || first.kind == tString) && p.peekAt(1).kind == tAssign {
		p.next() // name
		p.next() // '='
		value, err := p.parseExpr()
		if err != nil {
			return callArg{}, err
		}
		return callArg{name: first.text, value: value}, nil
	}
	value, err := p.parseExpr()
	if err != nil {
		return callArg{}, err
	}
	return callArg{value: value}, nil
}

func (p *parser) parseMatch() (expr, error) {
	p.next() // 'match'
	subject, err := p.parseExpr()
	if err != nil {
		return nil, err
	}
	if _, err := p.expect(tLBrace, "'{' after match subject"); err != nil {
		return nil, err
	}
	var arms []matchArm
	for !p.at(tRBrace) {
		if p.at(tEOF) {
			return nil, fmt.Errorf("line %d: unterminated match block", p.peek().line)
		}
		arm, err := p.parseMatchArm()
		if err != nil {
			return nil, err
		}
		arms = append(arms, arm)
	}
	p.next() // '}'
	if len(arms) == 0 {
		return nil, fmt.Errorf("match block needs at least one arm")
	}
	return matchExpr{subject: subject, arms: arms}, nil
}

func (p *parser) parseMatchArm() (matchArm, error) {
	t := p.peek()
	var arm matchArm
	if t.kind == tIdent && t.text == "else" {
		arm.isElse = true
		p.next()
	} else {
		switch t.kind {
		case tIdent, tString, tQIdent, tNumber:
			arm.pat = t.text
			p.next()
		default:
			return matchArm{}, fmt.Errorf("line %d: match pattern must be an identifier, string, or number, found %s", t.line, t)
		}
	}
	if _, err := p.expect(tArrow, "'=>' in match arm"); err != nil {
		return matchArm{}, err
	}
	result, err := p.parseExpr()
	if err != nil {
		return matchArm{}, err
	}
	arm.result = result
	return arm, nil
}

func (p *parser) parseWeighted() (expr, error) {
	p.next() // 'weighted'
	if _, err := p.expect(tLBrace, "'{' after weighted"); err != nil {
		return nil, err
	}
	var terms []weightedTerm
	for !p.at(tRBrace) {
		if p.at(tEOF) {
			return nil, fmt.Errorf("line %d: unterminated weighted block", p.peek().line)
		}
		weight, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		if _, err := p.expect(tColon, "':' between weight and contribution"); err != nil {
			return nil, err
		}
		contrib, err := p.parseExpr()
		if err != nil {
			return nil, err
		}
		terms = append(terms, weightedTerm{weight: weight, contrib: contrib})
	}
	p.next() // '}'
	if len(terms) == 0 {
		return nil, fmt.Errorf("weighted block needs at least one term")
	}
	return weightedExpr{terms: terms}, nil
}

func directionFor(word string) Direction {
	switch word {
	case "in":
		return DirIn
	case "bi":
		return DirBi
	default:
		return DirOut
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
