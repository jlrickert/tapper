package cli

import (
	"errors"
	"io"

	"github.com/jlrickert/cli-toolkit/mylog"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/spf13/cobra"
)

func NewMcpCmd(deps *Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mcp",
		Short: "start an MCP server on stdio",
		Long: `Start a Model Context Protocol (MCP) server that exposes KEG
operations as tools over the stdio JSON-RPC transport.

Configure this in your AI agent's MCP settings:
  "tap mcp"

All keg operations become available as MCP tools without
per-command permission prompts.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			rt := deps.Runtime

			// Ensure the logger writes to stderr for the MCP server when
			// no explicit --log-file was provided. The default runtime
			// logger is a discard logger; MCP servers on stdio should
			// always log to stderr so operational data is visible without
			// requiring explicit flags.
			//
			// When --log-file is set, PersistentPreRunE already opened the
			// file and configured the logger to write there. Override the
			// logger to fan out to both the file and stderr so operators
			// can observe MCP traffic live while also persisting it.
			if deps.LogFile == "" {
				lg := mylog.NewLogger(mylog.LoggerConfig{
					Out:     rt.Stream().Err,
					Level:   mylog.ParseLevel(deps.LogLevel),
					JSON:    deps.LogJSON,
					Version: Version,
				})
				if err := rt.SetLogger(lg); err != nil {
					return err
				}
			} else if deps.logFileHandle != nil {
				lg := mylog.NewLogger(mylog.LoggerConfig{
					Out:     io.MultiWriter(deps.logFileHandle, rt.Stream().Err),
					Level:   mylog.ParseLevel(deps.LogLevel),
					JSON:    deps.LogJSON,
					Version: Version,
				})
				if err := rt.SetLogger(lg); err != nil {
					return err
				}
			}

			defaults := mcp.KegDefaults{
				KegTargetOptions: deps.KegTargetOptions,
			}
			srv := mcp.NewServer(deps.Tap, Version, defaults, mcp.ServerOptions{
				LicenseText: LicenseText,
				Logger:      rt.Logger(),
			})
			err := srv.Run(cmd.Context(), &sdkmcp.StdioTransport{})
			if err != nil && errors.Is(err, io.EOF) {
				return nil
			}
			return err
		},
	}
	return cmd
}
