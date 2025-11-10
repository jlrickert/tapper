package cli_test

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInitLocalKeg(t *testing.T) {
	fx := NewSandbox(t)
	h := NewProcess(t, false, "init", "--type", "local", "--title", "myalias", "--creator", "me")

	// Note: the CLI binds the alias into the `--title` flag (see
	// init_cmd.go). To produce the printed alias use --title.
	res := h.Run(fx.Context())
	require.NoError(t, res.Err, "init command should succeed")

	require.Contains(t, string(res.Stdout), "keg myalias created",
		"unexpected output: %q", string(res.Stdout),
	)

	// stderr should be empty for a successful run.
	require.Equal(t, "", string(res.Stderr))

	// Verify the created keg contains the example contents.
	// Expect a nodes index with the zero node and a zero node
	// README/meta.
	nodes := fx.MustReadFile("dex/nodes.tsv")
	require.Contains(t,
		string(nodes),
		"0\t",
		"nodes index should contain zero node",
	)

	readme := fx.MustReadFile("0/README.md")
	require.Contains(t, string(readme),
		"Sorry, planned but not yet available",
		"zero node README should contain placeholder text",
	)

	meta := fx.MustReadFile("0/meta.yaml")
	require.Contains(t,
		string(meta),
		"title: Sorry, planned but not yet available",
		"zero node meta should include the placeholder title",
	)
}
