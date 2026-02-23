package cli

import (
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewRepoConfigEditCmd returns the `repo config edit` cobra subcommand.
//
// Usage examples:
//
//	tap repo config edit
//	tap repo config edit --project
func NewRepoConfigEditCmd(deps *Deps) *cobra.Command {
	var opts tapper.ConfigEditOptions

	cmd := &cobra.Command{
		Use:   "edit",
		Short: "edit tap configuration with default editor",
		Long: `Open the configuration file in your default editor for editing.

By default, edits the user configuration. Use '--project' to edit project
configuration.

The editor is determined by the EDITOR environment variable, defaulting to 'vim'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			if deps.ConfigPath != "" {
				opts.ConfigPath = deps.ConfigPath
			}
			return deps.Tap.ConfigEdit(ctx, opts)
		},
	}

	cmd.Flags().BoolVar(&opts.Project, "project", false, "edit project configuration")
	cmd.Flags().BoolVar(&opts.User, "user", false, "edit user configuration")

	return cmd
}
