package cli

import (
	"errors"
	"io"

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
			defaults := mcp.KegDefaults{
				KegTargetOptions: deps.KegTargetOptions,
			}
			srv := mcp.NewServer(deps.Tap, Version, defaults)
			err := srv.Run(cmd.Context(), &sdkmcp.StdioTransport{})
			if err != nil && errors.Is(err, io.EOF) {
				return nil
			}
			return err
		},
	}
	return cmd
}
