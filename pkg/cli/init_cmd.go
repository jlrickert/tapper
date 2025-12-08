package cli

import (
	"fmt"
	"path/filepath"

	"github.com/jlrickert/tapper/pkg/keg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
	"github.com/jlrickert/tapper/pkg/tapper"
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
	initOpts := &tapper.InitOptions{}

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

			ctx := cmd.Context()
			r, err := tapper.NewTap(ctx)
			if err != nil {
				return err
			}

			if initOpts.Type == "" {
				if name == "." {
					initOpts.Type = "local"
				} else {
					initOpts.Type = "user"
				}
			}

			switch initOpts.Type {
			case "local":
				if initOpts.Alias == "" && name == "." {
					initOpts.Alias = filepath.Base(r.Root)
				}
				initOpts.Path = name
			case "user":
				initOpts.Name = name
				if name == "." {
					initOpts.Name = filepath.Base(r.Root)
				}

				if initOpts.Alias == "" {
					if name == "." {
						initOpts.Alias = filepath.Base(r.Root)
					} else {
						initOpts.Alias = filepath.Base(name)
					}
				}
			case "registry":
				u, _ := kegurl.Parse(name)
				initOpts.User = u.User
				initOpts.Repo = u.Repo
			default:
				return fmt.Errorf(
					"%s is not a valid type: %w",
					initOpts.Type, keg.ErrInvalid,
				)
			}

			if initOpts.Alias == "" {
				panic("Alias needs to be defined")
			}

			err = r.Init(cmd.Context(), name, initOpts)
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

	cmd.Flags().BoolVar(&initOpts.FlagAddToConfig, "add-user-config", false, "add created target to user config automatically")
	cmd.Flags().BoolVar(&initOpts.FlagNoAddConfig, "no-add-user-config", false, "add created target to user config automatically")
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
