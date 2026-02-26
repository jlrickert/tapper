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
//	tap repo config --template
//	tap repo config --template --project
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
Use '--template' to print starter config with defaultKeg, fallbackKeg, and kegSearchPaths.

Deprecation notes:
- 'userRepoPath' is deprecated; use 'kegSearchPaths'.`,
		RunE: func(cmd *cobra.Command, args []string) error {
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
	cmd.Flags().BoolVar(&opts.Template, "template", false, "display template configuration (includes defaultKeg, fallbackKeg, kegSearchPaths)")

	cmd.AddCommand(NewRepoConfigEditCmd(deps))

	return cmd
}
