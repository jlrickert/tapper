package omegadsl

import "fmt"

// evalCall dispatches a built-in function. Set functions (count/has/any/all/
// frac/avg/sum) receive a link set as their first argument and evaluate their
// predicate/expression argument once per link target (in the target's scope);
// the other builtins evaluate their arguments normally.
func evalCall(n callExpr, sc *scope) (value, error) {
	switch n.name {
	case "score":
		return evalScore(n, sc)
	case "clamp":
		return evalClamp(n, sc)
	case "min", "max":
		return evalMinMax(n, sc)
	case "present", "missing":
		return evalPresence(n, sc)
	case "count", "has", "any", "all", "frac", "avg", "sum":
		return evalSetFunc(n, sc)
	default:
		return value{}, fmt.Errorf("unknown function %q", n.name)
	}
}

func positionalArgs(n callExpr) ([]expr, error) {
	out := make([]expr, 0, len(n.args))
	for _, a := range n.args {
		if a.name != "" {
			return nil, fmt.Errorf("%s does not take named arguments (%q)", n.name, a.name)
		}
		out = append(out, a.value)
	}
	return out, nil
}

// score(field, k1=v1, ..., else=e) — the enum-scoring builtin. The single
// positional argument is the subject field; named arguments map subject values
// to numeric contributions, with `else` as the fallback.
func evalScore(n callExpr, sc *scope) (value, error) {
	var subject expr
	pos := 0
	for _, a := range n.args {
		if a.name == "" {
			pos++
			subject = a.value
		}
	}
	if pos != 1 {
		return value{}, fmt.Errorf("score expects exactly one positional field argument, got %d", pos)
	}
	subj, err := eval(subject, sc)
	if err != nil {
		return value{}, err
	}
	key, ok := scalarString(subj)

	var matched, elseExpr expr
	for _, a := range n.args {
		switch {
		case a.name == "":
			// subject, already handled
		case a.name == "else":
			elseExpr = a.value
		case ok && a.name == key:
			matched = a.value
		}
	}
	switch {
	case matched != nil:
		return evalNumber(matched, sc)
	case elseExpr != nil:
		return evalNumber(elseExpr, sc)
	default:
		return numberVal(0), nil
	}
}

func evalClamp(n callExpr, sc *scope) (value, error) {
	args, err := positionalArgs(n)
	if err != nil {
		return value{}, err
	}
	if len(args) != 3 {
		return value{}, fmt.Errorf("clamp expects 3 arguments (x, lo, hi), got %d", len(args))
	}
	x, err := evalToNumber(args[0], sc)
	if err != nil {
		return value{}, err
	}
	lo, err := evalToNumber(args[1], sc)
	if err != nil {
		return value{}, err
	}
	hi, err := evalToNumber(args[2], sc)
	if err != nil {
		return value{}, err
	}
	if x < lo {
		x = lo
	}
	if x > hi {
		x = hi
	}
	return numberVal(x), nil
}

func evalMinMax(n callExpr, sc *scope) (value, error) {
	args, err := positionalArgs(n)
	if err != nil {
		return value{}, err
	}
	if len(args) == 0 {
		return value{}, fmt.Errorf("%s expects at least one argument", n.name)
	}
	acc, err := evalToNumber(args[0], sc)
	if err != nil {
		return value{}, err
	}
	for _, a := range args[1:] {
		f, err := evalToNumber(a, sc)
		if err != nil {
			return value{}, err
		}
		if n.name == "min" && f < acc {
			acc = f
		}
		if n.name == "max" && f > acc {
			acc = f
		}
	}
	return numberVal(acc), nil
}

// present(ref) reports whether a metadata/stat reference is set; missing is its
// negation. It is defined for any argument (a literal is always "present").
func evalPresence(n callExpr, sc *scope) (value, error) {
	args, err := positionalArgs(n)
	if err != nil {
		return value{}, err
	}
	if len(args) != 1 {
		return value{}, fmt.Errorf("%s expects exactly one argument, got %d", n.name, len(args))
	}
	v, err := eval(args[0], sc)
	if err != nil {
		return value{}, err
	}
	present := v.kind != kAbsent
	if n.name == "missing" {
		return boolVal(!present), nil
	}
	return boolVal(present), nil
}

// setFuncArity is the required positional argument count per set function. A
// value of -1 means "1 or 2".
var setFuncArity = map[string]int{
	"count": -1, "has": 1,
	"any": 2, "all": 2, "frac": 2, "avg": 2, "sum": 2,
}

func evalSetFunc(n callExpr, sc *scope) (value, error) {
	args, err := positionalArgs(n)
	if err != nil {
		return value{}, err
	}
	want := setFuncArity[n.name]
	switch {
	case want == -1 && (len(args) < 1 || len(args) > 2):
		return value{}, fmt.Errorf("%s expects 1 or 2 arguments, got %d", n.name, len(args))
	case want != -1 && len(args) != want:
		return value{}, fmt.Errorf("%s expects %d arguments, got %d", n.name, want, len(args))
	}

	setValue, err := eval(args[0], sc)
	if err != nil {
		return value{}, err
	}
	if setValue.kind != kSet {
		return value{}, fmt.Errorf("%s expects a link set (out./in./bi.) as its first argument", n.name)
	}
	nodes := setValue.set

	// Functions with no predicate.
	switch n.name {
	case "has":
		return boolVal(len(nodes) > 0), nil
	case "count":
		if len(args) == 1 {
			return numberVal(float64(len(nodes))), nil
		}
	}

	pred := args[1]
	switch n.name {
	case "count":
		matches, err := countMatches(nodes, pred, sc)
		if err != nil {
			return value{}, err
		}
		return numberVal(float64(matches)), nil
	case "any":
		matches, err := countMatches(nodes, pred, sc)
		if err != nil {
			return value{}, err
		}
		return boolVal(matches > 0), nil
	case "all":
		matches, err := countMatches(nodes, pred, sc)
		if err != nil {
			return value{}, err
		}
		return boolVal(matches == len(nodes)), nil
	case "frac":
		if len(nodes) == 0 {
			return numberVal(0), nil
		}
		matches, err := countMatches(nodes, pred, sc)
		if err != nil {
			return value{}, err
		}
		return numberVal(float64(matches) / float64(len(nodes))), nil
	case "avg", "sum":
		var total float64
		for _, target := range nodes {
			f, err := evalToNumber(pred, sc.child(target))
			if err != nil {
				return value{}, err
			}
			total += f
		}
		if n.name == "avg" {
			if len(nodes) == 0 {
				return numberVal(0), nil
			}
			return numberVal(total / float64(len(nodes))), nil
		}
		return numberVal(total), nil
	}
	return value{}, fmt.Errorf("omegadsl: unhandled set function %q", n.name)
}

func countMatches(nodes []Node, pred expr, sc *scope) (int, error) {
	matches := 0
	for _, target := range nodes {
		v, err := eval(pred, sc.child(target))
		if err != nil {
			return 0, err
		}
		if truthy(v) {
			matches++
		}
	}
	return matches, nil
}

// evalNumber evaluates e and coerces the result to a number value.
func evalNumber(e expr, sc *scope) (value, error) {
	f, err := evalToNumber(e, sc)
	if err != nil {
		return value{}, err
	}
	return numberVal(f), nil
}

func evalToNumber(e expr, sc *scope) (float64, error) {
	v, err := eval(e, sc)
	if err != nil {
		return 0, err
	}
	return asNumber(v)
}
