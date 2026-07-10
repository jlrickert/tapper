package omegadsl

import (
	"errors"
	"fmt"
	"strconv"
)

var errEmptyProgram = errors.New("omegadsl: empty program")

// --- runtime values -------------------------------------------------------

type valueKind int

const (
	kNumber valueKind = iota
	kBool
	kString
	kAbsent // a referenced field/stat that is not present
	kSet    // a link set; only valid as the first argument of a set function
)

type value struct {
	kind valueKind
	num  float64
	b    bool
	s    string
	set  []Node
}

func numberVal(f float64) value { return value{kind: kNumber, num: f} }
func boolVal(b bool) value      { return value{kind: kBool, b: b} }
func stringVal(s string) value  { return value{kind: kString, s: s} }
func absentVal() value          { return value{kind: kAbsent} }
func setVal(n []Node) value     { return value{kind: kSet, set: n} }

// --- evaluation scope -----------------------------------------------------

// scope carries the state threaded through evaluation. root is always the node
// being scored (backing self.* and out./in./bi.); current is the node bare
// meta.*/stat.* read from (root, or a link target inside a predicate).
type scope struct {
	root    Env
	current Node
	lets    map[string]value
	atRoot  bool
}

func evalProgram(p *Program, env Env) (float64, error) {
	if env == nil {
		return 0, errors.New("omegadsl: nil env")
	}
	sc := &scope{root: env, current: env, lets: map[string]value{}, atRoot: true}
	for _, lb := range p.lets {
		v, err := eval(lb.value, sc)
		if err != nil {
			return 0, err
		}
		sc.lets[lb.name] = v
	}
	v, err := eval(p.omega, sc)
	if err != nil {
		return 0, err
	}
	n, err := asNumber(v)
	if err != nil {
		return 0, fmt.Errorf("omega must be a number: %w", err)
	}
	return clamp01(n), nil
}

// child returns a scope for evaluating a predicate against a link target.
func (sc *scope) child(target Node) *scope {
	return &scope{root: sc.root, current: target, lets: sc.lets, atRoot: false}
}

// --- expression evaluation ------------------------------------------------

// eval walks the parsed AST. This is a pure, sandboxed interpreter over a fixed,
// closed set of arithmetic/logic/lookup operations — it is NOT host-code
// execution: there is no reflection, no I/O, and no access to anything beyond
// the supplied Env's scalar meta/stat/link accessors. It cannot escape that
// surface regardless of program input.
func eval(e expr, sc *scope) (value, error) {
	switch n := e.(type) {
	case numberLit:
		return numberVal(n.val), nil
	case boolLit:
		return boolVal(n.val), nil
	case stringLit:
		return stringVal(n.val), nil
	case letRef:
		v, ok := sc.lets[n.name]
		if !ok {
			return value{}, fmt.Errorf("undefined binding %q", n.name)
		}
		return v, nil
	case metaRef:
		return fieldValue(sc, n.self, false, n.name), nil
	case statRef:
		return fieldValue(sc, n.self, true, n.name), nil
	case linkSetRef:
		return evalLinkSet(n, sc)
	case unaryExpr:
		return evalUnary(n, sc)
	case binaryExpr:
		return evalBinary(n, sc)
	case matchExpr:
		return evalMatch(n, sc)
	case weightedExpr:
		return evalWeighted(n, sc)
	case callExpr:
		return evalCall(n, sc)
	default:
		return value{}, fmt.Errorf("omegadsl: unhandled expression %T", e)
	}
}

func fieldValue(sc *scope, self, stat bool, name string) value {
	node := sc.current
	if self {
		node = sc.root
	}
	var (
		s  string
		ok bool
	)
	if stat {
		s, ok = node.Stat(name)
	} else {
		s, ok = node.Meta(name)
	}
	if !ok {
		return absentVal()
	}
	return stringVal(s)
}

func evalLinkSet(n linkSetRef, sc *scope) (value, error) {
	if !sc.atRoot {
		return value{}, fmt.Errorf("%s.%s: link sets cannot be used inside a predicate (multi-hop traversal is not supported)", n.dir, n.rel)
	}
	nodes, err := sc.root.LinkSet(n.rel, n.dir)
	if err != nil {
		return value{}, err
	}
	return setVal(nodes), nil
}

func evalUnary(n unaryExpr, sc *scope) (value, error) {
	v, err := eval(n.x, sc)
	if err != nil {
		return value{}, err
	}
	switch n.op {
	case "-":
		f, err := asNumber(v)
		if err != nil {
			return value{}, err
		}
		return numberVal(-f), nil
	case "not":
		return boolVal(!truthy(v)), nil
	default:
		return value{}, fmt.Errorf("omegadsl: unknown unary operator %q", n.op)
	}
}

