package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestRepoConfigEdit_UserUsesPipedStdinWithoutEditor(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	input := `fallbackKeg: stdin-user
kegSearchPaths:
  - ~/Documents/kegs
kegMap: []
kegs: {}
defaultRegistry: ""
`
	res := NewProcess(t, false, "repo", "config", "edit", "--user").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader(input),
	)
	require.NoError(t, res.Err)

	saved := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Equal(t, input, saved)
}

func TestRepoConfigEdit_UserRejectsInvalidPipedStdin(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	before := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	res := NewProcess(t, false, "repo", "config", "edit", "--user").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader("fallbackKeg: [\n"),
	)
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "tap config from stdin is invalid")

	after := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Equal(t, before, after)
}

func TestRepoConfigEdit_UserAcceptsUnknownFieldsFromStdin(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	input := "defaultKeg: example\nunknownKey: value\n"
	res := NewProcess(t, false, "repo", "config", "edit", "--user").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader(input),
	)
	require.NoError(t, res.Err)

	after := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Equal(t, input, after)
}

func TestRepoConfigEdit_ProjectUsesPipedStdinWithoutEditor(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	require.NoError(t, sb.Runtime().Mkdir("/home/testuser/project/.tapper", 0o755, true))
	sb.Setwd("~/project")
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	input := `defaultKeg: stdin-project
kegMap: []
kegs: {}
defaultRegistry: ""
`
	res := NewProcess(t, false, "repo", "config", "edit", "--project").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader(input),
	)
	require.NoError(t, res.Err)

	saved := string(sb.MustReadFile("~/project/.tapper/config.yaml"))
	require.Equal(t, input, saved)
}

func TestRepoConfigEdit_ProjectRejectsInvalidPipedStdin(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	require.NoError(t, sb.Runtime().Mkdir("/home/testuser/project/.tapper", 0o755, true))
	require.NoError(t, sb.Runtime().AtomicWriteFile("/home/testuser/project/.tapper/config.yaml", []byte("defaultKeg: before\n"), 0o644))
	sb.Setwd("~/project")
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	before := string(sb.MustReadFile("~/project/.tapper/config.yaml"))
	res := NewProcess(t, false, "repo", "config", "edit", "--project").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader("defaultKeg: [\n"),
	)
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), "tap config from stdin is invalid")

	after := string(sb.MustReadFile("~/project/.tapper/config.yaml"))
	require.Equal(t, before, after)
}

func TestRepoConfigEdit_UsesTempFileAndPreservesUnknownFields(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-repo-config.sh")
	capturePath := filepath.Join(jail, "editor-arg.txt")
	script := `#!/bin/sh
printf '%s' "$1" > "$CAPTURE_FILE"
cat > "$1" <<'EOF'
defaultKeg: edited
unknownKey: keep-me
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	require.NoError(t, sb.Runtime().Set("CAPTURE_FILE", capturePath))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "repo", "config", "edit", "--user").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader(""),
	)
	require.NoError(t, res.Err)

	saved := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Contains(t, saved, "defaultKeg: edited")
	require.Contains(t, saved, "unknownKey: keep-me")

	rawArg, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	editorArg := strings.TrimSpace(string(rawArg))
	require.NotEmpty(t, editorArg)
	require.True(t, strings.HasSuffix(editorArg, ".yaml"))
	require.NotEqual(t, "/home/testuser/.config/tapper/config.yaml", editorArg)
}

func TestRepoConfigEdit_LiveSavePreservesEarlierValidConfigOnLaterInvalidSave(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	scriptPath := filepath.Join(jail, "edit-repo-config-live.sh")
	script := `#!/bin/sh
cat > "$1" <<'EOF'
defaultKeg: valid
unknownKey: keep-me
EOF
sleep 1
cat > "$1" <<'EOF'
defaultKeg: [
EOF
`
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))

	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "repo", "config", "edit", "--user").RunWithIO(
		sb.Context(),
		sb.Runtime(),
		strings.NewReader(""),
	)
	require.NoError(t, res.Err)

	saved := string(sb.MustReadFile("~/.config/tapper/config.yaml"))
	require.Contains(t, saved, "defaultKeg: valid")
	require.Contains(t, saved, "unknownKey: keep-me")
}
