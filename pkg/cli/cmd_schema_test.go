package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

func TestSchemaEdit_UsesPipedStdinWithoutEditor(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	createSchemaForCLI(t, sb, "example", `type: task
markdown:
  requireTitle: true
`)

	updated := `type: task
markdown:
  requireTitle: false
  sections:
    - heading: Done
      level: 2
`
	res := NewProcess(t, false, "schema", "edit", "--keg", "example", "task").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(updated))
	require.NoError(t, res.Err)

	got := readSchemaForCLI(t, sb, "example", "task")
	require.Contains(t, got, "requireTitle: false")
	require.Contains(t, got, "heading: Done")
}

func TestSchemaEdit_EditorStartsWithSchemaModeline(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))

	original := `type: task
markdown:
  requireTitle: true
`
	createSchemaForCLI(t, sb, "example", original)

	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))
	jail = resolvedJail

	capturePath := filepath.Join(jail, "schema-edit-opened.yaml")
	captureBasenamePath := filepath.Join(jail, "schema-edit-opened.basename")
	scriptPath := filepath.Join(jail, "capture-schema-edit.sh")
	script := fmt.Sprintf("#!/bin/sh\ncp \"$1\" %q\nbasename \"$1\" > %q\n", capturePath, captureBasenamePath)
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")

	res := NewProcess(t, false, "schema", "edit", "--keg", "example", "task").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	raw, err := os.ReadFile(capturePath)
	require.NoError(t, err)
	basenameRaw, err := os.ReadFile(captureBasenamePath)
	require.NoError(t, err)
	require.True(t, strings.HasPrefix(strings.TrimSpace(string(basenameRaw)), "tap-schema-edit-local-example-task-"))
	opened := string(raw)
	require.True(t, strings.HasPrefix(opened, "# yaml-language-server: $schema="+keg.KegSchemaDefinitionSchemaURL+"\n"))
	require.Contains(t, opened, "type: task")

	got := readSchemaForCLI(t, sb, "example", "task")
	require.Equal(t, original, got)
}

func TestSchemaEdit_TypeMismatchPreservesOriginal(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/false"))
	sb.Runtime().Unset("VISUAL")

	original := `type: task
markdown:
  requireTitle: true
`
	createSchemaForCLI(t, sb, "example", original)

	res := NewProcess(t, false, "schema", "edit", "--keg", "example", "task").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(`type: person
markdown:
  requireTitle: false
`))
	require.Error(t, res.Err)
	require.Contains(t, string(res.Stderr), `does not match target type "task"`)

	got := readSchemaForCLI(t, sb, "example", "task")
	require.Equal(t, original, got)
}

func TestSchemaCompletion_TypeArgs(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("testuser", "~"))
	createSchemaForCLI(t, sb, "example", "type: task\n")
	createSchemaForCLI(t, sb, "example", "type: person\n")

	for _, subcommand := range []string{"get", "edit", "rm"} {
		subcommand := subcommand
		t.Run(subcommand, func(t *testing.T) {
			comp := NewCompletionProcess(t, false, 0, "schema", subcommand, "--keg", "example", "").
				Run(sb.Context(), sb.Runtime())
			require.NoError(t, comp.Err)
			require.ElementsMatch(t, []string{"person", "task"}, parseCompletionSuggestions(string(comp.Stdout)))
			require.Contains(t, string(comp.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))

			filtered := NewCompletionProcess(t, false, 0, "schema", subcommand, "--keg", "example", "ta").
				Run(sb.Context(), sb.Runtime())
			require.NoError(t, filtered.Err)
			require.Equal(t, []string{"task"}, parseCompletionSuggestions(string(filtered.Stdout)))

			afterArg := NewCompletionProcess(t, false, 0, "schema", subcommand, "--keg", "example", "task", "").
				Run(sb.Context(), sb.Runtime())
			require.NoError(t, afterArg.Err)
			require.Empty(t, parseCompletionSuggestions(string(afterArg.Stdout)))
			require.Contains(t, string(afterArg.Stdout), fmt.Sprintf(":%d", cobra.ShellCompDirectiveNoFileComp))
		})
	}
}

func TestSchemaCompletion_RespectsKegFlag(t *testing.T) {
	t.Parallel()
	sb := NewSandbox(t, testutils.WithFixture("joe", "~"))

	createSchemaForCLI(t, sb, "personal", "type: task\n")
	createSchemaForCLI(t, sb, "work", "type: workitem\n")

	workComp := NewCompletionProcess(t, false, 0, "schema", "get", "--keg", "work", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, workComp.Err)
	workSuggestions := parseCompletionSuggestions(string(workComp.Stdout))
	require.Equal(t, []string{"workitem"}, workSuggestions)
	require.NotContains(t, workSuggestions, "task")

	defaultComp := NewCompletionProcess(t, false, 0, "schema", "get", "").
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, defaultComp.Err)
	defaultSuggestions := parseCompletionSuggestions(string(defaultComp.Stdout))
	require.Contains(t, defaultSuggestions, "task")
	require.NotContains(t, defaultSuggestions, "workitem")
}

func createSchemaForCLI(t *testing.T, sb *testutils.Sandbox, kegName string, schema string) {
	t.Helper()
	res := NewProcess(t, false, "schema", "create", "--keg", kegName, "-").
		RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(schema))
	require.NoError(t, res.Err)
}

func readSchemaForCLI(t *testing.T, sb *testutils.Sandbox, kegName string, typeName string) string {
	t.Helper()
	res := NewProcess(t, false, "schema", "get", "--keg", kegName, typeName).
		Run(sb.Context(), sb.Runtime())
	require.NoError(t, res.Err)
	return string(res.Stdout)
}
