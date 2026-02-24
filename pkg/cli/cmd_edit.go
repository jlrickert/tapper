package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewEditCmd returns the `edit` cobra command.
func NewEditCmd(deps *Deps) *cobra.Command {
	var opts tapper.EditOptions

	cmd := &cobra.Command{
		Use:     "edit NODE_ID",
		Aliases: []string{"e"},
		Short:   "edit a node using a temporary markdown file",
		Long: `Edit a node in a temporary markdown file.

If the file includes YAML frontmatter, it is written to meta.yaml.
The remaining markdown body is written to the node content file.
If stdin is piped with non-empty content, it is applied directly and no editor
is launched.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Stream = deps.Runtime.Stream()
			applyKegTargetProfile(deps, &opts.KegTargetOptions)
			return deps.Tap.Edit(cmd.Context(), opts)
		},
	}

	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to edit from")

	return cmd
}
