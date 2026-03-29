package tapper

import (
	"slices"
	"testing"
	"time"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestQueryExpression_Evaluate(t *testing.T) {
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

			root, err := keg.ParseQueryExpression(tc.expr)
			require.NoError(innerT, err)

			gotSet := keg.EvaluateQueryExpression(root, universe, func(tag string) map[string]struct{} {
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

func TestQueryExpression_ParseErrors(t *testing.T) {
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
			_, err := keg.ParseQueryExpression(expr)
			require.Error(innerT, err)
		})
	}
}

func TestMatchTime(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	earlier := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	later := time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		entry time.Time
		op    string
		cmp   time.Time
		want  bool
	}{
		{"boolean_nonzero", base, "", time.Time{}, true},
		{"boolean_zero", time.Time{}, "", time.Time{}, false},
		{"gt_true", later, ">", base, true},
		{"gt_false", earlier, ">", base, false},
		{"gt_equal", base, ">", base, false},
		{"gte_true", base, ">=", base, true},
		{"gte_later", later, ">=", base, true},
		{"gte_earlier", earlier, ">=", base, false},
		{"lt_true", earlier, "<", base, true},
		{"lt_false", later, "<", base, false},
		{"lt_equal", base, "<", base, false},
		{"lte_true", base, "<=", base, true},
		{"lte_earlier", earlier, "<=", base, true},
		{"lte_later", later, "<=", base, false},
		{"eq_true", base, "=", base, true},
		{"eq_false", earlier, "=", base, false},
		{"neq_true", earlier, "!=", base, true},
		{"neq_false", base, "!=", base, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()
			got := matchTime(tc.entry, tc.op, tc.cmp)
			require.Equal(innerT, tc.want, got)
		})
	}
}

func TestMatchString(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		field string
		op    string
		value string
		want  bool
	}{
		{"boolean_nonempty", "hello", "", "", true},
		{"boolean_empty", "", "", "", false},
		{"eq_true", "abc", "=", "abc", true},
		{"eq_false", "abc", "=", "def", false},
		{"neq_true", "abc", "!=", "def", true},
		{"neq_false", "abc", "!=", "abc", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()
			got := matchString(tc.field, tc.op, tc.value)
			require.Equal(innerT, tc.want, got)
		})
	}
}

func TestMatchInt(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		field int
		op    string
		cmp   int
		want  bool
	}{
		{"boolean_nonzero", 5, "", 0, true},
		{"boolean_zero", 0, "", 0, false},
		{"gt_true", 10, ">", 5, true},
		{"gt_false", 3, ">", 5, false},
		{"gte_equal", 5, ">=", 5, true},
		{"lt_true", 3, "<", 5, true},
		{"lt_false", 10, "<", 5, false},
		{"lte_equal", 5, "<=", 5, true},
		{"eq_true", 5, "=", 5, true},
		{"eq_false", 3, "=", 5, false},
		{"neq_true", 3, "!=", 5, true},
		{"neq_false", 5, "!=", 5, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()
			got := matchInt(tc.field, tc.op, tc.cmp)
			require.Equal(innerT, tc.want, got)
		})
	}
}

func TestResolveTimeField(t *testing.T) {
	t.Parallel()

	entries := []keg.NodeIndexEntry{
		{ID: "0", Updated: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), Created: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "1", Updated: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), Created: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)},
		{ID: "2", Updated: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), Created: time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC)},
	}

	cases := []struct {
		name  string
		field string
		op    string
		value string
		want  []string
	}{
		{
			name:  "created_gt",
			field: "created",
			op:    ">",
			value: "2026-01-01",
			want:  []string{"1", "2"},
		},
		{
			name:  "updated_lt",
			field: "updated",
			op:    "<",
			value: "2026-03-01",
			want:  []string{"0", "1"},
		},
		{
			name:  "created_boolean",
			field: "created",
			op:    "",
			value: "",
			want:  []string{"0", "1", "2"},
		},
		{
			name:  "bad_date_returns_nothing",
			field: "created",
			op:    ">",
			value: "not-a-date",
			want:  []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(innerT *testing.T) {
			innerT.Parallel()
			out := make(map[string]struct{})
			resolveTimeField(entries, tc.field, tc.op, tc.value, out)
			got := setKeys(out)
			slices.Sort(got)
			want := append([]string{}, tc.want...)
			slices.Sort(want)
			require.Equal(innerT, want, got)
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
