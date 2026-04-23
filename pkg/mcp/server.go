package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/cli-toolkit/clock"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// KegDefaults holds server-wide keg targeting defaults.
type KegDefaults struct {
	tapper.KegTargetOptions
}

// ServerOptions holds configuration for creating an MCP server.
type ServerOptions struct {
	LicenseText string
	// Logger is the structured logger for invocation logging. When nil,
	// invocation logging is silently skipped.
	Logger *slog.Logger
}

// NewServer builds an MCP server with all registered tools.
func NewServer(tap *tapper.Tap, version string, defaults KegDefaults, opts ...ServerOptions) *sdkmcp.Server {
	var opt ServerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	srv := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "tap",
		Version: version,
	}, nil)

	registerReadTools(srv, tap, defaults)
	registerWriteTools(srv, tap, defaults)
	registerIndexTools(srv, tap, defaults)
	registerDoctorTools(srv, tap, defaults)
	registerSnapshotTools(srv, tap, defaults)
	registerFileTools(srv, tap, defaults)
	registerLockTools(srv, tap, defaults)
	registerServeTools(srv, tap, defaults)
	registerSiteTools(srv, tap, defaults)
	registerRepoTools(srv, tap, defaults)
	registerImportTools(srv, tap, defaults)
	registerArchiveTools(srv, tap, defaults)
	registerGraphTools(srv, tap, defaults)
	registerOrientTools(srv, tap, defaults)
	registerResourceTools(srv, tap, defaults)
	registerIntegrateTools(srv, tap, defaults)
	registerAuthTools(srv, tap)
	registerLicenseTools(srv, opt.LicenseText)

	if opt.Logger != nil {
		var clk clock.Clock
		if tap != nil && tap.Runtime != nil {
			clk = tap.Runtime.Clock()
		}
		srv.AddReceivingMiddleware(invocationLoggingMiddleware(opt.Logger, clk))
	}

	return srv
}

// resolveKegTarget merges a per-tool keg alias with server-wide defaults.
func resolveKegTarget(perToolKeg string, defaults KegDefaults) tapper.KegTargetOptions {
	if perToolKeg != "" {
		return tapper.KegTargetOptions{Keg: perToolKeg}
	}
	return defaults.KegTargetOptions
}

// textResult wraps a string in a CallToolResult.
func textResult(text string) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: text},
		},
	}
}

// linesResult joins a slice of strings and wraps them in a CallToolResult.
func linesResult(lines []string) *sdkmcp.CallToolResult {
	return textResult(strings.Join(lines, "\n"))
}

// boolPtr returns a pointer to b. Used for ToolAnnotations fields that
// default to true when nil (DestructiveHint, OpenWorldHint).
func boolPtr(b bool) *bool { return &b }

// mcpDefaultLimitValue is the default maximum number of results returned by MCP
// read tools when the caller does not specify a limit. CLI tools default to
// unlimited (0) because shell pipelines handle truncation; MCP callers are
// typically AI agents that benefit from bounded context windows.
const mcpDefaultLimitValue = 50

// mcpDefaultLimit resolves the limit value for MCP tools. When the caller
// omits limit (JSON zero value 0), the default of 50 is applied. Passing -1
// explicitly requests unlimited results (converted to 0 for the Tap API).
// Any positive value is passed through unchanged.
func mcpDefaultLimit(limit int) int {
	switch {
	case limit < 0:
		return 0 // unlimited
	case limit == 0:
		return mcpDefaultLimitValue
	default:
		return limit
	}
}

// mcpDefaultMaxLinesValue is the default maximum number of matched lines per
// node returned by MCP grep when the caller does not specify max_lines. CLI
// defaults to unlimited (0); MCP callers benefit from bounded output to
// reduce token usage.
const mcpDefaultMaxLinesValue = 3

// mcpDefaultMaxLines resolves the max_lines value for MCP grep. When the
// caller omits max_lines (JSON zero value 0), the default of 3 is applied.
// Passing -1 explicitly requests unlimited (converted to 0 for the Tap API).
// Any positive value is passed through unchanged.
func mcpDefaultMaxLines(maxLines int) int {
	switch {
	case maxLines < 0:
		return 0 // unlimited
	case maxLines == 0:
		return mcpDefaultMaxLinesValue
	default:
		return maxLines
	}
}

// errorResult returns a CallToolResult with IsError set.
func errorResult(err error) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: err.Error()},
		},
		IsError: true,
	}
}

// invocationLoggingMiddleware returns an MCP middleware that logs every
// tools/call request with timing and client metadata. Timing is measured
// via the provided Clock so that sandboxed tests can inject a fake clock
// and production uses the real wall clock via the OS clock implementation.
// A nil clock falls back to the package default clock to keep the middleware
// safe to construct without a runtime.
func invocationLoggingMiddleware(lg *slog.Logger, clk clock.Clock) sdkmcp.Middleware {
	clk = clock.OrDefault(clk)
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			start := clk.Now()
			result, err := next(ctx, method, req)
			duration := clk.Now().Sub(start)

			attrs := []slog.Attr{
				slog.String("surface", "mcp"),
				slog.Int64("duration_ms", duration.Milliseconds()),
			}

			// Extract tool name and arguments from params.
			if params, ok := req.GetParams().(*sdkmcp.CallToolParamsRaw); ok {
				attrs = append(attrs, slog.String("tool", params.Name))
				attrs = append(attrs, slog.String("args", truncatePayload(string(params.Arguments), maxPayloadBytes)))

				// Try to extract keg alias from arguments.
				var argMap map[string]json.RawMessage
				if json.Unmarshal(params.Arguments, &argMap) == nil {
					if raw, exists := argMap["keg"]; exists {
						var keg string
						if json.Unmarshal(raw, &keg) == nil && keg != "" {
							attrs = append(attrs, slog.String("keg", keg))
						}
					}
				}
			}

			// Extract client metadata from session.
			if sess, ok := req.GetSession().(*sdkmcp.ServerSession); ok {
				attrs = append(attrs, slog.String("session_id", sess.ID()))
				if initParams := sess.InitializeParams(); initParams != nil {
					attrs = append(attrs, slog.String("protocol", initParams.ProtocolVersion))
					if ci := initParams.ClientInfo; ci != nil {
						attrs = append(attrs, slog.String("client.name", ci.Name))
						attrs = append(attrs, slog.String("client.version", ci.Version))
						attrs = append(attrs, slog.String("client.title", ci.Title))
					}
				}
			}

			// Determine success/failure.
			success := err == nil
			if ctr, ok := result.(*sdkmcp.CallToolResult); ok && ctr != nil && ctr.IsError {
				success = false
			}
			attrs = append(attrs, slog.Bool("success", success))
			if err != nil {
				attrs = append(attrs, slog.String("error", err.Error()))
			}

			lg.LogAttrs(ctx, slog.LevelInfo, "invocation", attrs...)
			return result, err
		}
	}
}

// maxPayloadBytes is the maximum byte length for argument payloads in MCP
// invocation log entries. Payloads exceeding this limit are truncated with
// an indicator to prevent large content from bloating the log.
const maxPayloadBytes = 512

// truncatePayload returns s unchanged if it is within limit bytes;
// otherwise it returns the first limit bytes followed by a truncation marker.
func truncatePayload(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}
