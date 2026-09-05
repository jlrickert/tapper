package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewCreateCmd constructs the `create` subcommand.
//
// Usage examples:
//
//	tap create
//	tap create --schema task
//	printf '---\ntype: task\n---\n# My note\n' | tap create
func NewCreateCmd(deps *Deps) *cobra.Command {
	var opts tapper.CreateOptions

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "create a new node in the current keg",
		Aliases: []string{"c"},
		Long: `Create a new node in the current keg.

If stdin is piped with non-empty content, it is used as the node body and no
editor is launched. The content may optionally include YAML frontmatter; if
present, the frontmatter is written to meta.yaml.

Otherwise, on a TTY, an editor is opened on the new node. Everything about the
node is written there: the title is the H1, and metadata is the YAML
frontmatter above it.

--schema preselects the node type, which is prefilled as type: in the editor's
frontmatter and applied when you save. It is required when the keg is strict and
the resolved human validation mode blocks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Stream = deps.Runtime.Stream()
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			node, err := deps.Tap.Create(cmd.Context(), opts)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s", node.Path())
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Schema, "schema", "", "schema to select for this write")

	return cmd
}
