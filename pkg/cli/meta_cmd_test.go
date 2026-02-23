package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestMetaCommand_TableDrivenErrors(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		fixture     *string
		expectedErr string
	}{
		{
			name:        "missing_node_id",
			args:        []string{"meta"},
			expectedErr: "accepts 1 arg",
		},
		{
			name:        "invalid_node_id",
			args:        []string{"meta", "abc"},
			fixture:     strPtr("joe"),
			expectedErr: "invalid node ID",
		},
		{
			name:        "missing_alias",
			args:        []string{"meta", "0", "--keg", "missing"},
			fixture:     strPtr("joe"),
			expectedErr: "keg alias not found",
		},
		{
			name:        "missing_node",
			args:        []string{"meta", "424242", "--keg", "personal"},
			fixture:     strPtr("joe"),
			expectedErr: "node 424242 not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(innerT *testing.T) {
			innerT.Parallel()
			var opts []testutils.Option
			if tt.fixture != nil {
				opts = append(opts, testutils.WithFixture(*tt.fixture, "~"))
			}
			sb := NewSandbox(innerT, opts...)

			res := NewProcess(innerT, false, tt.args...).Run(sb.Context(), sb.Runtime())

			require.Error(innerT, res.Err)
			require.Contains(innerT, string(res.Stderr), tt.expectedErr)
		})
	}
}

func TestMetaCommand_PrintsFormattedMeta(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	sb.MustWriteFile("~/kegs/personal/0/meta.yaml", []byte(`tags:
  - beta
  - alpha
summary: hello world
`), 0o644)

	res := NewProcess(t, false, "meta", "0", "--keg", "personal").Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)

	out := strings.TrimSpace(string(res.Stdout))
	require.Contains(t, out, "summary: hello world")
	require.Contains(t, out, "tags:")
	require.Contains(t, out, "- alpha")
	require.Contains(t, out, "- beta")
	require.NotContains(t, out, "title:")
}

func TestMetaCommand_ReplaceFromStdin(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	stdin := strings.NewReader(`summary: replaced
tags:
  - zeta
  - alpha
`)
	res := NewProcess(t, false, "meta", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.NoError(t, res.Err)
	require.Equal(t, "", strings.TrimSpace(string(res.Stdout)))

	meta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	require.Contains(t, meta, "summary: replaced")
	require.Contains(t, meta, "- alpha")
	require.Contains(t, meta, "- zeta")
	require.NotContains(t, meta, "title:")
}

func TestMetaCommand_ReplaceFromStdinRejectsInvalidYaml(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	before := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	stdin := strings.NewReader("tags: [\n")
	res := NewProcess(t, false, "meta", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "metadata from stdin is invalid")

	after := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	require.Equal(t, before, after)
}

func TestMetaCommand_Edit_UsesTempFileAndSaves(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	sb.MustWriteFile("~/kegs/personal/0/meta.yaml", []byte("summary: before\n"), 0o644)

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-meta.sh")
	capturePath := filepath.Join(jail, "meta-editor-arg.txt")
	script := `#!/bin/sh
printf '%s' "$1" > "$CAPTURE_FILE"
cat > "$1" <<'EOF'
summary: after edit
tags:
  - ops
  - docs
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("CAPTURE_FILE", capturePath))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "meta", "0", "--keg", "personal", "--edit").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	meta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	require.Contains(t, meta, "summary: after edit")
	require.Contains(t, meta, "- docs")
	require.Contains(t, meta, "- ops")
	require.NotContains(t, meta, "title:")

	rawArg, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	editorArg := strings.TrimSpace(string(rawArg))
	require.NotEmpty(t, editorArg)
	require.True(t, strings.HasSuffix(editorArg, ".yaml"))
	require.NotEqual(t, "/home/testuser/kegs/personal/0/meta.yaml", editorArg)
}

func TestMetaCommand_Edit_InvalidEditsDoNotPersist(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	sb.MustWriteFile("~/kegs/personal/0/meta.yaml", []byte("summary: before\n"), 0o644)

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-meta-invalid.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
tags: [
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	before := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	res := NewProcess(t, false, "meta", "0", "--keg", "personal", "--edit").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "node metadata is invalid after editing")

	after := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	require.Equal(t, before, after)
}

func TestMetaCommand_Edit_UsesPipedStdinAsInitialDraft(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-meta-stdin.sh")
	capturePath := filepath.Join(jail, "meta-editor-initial.txt")
	script := `#!/bin/sh
cat "$1" > "$CAPTURE_FILE"
cat > "$1" <<'EOF'
summary: saved from editor
tags:
  - final
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("CAPTURE_FILE", capturePath))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	stdin := strings.NewReader(`summary: from stdin
tags:
  - draft
`)
	res := NewProcess(t, false, "meta", "0", "--keg", "personal", "--edit").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.NoError(t, res.Err)

	initialRaw, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	require.Contains(t, string(initialRaw), "summary: from stdin")
	require.Contains(t, string(initialRaw), "- draft")

	meta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	require.Contains(t, meta, "summary: saved from editor")
	require.Contains(t, meta, "- final")
}
