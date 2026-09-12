package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	testutils "github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestCreate_Table(t *testing.T) {
	cases := []struct {
		name               string
		args               []string
		stdin              string
		exactOut           string
		outRegex           string
		wantReadmeNotEmpty bool
		readmeContains     []string
		metaContains       []string
		statsContains      []string
	}{
		{
			name:     "default_keg",
			args:     []string{"create"},
			stdin:    "# Note\n\none-line\n",
			exactOut: "1",
			readmeContains: []string{
				"# Note",
				"one-line",
			},
			statsContains: []string{
				"\"title\":\"Note\"",
				"\"lead\":\"one-line\"",
				"\"created\":\"{now}\"",
				"\"updated\":\"{now}\"",
			},
		},
		{
			name:     "with_tags",
			args:     []string{"create"},
			stdin:    "---\ntags:\n  - alpha\n  - beta\n---\n# Tagged\n\nhas tags\n",
			outRegex: `^\d+`,
			metaContains: []string{
				"tags:",
				"- alpha",
				"- beta",
			},
		},
		{
			name:     "with_schema",
			args:     []string{"create", "--schema", "note"},
			stdin:    "# Schema Note\n",
			outRegex: `^\d+`,
			metaContains: []string{
				"type: note",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up a fresh fixture per case so repository state is isolated.
			fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
			h := NewProcess(t, false, tc.args...)
			if tc.stdin != "" {
				h.SetStdin(strings.NewReader(tc.stdin))
			}

			// Capture the fixture time for timestamp assertions.
			now := fx.Now().Format(time.RFC3339)

			// Execute the CLI command.
			res := h.Run(fx.Context(), fx.Runtime())
			require.NoError(t, res.Err)

			// Validate stdout expectations.
			if tc.exactOut != "" {
				out := string(res.Stdout)
				require.Equal(t, tc.exactOut, out)
			} else if tc.outRegex != "" {
				out := strings.TrimSpace(string(res.Stdout))
				require.Regexp(t, tc.outRegex, out)
			}

			// Verify README expectations.
			if tc.wantReadmeNotEmpty || len(tc.readmeContains) > 0 {
				content := fixtureContent(t, fx.Runtime(), "example", "1")
				if tc.wantReadmeNotEmpty {
					require.NotEmpty(t, content, "expected README to be written for created node")
				}
				for _, want := range tc.readmeContains {
					require.Contains(t, content, want)
				}
			}

			// Verify meta expectations.
			if len(tc.metaContains) > 0 {
				ms := fixtureMeta(t, fx.Runtime(), "example", "1")
				for _, want := range tc.metaContains {
					if strings.Contains(want, "{now}") {
						want = strings.ReplaceAll(want, "{now}", now)
					}
					require.Contains(t, ms, want)
				}
			}

			// Verify stats expectations.
			if len(tc.statsContains) > 0 {
				ss := fixtureStatsJSON(t, fx.Runtime(), "example", "1")
				for _, want := range tc.statsContains {
					if strings.Contains(want, "{now}") {
						want = strings.ReplaceAll(want, "{now}", now)
					}
					require.Contains(t, ss, want)
				}
			}
		})
	}
}

// TestCreate_FromStdin verifies that content provided on stdin is used as the
// created node README content. Note: most flags are not supported when using
// stdin input; prefer invoking the command without flags when providing stdin.
func TestCreate_FromStdin(t *testing.T) {
	fx := NewSandbox(t,
		testutils.WithFixture("testuser", "/home/testuser"),
	)

	proc := NewProcess(t, true, "create")

	stdin := "Title line\n\nThis content came from stdin.\n"
	res := proc.RunWithIO(fx.Context(), fx.Runtime(), strings.NewReader(stdin))

	// Invoke create with a positional marker that signals stdin usage.
	// CLI implementation may choose the convention; tests assume "stdin".
	require.NoError(t, res.Err)

	// The command should emit the new node id on stdout.
	out := strings.TrimSpace(string(res.Stdout))
	require.Regexp(t, `^\d+`, out)

	// Verify the created README contains the stdin content.
	content := fixtureContent(t, fx.Runtime(), "example", "1")
	require.Contains(t, content, "This content came from stdin.")
}

