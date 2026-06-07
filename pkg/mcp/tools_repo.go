package mcp

import (
	"context"
	"fmt"
	"strings"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/tapper"
)

func registerRepoTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerRepoInit(srv, tap, defaults)
	registerRepoRm(srv, tap)
	registerConfig(srv, tap)
	registerConfigTemplate(srv, tap)
}

// --- repo_init ---

type repoInitInput struct {
	Keg            string `json:"keg" jsonschema:"keg name for the new repository"`
	Namespace      string `json:"namespace,omitempty" jsonschema:"namespace the keg belongs to; empty resolves via config. Use 'local' to pin this machine's filesystem hub"`
	Hub            string `json:"hub,omitempty" jsonschema:"hub override; empty resolves the hub from the namespace"`
	User           bool   `json:"user,omitempty" jsonschema:"pin the reserved @local namespace (filesystem hub)"`
	Project        bool   `json:"project,omitempty" jsonschema:"create under project path"`
	Path           string `json:"path,omitempty" jsonschema:"explicit filesystem path (implies project destination)"`
	Title          string `json:"title,omitempty" jsonschema:"keg title"`
	Creator        string `json:"creator,omitempty" jsonschema:"keg creator identifier"`
	NonInteractive bool   `json:"non_interactive,omitempty" jsonschema:"skip interactive prompts (always true in MCP context)"`
}

func registerRepoInit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "repo_init",
		Description: "Initialize a new KEG repository",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in repoInitInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.InitOptions{
			Keg:            in.Keg,
			Namespace:      in.Namespace,
			Hub:            in.Hub,
			User:           in.User,
			Project:        in.Project,
			Path:           in.Path,
			Title:          in.Title,
			Creator:        in.Creator,
			NonInteractive: true,
		}
		_ = in.NonInteractive // MCP never prompts; field exists for parity with the CLI flag

		// Destination resolves namespace→hub: a bare name lands in the default
		// namespace+hub (typically a remote create); namespace "local" or --user
		// pins this machine's filesystem hub. No implicit local default here.

		target, err := tap.InitKeg(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		label := tapper.KegBackendLabel(target)
		if label == "" {
			return textResult(fmt.Sprintf("initialized keg %q", in.Keg)), nil, nil
		}
		return textResult(fmt.Sprintf("initialized keg %q (%s)", in.Keg, label)), nil, nil
	})
}

// --- repo_rm ---

type repoRmInput struct {
	Alias string `json:"alias" jsonschema:"keg alias to remove from config"`
	Force bool   `json:"force,omitempty" jsonschema:"force removal even if alias is the default or fallback keg"`
}

func registerRepoRm(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "repo_rm",
		Description: "Remove a registered KEG alias from user configuration",
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in repoRmInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.RemoveRepoOptions{
			Alias: in.Alias,
			Force: in.Force,
		}

		if err := tap.RemoveRepo(ctx, opts); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("removed keg alias %q", in.Alias)), nil, nil
	})
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
