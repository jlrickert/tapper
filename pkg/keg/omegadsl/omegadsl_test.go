package omegadsl

import (
	"math"
	"testing"
)

// mockNode is a test Node with fixed meta/stat maps.
type mockNode struct {
	meta map[string]string
	stat map[string]string
}

func (m mockNode) Meta(f string) (string, bool) { v, ok := m.meta[f]; return v, ok }
func (m mockNode) Stat(f string) (string, bool) { v, ok := m.stat[f]; return v, ok }

// mockEnv is a test Env: the scored node plus its directional link sets keyed by
// relation name.
type mockEnv struct {
	mockNode
	out map[string][]Node
	in  map[string][]Node
	rel map[string]bool // declared relations (for LinkSet error behavior)
}

func (e mockEnv) LinkSet(rel string, dir Direction) ([]Node, error) {
	if e.rel != nil && !e.rel[rel] {
		return nil, errUnknownRelation(rel)
	}
	switch dir {
	case DirOut:
		return e.out[rel], nil
	case DirIn:
		return e.in[rel], nil
	case DirBi:
		return dedupeNodes(append(append([]Node{}, e.out[rel]...), e.in[rel]...)), nil
	default:
		return nil, nil
	}
}

type unknownRel string

func (u unknownRel) Error() string      { return "unknown relation " + string(u) }
func errUnknownRelation(r string) error { return unknownRel(r) }

func dedupeNodes(ns []Node) []Node {
	seen := map[Node]bool{}
	out := make([]Node, 0, len(ns))
	for _, n := range ns {
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out
}

// node returns a pointer so link targets are hashable by identity (the real Env
// dedupes bi. sets by NodeId; mockNode holds maps and cannot be a map key).
func node(meta map[string]string) Node { return &mockNode{meta: meta} }

func evalSrc(t *testing.T, src string, env Env) float64 {
	t.Helper()
	prog, err := Parse(src)
	if err != nil {
		t.Fatalf("parse error: %v\nsource:\n%s", err, src)
	}
	got, err := prog.Eval(env)
	if err != nil {
		t.Fatalf("eval error: %v\nsource:\n%s", err, src)
	}
	return got
}

func assertDelta(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestParseErrors(t *testing.T) {
	cases := map[string]string{
		"no omega":            `let a = 1`,
		"duplicate omega":     "omega = 1\nomega = 0",
		"unknown identifier":  `omega = status`,
		"unknown function":    `omega = frobnicate(1)`,
		"bare namespace":      `omega = meta`,
		"reserved let name":   `let meta = 1`,
		"empty match":         `omega = match meta.x { }`,
		"unterminated string": `omega = "abc`,
		"duration literal":    `omega = 90d`,
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(src); err == nil {
				t.Fatalf("expected parse error for %q", src)
			}
		})
	}
}

func TestMatchAndScore(t *testing.T) {
	env := mockEnv{mockNode: mockNode{meta: map[string]string{"status": "ready"}}}

	assertDelta(t, evalSrc(t, `
		omega = match meta.status {
			done  => 1.0
			ready => 0.6
			else  => 0.0
		}`, env), 0.6)

	assertDelta(t, evalSrc(t, `omega = score(meta.status, done=1, ready=0.6, else=0)`, env), 0.6)

	// Absent field falls through to else.
	empty := mockEnv{mockNode: mockNode{meta: map[string]string{}}}
	assertDelta(t, evalSrc(t, `omega = score(meta.status, done=1, else=0.1)`, empty), 0.1)
	// No else, no match -> 0.
	assertDelta(t, evalSrc(t, `omega = score(meta.status, done=1)`, empty), 0)
}

func TestArithmeticComparisonAndClamp(t *testing.T) {
	env := mockEnv{mockNode: mockNode{
		meta: map[string]string{"count": "8"},
		stat: map[string]string{"accessCount": "25"},
	}}
	assertDelta(t, evalSrc(t, `omega = clamp(meta.count / 10, 0, 1)`, env), 0.8)
	assertDelta(t, evalSrc(t, `omega = clamp(stat.accessCount / 10, 0, 1)`, env), 1)
	assertDelta(t, evalSrc(t, `omega = meta.count > 5`, env), 1) // bool -> 1.0
	assertDelta(t, evalSrc(t, `omega = meta.count < 5`, env), 0) // bool -> 0.0
	assertDelta(t, evalSrc(t, `omega = min(meta.count, 3) / 3`, env), 1)
}

func TestStringEqualityRequiresQuotes(t *testing.T) {
	env := mockEnv{mockNode: mockNode{meta: map[string]string{"status": "done"}}}
	assertDelta(t, evalSrc(t, `omega = meta.status == "done"`, env), 1)
	assertDelta(t, evalSrc(t, `omega = meta.status == "draft"`, env), 0)
	// absent never equals anything
	empty := mockEnv{mockNode: mockNode{meta: map[string]string{}}}
	assertDelta(t, evalSrc(t, `omega = meta.status == "done"`, empty), 0)
}

