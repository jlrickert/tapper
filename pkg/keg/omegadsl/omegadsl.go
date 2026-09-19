// Package omegadsl implements the Omega DSL — a small expression language for
// keg schema maturity calculations. A program computes a single node's omega
// score (a float64 clamped to [0,1]) from the node's metadata and its links.
//
// The language, grammar, and semantics are specified in the tapper-hub repo at
// .tapper/specs/omega-dsl.md and .tapper/specs/omega-dsl.ebnf. This package is
// the reference implementation of that spec, minus temporal features (age() and
// duration literals), which are deferred because they interact poorly with
// deterministic snapshot replay.
//
// The package is self-contained (standard library only). Callers supply an Env
// that resolves the current node's metadata and its directional link sets; the
// evaluator never touches the keg graph directly.
package omegadsl

// Direction selects which edges a link-set accessor (out./in./bi.) traverses.
type Direction int

const (
	// DirOut is a forward link: nodes the current node points to.
	DirOut Direction = iota
	// DirIn is a backlink: nodes that point to the current node.
	DirIn
	// DirBi is the deduped union of forward links and backlinks.
	DirBi
)

func (d Direction) String() string {
	switch d {
	case DirOut:
		return "out"
	case DirIn:
		return "in"
	case DirBi:
		return "bi"
	default:
		return "unknown"
	}
}

// Node is the evaluator's view of a single node — the scored node or a linked
// one. It exposes the two scalar namespaces a program can read: metadata
// (author-defined schema fields, addressed as meta.<name>) and stats (computed
// node stats such as accessCount or links, addressed as stat.<name>). Values are
// scalars rendered as strings; absent fields report ok == false.
type Node interface {
	// Meta returns a scalar metadata field value and whether it is present.
	Meta(field string) (string, bool)
	// Stat returns a scalar node-stat value and whether it is present.
	Stat(field string) (string, bool)
}

// Env is the evaluator's view of the node being scored. It is a Node (its own
// meta.* and stat.*) plus LinkSet, which resolves a named relation in a given
// direction (out./in./bi.) to the set of linked nodes. LinkSet returns an error
// when the relation name is not declared by the schema.
type Env interface {
	Node
	LinkSet(relation string, dir Direction) ([]Node, error)
}

// Program is a parsed, reusable Omega DSL program. It is safe to Eval
// concurrently against different Envs; parsing is done once via Parse.
type Program struct {
	lets  []letBinding
	omega expr
	rels  []string // distinct relation names referenced by out./in./bi.
}

// Parse compiles Omega DSL source into a Program. It returns an error on any
// lexical or syntactic problem; it does not evaluate the program.
func Parse(src string) (*Program, error) {
	return parse(src)
}

// Relations lists the distinct relation names the program references through
// out./in./bi. accessors. The schema layer uses this to verify every referenced
// relation is actually declared.
func (p *Program) Relations() []string {
	if p == nil {
		return nil
	}
	out := make([]string, len(p.rels))
	copy(out, p.rels)
	return out
}

// Eval runs the program against env and returns the clamped omega score in
// [0,1]. It returns an error if evaluation hits a type or lookup failure (for
// example, arithmetic on a non-numeric field, or a link-set inside a link-set).
func (p *Program) Eval(env Env) (float64, error) {
	if p == nil {
		return 0, errEmptyProgram
	}
	return evalProgram(p, env)
}
