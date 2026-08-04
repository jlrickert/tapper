package cli

import (
	"errors"
	"io"
	"log/slog"

	"github.com/jlrickert/cli-toolkit/mylog"
	"github.com/jlrickert/cli-toolkit/toolkit"
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
			launcherBound := cmd.Flags().Changed("flight")

			// MCP servers communicate over stdio, so the logger must
			// write to stderr. When no --log-file is provided, use
			// stderr at the requested level. When a log file is set,
			// fan out to both the file and stderr so operators can
			// observe MCP traffic live while also persisting it.
			lg, err := buildMCPLogger(rt, deps)
			if err != nil {
				return err
			}
			if err := rt.SetLogger(lg); err != nil {
				return err
			}

			defaults := mcp.KegDefaults{
				KegTargetOptions: deps.KegTargetOptions,
			}
			if !launcherBound {
				// Config-driven selection is re-resolved on initialize/orient.
				defaults.Flight = ""
			}
			srv := mcp.NewServer(deps.Tap, Version, defaults, mcp.ServerOptions{
				Logger:   rt.Logger(),
				Reporter: deps.InvocationReporter,
			})
			err = srv.Run(cmd.Context(), &sdkmcp.StdioTransport{})
			if err != nil && errors.Is(err, io.EOF) {
				return nil
			}
			return err
		},
	}
	return cmd
}

// buildMCPLogger constructs the structured logger for the MCP server.
// Unlike CLI commands, MCP always logs to stderr because stdout is reserved
// for JSON-RPC. When a log file is also configured, entries fan out to both.
func buildMCPLogger(rt *toolkit.Runtime, deps *Deps) (*slog.Logger, error) {
	level := mylog.ParseLevel(deps.LogLevel)

	if deps.LogFile == "" {
		// No log file — stderr at the requested level and format.
		lg := mylog.NewLogger(mylog.LoggerConfig{
			Out:     rt.Stream().Err,
			Level:   level,
			JSON:    deps.LogJSON,
			Version: Version,
		})
		return lg, nil
	}

	// Log file is set — fan out to both file and stderr at the same level.
	if deps.logFileHandle == nil {
		// Should not happen: PersistentPreRunE opens the file. Defensive.
		lg := mylog.NewLogger(mylog.LoggerConfig{
			Out:     rt.Stream().Err,
			Level:   level,
			JSON:    deps.LogJSON,
			Version: Version,
		})
		return lg, nil
	}
	lg := mylog.NewLogger(mylog.LoggerConfig{
		Out:     io.MultiWriter(deps.logFileHandle, rt.Stream().Err),
		Level:   level,
		JSON:    deps.LogJSON,
		Version: Version,
	})
	return lg, nil
}
