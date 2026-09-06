package omegadsl

// The AST mirrors the grammar in .tapper/specs/omega-dsl.ebnf. All node types
// are unexported; the only public handle is *Program (see omegadsl.go).

type letBinding struct {
	name  string
	value expr
}

// expr is any evaluable Omega DSL expression.
type expr interface{ isExpr() }

// numberLit is a decimal literal, e.g. 0.25.
type numberLit struct{ val float64 }

// boolLit is true / false.
type boolLit struct{ val bool }

// stringLit is a double-quoted literal used for comparisons and enum keys.
type stringLit struct{ val string }

// letRef is a bare identifier referring to an earlier let binding.
type letRef struct {
	name string
	line int
}

// metaRef reads a metadata field: meta.<name>, or self.meta.<name> in a
// predicate (self == true addresses the scored node rather than the target).
type metaRef struct {
	name string
	self bool
}

// statRef reads a node stat: stat.<name> / self.stat.<name>.
type statRef struct {
	name string
	self bool
}

// linkSetRef is a directional relation accessor: out.<rel> / in.<rel> / bi.<rel>.
type linkSetRef struct {
	dir  Direction
	rel  string
	line int
}

// unaryExpr is prefix '-' or 'not'.
type unaryExpr struct {
	op string
	x  expr
}

// binaryExpr covers arithmetic, comparison, and and/or.
type binaryExpr struct {
	op   string
	l, r expr
}

// matchExpr maps a subject value to a number by pattern.
type matchExpr struct {
	subject expr
	arms    []matchArm
}

type matchArm struct {
	isElse bool
	pat    string // subject is compared to this text (identifier/string/number/bool)
	result expr
}

// weightedExpr is the normalized weighted-average combinator.
type weightedExpr struct{ terms []weightedTerm }

type weightedTerm struct {
	weight  expr
	contrib expr
}

// callExpr is a built-in function invocation.
type callExpr struct {
	name string
	args []callArg
	line int
}

// callArg is a positional (name == "") or named (score's k=v) argument.
type callArg struct {
	name  string
	value expr
}

func (numberLit) isExpr()    {}
func (boolLit) isExpr()      {}
func (stringLit) isExpr()    {}
func (letRef) isExpr()       {}
func (metaRef) isExpr()      {}
func (statRef) isExpr()      {}
func (linkSetRef) isExpr()   {}
func (unaryExpr) isExpr()    {}
func (binaryExpr) isExpr()   {}
func (matchExpr) isExpr()    {}
func (weightedExpr) isExpr() {}
func (callExpr) isExpr()     {}