func TestLinkDirectionsAndSetFunctions(t *testing.T) {
	children := []Node{
		node(map[string]string{"status": "done"}),
		node(map[string]string{"status": "done"}),
		node(map[string]string{"status": "draft"}),
		node(map[string]string{"status": "done"}),
	}
	parents := []Node{node(map[string]string{"type": "task"})}
	env := mockEnv{
		mockNode: mockNode{meta: map[string]string{"status": "ready"}},
		out:      map[string][]Node{"children": children, "parent": parents},
		rel:      map[string]bool{"children": true, "parent": true},
	}

	assertDelta(t, evalSrc(t, `omega = count(out.children) / 10`, env), 0.4) // omega clamps to [0,1]
	assertDelta(t, evalSrc(t, `omega = has(out.parent)`, env), 1)
	assertDelta(t, evalSrc(t, `omega = frac(out.children, meta.status == "done")`, env), 0.75)
	assertDelta(t, evalSrc(t, `omega = any(out.children, meta.status == "draft")`, env), 1)
	assertDelta(t, evalSrc(t, `omega = all(out.children, meta.status == "done")`, env), 0)
	assertDelta(t, evalSrc(t, `omega = avg(out.children, score(meta.status, done=1, else=0))`, env), 0.75)
}

func TestBidirectionalAndBacklinks(t *testing.T) {
	a := node(map[string]string{"n": "a"})
	b := node(map[string]string{"n": "b"})
	env := mockEnv{
		mockNode: mockNode{meta: map[string]string{}},
		out:      map[string][]Node{"related": {a}},
		in:       map[string][]Node{"related": {b}, "parent": {a, b, node(nil)}},
		rel:      map[string]bool{"related": true, "parent": true},
	}
	assertDelta(t, evalSrc(t, `omega = count(in.parent) / 10`, env), 0.3) // backlinks (omega clamps)
	assertDelta(t, evalSrc(t, `omega = count(bi.related) / 2`, env), 1)   // union of {a} and {b}
}

func TestSelfReferenceInPredicate(t *testing.T) {
	kids := []Node{
		node(map[string]string{"priority": "high"}),
		node(map[string]string{"priority": "low"}),
	}
	env := mockEnv{
		mockNode: mockNode{meta: map[string]string{"priority": "high"}},
		in:       map[string][]Node{"parent": kids},
		rel:      map[string]bool{"parent": true},
	}
	// children whose priority matches THIS node's priority
	assertDelta(t, evalSrc(t, `omega = frac(in.parent, meta.priority == self.meta.priority)`, env), 0.5)
}

func TestUnknownRelationErrors(t *testing.T) {
	env := mockEnv{mockNode: mockNode{meta: map[string]string{}}, rel: map[string]bool{"parent": true}}
	prog, err := Parse(`omega = count(out.children)`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if _, err := prog.Eval(env); err == nil {
		t.Fatal("expected eval error for undeclared relation")
	}
	// Relations() surfaces referenced relations for schema validation.
	if got := prog.Relations(); len(got) != 1 || got[0] != "children" {
		t.Fatalf("Relations() = %v, want [children]", got)
	}
}

func TestNestedLinkSetRejected(t *testing.T) {
	env := mockEnv{
		mockNode: mockNode{meta: map[string]string{}},
		out:      map[string][]Node{"children": {node(nil)}},
		rel:      map[string]bool{"children": true},
	}
	prog, err := Parse(`omega = frac(out.children, has(out.grandchildren))`)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if _, err := prog.Eval(env); err == nil {
		t.Fatal("expected error: link set inside a predicate")
	}
}

// TestSpecWorkedExamples reproduces the two hand-traced examples from
// .tapper/specs/omega-dsl.md §12 (task ≈ 0.757, project ≈ 0.833).
func TestSpecWorkedExamples(t *testing.T) {
	t.Run("task", func(t *testing.T) {
		children := []Node{
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "draft"}),
		}
		env := mockEnv{
			mockNode: mockNode{meta: map[string]string{"status": "ready"}},
			out:      map[string][]Node{"parent": {node(nil)}, "children": children},
			rel:      map[string]bool{"parent": true, "children": true},
		}
		src := `
			let status_score = match meta.status {
			  done  => 1.0
			  ready => 0.6
			  draft => 0.25
			  else  => 0.0
			}
			omega = weighted {
			  0.4 : status_score
			  0.3 : has(out.parent)
			  0.2 : frac(out.children, meta.status == "done")
			}`
		// (0.4*0.6 + 0.3*1 + 0.2*0.75) / 0.9
		want := (0.4*0.6 + 0.3*1.0 + 0.2*0.75) / 0.9
		assertDelta(t, evalSrc(t, src, env), want)
	})

	t.Run("project", func(t *testing.T) {
		tasks := []Node{
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "done"}),
			node(map[string]string{"status": "ready"}),
		}
		related := []Node{node(nil), node(nil)}
		env := mockEnv{
			mockNode: mockNode{meta: map[string]string{}},
			in:       map[string][]Node{"parent": tasks},
			out:      map[string][]Node{"related": related},
			rel:      map[string]bool{"parent": true, "related": true},
		}
		src := `
			omega = weighted {
			  0.5 : frac(in.parent, meta.status == "done")
			  0.3 : clamp(count(in.parent) / 5, 0, 1)
			  0.2 : min(count(bi.related), 3) / 3
			}`
		want := (0.5*0.8 + 0.3*1.0 + 0.2*(2.0/3.0)) / 1.0
		assertDelta(t, evalSrc(t, src, env), want)
	})
}
