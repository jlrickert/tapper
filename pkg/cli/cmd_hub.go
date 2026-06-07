package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewHubCmd returns the `hub` cobra command group.
//
//	tap hub list            # kegs on the local hub, as @namespace/keg
//	tap hub list --hub atlas
func NewHubCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "inspect hubs",
		Long:  `List the kegs available on a hub.`,
	}
	cmd.AddCommand(newHubListCmd(deps))
	return cmd
}

func newHubListCmd(deps *Deps) *cobra.Command {
	var opts tapper.HubListOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "list kegs on a hub as @namespace/keg",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kegs, err := deps.Tap.HubListKegs(cmd.Context(), opts)
			if err != nil {
				return err
			}
			for _, k := range kegs {
				fmt.Fprintln(cmd.OutOrStdout(), k)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&opts.Hub, "hub", "", "hub to list (default: the local hub)")
	return cmd
}
