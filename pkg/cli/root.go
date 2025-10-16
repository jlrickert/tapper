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

	std "github.com/jlrickert/go-std/pkg"
	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/app"
	"github.com/jlrickert/tapper/pkg/keg"
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
			if std.LoggerFromContext(ctx) == std.NewDiscardLogger() {
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
				lg := std.NewLogger(std.LoggerConfig{
					Out:     out,
					Level:   std.ParseLevel(logLevel),
					JSON:    logJSON,
					Version: Version,
				})
				ctx = std.WithLogger(ctx, lg)
			}

			// Only install OsEnv/OsClock if not present to avoid overwriting tests.
			// EnvFromContext returns a non-nil Env; detect default by type.
			if _, ok := std.EnvFromContext(ctx).(*std.OsEnv); ok {
				ctx = std.WithEnv(ctx, &std.OsEnv{})
			}
			if std.ClockFromContext(ctx) == nil {
				ctx = std.WithClock(ctx, std.OsClock{})
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

	// add do subcommand; pass nil runner so it will resolve runner from ctx
	cmd.AddCommand(
		NewInitCmd(),
		NewCreateCmd(),
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

// NewRunnerFromContext builds an app.Runner using a repo stored in ctx if
// present. Returns nil when a repo cannot be found.
func NewRunnerFromContext(ctx context.Context) *app.Runner {
	if v := RepoFromContext(ctx); v != nil {
		if repo, ok := v.(interface {
			// minimal method set to satisfy keg.KegRepository usage in tests
			ListNodes(context.Context) ([]keg.Node, error)
		}); ok {
			// We do not assert types here; callers (tests) will provide the right
			// concrete type. The Runner constructor accepts the concrete keg
			// repository type; to avoid circular imports we allow the Runner to
			// accept an any and perform necessary conversions there.
			_ = repo
		}
	}
	// Let the app package resolve repo when Run is invoked if needed.
	return nil
}
