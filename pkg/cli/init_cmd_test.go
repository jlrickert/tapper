package cli_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitLocalKeg(t *testing.T) {
	fx := NewSandbox(t)
	h := NewHarness(t, fx)

	// Note: the CLI binds the alias into the `--title` flag (see
	// init_cmd.go). To produce the printed alias use --title.
	err := h.Run(
		"init",
		"mykeg",
		"--type", "local",
		"--title", "myalias",
		"--creator", "me",
	)
	require.NoError(t, err, "init command should succeed")

	out := h.OutBuf.String()
	require.True(t,
		strings.Contains(out, "keg myalias created"),
		"unexpected output: %q", out,
	)

	// stderr should be empty for a successful run.
	require.Equal(t, "", h.ErrBuf.String())

	// Verify the created keg contains the example contents.
	// Expect a nodes index with the zero node and a zero node README/meta.
	nodes := fx.MustReadJailFile("dex/nodes.tsv")
	require.Contains(t,
		string(nodes),
		"0\t",
		"nodes index should contain zero node",
	)

	readme := fx.MustReadJailFile("0/README.md")
	require.Contains(t, string(readme),
		"Sorry, planned but not yet available",
		"zero node README should contain placeholder text",
	)

	meta := fx.MustReadJailFile("0/meta.yaml")
	require.Contains(t,
		string(meta),
		"title: Sorry, planned but not yet available",
		"zero node meta should include the placeholder title",
	)
}
