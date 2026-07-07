package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewHubCmd returns the `hub` command group: manage hub *connections* (the
// user-config `hubs:` map) and inspect a hub. Listing the kegs on a hub lives
// under `tap keg list`.
//
//	tap hub list
//	tap hub status
//	tap hub add work --url https://hub.example
//	tap hub remove work
//	tap hub set-default atlas
func NewHubCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hub",
		Short: "manage and inspect hub connections",
		Long: `Manage the configured hub connections and inspect a hub.

'hub list' shows the configured hubs; 'hub status' validates your login;
'hub add'/'hub remove' edit the user-level hub connections; 'hub set-default'
picks the default hub (writing project config by default). To list the kegs on
a hub, use 'tap keg list'.`,
	}
	cmd.AddCommand(
		newHubListCmd(deps),
		newHubStatusCmd(deps),
		newHubAddCmd(deps),
		newHubRemoveCmd(deps),
		newHubSetDefaultCmd(deps),
	)
	return cmd
}

func newHubListCmd(deps *Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "list the configured hubs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			hubs, err := deps.Tap.HubList(cmd.Context())
			if err != nil {
				return err
			}
			for _, h := range hubs {
				marker := " "
				if h.IsDefault {
					marker = "*"
				}
				fmt.Fprintf(cmd.OutOrStdout(), "%s %s\t%s\t%s\t%s\n", marker, h.Name, h.Kind, h.URL, h.Source)
			}
			return nil
		},
	}
}

func newHubStatusCmd(deps *Deps) *cobra.Command {
	var (
		hubURL  string
		offline bool
	)
	cmd := &cobra.Command{
		Use:   "status",
		Short: "show login status for a hub",
		Long: `Report whether a hub has a cached login and validate the stored
token against the hub. With --hub, reports on that specific hub; with no --hub,
reports every stored hub login.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			result, err := deps.Tap.AuthStatus(cmd.Context(), tapper.AuthStatusOptions{Hub: hubURL, Offline: offline})
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), result.Formatted)
			return err
		},
	}
	cmd.Flags().StringVar(&hubURL, "hub", "", "hub base URL to query (omit to show every stored hub)")
	cmd.Flags().BoolVar(&offline, "offline", false, "skip the live hub check and report from the local store only")
	return cmd
}

func newHubAddCmd(deps *Deps) *cobra.Command {
	var opts tapper.HubAddOptions
	cmd := &cobra.Command{
		Use:   "add <name> --url <url>",
		Short: "add a remote hub connection (writes user config)",
		Long: `Register a remote hub connection in the user configuration. Hub
connections may only live in user config, so this always writes there
regardless of the working directory.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			return deps.Tap.HubAdd(cmd.Context(), opts)
		},
	}
	cmd.Flags().StringVar(&opts.URL, "url", "", "hub base URL (required)")
	cmd.Flags().StringVar(&opts.TokenEnv, "token-env", "", "environment variable holding the hub token")
	_ = cmd.MarkFlagRequired("url")
	return cmd
}

func newHubRemoveCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <name>",
		Short: "remove a hub connection (writes user config)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return deps.Tap.HubRemove(cmd.Context(), tapper.HubRemoveOptions{Name: args[0]})
		},
	}
	cmd.ValidArgsFunction = hubNameArgCompletion(deps)
	return cmd
}

func newHubSetDefaultCmd(deps *Deps) *cobra.Command {
	var opts tapper.HubSetDefaultOptions
	cmd := &cobra.Command{
		Use:   "set-default <name>",
		Short: "set the default hub (project config by default; --user for user)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Name = args[0]
			return deps.Tap.HubSetDefault(cmd.Context(), opts)
		},
	}
	cmd.Flags().BoolVar(&opts.User, "user", false, "write the user config instead of the project config")
	cmd.ValidArgsFunction = hubNameArgCompletion(deps)
	return cmd
}

// hubNameArgCompletion completes the first positional argument with the names of
// configured hubs (best-effort, offline). Later args get nothing.
func hubNameArgCompletion(deps *Deps) func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) != 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		tap, err := completionTap(deps)
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		hubs, err := tap.HubList(cmd.Context())
		if err != nil {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		names := make([]string, 0, len(hubs))
		for _, h := range hubs {
			names = append(names, h.Name)
		}
		return filterByPrefix(names, toComplete), cobra.ShellCompDirectiveNoFileComp
	}
}
