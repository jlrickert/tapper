package cli

import (
	"errors"
	"fmt"
	"os"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewConfigCmd returns the `config` cobra command.
//
// Usage examples:
//
//	Tap config
//	Tap config --project
//	Tap config --user
//	Tap config edit
//	Tap config edit --project
func NewConfigCmd(deps *Deps) *cobra.Command {
	var opts tapper.ConfigOptions

	cmd := &cobra.Command{
		Use:   "config",
		Short: "display configuration",
		Long: `Display the merged configuration (user + local).

Use 'Tap config edit' to modify the configuration.
Use '--project' flag to view only local project configuration.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()

			output, err := deps.Tap.Config(ctx, opts)
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
	cmd.Flags().BoolVar(&opts.Template, "template", false, "display template configuration")

	// Add the edit subcommand
	cmd.AddCommand(NewConfigEditCmd(deps))

	return cmd
}
