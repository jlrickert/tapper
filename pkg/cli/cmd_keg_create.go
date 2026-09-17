package cli

import (
	"fmt"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// newKegCreateCmd returns the hub-only `tap keg create` command.
func newKegCreateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create @namespace/keg",
		Short: "create a new KEG on a configured hub",
		Long:  "Create a KEG through the configured Tapper Hub. Filesystem destinations are not supported.",
		Example: strings.TrimSpace(`
tap keg create @acme/notes
tap keg create @acme/engineering --title "Engineering"
tap keg create @alice/notes --hub enterprise
`),
	}
	configureKegCreateCmd(deps, cmd)
	return cmd
}

func configureKegCreateCmd(deps *Deps, cmd *cobra.Command) {
	options := tapper.InitOptions{}
	cmd.ValidArgsFunction = func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	cmd.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 || cmd.Flags().Changed("namespace") || cmd.Flags().Changed("keg") {
			return fmt.Errorf("use tap keg create @namespace/keg with exactly one argument and no --namespace or --keg")
		}
		// The library's reference error names the shape but not the command,
		// since its other callers have no CLI. Add the command back here.
		if _, _, err := tapper.ParseCanonicalKegRef(args[0]); err != nil {
			return fmt.Errorf("%w; use tap keg create @namespace/keg", err)
		}
		return nil
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		options.RequireBootstrap = deps.Profile.withDefaults().IncludeConfigCommand
		options.Keg = args[0]

		target, err := deps.Tap.InitKeg(cmd.Context(), options)
		if err != nil {
			return err
		}
		message := fmt.Sprintf("keg %s created", options.Keg)
		if label := tapper.KegBackendLabel(target); label != "" {
			message += fmt.Sprintf(" (%s)", label)
		}
		if location := tapper.KegLocation(target); location != "" {
			message += " " + location
		}
		_, err = fmt.Fprintln(cmd.OutOrStdout(), message)
		return err
	}

	cmd.Flags().StringVar(&options.Hub, "hub", "", "configured hub name")
	cmd.Flags().StringVar(&options.Title, "title", "", "human-readable KEG title")
	cmd.Flags().StringVar(&options.Visibility, "visibility", "", "KEG visibility: private or public")
}
