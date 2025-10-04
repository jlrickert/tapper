package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/jlrickert/tapper/pkg/internal"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/log"
	"github.com/jlrickert/tapper/pkg/tapper"
	"github.com/spf13/cobra"
)

var (
	// Version is the build-time Version. Override with:
	//   -ldflags "-X github.com/jlrickert/tapper/cmd.Version=v1.2.3"
	Version string = "dev"
)

// NewRootCmd constructs the root cobra.Command and wires up subcommands using
// factory functions. It accepts zero or more CmdOption functional options,
// converts them into a CmdDeps instance via applyCmdOptions, and delegates to
// NewRootCmdWithDeps to produce the fully assembled command.
//
// Notes:
//   - This helper is convenient when callers prefer the functional option API
//     rather than constructing CmdDeps directly.
//   - It does not implicitly mutate or create heavy-weight resources; callers
//     that rely on runtime defaults should call the returned command's
//     dependency ApplyDefaults path (via NewRootCmdWithDeps) or ensure
//     defaults are applied before executing the command.
func NewRootCmd(options ...CmdOption) *cobra.Command {
	deps := applyCmdOptions(options...)
	return NewRootCmdWithDeps(deps)
}

// NewRootCmd builds the root command and wires up subcommands via factory functions.
// Commands are created by functions rather than relying on global variables.
func NewRootCmdWithDeps(deps *CmdDeps) *cobra.Command {
	root := &cobra.Command{
		Use:   "kegv2",
		Short: "keg — KEG (Knowledge Exchange Graph) utility",
		Long: `keg manages KEG nodes and provides a small set of commands for creating,
	editing, indexing, and inspecting nodes. See docs/ for repository-specific
	conventions (meta.yaml, README.md, dex/* indices).`,
		Version: Version,
		PreRunE: func(cmd *cobra.Command, args []string) error {
			if deps.flags.CfgFile != "" {
				userCfg, err := tapper.ReadUserConfigFrom(deps.flags.CfgFile)
				if err != nil {
					return err
				}
				deps.UserConfig = userCfg
			}
			userCfg, err := tapper.ReadUserConfig(tapper.ConfigAppName)
			deps.UserConfig = userCfg

			if deps.flags.Debug {
				deps.Logger
			} else if deps.flags.Verbose {
			}
			err = deps.ApplyDefaults()
			if err != nil {
				return err
			}

			fmt.Fprintln(cmd.OutOrStdout(), "TEST")
			return deps.ApplyDefaults()
		},
		// Default action: show help if no subcommand is provided.
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}

	root.SetIn(deps.In)
	root.SetOut(deps.Out)
	root.SetErr(deps.Err)

	// Persistent flags available to all subcommands
	root.PersistentFlags().StringVar(
		&deps.flags.CfgFile,
		"config",
		"",
		"config file (default: $XDG_CONFIG_HOME/tapper/keg.yaml)",
	)
	root.PersistentFlags().BoolVarP(
		&deps.flags.Verbose,
		"verbose",
		"v",
		false,
		"enable verbose output",
	)
	root.PersistentFlags().BoolVarP(
		&deps.flags.Debug,
		"debug",
		"d",
		false,
		"enable debug output",
	)

	// Keep Cobra's automatic completion enabled for the root command.
	root.CompletionOptions.DisableDefaultCmd = false

	// Core commands
	root.AddCommand(newCreateCmdWithDeps(deps))

	return root
}

type KegResolver func(root string, cfg *tapper.UserConfig) (tapper.KegTarget, error)

// CmdOption is a functional option for configuring command dependencies.
type CmdOption func(*CmdDeps)

// CmdDeps holds injectable dependencies for commands (Keg service, IO streams, etc.).
type CmdDeps struct {
	Keg         *keg.Keg
	Logger      *slog.Logger
	LocalConfig *tapper.LocalConfig
	UserConfig  *tapper.UserConfig

	In  io.Reader
	Out io.Writer
	Err io.Writer

	Project     string
	KegResolver KegResolver
	Editor      internal.EditorRunner
	Clock       internal.Clock

	flags CmdGlobalFlags
}

type CmdGlobalFlags struct {
	CfgFile string
	Verbose bool
	Debug   bool
}

func WithKeg(k *keg.Keg) CmdOption {
	return func(cd *CmdDeps) {
		cd.Keg = k
	}
}

func WithConfig(cfg *tapper.UserConfig) CmdOption {
	return func(cd *CmdDeps) {
		cd.UserConfig = cfg
	}
}

