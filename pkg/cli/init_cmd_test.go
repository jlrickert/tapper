package cli_test

import (
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/stretchr/testify/require"
)

func TestInitLocalKeg(t *testing.T) {
	fx := NewSandbox(t)
	h := NewProcess(t, false,
		"init",
		"--type", "local",
		"--alias", "myalias",
		"--creator", "me",
	)

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

func TestCreateFileTypeRepo(t *testing.T) {
	fx := NewSandbox(t, sandbox.WithFixture("testuser", "~"))
	h := NewProcess(t, false,
		"create",
		"--title", "My Test Node",
		"--lead", "A brief summary of the node",
		"--tags", "tag1",
		"--tags", "tag2",
	)

	res := h.Run(fx.Context())
	require.NoError(t, res.Err, "create command should succeed")

	// Verify successful creation message in stdout
	require.Contains(t, string(res.Stdout), "1",
		"unexpected output: %q", string(res.Stdout),
	)

	// stderr should be empty for a successful run
	require.Equal(t, "", string(res.Stderr))

	// Verify the created node files exist
	meta := fx.MustReadFile("kegs/example/1/meta.yaml")
	require.Contains(t,
		string(meta),
		"title: My Test Node",
		"node meta should include the title",
	)
	require.Contains(t,
		string(meta),
		"tag1",
		"node meta should include tag1",
	)
	require.Contains(t,
		string(meta),
		"tag2",
		"node meta should include tag2",
	)

	readme := fx.MustReadFile("kegs/example/1/README.md")
	require.NotEmpty(t, readme, "node README should be created")
}
