package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

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
	registerLicenseTools(srv, opt.LicenseText)

	if opt.Logger != nil {
		srv.AddReceivingMiddleware(invocationLoggingMiddleware(opt.Logger))
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
// tools/call request with timing and client metadata.
func invocationLoggingMiddleware(lg *slog.Logger) sdkmcp.Middleware {
	return func(next sdkmcp.MethodHandler) sdkmcp.MethodHandler {
		return func(ctx context.Context, method string, req sdkmcp.Request) (sdkmcp.Result, error) {
			if method != "tools/call" {
				return next(ctx, method, req)
			}

			// Wall-clock time is correct here: middleware signature does not carry
			// a Runtime, and server-side timing requires real elapsed time
			// (analogous to the fsnotify debounce exception in repo_fs_events.go).
			start := time.Now()
			result, err := next(ctx, method, req)
			duration := time.Since(start)

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
