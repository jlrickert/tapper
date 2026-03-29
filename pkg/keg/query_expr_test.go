package keg_test

import (
	"slices"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestParseQueryExpression_DotPrefix(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		expr    string
		wantErr bool
	}{
		{
			name: "dot_field_with_gt",
			expr: ".created>2026-01-01",
		},
		{
			name: "dot_field_with_gte",
			expr: ".accessCount>=5",
		},
		{
			name: "dot_field_with_lt",
			expr: ".updated<2026-03-01",
		},
		{
			name: "dot_field_with_lte",
			expr: ".accessed<=2026-06-15T12:00:00Z",
		},
		{
			name: "dot_field_equality",
			expr: ".hash=abc123",
		},
		{
			name: "dot_field_not_equal",
			expr: ".lead!=deprecated",
		},
		{
			name: "dot_field_boolean",
			expr: ".created",
		},
		{
			name: "dot_field_combined_with_tag",
			expr: ".created>2026-01-01 and golang",
		},
		{
			name: "dot_field_combined_with_attribute",
			expr: ".updated<2026-03-01 and entity=plan",
		},
		{
			name: "dot_field_in_parentheses",
			expr: "(.created>2026-01-01 or .updated<2026-03-01) and golang",
		},
		{
			name: "dot_field_with_not",
			expr: "not .accessCount>=10",
		},
		{
			name: "plain_tag_still_works",
			expr: "golang",
		},
		{
			name: "key_value_still_works",
			expr: "entity=plan",
		},
		{
			name: "complex_expression_backward_compat",
			expr: "a and (b or c)",
		},
		{
			name: "missing_value_after_operator",
			expr: ".created>",
			wantErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()
			_, err := keg.ParseQueryExpression(tc.expr)
			if tc.wantErr {
				require.Error(innerT, err)
			} else {
				require.NoError(innerT, err)
			}
		})
	}
}

func TestEvaluateQueryExpression_DotPrefix(t *testing.T) {
	t.Parallel()

	universe := map[string]struct{}{
		"0": {},
		"1": {},
		"2": {},
		"3": {},
	}

	byTag := map[string]map[string]struct{}{
		"golang": {"1": {}, "2": {}},
		"rust":   {"3": {}},
	}

	// resolveCompare simulates stats field resolution.
	resolveCompare := func(field, op, value string) map[string]struct{} {
		switch field {
		case "created":
			// Simulate: node 1 created 2026-02-01, node 2 created 2026-04-01, others 2025-01-01
			if op == ">" && value == "2026-01-01" {
				return map[string]struct{}{"1": {}, "2": {}}
			}
			if op == "<" && value == "2026-03-01" {
				return map[string]struct{}{"0": {}, "1": {}, "3": {}}
			}
			if op == "" {
				// Boolean: all nodes have a created time.
				return map[string]struct{}{"0": {}, "1": {}, "2": {}, "3": {}}
			}
		case "accessCount":
			if op == ">=" && value == "5" {
				return map[string]struct{}{"2": {}, "3": {}}
			}
		case "hash":
			if op == "=" && value == "abc123" {
				return map[string]struct{}{"1": {}}
			}
		}
		return map[string]struct{}{}
	}

	cases := []struct {
		name string
		expr string
		want []string
	}{
		{
			name: "dot_created_gt",
			expr: ".created>2026-01-01",
			want: []string{"1", "2"},
		},
		{
			name: "dot_created_lt",
			expr: ".created<2026-03-01",
			want: []string{"0", "1", "3"},
		},
		{
			name: "dot_accessCount_gte",
			expr: ".accessCount>=5",
			want: []string{"2", "3"},
		},
		{
			name: "dot_hash_eq",
			expr: ".hash=abc123",
			want: []string{"1"},
		},
		{
			name: "dot_boolean",
			expr: ".created",
			want: []string{"0", "1", "2", "3"},
		},
		{
			name: "dot_combined_with_tag",
			expr: ".created>2026-01-01 and golang",
			want: []string{"1", "2"},
		},
		{
			name: "dot_combined_with_tag_or",
			expr: ".accessCount>=5 or golang",
			want: []string{"1", "2", "3"},
		},
		{
			name: "not_dot_field",
			expr: "not .created>2026-01-01",
			want: []string{"0", "3"},
		},
		{
			name: "plain_tag_backward_compat",
			expr: "golang",
			want: []string{"1", "2"},
		},
		{
			name: "plain_tag_and_or_backward_compat",
			expr: "golang or rust",
			want: []string{"1", "2", "3"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()

			parsed, err := keg.ParseQueryExpression(tc.expr)
			require.NoError(innerT, err)

			gotSet := keg.EvaluateQueryExpressionWithCompare(
				parsed,
				universe,
				func(tag string) map[string]struct{} {
					if ids, ok := byTag[tag]; ok {
						return ids
					}
					return map[string]struct{}{}
				},
				resolveCompare,
			)

			got := setKeys(gotSet)
			want := append([]string{}, tc.want...)
			slices.Sort(got)
			slices.Sort(want)
			require.Equal(innerT, want, got)
		})
	}
}

// TestEvaluateQueryExpression_NilCompareResolver verifies that dot-prefix nodes
// match nothing when no CompareResolver is provided (backward compatibility).
func TestEvaluateQueryExpression_NilCompareResolver(t *testing.T) {
	t.Parallel()

	universe := map[string]struct{}{
		"0": {},
		"1": {},
	}

	parsed, err := keg.ParseQueryExpression(".created>2026-01-01")
	require.NoError(t, err)

	// Use the original EvaluateQueryExpression (no compare resolver).
	gotSet := keg.EvaluateQueryExpression(
		parsed,
		universe,
		func(tag string) map[string]struct{} {
			return map[string]struct{}{}
		},
	)

	require.Empty(t, gotSet, "dot-prefix should match nothing without a compare resolver")
}

func setKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	return out
}