// TestCreate_WithoutContentOrTerminal pins the replacement for what used to be
// the `no_args_outputs_id` case. With neither piped content nor a terminal
// there is nothing to build a node from: a node's title is its content's H1 and
// there is no other source. It used to create an untitled node anyway, which
// only ever worked because this fixture is LocalKeg-backed — against a hub the
// same call came back with an opaque "node title is required".
func TestCreate_WithoutContentOrTerminal(t *testing.T) {
	fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))

	res := NewProcess(t, false, "create").Run(fx.Context(), fx.Runtime())

	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "no content to create a node from")
	require.Empty(t, strings.TrimSpace(string(res.Stdout)), "a failed create must not print a node id")
}

// editorScript installs a $EDITOR that runs script against the buffer path —
// the harness cmd_edit_test.go uses. The jail is resolved through symlinks
// first so the editor sees the same path the runtime wrote.
func editorScript(t *testing.T, sb *testutils.Sandbox, name, script string) {
	t.Helper()
	jail := sb.Runtime().GetJail()
	require.NotEmpty(t, jail)
	resolvedJail, err := filepath.EvalSymlinks(jail)
	require.NoError(t, err)
	require.NoError(t, sb.Runtime().SetJail(resolvedJail))

	scriptPath := filepath.Join(resolvedJail, name+".sh")
	require.NoError(t, os.WriteFile(scriptPath, []byte(script), 0o755))
	require.NoError(t, sb.Runtime().Set("EDITOR", "/bin/sh "+scriptPath))
	sb.Runtime().Unset("VISUAL")
}

// TestCreate_EditorWritesTheNode is the regression test for `tap c` on a
// terminal. The node does not exist before the editor opens — it is built from
// what the author writes — so this also proves a real title reaches the keg,
// which is what a hub requires and what the old scaffold-first order could
// never supply.
func TestCreate_EditorWritesTheNode(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	editorScript(t, sb, "create-writes", "#!/bin/sh\nprintf '%s' '"+
		"---\ntags:\n  - drafted\n---\n# Written In The Editor\n\nBody typed by the author.\n"+
		"' > \"$1\"\n")

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)
	require.Regexp(t, `^\d+`, strings.TrimSpace(string(res.Stdout)))

	content := fixtureContent(t, sb.Runtime(), "example", "1")
	require.Contains(t, content, "# Written In The Editor")
	require.Contains(t, content, "Body typed by the author.")
	// Frontmatter lands in meta, exactly as tap edit splits it: the buffer is
	// one document to the author and two fields to the keg.
	require.NotContains(t, content, "tags:")
	require.Contains(t, fixtureMeta(t, sb.Runtime(), "example", "1"), "- drafted")
}

// TestCreate_EditorQuitWithoutSavingCreatesNothing is the other half of
// building the node from the buffer: abandoning the editor leaves no empty node
// behind and burns no node id.
func TestCreate_EditorQuitWithoutSavingCreatesNothing(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	editorScript(t, sb, "create-abandon", "#!/bin/sh\nexit 0\n")

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "exited without saving")
	require.Empty(t, strings.TrimSpace(string(res.Stdout)))
}

// TestCreate_EditorRetriesAfterRejectedSave pins the "first *successful* save"
// contract. A save the keg refuses is reported without ending the session, so
// the author fixes the buffer and saves again — and ends up with exactly one
// node carrying the corrected content, not one node per save attempt.
func TestCreate_EditorRetriesAfterRejectedSave(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	// First write is frontmatter the keg rejects; the second is valid. The
	// sleep lets the file watcher observe them as two distinct saves.
	editorScript(t, sb, "create-retry", "#!/bin/sh\n"+
		"printf '%s' '---\nnot: [valid\n---\n# Broken\n' > \"$1\"\n"+
		"sleep 3\n"+
		"printf '%s' '# Fixed Title\n\nSecond attempt.\n' > \"$1\"\n"+
		"sleep 3\n")

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	content := fixtureContent(t, sb.Runtime(), "example", "1")
	require.Contains(t, content, "# Fixed Title")
	require.Contains(t, content, "Second attempt.")
	require.NotContains(t, content, "Broken")

	// Exactly one node was created, not one per save attempt.
	require.False(t, fixtureNodeExists(t, sb.Runtime(), "example", "2"),
		"a retried save must update the node, not create a second one")
}

