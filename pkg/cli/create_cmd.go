package cli

import (
	"fmt"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewCreateCmd constructs the `create` subcommand.
//
// Usage examples:
//
//	tap create --title "My note" --lead "one-line summary"
//	tap create --title "Note" --tags tag1 --tags tag2 --attrs foo=bar --attrs x=1
func NewCreateCmd() *cobra.Command {
	var opts tapper.CreateOptions

	cmd := &cobra.Command{
		Use:     "create",
		Short:   "create a new node in the current keg",
		Aliases: []string{"c"},
		RunE: func(cmd *cobra.Command, args []string) error {
			stream := toolkit.StreamFromContext(cmd.Context())
			opts.Stream = stream
			ctx := cmd.Context()

			tap, err := tapper.NewTap(ctx)
			if err != nil {
				return err
			}
			node, err := tap.Create(cmd.Context(), opts)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "%s", node.Path())
			return nil
		},
	}

	cmd.Flags().StringVar(&opts.Title, "title", "", "title for the new node")
	cmd.Flags().StringVar(&opts.Lead, "lead", "", "lead/short summary for the new node")
	cmd.Flags().StringSliceVar(&opts.Tags, "tags", nil, "tags to apply to the node (repeatable)")
	cmd.Flags().StringToStringVar(
		&opts.Attrs, "attrs", nil,
		"attributes as key=value pairs (repeatable)",
	)

	return cmd
}
