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
	gate *sessionFlightGate
}

// Surface selects which tool groups NewServer registers.
type Surface int

const (
	// SurfaceFull registers every tool — the CLI peer surface and the default.
	SurfaceFull Surface = iota
	// SurfaceHub registers only the per-user node read/write tools appropriate
	// for a remote, OAuth-scoped hub connector (tapper-hub's /mcp endpoint). It
	// omits CLI/local tools (auth_status, config, doctor, license,
	// file downloads, path-based image downloads, archive, locks) and hub-admin
	// tools (keg/namespace administration, flights) — none of which make sense
	// for a remote multi-tenant connector. Uploads remain available, but local
	// path sources are disabled. Image downloads return MCP image content rather
	// than writing to the server filesystem. Schema tools (schema_list/read/
	// create/edit/delete, validate) ARE registered here: schema mutation resolves
	// at editor role, consistent with how node writes already work on this
	// surface. The hub pairs this with Tap.KegResolver so the registered tools
	// resolve against the caller's catalog.
	SurfaceHub
)

// ServerOptions holds configuration for creating an MCP server.
type ServerOptions struct {
	LicenseText string
	// Logger is the structured logger for invocation logging. When nil,
	// invocation logging is silently skipped.
	Logger *slog.Logger
	// Reporter receives privacy-minimized tool invocation telemetry. It is
	// independent of Logger and may be nil.
	Reporter tapper.InvocationReporter
	// Surface selects the registered tool set. The zero value (SurfaceFull)
	// registers everything, preserving the CLI peer surface.
	Surface Surface
}

// NewServer builds an MCP server with all registered tools.
func NewServer(tap *tapper.Tap, version string, defaults KegDefaults, opts ...ServerOptions) *sdkmcp.Server {
	var opt ServerOptions
	if len(opts) > 0 {
		opt = opts[0]
	}
	if opt.Surface != SurfaceHub {
		defaults.gate = newSessionFlightGate(tap, defaults.Flight)
	}

	var srv *sdkmcp.Server
	nodeSubs := newNodeResourceSubscriptions(tap, defaults, func(ctx context.Context, uri string) {
		if srv != nil {
			_ = srv.ResourceUpdated(ctx, &sdkmcp.ResourceUpdatedNotificationParams{URI: uri})
		}
	})
	srv = sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "tap",
		Version: version,
	}, &sdkmcp.ServerOptions{
		SubscribeHandler:   nodeSubs.Subscribe,
		UnsubscribeHandler: nodeSubs.Unsubscribe,
	})
	if defaults.gate != nil {
		srv.AddReceivingMiddleware(defaults.gate.middleware)
	}

	// Node read/write tools — registered on every surface. These all funnel
	// through Tap.resolveKegForRole, so a hub-injected KegResolver scopes them
	// to the caller's catalog with viewer/editor enforcement.
	registerReadTools(srv, tap, defaults)
	registerWriteTools(srv, tap, defaults)
	registerIndexTools(srv, tap, defaults)
	registerSnapshotTools(srv, tap, defaults)
	registerGraphTools(srv, tap, defaults)
	registerOrientTools(srv, tap, defaults)
	registerSchemaTools(srv, tap, defaults)
	fileOpts := fileToolOptions{
		AllowLocalSources: opt.Surface != SurfaceHub,
		DownloadFiles:     opt.Surface != SurfaceHub,
		ImageDownloads:    imageDownloadLocalPath,
	}
	if opt.Surface == SurfaceHub {
		fileOpts.ImageDownloads = imageDownloadContent
	}
	registerFileTools(srv, tap, defaults, fileOpts)

	// CLI/local and hub-admin tools — omitted on the remote hub connector
	// surface (see Surface docs). They depend on the local CLI environment
	// (config cascade, AuthStore, local filesystem writes) or perform
	// multi-tenant administration, neither of which belongs on a per-user remote
	// connector.
	if opt.Surface != SurfaceHub {
		registerDoctorTools(srv, tap, defaults)
		registerLockTools(srv, tap, defaults)
		registerRepoTools(srv, tap, defaults)
		registerImportTools(srv, tap, defaults)
		registerArchiveTools(srv, tap, defaults)
		registerFlightTools(srv, tap, defaults)
		registerFlightSwitchControl(srv, defaults.gate)
		registerKegTools(srv, tap, defaults)
		registerNamespaceTools(srv, tap, defaults)
		registerResourceTools(srv, tap, defaults)
		registerAuthTools(srv, tap)
		registerLicenseTools(srv, opt.LicenseText)
	}

	if opt.Logger != nil || opt.Reporter != nil {
		var clk clock.Clock
		if tap != nil && tap.Runtime != nil {
			clk = tap.Runtime.Clock()
		}
		srv.AddReceivingMiddleware(invocationLoggingMiddleware(opt.Logger, opt.Reporter, clk))
	}

	return srv
}

// resolveKegTarget merges a per-tool keg alias with server-wide defaults.
func resolveKegTarget(ctx context.Context, perToolKeg string, defaults KegDefaults) tapper.KegTargetOptions {
	out := defaults.KegTargetOptions
	if perToolKeg != "" {
		out.Keg = perToolKeg
		out.Project = false
		out.Cwd = false
		out.Path = ""
	}
	if defaults.gate != nil {
		out.Flight = defaults.gate.activeFlight(ctx)
	}
	// The MCP server is a full surface (peer to `tap`): config-driven keg
	// resolution requires `tap bootstrap` to have run.
	out.RequireBootstrap = true
	return out
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
func invocationLoggingMiddleware(lg *slog.Logger, reporter tapper.InvocationReporter, clk clock.Clock) sdkmcp.Middleware {
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

			if lg != nil {
				lg.LogAttrs(ctx, slog.LevelInfo, "invocation", attrs...)
			}
			if reporter != nil {
				tool := ""
				if params, ok := req.GetParams().(*sdkmcp.CallToolParamsRaw); ok {
					tool = params.Name
				}
				if tool != "" {
					reporter.Report(tapper.InvocationEvent{
						Surface:    "mcp",
						Tool:       tool,
						DurationMS: duration.Milliseconds(),
						Success:    success,
					})
				}
			}
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