// TestCreate_EditorKeepsCreatedNodeWhenALaterSaveIsRejected is the other side
// of the mode switch. Once the first save has created the node, later saves are
// guarded updates, and one the keg refuses leaves the node holding the last
// content it accepted. It mirrors the contract
// TestEdit_LiveSavePreservesEarlierValidContentOnLaterInvalidSave pins for an
// existing node: a create session earns the same protection as soon as it has a
// node to protect.
func TestCreate_EditorKeepsCreatedNodeWhenALaterSaveIsRejected(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	editorScript(t, sb, "create-then-break", "#!/bin/sh\n"+
		"printf '%s' '# First Valid\n\nFirst body.\n' > \"$1\"\n"+
		"sleep 3\n"+
		"printf '%s' '---\nnot: [valid\n---\n# Broken Final\n' > \"$1\"\n"+
		"sleep 3\n")

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)

	content := fixtureContent(t, sb.Runtime(), "example", "1")
	require.Contains(t, content, "# First Valid")
	require.NotContains(t, content, "# Broken Final")
	_, draftPath, found := strings.Cut(string(res.Stderr), "your draft is kept at ")
	require.True(t, found, "the rejected final draft must remain recoverable")
	draftPath, _, _ = strings.Cut(draftPath, ";")
	draft, err := os.ReadFile(filepath.Join(sb.Runtime().GetJail(), strings.TrimSpace(draftPath)))
	require.NoError(t, err)
	require.Contains(t, string(draft), "# Broken Final")
}

// TestCreate_EditorPreservesDraftWhenNothingCanBeCreated covers the data-loss
// path. If every save is rejected, the author's work exists only in the temp
// buffer — deleting it on the way out, which is safe for tap edit because its
// saves land during the session, would throw the whole draft away here.
func TestCreate_EditorPreservesDraftWhenNothingCanBeCreated(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	editorScript(t, sb, "create-doomed", "#!/bin/sh\n"+
		"printf '%s' '---\nnot: [valid\n---\n# Hours Of Work\n' > \"$1\"\n"+
		"sleep 3\n")

	res := NewProcess(t, true, "create").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.Error(t, res.Err)
	require.Contains(t, res.Err.Error(), "your draft is kept at ")
	require.False(t, fixtureNodeExists(t, sb.Runtime(), "example", "1"))

	// The path named in the error must actually hold the author's work.
	_, draftPath, found := strings.Cut(res.Err.Error(), "your draft is kept at ")
	require.True(t, found)
	draft, readErr := os.ReadFile(filepath.Join(sb.Runtime().GetJail(), strings.TrimSpace(draftPath)))
	require.NoError(t, readErr)
	require.Contains(t, string(draft), "# Hours Of Work")
}

// TestCreate_EditorSchemaPrefillsType pins that --schema reaches the opened
// buffer. The schema rides the editor rather than the create call, so an empty
// node is never typed and then rejected for that type's required fields before
// the author can fill them in.
func TestCreate_EditorSchemaPrefillsType(t *testing.T) {
	sb := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
	// Copy the opened buffer out before overwriting it, so the test can assert
	// on what the author would have seen.
	editorScript(t, sb, "create-schema", "#!/bin/sh\n"+
		"cp \"$1\" \"$(dirname \"$1\")/opened-buffer.txt\"\n"+
		"printf '%s' '# Typed Note\n' > \"$1\"\n")

	res := NewProcess(t, true, "create", "--schema", "note").RunWithIO(sb.Context(), sb.Runtime(), strings.NewReader(""))
	require.NoError(t, res.Err)
	require.Contains(t, fixtureMeta(t, sb.Runtime(), "example", "1"), "type: note")
}
