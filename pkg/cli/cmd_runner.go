package cli

import (
	"errors"
	"io"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"

	"github.com/jlrickert/tapper/pkg/tapper"
)

// NewRunnerCmd builds `tap runner`, the coding-runner MCP server the relay
// starts, one harness at a time, as its built-in runner servers.
func NewRunnerCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "runner",
		Short: "delegate tasks to this machine's coding agents (experimental)",
		Long: `Commands for handing tasks to the coding agent CLIs installed on this
machine: Claude Code, Codex, opencode and pi. 'tap relay' starts
'tap runner serve --runner NAME' as its built-in runner MCP servers, one per
harness turned on in relay.runners; see 'tap relay --help'.`,
	}
	cmd.AddCommand(newRunnerServeCmd(deps))
	return cmd
}

func newRunnerServeCmd(deps *Deps) *cobra.Command {
	var only string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "serve the installed coding agents as MCP tools on stdio",
		Long: `Start an MCP server on stdio with one tool per coding agent CLI found on
PATH: claude_code_run, codex_run, opencode_run and pi_run. Each tool takes a
task, an absolute cwd, and optionally a session id to continue and a model,
runs the agent non-interactively there, and returns its final reply and
session id. --runner serves just the named one (claude, codex, opencode, or
pi), which must be installed.

Each agent applies its own permission settings; nothing here bypasses them.
Claude Code's print mode denies anything that would need a prompt, and
'codex exec' runs in a read-only sandbox unless your config grants more.

Environment:
  TAP_RUNNER_ROOTS    directories a task's cwd must be under (PATH-style
                      list; default $HOME)
  TAP_RUNNER_TIMEOUT  longest one task may run (default 30m)
  TAP_RUNNER_DEPTH    set to 1 or more, every call is refused; each agent
                      started here gets it incremented, so a delegated
                      agent cannot delegate again

Agents started here also get TAP_RELAY_TOOLS=off, which keeps their own
'tap mcp' from offering relayed tools.`,
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			svc, err := tapper.NewRunnerService(deps.Runtime)
			if err != nil {
				return err
			}
			if only != "" {
				if err := svc.Only(only); err != nil {
					return err
				}
			}
			srv := tapper.NewRunnerMCPServer(svc, Version)
			err = srv.Run(cmd.Context(), &sdkmcp.StdioTransport{})
			if err != nil && errors.Is(err, io.EOF) {
				return nil
			}
			return err
		},
	}
	cmd.Flags().StringVar(&only, "runner", "", "serve only this runner: claude, codex, opencode, or pi")
	_ = cmd.RegisterFlagCompletionFunc("runner", func(*cobra.Command, []string, string) ([]string, cobra.ShellCompDirective) {
		names := make([]string, 0, len(tapper.Runners))
		for _, spec := range tapper.Runners {
			names = append(names, spec.Name)
		}
		return names, cobra.ShellCompDirectiveNoFileComp
	})
	return cmd
}
