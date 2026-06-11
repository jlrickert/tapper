package tapper

import (
	"context"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestNormalizeMetaYAML(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "json_object_renders_as_block_yaml",
			raw:  `{"tags":["test"]}`,
			want: "tags:\n  - test",
		},
		{
			name: "empty_json_object_renders_empty",
			raw:  `{}`,
			want: "",
		},
		{
			name: "empty_input_renders_empty",
			raw:  "",
			want: "",
		},
		{
			name: "yaml_preserves_all_fields",
			raw:  "title: Example\ntags:\n  - wow\n",
			want: "title: Example\ntags:\n  - wow",
		},
		{
			name: "json_nested_values",
			raw:  `{"tags":["a","b"],"custom":{"k":"v"}}`,
			want: "tags:\n  - a\n  - b\ncustom:\n  k: v",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, normalizeMetaYAML(ctx, []byte(tt.raw)))
		})
	}
}

func TestNormalizeMetaYAML_UnparseableReturnsRaw(t *testing.T) {
	t.Parallel()
	raw := []byte(":\tnot yaml [\n")
	got := normalizeMetaYAML(context.Background(), raw)
	require.Equal(t, ":\tnot yaml [", got)
}

func TestEditFilesEquivalent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	tests := []struct {
		name string
		a    string
		b    string
		want bool
	}{
		{
			name: "json_meta_equals_yaml_meta",
			a:    "---\n{\"tags\":[\"test\"]}\n---\n# Title\n\nbody\n",
			b:    "---\ntags:\n  - test\n---\n# Title\n\nbody\n",
			want: true,
		},
		{
			name: "trailing_newline_drift_in_body",
			a:    "---\ntags:\n  - test\n---\n# Title\n",
			b:    "---\ntags:\n  - test\n---\n# Title",
			want: true,
		},
		{
			name: "different_body",
			a:    "---\n{}\n---\n# Title\n\nold\n",
			b:    "---\n{}\n---\n# Title\n\nnew\n",
			want: false,
		},
		{
			name: "different_meta",
			a:    "---\ntags:\n  - one\n---\nbody\n",
			b:    "---\ntags:\n  - two\n---\nbody\n",
			want: false,
		},
		{
			name: "empty_json_meta_equals_empty_frontmatter",
			a:    "---\n{}\n---\nbody\n",
			b:    "---\n\n---\nbody\n",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want,
				editFilesEquivalent(ctx, []byte(tt.a), []byte(tt.b)))
		})
	}
}

func TestComposeEditNodeFile_NormalizesJSONMeta(t *testing.T) {
	t.Parallel()
	got := composeEditNodeFile(context.Background(),
		[]byte(`{"tags":["test"]}`), []byte("# Title\n"))
	require.Equal(t, "---\ntags:\n  - test\n---\n# Title\n", string(got))
}

func TestEditorTempFilePrefix_UsesLogicalKegIdentity(t *testing.T) {
	t.Parallel()
	k := &keg.LocalKeg{Target: &keg.Target{Namespace: "jlrickert", KegName: "example"}}

	got := editorTempFilePrefix(k, keg.NodeId{ID: 2}, "edit")

	require.Equal(t, "tap-edit-jlrickert-example-2-", got)
}

func TestEditorTempFilePrefix_MetadataUsesSameLogicalIdentity(t *testing.T) {
	t.Parallel()
	k := &keg.LocalKeg{Target: &keg.Target{Namespace: "jlrickert", KegName: "example"}}

	got := editorTempFilePrefix(k, keg.NodeId{ID: 2}, "meta")

	require.Equal(t, "tap-meta-jlrickert-example-2-", got)
}

func TestEditorTempFilePrefix_FileTargetDoesNotUsePathSegments(t *testing.T) {
	t.Parallel()
	k := &keg.LocalKeg{Target: &keg.Target{File: "/Users/jlrickert/kegs/example"}}

	got := editorTempFilePrefix(k, keg.NodeId{ID: 2}, "edit")

	require.Equal(t, "tap-edit-local-keg-2-", got)
	require.NotContains(t, got, "jlrickert")
	require.NotContains(t, got, "example")
}

func TestEditorTempFilePrefix_SanitizesUnsafeCharacters(t *testing.T) {
	t.Parallel()
	k := &keg.LocalKeg{Target: &keg.Target{
		Namespace: "team/foo @bar",
		KegName:   "notes:bad/thing",
	}}

	got := editorTempFilePrefix(k, keg.NodeId{ID: 2}, "edit")

	require.Equal(t, "tap-edit-team-foo-bar-notes-bad-thing-2-", got)
	require.NotContains(t, got, "/")
	require.NotContains(t, got, " ")
	require.NotContains(t, got, ":")
}
