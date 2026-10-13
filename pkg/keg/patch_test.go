package keg_test

import (
	"strings"
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

func TestApplyUnifiedPatch(t *testing.T) {
	t.Parallel()
	base := "# Title\n\nalpha\nbeta\ngamma\n\n## Status\nDraft\n"
	cases := []struct {
		name  string
		base  string
		patch string
		want  string
	}{
		{
			name:  "replaces a line",
			base:  base,
			patch: "@@ -7,2 +7,2 @@\n ## Status\n-Draft\n+Published\n",
			want:  "# Title\n\nalpha\nbeta\ngamma\n\n## Status\nPublished\n",
		},
		{
			name:  "accepts file headers",
			base:  base,
			patch: "--- a/README.md\n+++ b/README.md\n@@ -3,1 +3,1 @@\n-alpha\n+ALPHA\n",
			want:  "# Title\n\nALPHA\nbeta\ngamma\n\n## Status\nDraft\n",
		},
		{
			name:  "applies when the stated line is wrong but the lines are unique",
			base:  base,
			patch: "@@ -40,3 +40,3 @@\n alpha\n-beta\n+BETA\n gamma\n",
			want:  "# Title\n\nalpha\nBETA\ngamma\n\n## Status\nDraft\n",
		},
		{
			name:  "applies several hunks in order",
			base:  base,
			patch: "@@ -1 +1 @@\n-# Title\n+# New title\n@@ -8 +8,2 @@\n-Draft\n+Published\n+On 2026-10-10\n",
			want:  "# New title\n\nalpha\nbeta\ngamma\n\n## Status\nPublished\nOn 2026-10-10\n",
		},
		{
			name:  "reads a bare empty line as empty context",
			base:  base,
			patch: "@@ -5,3 +5,3 @@\n gamma\n\n-## Status\n+## State\n",
			want:  "# Title\n\nalpha\nbeta\ngamma\n\n## State\nDraft\n",
		},
		{
			name:  "inserts after a stated line with no context",
			base:  "one\ntwo\n",
			patch: "@@ -1,0 +2 @@\n+one and a half\n",
			want:  "one\none and a half\ntwo\n",
		},
		{
			name:  "drops the final newline when told to",
			base:  "one\ntwo\n",
			patch: "@@ -2 +2 @@\n-two\n+two\n\\ No newline at end of file\n",
			want:  "one\ntwo",
		},
		{
			name:  "adds a final newline when the old side lacked one",
			base:  "one\ntwo",
			patch: "@@ -2 +2 @@\n-two\n\\ No newline at end of file\n+two\n",
			want:  "one\ntwo\n",
		},
		{
			name:  "uses the stated line when the lines repeat",
			base:  "x\ny\nx\ny\n",
			patch: "@@ -3,2 +3,2 @@\n x\n-y\n+z\n",
			want:  "x\ny\nx\nz\n",
		},
		{
			name:  "empties a note",
			base:  "only\n",
			patch: "@@ -1 +0,0 @@\n-only\n",
			want:  "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := keg.ApplyUnifiedPatch([]byte(tc.base), tc.patch)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestApplyUnifiedPatchRefusesWhatItCannotPlace(t *testing.T) {
	t.Parallel()
	base := "x\ny\nx\ny\n"
	cases := []struct {
		name     string
		patch    string
		contains string
	}{
		{name: "empty", patch: "  \n", contains: "empty"},
		{name: "no hunks", patch: "--- a\n+++ b\n", contains: "no hunks"},
		{name: "text before a hunk", patch: "change y\n@@ -2 +2 @@\n-y\n+z\n", contains: "hunk header"},
		{name: "context that is not there", patch: "@@ -1,2 +1,2 @@\n x\n-q\n+z\n", contains: "do not match"},
		{name: "ambiguous lines away from the stated line", patch: "@@ -9,2 +9,2 @@\n x\n-y\n+z\n", contains: "match 2 places"},
		{name: "overlapping hunks", patch: "@@ -1,2 +1,2 @@\n-x\n+w\n y\n@@ -2,1 +2,1 @@\n-y\n+v\n@@ -1 +1 @@\n-x\n+u\n", contains: "hunk 3: its lines only occur before"},
		{name: "a hunk that changes nothing", patch: "@@ -1 +1 @@\n x\n", contains: "changes nothing"},
		{name: "a second file", patch: "@@ -1 +1 @@\n-x\n+z\n--- a/other\n+++ b/other\n", contains: "another file"},
		{name: "an insertion with no line", patch: "@@ @@\n+new\n", contains: "line number"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := keg.ApplyUnifiedPatch([]byte(base), tc.patch)
			require.ErrorIs(t, err, keg.ErrInvalid)
			require.ErrorContains(t, err, tc.contains)
		})
	}
}

func TestLocalKegUpdateNodesAppliesContentPatch(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Note\n\nDraft\n")})
	require.NoError(t, err)
	original, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)

	results, err := k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, ContentPatch: "@@ -3 +3 @@\n-Draft\n+Published\n", HasContentPatch: true, ExpectedContentHash: original.ContentHash(),
	}})
	require.NoError(t, err)
	patched, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "# Note\n\nPublished\n", string(patched.Content))
	require.Equal(t, patched.ContentHash(), results[0].ContentHash)

	// A stale token is a conflict, even though the patch would no longer
	// fit either: the caller learns the note moved on.
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, ContentPatch: "@@ -3 +3 @@\n-Draft\n+Final\n", HasContentPatch: true, ExpectedContentHash: original.ContentHash(),
	}})
	var conflict *keg.PreconditionConflictError
	require.ErrorAs(t, err, &conflict)

	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, Content: []byte("# X\n"), HasContent: true, ContentPatch: "@@ -1 +1 @@\n-# Note\n+# X\n", HasContentPatch: true, ExpectedContentHash: patched.ContentHash(),
	}})
	require.ErrorIs(t, err, keg.ErrInvalid)

	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, ContentPatch: "@@ -1 +1,3 @@\n-# Note\n+---\n+tags: [x]\n+---\n", HasContentPatch: true, ExpectedContentHash: patched.ContentHash(),
	}})
	require.Error(t, err, "a patch may not add frontmatter")
	unchanged, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, patched.Content, unchanged.Content)
}

