package cmd

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/spf13/cobra"
)

var (
	// Version may be overridden at build-time with -ldflags "-X main.Version=..."
	Version = "dev"

	rootFlags struct {
		debug bool
		repo  string
	}
)

func NewRootCmd(opts ...CmdOption) *cobra.Command {
	deps := applyCmdOptions(opts...)
	return NewRootCmdFromDeps(deps)
}

// NewRootCmd builds the root cobra.Command for tapper.
// It mirrors the shape and ergonomics used by the keg command set:
// - provides common persistent flags
// - wires a small set of usually-present helper subcommands (version, completion)
// - leaves real subcommands to be added by callers/tests
func NewRootCmdFromDeps(deps *CmdDeps) *cobra.Command {
	root := &cobra.Command{
		Use:     "tapper",
		Short:   "tapper — KEG-oriented repository tooling",
		Long:    "tapper is a small set of tools and helpers for operating on KEG-style repositories.",
		Version: Version,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			// initialize global behavior here (logging, telemetry, repo discovery).
			// keep minimal: callers may inspect rootFlags after flag parsing.
			if rootFlags.debug {
				// For now, just emit a small hint. Real implementations should
				// configure a logger (zap/logrus) according to this flag.
				fmt.Fprintln(deps.Err, "debug: enabled")
			}
		},
	}

	root.SetIn(deps.In)
	root.SetErr(deps.Err)
	root.SetErr(deps.Err)

	// persistent flags available to all subcommands
	flags := root.PersistentFlags()
	flags.BoolVar(&rootFlags.debug, "debug", false, "enable debug output")
	flags.StringVar(&rootFlags.repo, "repo", "", "path to repository (discovered automatically if empty)")

	// version subcommand
	root.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintln(cmd.OutOrStdout(), Version)
		},
	})

	// // completion generator (bash|zsh|fish|powershell)
	// root.AddCommand(&cobra.Command{
	// 	Use:   "completion [bash|zsh|fish|powershell]",
	// 	Short: "Generate shell completion script",
	// 	Args:  cobra.ExactArgs(1),
	// 	RunE: func(cmd *cobra.Command, args []string) error {
	// 		shell := args[0]
	// 		switch shell {
	// 		case "bash":
	// 			return root.GenBashCompletion(os.Stdout)
	// 		case "zsh":
	// 			return root.GenZshCompletion(os.Stdout)
	// 		case "fish":
	// 			// true = include descriptions
	// 			return root.GenFishCompletion(os.Stdout, true)
	// 		case "powershell":
	// 			return root.GenPowerShellCompletionWithDesc(os.Stdout)
	// 		default:
	// 			return fmt.Errorf("unsupported shell: %s", shell)
	// 		}
	// 	},
	// })

	return root
}

// Execute runs the root command using a background context and returns any error.
func Execute() error {
	ctx := context.Background()
	return NewRootCmd().ExecuteContext(ctx)
}

// CreateCmdOption is a functional option for configuring the create command.
type CmdOption func(*CmdDeps)

// createCmdConfig holds injectable dependencies for the create command.
type CmdDeps struct {
	Keg *keg.Keg
	In  io.Reader
	Out io.Writer
	Err io.Writer

	Flags CmdGlobalFlags
}

type CmdGlobalFlags struct {
	CfgFile string
	Verbose bool
}

// WithKeg returns a CreateCmdOption that injects the provided *keg.Keg into the
// create command config. Tests or callers can provide a keg backed by a
// MemoryRepo to exercise real create behavior without touching disk.
func WithKeg(k *keg.Keg) CmdOption {
	return func(c *CmdDeps) {
		c.Keg = k
	}
}

// WithIO returns a CreateCmdOption that sets stdin/stdout/stderr for the create
// command. Passing an io.Writer for errOut allow tests to capture command error
// output separately from normal output. If errOut is nil, out will be used for
// error output as a fallback by callers.
func WithIO(in io.Reader, out io.Writer, errOut io.Writer) CmdOption {
	return func(c *CmdDeps) {
		c.In = in
		c.Out = out
		c.Err = errOut
	}
}

func RunWithDeps(ctx context.Context, args []string, deps *CmdDeps) error {
	root := NewRootCmdFromDeps(deps)
	// if caller supplied args, use them; otherwise cobra will use os.Args
	if len(args) > 0 {
		root.SetArgs(args)
	}
	return root.ExecuteContext(ctx)
}

// Run is a convenience wrapper that creates the root command (with provided options),
// sets the provided args, and executes it. Tests or main can call this.
func Run(ctx context.Context, args []string, opts ...CmdOption) error {
	root := NewRootCmd(opts...)
	// if caller supplied args, use them; otherwise cobra will use os.Args
	if len(args) > 0 {
		root.SetArgs(args)
	}
	return root.ExecuteContext(ctx)
}

// applyCreateCmdOptions applies provided options and returns a populated config.
// Callers relying on default behavior should provide sensible fallbacks after
// this returns (for example, setting Out to os.Stdout if nil).
func applyCmdOptions(opts ...CmdOption) *CmdDeps {
	cfg := &CmdDeps{}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(cfg)
	}
	return cfg
}
