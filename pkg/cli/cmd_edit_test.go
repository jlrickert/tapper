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

	meta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	content := fixtureContent(t, sb.Runtime(), "personal", "0")
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

	meta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	content := fixtureContent(t, sb.Runtime(), "personal", "0")
	require.Contains(t, meta, "summary: from stdin")
	require.Contains(t, meta, "- piped")
	require.Contains(t, content, "# Piped Body")
}

func TestEdit_PipedSchemaSelectionPersistsType(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	created := NewProcess(t, false, "create", "--keg", "personal", "--title", "Editable").Run(sb.Context(), sb.Runtime())
	require.NoError(t, created.Err)

	res := NewProcess(t, false, "edit", "1", "--keg", "personal", "--schema", "task").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader("# Edited with schema\n"))
	require.NoError(t, res.Err)
	require.Contains(t, fixtureMeta(t, sb.Runtime(), "personal", "1"), "type: task")
}

func TestEdit_RejectsInvalidPipedFrontmatter(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	beforeMeta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	beforeContent := fixtureContent(t, sb.Runtime(), "personal", "0")

	stdin := strings.NewReader(`---
tags: [
---
# Broken
`)
	res := NewProcess(t, false, "edit", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "invalid frontmatter yaml")

	afterMeta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	afterContent := fixtureContent(t, sb.Runtime(), "personal", "0")
	require.Equal(t, beforeMeta, afterMeta)
	require.Equal(t, beforeContent, afterContent)
}

func TestEdit_LiveSavePreservesEarlierValidContentOnLaterInvalidSave(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-live-valid-then-invalid.sh")
	// The sleep between saves must exceed the fsnotify debounce window
	// (120ms ticker + 120ms debounce = ~240ms minimum). Use 3 seconds to
	// avoid flakiness under the race detector, where I/O overhead is higher.
	script := `#!/bin/sh
cat > "$1" <<'EOF'
---
summary: first valid save
tags:
  - live
---
# Saved First
EOF
sleep 3
cat > "$1" <<'EOF'
---
tags: [
---
# Broken Final
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "edit", "0", "--keg", "personal").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	meta := fixtureMeta(t, sb.Runtime(), "personal", "0")
	content := fixtureContent(t, sb.Runtime(), "personal", "0")
	require.Contains(t, meta, "summary: first valid save")
	require.Contains(t, meta, "- live")
	require.Contains(t, content, "# Saved First")
	require.NotContains(t, content, "# Broken Final")
}

// TestEdit_InteractiveEdit_BumpsAccessCount verifies that opening a node in
// the editor (interactive edit via editWithTempFile) increments access_count.
func TestEdit_InteractiveEdit_BumpsAccessCount(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	// Editor that immediately exits without changes.
	scriptPath := filepath.Join(jail, "edit-noop.sh")
	script := "#!/bin/sh\ntrue\n"
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	statsPath := "~/kegs/@local/personal/0/stats.json"
	sb.MustWriteFile(statsPath, []byte(`{"accessed":"2001-01-01T00:00:00Z","access_count":3}`), 0o644)

	res := NewProcess(t, false, "edit", "0", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	stats := fixtureStats(t, sb.Runtime(), "personal", "0")
	require.Equal(t, 4, stats.AccessCount(),
		"interactive edit should bump access_count")
}

// TestEdit_PipedEdit_DoesNotBumpAccessCount verifies that piped edits
// (content via stdin) do not increment access_count.
func TestEdit_PipedEdit_DoesNotBumpAccessCount(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	statsPath := "~/kegs/@local/personal/0/stats.json"
	sb.MustWriteFile(statsPath, []byte(`{"accessed":"2001-01-01T00:00:00Z","access_count":3}`), 0o644)

	stdin := strings.NewReader(`---
tags:
  - piped-count-test
---
# Piped Count Test
`)
	res := NewProcess(t, false, "edit", "0", "--keg", "personal").
		RunWithIO(sb.Context(), sb.Runtime(), stdin)
	require.NoError(t, res.Err)

	stats := fixtureStats(t, sb.Runtime(), "personal", "0")
	require.Equal(t, 3, stats.AccessCount(),
		"piped edit should not bump access_count")
}
