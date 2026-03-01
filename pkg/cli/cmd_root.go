package cli

// NewRootCmd builds the root cobra command, wires persistent flags, and
// initializes services from explicit runtime dependencies.
import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/jlrickert/cli-toolkit/mylog"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

type shutdownKey struct{}

type Deps struct {
	Root     string
	Shutdown func()
	Runtime  *toolkit.Runtime
	Profile  Profile

	KegTargetOptions tapper.KegTargetOptions

	ConfigPath string
	LogFile    string
	LogLevel   string
	LogJSON    bool

	Tap *tapper.Tap
	Err error
}

func NewRootCmd(deps *Deps) *cobra.Command {
	if deps == nil {
		deps = &Deps{}
	}
	deps.Profile = deps.Profile.withDefaults()
	if deps.Shutdown == nil {
		deps.Shutdown = func() {}
	}

	cmd := &cobra.Command{
		Use:           deps.Profile.Use,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Respect an existing context (tests set f.Ctx). Use it as the base.
			ctx := cmd.Context()
			rt := deps.Runtime
			if rt == nil {
				return fmt.Errorf("runtime is required")
			}

			wd, err := rt.Getwd()
			if err != nil {
				return err
			}
			tap, err := tapper.NewTap(tapper.TapOptions{
				Root:       wd,
				ConfigPath: deps.ConfigPath,
				Runtime:    rt,
			})
			if err != nil {
				return err
			}
			deps.Tap = tap
			deps.Root = wd
			if deps.Profile.withDefaults().AllowKegAliasFlags {
				_ = cmd.Root().RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					kegs, _ := deps.Tap.ListKegs(true)
					return kegs, cobra.ShellCompDirectiveNoFileComp
				})
			}

			if deps.ConfigPath != "" {
				_, err := tapper.ReadConfig(deps.Runtime, deps.ConfigPath)
				deps.Err = err
			}

			if deps.LogFile != "" || deps.LogJSON || deps.LogLevel != "" {
				// create a logger out-> stderr or file
				var out = os.Stderr
				var f *os.File
				if deps.LogFile != "" {
					var err error
					f, err = os.OpenFile(deps.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
					if err != nil {
						return err
					}
					out = f
				}
				lg := mylog.NewLogger(mylog.LoggerConfig{
					Out:     out,
					Level:   mylog.ParseLevel(deps.LogLevel),
					JSON:    deps.LogJSON,
					Version: Version,
				})
				if err := deps.Runtime.SetLogger(lg); err != nil {
					return err
				}
			}

			cmd.SetContext(ctx)
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			// invoke shutdown if present
			if v := cmd.Context().Value(shutdownKey{}); v != nil {
				if sd, ok := v.(func()); ok && sd != nil {
					sd()
				}
			}
			return nil
		},
		//RunE: func(cmd *cobra.Command, args []string) error {
		//	_, err := fmt.Fprint(cmd.OutOrStdout(), "test")
		//	return err
		//},
	}

	cmd.PersistentFlags().StringVar(&deps.LogFile, "log-file", "", "write logs to file (default stderr)")
	cmd.PersistentFlags().StringVar(&deps.LogLevel, "log-level", "info", "minimum log level")
	cmd.PersistentFlags().BoolVar(&deps.LogJSON, "log-json", false, "output logs as JSON")
	cmd.PersistentFlags().StringVarP(&deps.ConfigPath, "config", "c", "", "path to config file")
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		cmd.PersistentFlags().StringVarP(&deps.KegTargetOptions.Keg, "keg", "k", "", "alias of the keg to use")
		cmd.PersistentFlags().BoolVar(&deps.KegTargetOptions.Project, "project", false, "resolve against the project-local keg")
		cmd.PersistentFlags().StringVar(&deps.KegTargetOptions.Path, "path", "", "explicit project path to resolve a local keg")
		cmd.PersistentFlags().BoolVar(&deps.KegTargetOptions.Cwd, "cwd", false, "with --project, use cwd instead of git root")
	}

	subcommands := []*cobra.Command{
		NewBacklinksCmd(deps),
		NewCatCmd(deps),
		NewCreateCmd(deps),
		NewEditCmd(deps),
		NewArchiveCmd(deps),
		NewFileCmd(deps),
		NewGraphCmd(deps),
		NewGrepCmd(deps),
		NewImageCmd(deps),
		NewImportCmd(deps),
		NewIndexCmd(deps),
		NewInfoCmd(deps),
		NewListCmd(deps),
		NewMetaCmd(deps),
		NewMoveCmd(deps),
		NewSnapshotCmd(deps),
		NewPwdCmd(deps),
		NewReindexCmd(deps),
		NewRemoveCmd(deps),
		NewStatsCmd(deps),
		NewTagsCmd(deps),
	}
	if deps.Profile.IncludeConfigCommand {
		subcommands = append(subcommands, NewConfigCmd(deps))
	}
	var repoCmd *cobra.Command
	if deps.Profile.IncludeRepoCommand {
		repoCmd = NewRepoCmd(deps)
		subcommands = append(subcommands, repoCmd)
	}
	cmd.AddCommand(subcommands...)
	if repoCmd != nil {
		filterRepoTargetFlagsInHelp(repoCmd)
	}

	return cmd
}

func filterRepoTargetFlagsInHelp(cmd *cobra.Command) {
	original := cmd.HelpFunc()
	cmd.SetHelpFunc(func(c *cobra.Command, args []string) {
		var buf bytes.Buffer
		out := c.OutOrStdout()
		errOut := c.ErrOrStderr()
		c.SetOut(&buf)
		c.SetErr(&buf)
		original(c, args)
		c.SetOut(out)
		c.SetErr(errOut)
		_, _ = fmt.Fprint(out, stripRepoTargetFlagsFromGlobalHelp(buf.String()))
	})
	for _, child := range cmd.Commands() {
		filterRepoTargetFlagsInHelp(child)
	}
}

func stripRepoTargetFlagsFromGlobalHelp(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	inGlobal := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "Global Flags:":
			inGlobal = true
			out = append(out, line)
		case inGlobal && trimmed == "":
			inGlobal = false
			out = append(out, line)
		case inGlobal && isRepoTargetFlagHelpLine(trimmed):
			continue
		default:
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func isRepoTargetFlagHelpLine(line string) bool {
	return strings.Contains(line, "--keg") ||
		strings.Contains(line, "--project") ||
		strings.Contains(line, "--path") ||
		strings.Contains(line, "--cwd")
}
