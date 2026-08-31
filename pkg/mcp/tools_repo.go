package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerRepoTools(srv *sdkmcp.Server, tap *tapper.Tap) {
	registerConfig(srv, tap)
	registerConfigTemplate(srv, tap)
}

// --- config ---

type configInput struct {
	Scope   string `json:"scope,omitempty" jsonschema:"config scope: user, project, or empty for merged"`
	Explain string `json:"explain,omitempty" jsonschema:"when set, return provenance for this field (or 'all' for all fields) instead of config dump"`
}

func registerConfig(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "config",
		Description: "Read tap configuration (merged, user, or project scope)",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in configInput) (*sdkmcp.CallToolResult, any, error) {
		// When explain is set, return provenance instead of the config dump.
		if in.Explain != "" {
			field := in.Explain
			if field == "all" {
				field = ""
			}
			results, err := tap.ConfigExplain(ctx, tapper.ConfigExplainOptions{
				Field: field,
			})
			if err != nil {
				return errorResult(err), nil, nil
			}
			return textResult(formatExplainResults(results)), nil, nil
		}

		opts := tapper.ConfigOptions{}
		switch in.Scope {
		case "user":
			opts.User = true
		case "project":
			opts.Project = true
		case "", "merged":
			// default: merged config
		default:
			return errorResult(fmt.Errorf("invalid scope %q: must be user, project, or empty for merged", in.Scope)), nil, nil
		}

		result, err := tap.Config(opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}

// formatExplainResults formats ConfigExplainResults into a human-readable string.
func formatExplainResults(results []tapper.ConfigExplainResult) string {
	var parts []string
	for _, r := range results {
		val := r.Value
		if val == "" {
			val = `""`
		}
		parts = append(parts, fmt.Sprintf("%s = %s  [%s]", r.Field, val, r.Source))
	}
	return strings.Join(parts, "\n")
}

// --- config_template ---

type configTemplateInput struct {
	Scope string `json:"scope,omitempty" jsonschema:"template scope: user (default) or project"`
}

func registerConfigTemplate(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "config_template",
		Description: "Return starter YAML template for tap configuration",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in configTemplateInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.ConfigTemplateOptions{}
		switch in.Scope {
		case "project":
			opts.Project = true
		case "", "user":
			// default: user template
		default:
			return errorResult(fmt.Errorf("invalid scope %q: must be user or project", in.Scope)), nil, nil
		}

		result, err := tap.ConfigTemplate(opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(result), nil, nil
	})
}
