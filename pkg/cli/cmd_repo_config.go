package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewRepoConfigCmd returns the `repo config` cobra command.
//
// Usage examples:
//
//	tap repo config
//	tap repo config --project
//	tap repo config --user
//	tap repo config template user
//	tap repo config template project
//	tap repo config edit
//	tap repo config edit --project
func NewRepoConfigCmd(deps *Deps) *cobra.Command {
	var opts tapper.ConfigOptions

	cmd := &cobra.Command{
		Use:   "config",
		Short: "display tap configuration",
		Long: `Display the merged tap configuration (user + project).

Use 'tap repo config edit' to modify configuration files.
Use '--project' to view only project configuration.
Use 'tap repo config template {user|project}' to print starter config.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.ConfigPath = deps.ConfigPath
			output, err := deps.Tap.Config(opts)
			if errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("no configuration available: %w", err)
			}
			if err != nil {
				return err
			}

			_, err = fmt.Fprint(cmd.OutOrStdout(), output)
			return err
		},
	}

	cmd.Flags().BoolVar(&opts.Project, "project", false, "display project configuration")
	cmd.Flags().BoolVar(&opts.User, "user", false, "display user configuration")

	cmd.AddCommand(NewRepoConfigTemplateCmd(deps))
	cmd.AddCommand(NewRepoConfigEditCmd(deps))

	return cmd
}
