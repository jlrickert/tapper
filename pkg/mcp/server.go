package mcp

import (
	"strings"

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
	registerLicenseTools(srv, opt.LicenseText)

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

// errorResult returns a CallToolResult with IsError set.
func errorResult(err error) *sdkmcp.CallToolResult {
	return &sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{
			&sdkmcp.TextContent{Text: err.Error()},
		},
		IsError: true,
	}
}
