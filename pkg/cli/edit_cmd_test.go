package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestEdit_SplitsFrontmatterAndBody(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-node.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
---
tags:
  - edited
summary: updated in editor
---
# Edited Title

Edited body content.
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "edit", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	meta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	content := string(sb.MustReadFile("~/kegs/personal/0/README.md"))
	require.Contains(t, meta, "tags:")
	require.Contains(t, meta, "- edited")
	require.Contains(t, meta, "summary: updated in editor")
	require.Contains(t, content, "# Edited Title")
	require.Contains(t, content, "Edited body content.")
}

func TestEdit_UsesPipedStdinWithoutEditor(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	stdin := strings.NewReader(`---
tags:
  - piped
summary: from stdin
---
# Piped Body
`)
	res := NewProcess(t, false, "edit", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.NoError(t, res.Err)

	meta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	content := string(sb.MustReadFile("~/kegs/personal/0/README.md"))
	require.Contains(t, meta, "summary: from stdin")
	require.Contains(t, meta, "- piped")
	require.Contains(t, content, "# Piped Body")
}

func TestEdit_RejectsInvalidPipedFrontmatter(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	beforeMeta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	beforeContent := string(sb.MustReadFile("~/kegs/personal/0/README.md"))

	stdin := strings.NewReader(`---
tags: [
---
# Broken
`)
	res := NewProcess(t, false, "edit", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "invalid frontmatter yaml")

	afterMeta := string(sb.MustReadFile("~/kegs/personal/0/meta.yaml"))
	afterContent := string(sb.MustReadFile("~/kegs/personal/0/README.md"))
	require.Equal(t, beforeMeta, afterMeta)
	require.Equal(t, beforeContent, afterContent)
}
