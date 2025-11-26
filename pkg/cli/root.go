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
	"github.com/spf13/cobra"
)

type shutdownKey struct{}

func NewRootCmd() *cobra.Command {
	var logFile string
	var logLevel string
	var logJSON bool

	cmd := &cobra.Command{
		Use: "tap",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Respect an existing context (tests set f.Ctx). Use it as the base.
			ctx := cmd.Context()

			// Only install a logger if the context does not already contain one.
			if mylog.LoggerFromContext(ctx) == mylog.DefaultLogger {
				// create logger out -> stderr or file
				var out = os.Stderr
				var f *os.File
				if logFile != "" {
					var err error
					f, err = os.OpenFile(logFile,
						os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
					if err != nil {
						return err
					}
					out = f
				}
				lg := mylog.NewLogger(mylog.LoggerConfig{
					Out:     out,
					Level:   mylog.ParseLevel(logLevel),
					JSON:    logJSON,
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

	cmd.PersistentFlags().StringVar(&logFile, "log-file", "",
		"write logs to file (default stderr)")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info",
		"minimum log level")
	cmd.PersistentFlags().BoolVar(&logJSON, "log-json", false,
		"output logs as JSON")

	// add subcommands; pass nil runner so it will resolve runner from ctx
	cmd.AddCommand(
		NewInitCmd(),
		NewCreateCmd(),
		NewCatCmd(),
	)

	return cmd
}

// Context key helpers for attaching a repo so commands can create runners from
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