func TestApplyUnifiedPatchKeepsLineEndingsAndCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		base  string
		patch string
		want  string
	}{
		{
			name:  "keeps CRLF line endings",
			base:  "a\r\nb\r\nc\r\n",
			patch: "@@ -2 +2 @@\n-b\n+B\n",
			want:  "a\r\nB\r\nc\r\n",
		},
		{
			name:  "keeps CRLF when adding lines at the end",
			base:  "a\r\nb\r\n",
			patch: "@@ -2 +2,2 @@\n b\n+c\n",
			want:  "a\r\nb\r\nc\r\n",
		},
		{
			name:  "keeps each untouched line's own ending in a mixed body",
			base:  "a\r\nb\n",
			patch: "@@ -1 +1 @@\n-a\n+A\n",
			want:  "A\r\nb\n",
		},
		{
			name:  "gives a former last line the body's ending when a line follows it",
			base:  "a\r\nb",
			patch: "@@ -1,2 +1,3 @@\n a\n-b\n\\ No newline at end of file\n+b\n+c\n\\ No newline at end of file\n",
			want:  "a\r\nb\r\nc",
		},
		{
			name:  "reads header-looking lines inside a hunk by its counts",
			base:  "x\n-- old\ny\n",
			patch: "@@ -1,3 +1,3 @@\n x\n--- old\n+++ new\n y\n",
			want:  "x\n++ new\ny\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := keg.ApplyUnifiedPatch([]byte(tc.base), tc.patch)
			require.NoError(t, err)
			require.Equal(t, tc.want, string(got))
		})
	}
}

func TestApplyUnifiedPatchRefusesBadMarkersAndCostlyPatches(t *testing.T) {
	t.Parallel()
	t.Run("no-newline marker on a hunk that does not reach the end", func(t *testing.T) {
		t.Parallel()
		_, err := keg.ApplyUnifiedPatch([]byte("one\ntwo\nthree\n"), "@@ -1 +1 @@\n-one\n+ONE\n\\ No newline at end of file\n")
		require.ErrorIs(t, err, keg.ErrInvalid)
		require.ErrorContains(t, err, "No newline")
	})
	t.Run("a second file after a complete hunk", func(t *testing.T) {
		t.Parallel()
		_, err := keg.ApplyUnifiedPatch([]byte("x\ny\n"), "@@ -1 +1 @@\n-x\n+z\n--- a/other\n+++ b/other\n@@ -1 +1 @@\n-q\n+r\n")
		require.ErrorIs(t, err, keg.ErrInvalid)
		require.ErrorContains(t, err, "another file")
	})
	t.Run("a patch too costly to place", func(t *testing.T) {
		t.Parallel()
		base := strings.Repeat("a\n", 200_000)
		var patch strings.Builder
		patch.WriteString("@@ -1,50001 +1,50001 @@\n")
		for range 50_000 {
			patch.WriteString(" a\n")
		}
		patch.WriteString("-b\n+c\n")
		_, err := keg.ApplyUnifiedPatch([]byte(base), patch.String())
		require.ErrorIs(t, err, keg.ErrInvalid)
		require.ErrorContains(t, err, "too expensive")
	})
	t.Run("a patch over the size limit", func(t *testing.T) {
		t.Parallel()
		patch := "@@ -1 +1 @@\n-a\n+" + strings.Repeat("b", 1<<20) + "\n"
		_, err := keg.ApplyUnifiedPatch([]byte("a\n"), patch)
		require.ErrorIs(t, err, keg.ErrInvalid)
	})
}

func TestUpdateNodesPatchKeepsCRLF(t *testing.T) {
	t.Parallel()
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	initNonStrictTestKeg(t, k, ctx)
	created, err := k.Create(ctx, &keg.CreateOptions{Body: []byte("# Note\n")})
	require.NoError(t, err)
	require.NoError(t, k.Repo.WriteContent(ctx, created.ID, []byte("# Note\r\n\r\nDraft\r\n")))
	view, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, ContentPatch: "@@ -3 +3 @@\n-Draft\n+Published\n", HasContentPatch: true, ExpectedContentHash: view.ContentHash(),
	}})
	require.NoError(t, err)
	got, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, "# Note\r\n\r\nPublished\r\n", string(got.Content))

	// A marker on a hunk away from the end changes nothing.
	_, err = k.UpdateNodes(ctx, []keg.NodeUpdateOptions{{
		ID: created.ID, ContentPatch: "@@ -1 +1 @@\n-# Note\n+# Title\n\\ No newline at end of file\n", HasContentPatch: true, ExpectedContentHash: got.ContentHash(),
	}})
	require.ErrorIs(t, err, keg.ErrInvalid)
	unchanged, err := k.ReadNode(ctx, created.ID)
	require.NoError(t, err)
	require.Equal(t, got.Content, unchanged.Content)
}
