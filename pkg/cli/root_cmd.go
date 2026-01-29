package cli

// NewRootCmd builds the root cobra command, wires persistent flags, and
// installs the "do" subcommand. The command's PersistentPreRunE will only
// create a production logger/ctx when the incoming command context does not
// already carry a logger/env/clock (this lets tests set a test context via
// cmd.SetContext(f.Ctx) before Execute).
//
// The new command does not hard-wire an app.Runner; the "do" subcommand will
// resolve a runner from context if one was not provided at construction.
import (
	"context"
	"os"

	"github.com/jlrickert/cli-toolkit/mylog"
	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

type shutdownKey struct{}

type Deps struct {
	Root     string
	Shutdown func()

	ConfigPath string
	LogFile    string
	LogLevel   string
	LogJSON    bool

	Tap *tapper.Tap
	Err error
}

func NewRootCmd() *cobra.Command {
	deps := &Deps{
		Root: "",
		Shutdown: func() {
		},
		ConfigPath: "",
		LogFile:    "",
		LogLevel:   "",
		LogJSON:    false,
		Tap:        nil,
	}

	cmd := &cobra.Command{
		Use: "tap",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Respect an existing context (tests set f.Ctx). Use it as the base.
			ctx := cmd.Context()

			env := toolkit.EnvFromContext(ctx)
			wd, err := env.Getwd()
			if err != nil {
				return err
			}
			tap, err := tapper.NewTap(ctx, tapper.TapOptions{
				Root:       wd,
				ConfigPath: deps.ConfigPath,
			})
			if err != nil {
				return err
			}
			deps.Tap = tap
			deps.Root = wd

			if deps.ConfigPath != "" {
				_, err := tapper.ReadConfig(ctx, deps.ConfigPath)
				deps.Err = err
			}

			// Only install a logger if the context does not already contain one.
			if mylog.LoggerFromContext(ctx) == mylog.DefaultLogger {
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
				ctx = mylog.WithLogger(ctx, lg)
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
	}

	cmd.PersistentFlags().StringVar(&deps.LogFile, "log-file", "", "write logs to file (default stderr)")
	cmd.PersistentFlags().StringVar(&deps.LogLevel, "log-level", "info", "minimum log level")
	cmd.PersistentFlags().BoolVar(&deps.LogJSON, "log-json", false, "output logs as JSON")
	cmd.PersistentFlags().StringVar(&deps.ConfigPath, "config", "", "path to config file")

	// add subcommands; pass nil runner so it will resolve runner from ctx
	cmd.AddCommand(
		NewCatCmd(deps),
		NewConfigCmd(deps),
		NewCreateCmd(deps),
		NewIndexCmd(deps),
		NewInfoCmd(deps),
		NewInitCmd(deps),
		NewRepoCmd(deps),
	)

	return cmd
}

// Api key helpers for attaching a repo so commands can create runners from
// the test fixture context.
type repoKeyType struct{}

// WithRepo returns a context containing the provided keg repo.
func WithRepo(ctx context.Context, r any) context.Context {
	return context.WithValue(ctx, repoKeyType{}, r)
}

// RepoFromContext extracts a keg repo from context if present.
func RepoFromContext(ctx context.Context) any {
	v := ctx.Value(repoKeyType{})
	return v
}
