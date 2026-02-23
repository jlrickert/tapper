package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestInfoEdit_UsesTempFileAndSaves(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-keg.sh")
	capturePath := filepath.Join(jail, "editor-arg.txt")
	script := `#!/bin/sh
printf '%s' "$1" > "$CAPTURE_FILE"
cat > "$1" <<'EOF'
kegv: 2025-07
title: Edited Title
entities:
  client:
    id: 42
    summary: Edited entity
custom_block:
  enabled: true
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	require.NoError(t, sb.Runtime().Set("CAPTURE_FILE", capturePath))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "config", "--edit", "--keg", "example").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	edited := string(sb.MustReadFile("~/kegs/example/keg"))
	require.Contains(t, edited, "title: Edited Title")
	require.Contains(t, edited, "entities:")
	require.Contains(t, edited, "custom_block:")

	rawArg, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	editorArg := strings.TrimSpace(string(rawArg))
	require.NotEmpty(t, editorArg)
	require.True(t, strings.HasSuffix(editorArg, ".yaml"), "editor should receive a temp yaml file")
	require.NotEqual(t, "/home/testuser/kegs/example/keg", editorArg)
}

func TestInfoEdit_InvalidEditsDoNotPersist(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-invalid-keg.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
kegv: [
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	before := sb.MustReadFile("~/kegs/example/keg")
	res := NewProcess(t, false, "config", "--edit", "--keg", "example").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "keg config is invalid after editing")

	after := sb.MustReadFile("~/kegs/example/keg")
	require.Equal(t, string(before), string(after))
}

func TestInfoEdit_UsesPipedStdinWithoutEditor(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))

	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	stdin := strings.NewReader(`kegv: 2025-07
title: Final Title
summary: piped content
`)
	res := NewProcess(t, false, "config", "--edit", "--keg", "example").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.NoError(t, res.Err)

	saved := string(sb.MustReadFile("~/kegs/example/keg"))
	require.Contains(t, saved, "title: Final Title")
	require.Contains(t, saved, "summary: piped content")
}

func TestInfoEdit_RejectsInvalidPipedStdin(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))

	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	before := sb.MustReadFile("~/kegs/example/keg")
	stdin := strings.NewReader("kegv: [\n")
	res := NewProcess(t, false, "config", "--edit", "--keg", "example").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "keg config from stdin is invalid")

	after := sb.MustReadFile("~/kegs/example/keg")
	require.Equal(t, string(before), string(after))
}
