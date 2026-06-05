package cli

// NewRootCmd builds the root cobra command, wires persistent flags, and
// initializes services from explicit runtime dependencies.
import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
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
	Strict     bool

	Tap *tapper.Tap
	Err error

	// AuthLoginDeviceFn is the seam through which `tap auth login` drives the
	// RFC 8628 device authorization grant — the single browser-based login
	// flow. Tests set their own function before NewRootCmd runs (via Run's
	// testDepsHook or by constructing Deps directly) so the real browser
	// opener and hub polling are never invoked. A nil value is lazy-defaulted
	// to tapper.AuthLoginDevice in NewRootCmd so production callers need not
	// populate it.
	AuthLoginDeviceFn func(ctx context.Context, rt *toolkit.Runtime, opts tapper.AuthLoginDeviceOptions) (*tapper.AuthEntry, error)

	// AuthValidateTokenFn backs the "paste an authentication token" login
	// path: it checks a pasted bearer token against the hub's whoami probe
	// before the token is written to the AuthStore. Nil is lazy-defaulted to
	// tapper.ValidateToken; tests inject a stub to avoid the network call.
	AuthValidateTokenFn func(ctx context.Context, rt *toolkit.Runtime, hubURL, token string) (*tapper.WhoAmI, error)

	// AuthPrompter renders the interactive hub / method / URL / token prompts
	// for `tap auth login`. Nil is lazy-defaulted to the huh-backed prompter;
	// tests inject a scripted fake so the command logic runs without a TTY.
	AuthPrompter AuthPrompter

	// logFileHandle is the opened log file; closed after invocation logging.
	logFileHandle io.WriteCloser

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
	// Lazy-default: a test that sets its own seam before NewRootCmd wins.
	// We do NOT overwrite a populated value.
	if deps.AuthLoginDeviceFn == nil {
		deps.AuthLoginDeviceFn = tapper.AuthLoginDevice
	}
	if deps.AuthValidateTokenFn == nil {
		deps.AuthValidateTokenFn = tapper.ValidateToken
	}
	if deps.AuthPrompter == nil {
		deps.AuthPrompter = huhAuthPrompter{}
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
			// Route the auth-validation seam onto the Tap so `tap auth
			// status`'s live whoami probe shares the same stub tests inject
			// via Deps.AuthValidateTokenFn (defaulted above to ValidateToken).
			tap.AuthValidateFn = deps.AuthValidateTokenFn
			deps.Tap = tap
			deps.Root = wd

			// Fall back to config values when CLI flags are not
			// explicitly set.  Precedence: CLI flag > config > default.
			cfg, cfgErr := tap.ConfigService.Config(true)

			// Surface config load warnings (corrupt YAML, permission errors).
			if warnings := tap.ConfigService.LoadWarnings; len(warnings) > 0 {
				if deps.Strict {
					var msgs []string
					for _, w := range warnings {
						msgs = append(msgs, w.Message)
					}
					return fmt.Errorf("config errors (--strict): %s", strings.Join(msgs, "; "))
				}
				for _, w := range warnings {
					fmt.Fprintf(rt.Stream().Err, "warning: %s\n", w.Message)
				}
			}

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

			// Build the logger with separate handlers for file and stderr.
			// Stderr always uses text format at error level so CLI users
			// only see genuine problems. The log file (when configured)
			// receives all entries at the requested level and format.
			lg, err := buildCLILogger(rt, deps)
			if err != nil {
				return err
			}
			if err := rt.SetLogger(lg); err != nil {
				return err
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
	cmd.PersistentFlags().BoolVar(&deps.Strict, "strict", false, "treat config warnings as errors")
	mustRegisterFlagCompletion(cmd, "log-level", func(_ *cobra.Command, _ []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		levels := []string{"debug", "info", "warn", "error"}
		var out []string
		for _, l := range levels {
			if strings.HasPrefix(l, strings.ToLower(toComplete)) {
				out = append(out, l)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	})
	if deps.Profile.withDefaults().AllowKegAliasFlags {
		cmd.PersistentFlags().StringVarP(&deps.KegTargetOptions.Keg, "keg", "k", "", "alias of the keg to use")
		cmd.PersistentFlags().BoolVar(&deps.KegTargetOptions.Project, "project", false, "resolve against the project-local keg")
		cmd.PersistentFlags().StringVar(&deps.KegTargetOptions.Path, "path", "", "explicit project path to resolve a local keg")
		cmd.PersistentFlags().BoolVar(&deps.KegTargetOptions.Cwd, "cwd", false, "resolve project keg at current working directory")
		cmd.PersistentFlags().StringVar(&deps.KegTargetOptions.Flight, "flight", "", "flight identifier for cross-keg work scope (mutually exclusive with --keg, --project, --path, --cwd)")
		mustRegisterFlagCompletion(cmd, "flight", func(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
			return nil, cobra.ShellCompDirectiveNoFileComp
		})
		// --flight names a cross-keg work scope and cannot combine
		// with any of the single-keg selectors. Pairwise mutex
		// preserves the existing ability to combine --project with
		// --path / --cwd.
		cmd.MarkFlagsMutuallyExclusive("flight", "keg")
		cmd.MarkFlagsMutuallyExclusive("flight", "project")
		cmd.MarkFlagsMutuallyExclusive("flight", "path")
		cmd.MarkFlagsMutuallyExclusive("flight", "cwd")
	}

	subcommands := []*cobra.Command{
		NewAuthCmd(deps),
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
		NewIntegrateCmd(deps),
		NewLinksCmd(deps),
		NewListCmd(deps),
		NewLockCmd(deps),
		NewMcpCmd(deps),
		NewMetaCmd(deps),
		NewMoveCmd(deps),
		NewOrientCmd(deps),
		NewSnapshotCmd(deps),
		NewRemoveCmd(deps),
		NewSiteCmd(deps),
		NewStatsCmd(deps),
		NewTagsCmd(deps),
		NewVersionCmd(deps),
	}
	if deps.Profile.IncludeConfigCommand {
		subcommands = append(subcommands, NewConfigCmd(deps))
		subcommands = append(subcommands, NewBootstrapCmd(deps))
	}
	var repoCmd *cobra.Command
	var initCmd *cobra.Command
	if deps.Profile.IncludeRepoCommand {
		repoCmd = NewRepoCmd(deps)
		initCmd = NewInitCmd(deps)
		subcommands = append(subcommands, repoCmd, initCmd)
	}
	cmd.AddCommand(subcommands...)
	if repoCmd != nil {
		filterRepoTargetFlagsInHelp(repoCmd)
	}
	// `tap init` re-binds --keg/--project/--path/--cwd locally with
	// create-time semantics. Strip the inherited keg-target persistent
	// flags from its "Global Flags" help section so users don't see two
	// entries for each name.
	if initCmd != nil && deps.Profile.withDefaults().AllowKegAliasFlags {
		filterRepoTargetFlagsInHelp(initCmd)
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

// buildCLILogger constructs the structured logger for CLI commands.
//
// When a log file is configured, two separate handlers are used: the file
// receives entries at the requested level and format, while stderr receives
// only error-level entries in text format. This prevents INFO-level noise
// from reaching CLI users while preserving full logs in the file.
//
// When no log file is configured, stderr is the only destination. The user's
// explicit flags (--log-level, --log-json) apply directly to stderr since
// that is the only place logs can go.
//
// The MCP subcommand overrides the logger in its own RunE, so this function
// only governs normal CLI command logging.
func buildCLILogger(rt *toolkit.Runtime, deps *Deps) (*slog.Logger, error) {
	fileLevel := mylog.ParseLevel(deps.LogLevel)

	if deps.LogFile == "" {
		// No log file — stderr is the only destination.
		// Honor explicit flags for level and format.
		stderrLevel := slog.LevelError
		if deps.LogLevel != "" {
			stderrLevel = fileLevel
		} else if deps.LogJSON {
			// When --log-json is set without --log-level, the user
			// explicitly wants structured output. Default to info.
			stderrLevel = slog.LevelInfo
		}
		var stderrHandler slog.Handler
		if deps.LogJSON {
			stderrHandler = slog.NewJSONHandler(rt.Stream().Err, &slog.HandlerOptions{Level: stderrLevel})
		} else {
			stderrHandler = slog.NewTextHandler(rt.Stream().Err, &slog.HandlerOptions{Level: stderrLevel})
		}
		lg := newLoggerWithAttrs(stderrHandler, Version)
		return lg, nil
	}

	// Open the log file through the Runtime abstraction so sandbox tests
	// can capture log output.
	f, err := rt.OpenFile(deps.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	deps.logFileHandle = f

	// File handler: requested level and format.
	var fileHandler slog.Handler
	if deps.LogJSON {
		fileHandler = slog.NewJSONHandler(f, &slog.HandlerOptions{Level: fileLevel})
	} else {
		fileHandler = slog.NewTextHandler(f, &slog.HandlerOptions{Level: fileLevel})
	}

	// Stderr handler: always text format at error level when a file is
	// present. The file captures everything; stderr shows only problems.
	stderrHandler := slog.NewTextHandler(rt.Stream().Err, &slog.HandlerOptions{
		Level: slog.LevelError,
	})

	// Fan out to both destinations with independent filtering.
	combined := newMultiHandler(fileHandler, stderrHandler)
	lg := newLoggerWithAttrs(combined, Version)
	return lg, nil
}

// newLoggerWithAttrs wraps a handler in a slog.Logger with standard
// per-process attributes (version, host, pid).
func newLoggerWithAttrs(h slog.Handler, version string) *slog.Logger {
	host, _ := os.Hostname()
	pid := os.Getpid()
	return slog.New(h).With(
		slog.String("version", version),
		slog.String("host", host),
		slog.Int("pid", pid),
	)
}
