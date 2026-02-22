package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInfoCmd returns the `info` cobra command.
//
// Usage examples:
//
//	Tap info
//	Tap info --alias myalias
//	Tap info edit
//	Tap info edit --alias myalias
func NewInfoCmd(deps *Deps) *cobra.Command {
	var opts tapper.InfoOptions

	cmd := &cobra.Command{
		Use:   "info",
		Short: "display keg metadata",
		Long: `Display the keg configuration (keg.yaml).

Shows metadata about the keg including title, creator, state, and other
configuration properties. Use 'Tap info edit' to modify the configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			output, err := deps.Tap.Info(ctx, opts)
			if err != nil {
				return err
			}

			fmt.Fprint(cmd.OutOrStdout(), output)
			return nil
		},
	}

	cmd.Flags().StringVarP(&opts.KegAlias, "keg", "k", "", "alias of the keg to display info for")
	// Backward-compatible alias flag.
	cmd.Flags().StringVar(&opts.KegAlias, "alias", "", "alias of the keg to display info for (deprecated; use --keg)")

	_ = cmd.RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(cmd.Context(), true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})
	_ = cmd.RegisterFlagCompletionFunc("alias", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		kegs, _ := deps.Tap.ListKegs(cmd.Context(), true)
		return kegs, cobra.ShellCompDirectiveNoFileComp
	})

	// Add the edit subcommand
	cmd.AddCommand(NewInfoEditCmd(deps))

	return cmd
}
