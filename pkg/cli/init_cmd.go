package cli

import (
	"fmt"
	"path/filepath"
	"slices"

	std "github.com/jlrickert/go-std/pkg"
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
	var flagType string

	var flagTitle string
	var flagAlias string
	var flagCreator string
	var flagTokenEnv string

	var flagAddConfig bool
	var flagNoConfig bool

	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "create a new keg target",
		// No-op persistent pre run used for symmetry with other commands.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {},
		RunE: func(cmd *cobra.Command, args []string) error {
			var name string
			if len(args) == 0 {
				name = "."
			} else if len(args) > 0 {
				name = args[0]
			}
			t := "registry"
			if flagType != "" {
				if !slices.Contains([]string{"registry", "local", "file"}, flagType) {
					return fmt.Errorf(
						"%s is not a valid type: %w",
						flagType, keg.ErrInvalid,
					)
				}
				t = flagType
			}
			env := std.EnvFromContext(cmd.Context())
			wd, _ := env.Getwd()
			r := app.Runner{Root: wd}
			err := r.Init(cmd.Context(), name, &app.InitOptions{
				Type:           t,
				Creator:        flagCreator,
				Title:          flagTitle,
				Alias:          flagAlias,
				AddUserConfig:  flagAddConfig,
				AddLocalConfig: flagAddConfig,
			})
			if err != nil {
				return err
			}
			alias := flagAlias
			if alias == "" && name == "." {
				alias = filepath.Base(filepath.Dir(r.Root))
			} else if alias == "" && t == "local" && name != "." {
				alias = name
			}
			fmt.Fprintf(cmd.OutOrStdout(), "keg %s created", flagAlias)
			return nil
		},
	}

	cmd.Flags().StringVar(&flagType, "type", "registry", "destination type: registry|file|local")

	cmd.Flags().StringVar(&flagTitle, "alias", "", "alias of keg to add to config")
	cmd.Flags().StringVar(&flagAlias, "title", "", "human title to write into the keg config")
	cmd.Flags().StringVar(&flagCreator, "creator", "", "creator identifier to include in the keg config")
	cmd.Flags().StringVar(&flagTokenEnv, "token-env", "", "environment variable name to store token reference (API targets)")

	cmd.Flags().BoolVar(&flagAddConfig, "add-config", true, "add created target to user config automatically")
	cmd.Flags().BoolVar(&flagNoConfig, "no-config", false, "do not add created target to user config")

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
