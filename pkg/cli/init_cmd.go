package cli

import (
	"fmt"
	"path/filepath"
	"slices"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/app"
	"github.com/jlrickert/tapper/pkg/keg"
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
	// var flagType string
	//
	// var flagTitle string
	// var flagAlias string
	// var flagCreator string
	// var flagTokenEnv string
	//
	// var flagAddConfig bool
	// var flagNoConfig bool

	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "create a new keg target",
		// No-op persistent pre run used for symmetry with other commands.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {},
		Aliases:          []string{"c"},
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) == 0 {
				name = "."
			} else if len(args) > 0 {
				name = args[0]
			}

			env := toolkit.EnvFromContext(cmd.Context())
			wd, _ := env.Getwd()

			if initOpts.Alias == "" {
				if name == "." || name == "" {
					initOpts.Alias = filepath.Base(wd)
				} else {
					initOpts.Alias = name
				}
			}

			if initOpts.Type != "" {
				if !slices.Contains([]string{"registry", "local", "file"}, initOpts.Type) {
					return fmt.Errorf(
						"%s is not a valid type: %w",
						initOpts.Type, keg.ErrInvalid,
					)
				}
			} else {
				if name == "" || name == "." {
					initOpts.Type = "local"
				} else {
					initOpts.Type = "registry"
				}
			}

			r := app.Runner{Root: wd}

			err := r.Init(cmd.Context(), name, initOpts)
			if err != nil {
				return err
			}

			fmt.Fprintf(cmd.OutOrStdout(), "keg %s created", initOpts.Alias)
			return nil
		},
	}

	cmd.Flags().StringVar(&initOpts.Type, "type", "", "destination type: registry|file|local")
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
			opts := []string{"registry", "file", "local"}
			return opts, cobra.ShellCompDirectiveNoFileComp
		},
	)

	return cmd
}
