package mcp

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// registerSchemaTools exposes keg schema administration over MCP. Schemas
// describe valid note types for a keg; these tools mirror `tap schema …` and
// `tap schema validate`. They are registered on every surface (CLI peer + hub
// connector) and funnel through Tap.resolveKegForRole, so a hub-injected
// KegResolver scopes them to the caller's catalog with viewer/editor
// enforcement.
func registerSchemaTools(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	registerSchemaList(srv, tap, defaults)
	registerSchemaRead(srv, tap, defaults)
	registerSchemaCreate(srv, tap, defaults)
	registerSchemaEdit(srv, tap, defaults)
	registerSchemaDelete(srv, tap, defaults)
	registerValidate(srv, tap, defaults)
}

// --- schema_list ---

type schemaListInput struct {
	Keg string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerSchemaList(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "schema_list",
		Description:  "Memory: List the schema type names a KEG defines.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in schemaListInput) (*sdkmcp.CallToolResult, any, error) {
		names, err := tap.ListSchemas(ctx, tapper.SchemaOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		if len(names) == 0 {
			return textResult("no schemas defined"), nil, nil
		}
		return linesResult(names), nil, nil
	})
}

// --- schema_read ---

type schemaReadInput struct {
	Type string `json:"type" jsonschema:"Schema type name to read."`
	Keg  string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

// schemaReadOutput is schema_read's structured content.
type schemaReadOutput struct {
	Type string `json:"type" jsonschema:"The schema type name."`
	Hash string `json:"hash" jsonschema:"The schema's hash: schema_edit's and schema_delete's expected_hash."`
	Data string `json:"data" jsonschema:"The schema definition as YAML."`
}

func registerSchemaRead(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name: "schema_read",
		Description: "Memory: Read one schema definition as YAML. The result carries the schema's " +
			"hash; pass it back as expected_hash when editing this schema.",
		OutputSchema: outputSchema[schemaReadOutput](),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in schemaReadInput) (*sdkmcp.CallToolResult, any, error) {
		data, err := tap.ReadSchema(ctx, tapper.SchemaOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Type:             in.Type,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		res := textResult(string(data))
		res.StructuredContent = schemaReadOutput{Type: in.Type, Hash: keg.DocumentHash(data), Data: string(data)}
		return res, nil, nil
	})
}

// --- schema_create ---

type schemaCreateInput struct {
	Data string `json:"data" jsonschema:"Full schema definition as YAML; the document must declare its type."`
	Keg  string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerSchemaCreate(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "schema_create",
		Description:  "Manage: Create a new keg schema from a YAML definition (fails if the type already exists). Requires admin access to the KEG, and admin cover when a flight is selected.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in schemaCreateInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.CreateSchema(ctx, tapper.SchemaOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Data:             []byte(in.Data),
		}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult("schema created"), nil, nil
	})
}

// --- schema_edit ---

type schemaEditInput struct {
	Type         string `json:"type" jsonschema:"Schema type name to replace."`
	Data         string `json:"data" jsonschema:"Full schema definition as YAML; its declared type must match."`
	ExpectedHash string `json:"expected_hash" jsonschema:"Precondition token returned by schema_read."`
	Keg          string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerSchemaEdit(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "schema_edit",
		Description:  "Manage: Change how a KEG validates notes; writing a note never needs this, so pick an existing type from schema_list instead. Call schema_read first, then replace an existing keg schema with a new YAML definition using its hash as expected_hash. Requires admin access to the KEG, and admin cover when a flight is selected. On conflict, merge into the returned current schema (or refetch with schema_read) and retry with the returned current hash.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(false),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in schemaEditInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.EditSchema(ctx, tapper.EditSchemaOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Type:             in.Type,
			ExpectedHash:     in.ExpectedHash,
			Stream: &toolkit.Stream{
				IsPiped: true,
				In:      bytes.NewReader([]byte(in.Data)),
			},
		}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("schema %q updated", in.Type)), nil, nil
	})
}

// --- schema_delete ---

type schemaDeleteInput struct {
	Type         string `json:"type" jsonschema:"Schema type name to delete."`
	ExpectedHash string `json:"expected_hash" jsonschema:"Precondition token returned by schema_read."`
	Keg          string `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerSchemaDelete(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "schema_delete",
		Description:  "Manage: Call schema_read first, then delete a keg schema using its hash as expected_hash. Requires admin access to the KEG, and admin cover when a flight is selected. On conflict, refetch with schema_read and retry with the returned current hash.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			DestructiveHint: boolPtr(true),
			OpenWorldHint:   boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in schemaDeleteInput) (*sdkmcp.CallToolResult, any, error) {
		if err := tap.DeleteSchema(ctx, tapper.SchemaOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			Type:             in.Type,
			ExpectedHash:     in.ExpectedHash,
		}); err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("schema %q deleted", in.Type)), nil, nil
	})
}

// --- validate ---

type validateInput struct {
	NodeIDs []string `json:"node_ids,omitempty" jsonschema:"Node IDs to validate (validates every node if empty)."`
	Keg     string   `json:"keg,omitempty" jsonschema:"KEG to use, as @namespace/keg; a bare name uses your default namespace. Omit it to use the default KEG."`
}

func registerValidate(srv *sdkmcp.Server, tap *tapper.Tap, defaults KegDefaults) {
	sdkmcp.AddTool(srv, &sdkmcp.Tool{
		Name:         "schema_validate",
		Description:  "Memory: Validate nodes against their declared schema type.",
		OutputSchema: textOutputSchema(),
		Annotations: &sdkmcp.ToolAnnotations{
			ReadOnlyHint:  true,
			OpenWorldHint: boolPtr(false),
		},
	}, func(ctx context.Context, req *sdkmcp.CallToolRequest, in validateInput) (*sdkmcp.CallToolResult, any, error) {
		results, err := tap.Validate(ctx, tapper.ValidateOptions{
			KegTargetOptions: resolveKegTarget(ctx, in.Keg, defaults),
			NodeIDs:          in.NodeIDs,
		})
		if err != nil {
			return errorResult(err), nil, nil
		}
		return textResult(formatValidationResults(results)), nil, nil
	})
}

// formatValidationResults renders validation results as plain text. Validity
// is a result the agent reads, not a tool error, so an invalid node yields a
// normal (non-IsError) text result with an explicit summary line. The per-node
// lines mirror `tap schema validate` (see pkg/cli/cmd_validate.go).
func formatValidationResults(results []keg.SchemaValidationResult) string {
	if len(results) == 0 {
		return "no nodes validated"
	}
	var b strings.Builder
	invalid := 0
	for _, result := range results {
		if result.Valid {
			fmt.Fprintf(&b, "ok: node %s", result.NodeID)
			if result.Type != "" {
				fmt.Fprintf(&b, " type=%s", result.Type)
			}
			b.WriteByte('\n')
			continue
		}
		invalid++
		for _, issue := range result.Issues {
			field := issue.Field
			if field == "" {
				field = "schema"
			}
			fmt.Fprintf(&b, "error: node %s %s: %s\n", result.NodeID, field, issue.Message)
		}
	}
	if invalid > 0 {
		fmt.Fprintf(&b, "\n%d invalid node(s) of %d", invalid, len(results))
	} else {
		fmt.Fprintf(&b, "\nall %d node(s) valid", len(results))
	}
	return b.String()
}
