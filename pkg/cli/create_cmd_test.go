package cli_test

import (
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
		exactOut           string
		outRegex           string
		wantReadmeNotEmpty bool
		readmeContains     []string
		metaContains       []string
	}{
		{
			name:     "default_keg",
			args:     []string{"create", "--title", "Note", "--lead", "one-line"},
			exactOut: "1",
			readmeContains: []string{
				"# Note",
				"one-line",
			},
			metaContains: []string{
				"title: Note",
				"lead: one-line",
				"created: {now}",
				"updated: {now}",
			},
		},
		{
			name:               "no_args_outputs_id",
			args:               []string{"create"},
			outRegex:           `^\d+`,
			wantReadmeNotEmpty: true,
			metaContains: []string{
				"created: {now}",
				"updated: {now}",
			},
		},
		{
			name:     "with_tags",
			args:     []string{"create", "--title", "Tagged", "--lead", "has tags", "--tags", "alpha", "--tags", "beta"},
			outRegex: `^\d+`,
			metaContains: []string{
				"tags:",
				"- alpha",
				"- beta",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up a fresh fixture per case so filesystem state is isolated.
			fx := NewSandbox(t, testutils.WithFixture("testuser", "/home/testuser"))
			h := NewProcess(t, false, tc.args...)

			// Capture the fixture time for timestamp assertions.
			now := fx.Now().Format(time.RFC3339)

			// Execute the CLI command.
			res := h.Run(fx.Context())
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
			readmePath := "~/kegs/example/1/README.md"
			if tc.wantReadmeNotEmpty || len(tc.readmeContains) > 0 {
				content := fx.MustReadFile(readmePath)
				if tc.wantReadmeNotEmpty {
					require.NotEmpty(t, content, "expected README to be written for created node")
				}
				for _, want := range tc.readmeContains {
					require.Contains(t, string(content), want)
				}
			}

			// Verify meta expectations.
			metaPath := "~/kegs/example/1/meta.yaml"
			if len(tc.metaContains) > 0 {
				meta := fx.MustReadFile(metaPath)
				ms := string(meta)
				for _, want := range tc.metaContains {
					if strings.Contains(want, "{now}") {
						want = strings.ReplaceAll(want, "{now}", now)
					}
					require.Contains(t, ms, want)
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
	res := proc.RunWithIO(fx.Context(), strings.NewReader(stdin))

	// Invoke create with a positional marker that signals stdin usage.
	// CLI implementation may choose the convention; tests assume "stdin".
	require.NoError(t, res.Err)

	// The command should emit the new node id on stdout.
	out := strings.TrimSpace(string(res.Stdout))
	require.Regexp(t, `^\d+`, out)

	// Verify the created README contains the stdin content.
	readmePath := "~/kegs/example/1/README.md"
	content := fx.MustReadFile(readmePath)
	require.Contains(t, string(content), "This content came from stdin.")
}
