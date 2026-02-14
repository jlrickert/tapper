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
	"fmt"
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
	Runtime  *toolkit.Runtime

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
	if deps.Shutdown == nil {
		deps.Shutdown = func() {}
	}

	cmd := &cobra.Command{
		Use: "tap",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Respect an existing context (tests set f.Ctx). Use it as the base.
			ctx := cmd.Context()
			rt := deps.Runtime
			if rt == nil {
				return fmt.Errorf("runtime is required")
			}

			wd, err := rt.Env.Getwd()
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

			if deps.ConfigPath != "" {
				_, err := tapper.ReadConfig(ctx, deps.ConfigPath)
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
				deps.Runtime.Logger = lg
			}

			ctx = mylog.WithLogger(ctx, deps.Runtime.Logger)
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

	// add subcommands; pass nil runner so it will resolve runner from ctx
	cmd.AddCommand(
		NewCatCmd(deps),
		NewConfigCmd(deps),
		NewCreateCmd(deps),
		NewIndexCmd(deps),
		NewInfoCmd(deps),
		NewInitCmd(deps),
		NewListCmd(deps),
		NewPwdCmd(deps),
		NewRepoCmd(deps),
	)

	return cmd
}
