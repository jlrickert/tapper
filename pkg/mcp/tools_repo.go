package mcp

import (
	"context"
	"fmt"

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
	Keg     string `json:"keg" jsonschema:"keg alias for the new repository"`
	User    bool   `json:"user,omitempty" jsonschema:"create under user keg search path (default true)"`
	Project bool   `json:"project,omitempty" jsonschema:"create under project path"`
	Path    string `json:"path,omitempty" jsonschema:"explicit filesystem path (implies project destination)"`
	Title   string `json:"title,omitempty" jsonschema:"keg title"`
	Creator string `json:"creator,omitempty" jsonschema:"keg creator identifier"`
}

func registerRepoInit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "repo_init",
		Description: "Initialize a new KEG repository",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in repoInitInput) (*sdkmcp.CallToolResult, any, error) {
		opts := tapper.InitOptions{
			Keg:     in.Keg,
			User:    in.User,
			Project: in.Project,
			Path:    in.Path,
			Title:   in.Title,
			Creator: in.Creator,
		}

		// Default to user destination when nothing else is set.
		if !opts.User && !opts.Project && opts.Path == "" {
			opts.User = true
		}

		target, err := tap.InitKeg(ctx, opts)
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("initialized keg %q at %s", in.Keg, target.String())), nil, nil
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
	Scope string `json:"scope,omitempty" jsonschema:"config scope: user, project, or empty for merged"`
}

func registerConfig(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "config",
		Description: "Read tap configuration (merged, user, or project scope)",
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in configInput) (*sdkmcp.CallToolResult, any, error) {
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

// --- config_template ---

type configTemplateInput struct {
	Scope string `json:"scope,omitempty" jsonschema:"template scope: user (default) or project"`
}

func registerConfigTemplate(srv *sdkmcp.Server, tap *tapper.Tap) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:        "config_template",
		Description: "Return starter YAML template for tap configuration",
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
