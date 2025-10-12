package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	std "github.com/jlrickert/go-std/pkg"
	kegurl "github.com/jlrickert/tapper/pkg/keg_url"
)

// NewInitCmd returns the `keg init` cobra command.
//
// Usage examples:
//
//	keg init NAME
//	keg init mykeg --local
//	keg init blog --path ./kegs/blog --title "Blog" --creator "me"
func NewInitCmd() *cobra.Command {
	var flagAPI bool
	var flagFile bool
	var flagLocal bool
	var flagPath string
	var flagUser string
	var flagRepo string
	var flagTitle string
	var flagURL string
	var flagCreator string
	var flagTokenEnv string
	var flagAlias string
	var flagAddConfig bool
	var flagNoConfig bool
	var flagYes bool
	var flagDryRun bool
	var flagQuiet bool

	cmd := &cobra.Command{
		Use:   "init NAME",
		Short: "create a new keg target",
		// No-op persistent pre run used for symmetry with other commands.
		PersistentPreRun: func(cmd *cobra.Command, args []string) {},
		RunE: func(cmd *cobra.Command, args []string) error {
			// Basic validation
			if len(args) == 0 {
				return fmt.Errorf("missing NAME argument")
			}
			name := args[0]

			ctx := cmd.Context()

			// Resolve alias default
			alias := flagAlias
			if alias == "" {
				alias = name
			}

			// Determine user default from env if not provided.
			if flagUser == "" {
				env := std.EnvFromContext(ctx)
				if u, err := env.GetUser(); err == nil && u != "" {
					flagUser = u
				}
			}

			// Decide destination mode with precedence:
			// --path > --local > --file > default (api)
			var kt kegurl.Target
			var destDesc string
			var destPath string

			// path explicit
			if flagPath != "" {
				expanded, err := std.ExpandPath(ctx, flagPath)
				if err != nil {
					return fmt.Errorf("expand path: %w", err)
				}
				// If path looks like a directory, write a "keg" file inside.
				base := filepath.Base(expanded)
				if base == "keg" || filepath.Ext(expanded) != "" {
					destPath = expanded
				} else {
					destPath = filepath.Join(expanded, "keg")
				}
				kt = kegurl.NewFile(destPath)
				destDesc = destPath
			} else if flagLocal {
				// local under cwd
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("getwd: %w", err)
				}
				destPath = filepath.Join(cwd, "kegs", alias, "keg")
				kt = kegurl.NewFile(destPath)
				destDesc = destPath
			} else if flagFile {
				// per-user data path
				base, err := std.UserDataPath(ctx)
				if err != nil {
					return fmt.Errorf("user data path: %w", err)
				}
				if flagUser == "" {
					flagUser = "unknown"
				}
				destPath = filepath.Join(base, "kegs", "@"+flagUser, alias, "keg")
				kt = kegurl.NewFile(destPath)
				destDesc = destPath
			} else {
				// default to API target
				kt = kegurl.NewApi(flagRepo, flagUser, name)
				// prefer tokenEnv field if provided
				if flagTokenEnv != "" {
					kt.TokenEnv = flagTokenEnv
				}
				destDesc = fmt.Sprintf("user config (alias %s)", alias)
			}

			// Populate simple metadata on the target where applicable.
			if flagURL != "" {
				kt.Url = flagURL
			}
			if flagCreator != "" {
				kt.User = flagCreator
			}

			// Attempt to expand any file paths in the target (no side effects).
			_ = kt.Expand(ctx)

			// Decide whether to add to config
			addConfig := flagAddConfig && !flagNoConfig

			// Build stubbed payload to report. Use the target String() as a
			// reasonable stand-in for serialized bytes.
			data := []byte(kt.String())

			// Report action unless quiet.
			if !flagQuiet {
				if flagDryRun {
					fmt.Fprintf(cmd.OutOrStdout(),
						"dry-run: would write %d bytes to %s\n",
						len(data), destDesc)
					if addConfig {
						fmt.Fprintf(cmd.OutOrStdout(),
							"dry-run: would add alias %q to user config\n", alias)
					}
				} else {
					// If not confirmed and an operation might overwrite config or
					// files, respect -y/--yes. For the stub we only report.
					if !flagYes {
						fmt.Fprintf(cmd.OutOrStdout(),
							"stub: no changes made. Use --yes to proceed.\n")
					} else {
						fmt.Fprintf(cmd.OutOrStdout(),
							"stub: would write %d bytes to %s\n",
							len(data), destDesc)
						if addConfig {
							fmt.Fprintf(cmd.OutOrStdout(),
								"stub: would add alias %q to user config\n", alias)
						}
					}
				}
			}

			// Side effects intentionally omitted. Return success.
			_ = ctx
			_ = data
			_ = destPath

			return nil
		},
	}

	cmd.Flags().BoolVar(&flagAPI, "api", false,
		"explicitly create an API/registry target (default when no selector given)")
	cmd.Flags().BoolVar(&flagFile, "file", false,
		"create a file-backed keg under the per-user data path")
	cmd.Flags().BoolVar(&flagLocal, "local", false,
		"create a project-local keg under the current working directory")
	cmd.Flags().StringVar(&flagPath, "path", "",
		"explicit destination path or directory for the new keg file")
	cmd.Flags().StringVar(&flagUser, "user", "",
		"user/namespace for registry or file path")
	cmd.Flags().StringVar(&flagRepo, "repo", "",
		"registry name when using API style")
	cmd.Flags().StringVar(&flagTitle, "title", "",
		"human title to write into the keg config")
	cmd.Flags().StringVar(&flagURL, "url", "",
		"url to include in the keg config")
	cmd.Flags().StringVar(&flagCreator, "creator", "",
		"creator identifier to include in the keg config")
	cmd.Flags().StringVar(&flagTokenEnv, "token-env", "",
		"environment variable name to store token reference (API targets)")
	cmd.Flags().StringVar(&flagAlias, "alias", "",
		"alias to write into user config (default = NAME)")
	cmd.Flags().BoolVar(&flagAddConfig, "add-config", true,
		"add created target to user config automatically")
	cmd.Flags().BoolVar(&flagNoConfig, "no-config", false,
		"do not add created target to user config")
	cmd.Flags().BoolVarP(&flagYes, "yes", "y", false,
		"skip confirmations")
	cmd.Flags().BoolVar(&flagDryRun, "dry-run", false,
		"show what would be created without writing")
	cmd.Flags().BoolVar(&flagQuiet, "quiet", false,
		"suppress non-error output")

	return cmd
}
