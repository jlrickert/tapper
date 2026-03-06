package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerDoctorTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerDoctor(srv, tap, defaults)
}

// --- doctor ---

type doctorInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"keg alias (uses default if empty)"`
}

func registerDoctor(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "doctor",
		Description: "Check KEG health and report issues",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in doctorInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.DoctorOptions{
			KegTargetOptions: resolveKegTarget(in.Keg, defaults),
		}
		issues, err := tap.Doctor(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}

		if len(issues) == 0 {
			return textResult("ok: keg is healthy"), nil, nil
		}

		var lines []string
		errorCount := 0
		warningCount := 0
		for _, issue := range issues {
			if issue.Level == "error" {
				errorCount++
			} else {
				warningCount++
			}
			if issue.NodeID != "" {
				lines = append(lines, fmt.Sprintf("%s: [node %s] %s", issue.Level, issue.NodeID, issue.Message))
			} else {
				lines = append(lines, fmt.Sprintf("%s: %s", issue.Level, issue.Message))
			}
		}
		lines = append(lines, fmt.Sprintf("%d error(s), %d warning(s)", errorCount, warningCount))

		return textResult(strings.Join(lines, "\n")), nil, nil
	})
}
