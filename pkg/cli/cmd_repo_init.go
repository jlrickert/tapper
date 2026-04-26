package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInitCmd returns the `tap repo init` cobra command.
//
// Usage examples:
//
//	tap repo init --keg blog
//	tap repo init --project
//	tap repo init --keg blog --cwd
//	tap repo init --keg blog --hub knut --namespace me
//	tap repo init --keg blog --path ./kegs/blog --title "Blog" --creator "me"
func NewInitCmd(deps *Deps) *cobra.Command {
	initOpts := tapper.InitOptions{}

	cmd := &cobra.Command{
		Use:   "init",
		Short: "create a new keg target",
		Args:  cobra.NoArgs,
		Long: strings.TrimSpace(`
Create a keg target and initialize it in one of three destinations:

1. user (default)
   Creates a filesystem-backed keg under your first configured kegSearchPaths entry and
   writes/updates the alias in user config.

2. local (--project, --cwd, or --path)
   Creates a local filesystem-backed keg. By default this resolves to
   <project>/kegs/<alias>,
   where <project> is the git root when available. Use --cwd to base it on the
   current working directory instead, or use --path to set an explicit
   location. --path implies a local destination even when --project is not
   passed.

3. hub (--hub <name>)
   Creates a hub/API keg target named <name> and stores it in config without
   creating local keg files. The hub name is required when --hub is used.

Alias behavior:
- --keg sets the alias written to config and the directory name.
- If --keg is omitted, alias is inferred from the current working directory basename.

Metadata:
- --title and --creator are written into the keg config for filesystem-backed kegs.
`),
		Example: strings.TrimSpace(`
tap repo init --keg blog
tap repo init --project --cwd
tap repo init --keg blog --cwd
tap repo init --keg blog --path ./kegs/blog
tap repo init --keg blog --user
tap repo init --keg blog --hub knut --namespace me
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(initOpts.Keg) == "" {
				cwd, err := deps.Runtime.Getwd()
				if err != nil {
					return fmt.Errorf("unable to determine working directory for alias inference: %w", err)
				}
				initOpts.Keg = filepath.Base(cwd)
			}

			target, err := deps.Tap.InitKeg(cmd.Context(), initOpts)
			if err != nil {
				return err
			}

			if initOpts.LocalDestination() && target != nil {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "keg %s created at %s", initOpts.Keg, target.Path())
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "keg %s created", initOpts.Keg)
			return err
		},
	}

	cmd.Flags().BoolVar(&initOpts.Project, "project", false, "create a project-local keg")
	cmd.Flags().BoolVar(&initOpts.User, "user", false, "create a user keg under the first configured kegSearchPaths entry")
	cmd.Flags().StringVar(&initOpts.Hub, "hub", "", "hub name (selects API-style hub target when set)")
	cmd.Flags().BoolVar(&initOpts.Cwd, "cwd", false, "use cwd instead of git root for local destination resolution")
	cmd.Flags().StringVar(&initOpts.Path, "path", "", "explicit local destination path; implies local mode")
	cmd.Flags().StringVar(&initOpts.UserName, "namespace", "", "hub namespace/user to use with --hub")
	cmd.Flags().StringVarP(&initOpts.Keg, "keg", "k", "", "alias of keg to add to config")
	cmd.Flags().StringVar(&initOpts.Title, "title", "", "human title to write into the keg config")
	cmd.Flags().StringVar(&initOpts.Creator, "creator", "", "creator identifier to include in the keg config")
	cmd.Flags().StringVar(&initOpts.TokenEnv, "token-env", "", "environment variable name to store token reference (API targets)")

	return cmd
}
