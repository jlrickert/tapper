package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

func NewMetaCmd(deps *Deps) *cobra.Command {
	var opts tapper.MetaOptions

	cmd := &cobra.Command{
		Use:   "meta NODE_ID",
		Short: "print or edit node metadata",
		Long: `Print node metadata (meta.yaml) for NODE_ID.

If stdin is piped, the piped yaml replaces metadata after validation.
Use --edit to edit metadata in a temporary file with your editor.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.NodeID = args[0]
			opts.Stream = deps.Runtime.Stream()
			applyKegTargetProfile(deps, &opts.KegTargetOptions)

			output, err := deps.Tap.Meta(cmd.Context(), opts)
			if err != nil {
				return err
			}
			if output == "" {
				return nil
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	cmd.Flags().BoolVar(&opts.Edit, "edit", false, "edit node metadata in a temporary file")
	bindKegTargetFlags(cmd, deps, &opts.KegTargetOptions, "alias of the keg to read from")

	return cmd
}
