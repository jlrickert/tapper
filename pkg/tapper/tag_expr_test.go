package tapper

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestTagExpression_Evaluate(t *testing.T) {
	t.Parallel()

	universe := map[string]struct{}{
		"0": {},
		"1": {},
		"2": {},
		"3": {},
	}
	byTag := map[string]map[string]struct{}{
		"a":   {"1": {}, "2": {}},
		"b":   {"1": {}},
		"c":   {"2": {}, "3": {}},
		"and": {"3": {}},
	}

	cases := []struct {
		name string
		expr string
		want []string
	}{
		{
			name: "and_or_with_parentheses",
			expr: "a and (b or c)",
			want: []string{"1", "2"},
		},
		{
			name: "and_not",
			expr: "a and not c",
			want: []string{"1"},
		},
		{
			name: "symbolic_operators",
			expr: "a && !c",
			want: []string{"1"},
		},
		{
			name: "precedence",
			expr: "a or b and c",
			want: []string{"1", "2"},
		},
		{
			name: "quoted_keyword_literal",
			expr: "'and' or a",
			want: []string{"1", "2", "3"},
		},
		{
			name: "not_expression",
			expr: "not a",
			want: []string{"0", "3"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()

			root, err := parseTagExpression(tc.expr)
			require.NoError(innerT, err)

			gotSet := evaluateTagExpression(root, universe, func(tag string) map[string]struct{} {
				if ids, ok := byTag[tag]; ok {
					return ids
				}
				return map[string]struct{}{}
			})

			got := setKeys(gotSet)
			want := append([]string{}, tc.want...)
			slices.Sort(got)
			slices.Sort(want)
			require.Equal(innerT, want, got)
		})
	}
}

func TestTagExpression_ParseErrors(t *testing.T) {
	t.Parallel()

	cases := []string{
		"",
		"a and (b",
		"a and )",
		"&& a",
	}

	for _, expr := range cases {
		t.Run(expr, func(innerT *testing.T) {
			innerT.Parallel()
			_, err := parseTagExpression(expr)
			require.Error(innerT, err)
		})
	}
}

func setKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
