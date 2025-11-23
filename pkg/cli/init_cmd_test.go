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
		".",
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

func TestInitUserKeg(t *testing.T) {
	fx := NewSandbox(t, sandbox.WithFixture("testuser", "~"))
	h := NewProcess(t, false,
		"init",
		"public",
		"--type", "user",
		"--alias", "public",
		"--creator", "testcreator",
	)

	res := h.Run(fx.Context())
	require.NoError(t, res.Err, "init user keg command should succeed")
	fx.DumpFileContent("~/.config/tapper/config.yaml")
	fx.DumpJailTree(0)

	require.Contains(t, string(res.Stdout), "keg public created",
		"unexpected output: %q", string(res.Stdout),
	)

	// stderr should be empty for a successful run.
	require.Equal(t, "", string(res.Stderr))

	// Verify the created keg exists in the userRepoPath (.local/share/tapper/kegs).
	// The keg should be created at .local/share/tapper/kegs/public
	nodes := fx.MustReadFile(".local/share/tapper/kegs/public/dex/nodes.tsv")
	require.Contains(t,
		string(nodes),
		"0\t",
		"nodes index should contain zero node",
	)

	readme := fx.MustReadFile(".local/share/tapper/kegs/public/0/README.md")
	require.Contains(t, string(readme),
		"Sorry, planned but not yet available",
		"zero node README should contain placeholder text",
	)

	meta := fx.MustReadFile(".local/share/tapper/kegs/public/0/meta.yaml")
	require.Contains(t,
		string(meta),
		"title: Sorry, planned but not yet available",
		"zero node meta should include the placeholder title",
	)

	// Verify the user config was updated with the new keg
	userConfig := fx.MustReadFile(".config/tapper/config.yaml")
	require.Contains(t,
		string(userConfig),
		"public:",
		"user config should contain the new keg alias",
	)
}