func evalBinary(n binaryExpr, sc *scope) (value, error) {
	switch n.op {
	case "and":
		l, err := eval(n.l, sc)
		if err != nil {
			return value{}, err
		}
		if !truthy(l) {
			return boolVal(false), nil
		}
		r, err := eval(n.r, sc)
		if err != nil {
			return value{}, err
		}
		return boolVal(truthy(r)), nil
	case "or":
		l, err := eval(n.l, sc)
		if err != nil {
			return value{}, err
		}
		if truthy(l) {
			return boolVal(true), nil
		}
		r, err := eval(n.r, sc)
		if err != nil {
			return value{}, err
		}
		return boolVal(truthy(r)), nil
	}

	l, err := eval(n.l, sc)
	if err != nil {
		return value{}, err
	}
	r, err := eval(n.r, sc)
	if err != nil {
		return value{}, err
	}

	switch n.op {
	case "==":
		return boolVal(valuesEqual(l, r)), nil
	case "!=":
		return boolVal(!valuesEqual(l, r)), nil
	case "<", "<=", ">", ">=":
		lf, err := asNumber(l)
		if err != nil {
			return value{}, err
		}
		rf, err := asNumber(r)
		if err != nil {
			return value{}, err
		}
		return boolVal(compareNumbers(n.op, lf, rf)), nil
	case "+", "-", "*", "/":
		lf, err := asNumber(l)
		if err != nil {
			return value{}, err
		}
		rf, err := asNumber(r)
		if err != nil {
			return value{}, err
		}
		if n.op == "/" && rf == 0 {
			return value{}, errors.New("division by zero")
		}
		return numberVal(arith(n.op, lf, rf)), nil
	default:
		return value{}, fmt.Errorf("omegadsl: unknown operator %q", n.op)
	}
}

func evalMatch(n matchExpr, sc *scope) (value, error) {
	subj, err := eval(n.subject, sc)
	if err != nil {
		return value{}, err
	}
	key, matchable := scalarString(subj)
	if matchable {
		for _, arm := range n.arms {
			if !arm.isElse && arm.pat == key {
				return eval(arm.result, sc)
			}
		}
	}
	for _, arm := range n.arms {
		if arm.isElse {
			return eval(arm.result, sc)
		}
	}
	return numberVal(0), nil
}

func evalWeighted(n weightedExpr, sc *scope) (value, error) {
	var acc, total float64
	for _, term := range n.terms {
		wv, err := eval(term.weight, sc)
		if err != nil {
			return value{}, err
		}
		w, err := asNumber(wv)
		if err != nil {
			return value{}, fmt.Errorf("weighted term weight: %w", err)
		}
		cv, err := eval(term.contrib, sc)
		if err != nil {
			return value{}, err
		}
		c, err := asNumber(cv)
		if err != nil {
			return value{}, fmt.Errorf("weighted term contribution: %w", err)
		}
		acc += w * clamp01(c)
		total += w
	}
	if total <= 0 {
		return numberVal(0), nil
	}
	return numberVal(acc / total), nil
}

// --- coercions & helpers --------------------------------------------------

func asNumber(v value) (float64, error) {
	switch v.kind {
	case kNumber:
		return v.num, nil
	case kBool:
		if v.b {
			return 1, nil
		}
		return 0, nil
	case kString:
		f, err := strconv.ParseFloat(v.s, 64)
		if err != nil {
			return 0, fmt.Errorf("%q is not a number", v.s)
		}
		return f, nil
	case kAbsent:
		return 0, nil
	default:
		return 0, errors.New("a link set is not a number")
	}
}

func truthy(v value) bool {
	switch v.kind {
	case kBool:
		return v.b
	case kNumber:
		return v.num != 0
	case kString:
		return v.s != "" && v.s != "false" && v.s != "0"
	case kAbsent:
		return false
	case kSet:
		return len(v.set) > 0
	default:
		return false
	}
}

// scalarString renders a scalar value for match/equality comparison. It returns
// ok == false for absent values and link sets, which never match.
func scalarString(v value) (string, bool) {
	switch v.kind {
	case kString:
		return v.s, true
	case kNumber:
		return strconv.FormatFloat(v.num, 'g', -1, 64), true
	case kBool:
		if v.b {
			return "true", true
		}
		return "false", true
	default:
		return "", false
	}
}

func valuesEqual(l, r value) bool {
	ls, lok := scalarString(l)
	rs, rok := scalarString(r)
	if !lok || !rok {
		return false // absent never equals anything, including another absent
	}
	return ls == rs
}

func compareNumbers(op string, l, r float64) bool {
	switch op {
	case "<":
		return l < r
	case "<=":
		return l <= r
	case ">":
		return l > r
	case ">=":
		return l >= r
	default:
		return false
	}
}

func arith(op string, l, r float64) float64 {
	switch op {
	case "+":
		return l + r
	case "-":
		return l - r
	case "*":
		return l * r
	case "/":
		return l / r
	default:
		return 0
	}
}

func clamp01(f float64) float64 {
	if f < 0 {
		return 0
	}
	if f > 1 {
		return 1
	}
	return f
}
