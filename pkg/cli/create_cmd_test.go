package cli_test

import (
	"strings"
	"testing"
	"time"

	"github.com/jlrickert/go-std/testutils"
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
			fx := NewFixture(t, testutils.WithFixture("testuser", "/home/testuser"))
			h := NewHarness(t, fx, true)

			// Capture the fixture time for timestamp assertions.
			now := fx.Now().Format(time.RFC3339)

			// Execute the CLI command.
			err := h.Run(tc.args...)
			require.NoError(t, err)

			// Validate stdout expectations.
			if tc.exactOut != "" {
				out := h.OutBuf.String()
				require.Equal(t, tc.exactOut, out)
			} else if tc.outRegex != "" {
				out := strings.TrimSpace(h.OutBuf.String())
				require.Regexp(t, tc.outRegex, out)
			}

			// Verify README expectations.
			readmePath := "~/kegs/example/1/README.md"
			if tc.wantReadmeNotEmpty || len(tc.readmeContains) > 0 {
				content := fx.MustReadJailFile(readmePath)
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
				meta := fx.MustReadJailFile(metaPath)
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
