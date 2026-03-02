package cli

import (
	"fmt"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

var repoConfigTemplateKinds = []string{"user", "project"}

// NewRepoConfigTemplateCmd returns the `repo config template` cobra subcommand.
func NewRepoConfigTemplateCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template {user|project}",
		Short: "print a starter tap config template",
		Args:  cobra.ExactArgs(1),
		ValidArgsFunction: func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			if len(args) > 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}
			return filterByPrefix(repoConfigTemplateKinds, toComplete), cobra.ShellCompDirectiveNoFileComp
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			if deps.ConfigPath != "" {
				return fmt.Errorf("--config cannot be used with repo config template")
			}

			var opts tapper.ConfigTemplateOptions
			switch args[0] {
			case "user":
				opts.Project = false
			case "project":
				opts.Project = true
			default:
				return fmt.Errorf("unknown template kind %q (expected user or project)", args[0])
			}

			output, err := deps.Tap.ConfigTemplate(opts)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	return cmd
}
