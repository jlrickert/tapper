package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

// NewInitCmd returns the `tap repo init` cobra command.
//
// Usage examples:
//
//	tap repo init NAME
//	tap repo init . --project
//	tap repo init blog --registry --repo knut --namespace me
//	tap repo init blog --project --path ./kegs/blog --title "Blog" --creator "me"
func NewInitCmd(deps *Deps) *cobra.Command {
	initOpts := tapper.InitOptions{}

	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "create a new keg target",
		Long: strings.TrimSpace(`
Create a keg target and initialize it in one of three destinations:

1. user (default)
   Creates a filesystem-backed keg under your first configured kegSearchPaths entry and
   writes/updates the alias in user config.

2. project (--project)
   Creates a project-local keg. By default this resolves to
   <project>/kegs/<alias>,
   where <project> is the git root when available. Use --cwd to base it on the
   current working directory instead. Use --path to set an explicit location.

3. registry (--registry)
   Creates a registry/API keg target and stores it in config without creating
   local keg files.

Alias and name behavior:
- --keg sets the alias written to config.
- If --keg is omitted, alias is inferred from NAME (or cwd basename when NAME=".").
- In user mode, NAME selects the directory name under the first kegSearchPaths entry.

Metadata:
- --title and --creator are written into the keg config for filesystem-backed kegs.
`),
		Example: strings.TrimSpace(`
tap repo init blog
tap repo init . --project
tap repo init . --project --cwd
tap repo init blog --project --path ./kegs/blog
tap repo init blog --user --keg myblog
tap repo init blog --registry --repo knut --namespace me
`),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("NAME is required: %w", keg.ErrInvalid)
			}
			name := args[0]

			if initOpts.Project {
				if initOpts.Path == "" && name != "." {
					initOpts.Path = name
				}
			}
			if initOpts.User && strings.TrimSpace(initOpts.Name) == "" {
				initOpts.Name = name
			}
			if strings.TrimSpace(initOpts.Keg) == "" {
				if name == "." {
					cwd, err := deps.Runtime.Getwd()
					if err != nil {
						return fmt.Errorf("unable to determine working directory for alias inference: %w", err)
					}
					initOpts.Keg = filepath.Base(cwd)
				} else {
					initOpts.Keg = filepath.Base(name)
				}
			}

			err := deps.Tap.InitKeg(cmd.Context(), name, initOpts)
			if err != nil {
				return err
			}

			_, err = fmt.Fprintf(cmd.OutOrStdout(), "keg %s created", initOpts.Keg)
			return err
		},
	}

	cmd.Flags().BoolVar(&initOpts.Project, "project", false, "create a project-local keg")
	cmd.Flags().BoolVar(&initOpts.User, "user", false, "create a user keg under the first configured kegSearchPaths entry")
	cmd.Flags().BoolVar(&initOpts.Registry, "registry", false, "create a registry target")
	cmd.Flags().BoolVar(&initOpts.Cwd, "cwd", false, "with --project, use cwd instead of git root")
	cmd.Flags().StringVar(&initOpts.Path, "path", "", "explicit local destination path (project destination)")
	cmd.Flags().StringVar(&initOpts.Repo, "repo", "", "registry name to use with --registry")
	cmd.Flags().StringVar(&initOpts.UserName, "namespace", "", "registry namespace/user to use with --registry")
	cmd.Flags().StringVarP(&initOpts.Keg, "keg", "k", "", "alias of keg to add to config")
	cmd.Flags().StringVar(&initOpts.Title, "title", "", "human title to write into the keg config")
	cmd.Flags().StringVar(&initOpts.Creator, "creator", "", "creator identifier to include in the keg config")
	cmd.Flags().StringVar(&initOpts.TokenEnv, "token-env", "", "environment variable name to store token reference (API targets)")

	return cmd
}
