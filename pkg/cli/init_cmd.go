package cli

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/app"
	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/spf13/cobra"
)

// NewInitCmd returns the `keg init` cobra command.
//
// Usage examples:
//
//	keg init NAME
//	keg init mykeg --type local
//	keg init blog --path ./kegs/blog --title "Blog" --creator "me"
func NewInitCmd() *cobra.Command {
	initOpts := &app.InitOptions{}

	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "create a new keg target",
		// No-op persistent pre run used for symmetry with other commands.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {},
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("NAME is required: %w", keg.ErrInvalid)
			}
			name := args[0]

			if initOpts.Type != "" && !slices.Contains([]string{"registry", "local", "user"}, initOpts.Type) {
				return fmt.Errorf(
					"%s is not a valid type: %w",
					initOpts.Type, keg.ErrInvalid,
				)
			}

			if name == "." && initOpts.Type != "local" {
				return fmt.Errorf(
					". is only valid for for a local keg type: %w",
					keg.ErrInvalid,
				)
			} else if name == "." && initOpts.Type == "" {

			}

			env := toolkit.EnvFromContext(cmd.Context())
			wd, _ := env.Getwd()
			r := app.Runner{Root: wd}
			switch initOpts.Type {
			case "local":
				if initOpts.Alias == "" {
					initOpts.Alias = filepath.Base(wd)
				}
			case "registry":
				u, _ := kegurl.Parse(initOpts.Name)
				initOpts.User = u.User
				initOpts.Repo = u.Repo
			case "user":
				fallthrough
			default:
				initOpts.Name = name
				if initOpts.Alias == "" {
					initOpts.Alias = name
				}
			}

			err := r.DoInit(cmd.Context(), name, initOpts)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "keg %s created", initOpts.Alias)
			return nil
		},
	}

	cmd.Flags().StringVar(&initOpts.Type, "type", "", "destination type: registry|user|local")
	cmd.Flags().StringVar(&initOpts.Alias, "alias", "", "alias of keg to add to config")
	cmd.Flags().StringVar(&initOpts.Title, "title", "", "human title to write into the keg config")
	cmd.Flags().StringVar(&initOpts.Creator, "creator", "", "creator identifier to include in the keg config")
	cmd.Flags().StringVar(&initOpts.TokenEnv, "token-env", "", "environment variable name to store token reference (API targets)")

	cmd.Flags().BoolVar(&initOpts.AddUserConfig, "add-user-config", true, "add created target to user config automatically")
	cmd.Flags().BoolVar(&initOpts.AddLocalConfig, "add-local-config", true, "add created created target to local config if a project is found")

	// Provide shell completion for the --type flag.
	_ = cmd.RegisterFlagCompletionFunc(
		"type",
		func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
			opts := []string{"registry", "file", "user"}
			return opts, cobra.ShellCompDirectiveNoFileComp
		},
	)

	return cmd
}