// WithKeg returns a CmdOption that injects the provided *keg.Keg into the command config.
// Tests or callers can provide a keg backed by a MemoryRepo to exercise real create
// behavior without touching disk.
func WithKegResolver(k KegResolver) CmdOption {
	return func(c *CmdDeps) {
		c.KegResolver = k
	}
}

func WithLogger(lg *slog.Logger) CmdOption {
	return func(cd *CmdDeps) {
		cd.Logger = lg
	}
}

// WithIO returns a CmdOption that sets stdin/stdout/stderr for commands. Passing an
// io.Writer for errOut allows tests to capture command error output separately from
// normal output. If errOut is nil, out will be used for error output as a fallback.
func WithIO(in io.Reader, out io.Writer, errOut io.Writer) CmdOption {
	return func(c *CmdDeps) {
		c.In = in
		c.Out = out
		c.Err = errOut
	}
}

// applyCmdOptions applies provided options and returns a populated CmdDeps.
// Callers relying on default behavior should provide sensible fallbacks after
// this returns (for example, setting Out to os.Stdout if nil).
func applyCmdOptions(opts ...CmdOption) *CmdDeps {
	deps := &CmdDeps{}
	for _, o := range opts {
		if o == nil {
			continue
		}
		o(deps)
	}
	return deps
}

// ApplyDefaults ensures command dependencies have clear, documented defaults
// before a Cobra command runs. Keep this function conservative:
//
//   - Do not implicitly create or mutate heavy-weight resources that callers
//     may intentionally leave nil (tests frequently depend on nil to detect
//     uninitialized dependencies).
//   - Document the expected production defaults here so callers know what to
//     provide when constructing CmdDeps via options.
//
// Typical expectations (NOT automatically applied by this no-op):
// - In  -> os.Stdin
// - Out -> os.Stdout
// - Err -> os.Stderr (or Out fallback)
// - Keg -> a usable *keg.Keg (e.g., backed by a FsRepo or MemoryRepo for tests)
//
// Rationale for conservative behavior:
//   - Tests should explicitly inject a MemoryRepo-backed Keg via WithKeg to
//     avoid surprising global defaults.
//   - CLI callers (main) should call WithIO to supply real stdio streams or
//     rely on a small wrapper that sets KEG-specific environment/state first.
//
// If automatic defaulting is later desired (for example, setting nil IO to
// os.Stdout or creating a default in-memory Keg), update this function and
// add the required imports (os, etc.). For now, keep it a no-op to be
// explicit and predictable.
func (deps *CmdDeps) ApplyDefaults() error {
	var errs []error
	if deps.In == nil {
		deps.In = os.Stdin
	}
	if deps.Out == nil {
		deps.Out = os.Stdout
	}
	if deps.Err == nil {
		deps.Err = deps.Out
	}
	if deps.Project == "" {
		if wd, err := os.Getwd(); err == nil {
			deps.Project = wd
		} else {
			deps.Project = "."
		}
	}

	if deps.Editor == nil {
		deps.Editor = internal.DefaultEditor
	}
	if deps.Clock == nil {
		deps.Clock = internal.RealClock{}
	}
	if deps.Logger == nil {
		deps.Logger = log.NewNopLogger()
	}
	if deps.UserConfig == nil {
		userCfg, err := tapper.ReadUserConfig(tapper.ConfigAppName)
		if err != nil {
			errs = append(errs, err)
		} else {
			deps.UserConfig = userCfg
		}
	}
	if deps.LocalConfig == nil {
		localCfg, err := tapper.ReadLocalFile(deps.Project)
		if err != nil {
			errs = append(errs, err)
		} else {
			deps.LocalConfig = localCfg
		}
	}
	return errors.Join(errs...)
}

// RunWithDeps runs the root command wired with the provided dependencies.
// It constructs the root Cobra command using NewRootCmdWithDeps and executes it
// with the supplied context so callers can cancel or time out the run. If args is
// non-empty it overrides the process args for the command execution. Any
// initialization registered in PersistentPreRunE (for example, deps.ApplyDefaults)
// will execute before the command runs. The returned error is whatever Cobra's
// ExecuteContext returns.
func RunWithDeps(ctx context.Context, args []string, deps *CmdDeps) error {
	root := NewRootCmdWithDeps(deps)
	// if caller supplied args, use them; otherwise Cobra will use os.Args
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
