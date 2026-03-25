package cli

// NewRootCmd builds the root cobra command, wires persistent flags, and
// initializes services from explicit runtime dependencies.
import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

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

	// logFileHandle is the opened log file; closed in PersistentPostRunE.
	logFileHandle *os.File

	// startTime records when PersistentPreRunE began, used for CLI
	// invocation duration logging in logCLIInvocation.
	startTime time.Time
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
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Respect an existing context (tests set f.Ctx). Use it as the base.
			ctx := cmd.Context()
			rt := deps.Runtime
			if rt == nil {
				return fmt.Errorf("runtime is required")
			}

			// Record invocation start time for duration logging.
			deps.startTime = rt.Clock().Now()

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

			// Fall back to config values when CLI flags are not
			// explicitly set.  Precedence: CLI flag > config > default.
			cfg, cfgErr := tap.ConfigService.Config(true)
			if cfgErr == nil && cfg != nil {
				if !cmd.Flags().Changed("log-file") {
					if v := cfg.LogFile(); v != "" {
						if expanded, err := toolkit.ExpandPath(rt, v); err == nil {
							deps.LogFile = expanded
						} else {
							deps.LogFile = v
						}
					}
				}
				if !cmd.Flags().Changed("log-level") {
					if v := cfg.LogLevel(); v != "" {
						deps.LogLevel = v
					}
				}
			}

			if deps.Profile.withDefaults().AllowKegAliasFlags {
				if regErr := cmd.Root().RegisterFlagCompletionFunc("keg", func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
					return listKegsFiltered(deps, cmd.Context(), toComplete), cobra.ShellCompDirectiveNoFileComp
				}); regErr != nil {
					return fmt.Errorf("failed to register --keg completion: %w", regErr)
				}
			}

			if deps.ConfigPath != "" {
				_, err := tapper.ReadConfig(deps.Runtime, deps.ConfigPath)
				deps.Err = err
			}

			// Auto-detect JSON log format from the log file extension
			// when --log-json was not explicitly set. This lets users
			// configure `logFile: ~/.local/state/tapper/log.json` and
			// get JSON output without a separate flag.
			if !cmd.Flags().Changed("log-json") && deps.LogFile != "" {
				if strings.EqualFold(filepath.Ext(deps.LogFile), ".json") {
					deps.LogJSON = true
				}
			}

			if deps.LogFile != "" || deps.LogJSON || deps.LogLevel != "" {
				// Default log output is the runtime stderr stream.
				var out io.Writer = rt.Stream().Err
				if deps.LogFile != "" {
					// os.OpenFile is required here because the Runtime
					// FileSystem interface does not provide an io.Writer
					// handle for append-mode log output.
					if err := os.MkdirAll(filepath.Dir(deps.LogFile), 0o755); err != nil {
						return err
					}
					f, err := os.OpenFile(deps.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
					if err != nil {
						return err
					}
					deps.logFileHandle = f
					out = io.MultiWriter(f, rt.Stream().Err)
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

			// When no logging is explicitly configured (no --log-file, no
			// config logFile, no --log-level, no --log-json), default to
			// error-level logging on stderr so genuine problems surface in
			// CLI output. The MCP subcommand overrides the logger in its
			// own RunE, so this fallback does not interfere with MCP.
			if deps.LogFile == "" && !deps.LogJSON && deps.LogLevel == "" {
				lg := mylog.NewLogger(mylog.LoggerConfig{
					Out:     rt.Stream().Err,
					Level:   mylog.ParseLevel("error"),
					Version: Version,
				})
				if err := rt.SetLogger(lg); err != nil {
					return err
				}
			}

			cmd.SetContext(ctx)
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			// NOTE: Do NOT close deps.logFileHandle here.
			// RunWithProfile calls logCLIInvocation after
			// ExecuteContext returns (which includes this hook),
			// so the file must remain open for the invocation
			// log entry. RunWithProfile closes the handle after
			// logCLIInvocation completes.

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
	cmd.PersistentFlags().StringVar(&deps.LogLevel, "log-level", "", "minimum log level (default \"error\")")
	cmd.PersistentFlags().BoolVar(&deps.LogJSON, "log-json", false, "output logs as JSON")
	cmd.PersistentFlags().StringVarP(&deps.ConfigPath, "config", "c", "", "path to config file")
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		cmd.PersistentFlags().StringVarP(&deps.KegTargetOptions.Keg, "keg", "k", "", "alias of the keg to use")
		cmd.PersistentFlags().BoolVar(&deps.KegTargetOptions.Project, "project", false, "resolve against the project-local keg")
		cmd.PersistentFlags().StringVar(&deps.KegTargetOptions.Path, "path", "", "explicit project path to resolve a local keg")
		cmd.PersistentFlags().BoolVar(&deps.KegTargetOptions.Cwd, "cwd", false, "resolve project keg at current working directory")
	}

	subcommands := []*cobra.Command{
		NewBacklinksCmd(deps),
		NewCatCmd(deps),
		NewCreateCmd(deps),
		NewDoctorCmd(deps),
		NewDocsCmd(deps),
		NewEditCmd(deps),
		NewArchiveCmd(deps),
		NewFileCmd(deps),
		NewGraphCmd(deps),
		NewGrepCmd(deps),
		NewImageCmd(deps),
		NewImportCmd(deps),
		NewIndexCmd(deps),
		NewInfoCmd(deps),
		NewLinksCmd(deps),
		NewListCmd(deps),
		NewLockCmd(deps),
		NewMcpCmd(deps),
		NewMetaCmd(deps),
		NewMoveCmd(deps),
		NewSnapshotCmd(deps),
		NewPwdCmd(deps),
		NewRemoveCmd(deps),
		NewSiteCmd(deps),
		NewStatsCmd(deps),
		NewTagsCmd(deps),
		NewVersionCmd(deps),
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
