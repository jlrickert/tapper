package tapper

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type SchemaOptions struct {
	KegTargetOptions
	Type string
	Data []byte
}

type ValidateOptions struct {
	KegTargetOptions
	NodeIDs []string
}

func (t *Tap) ListSchemas(ctx context.Context, opts SchemaOptions) ([]string, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleViewer)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	return k.ListSchemas(ctx)
}

func (t *Tap) ReadSchema(ctx context.Context, opts SchemaOptions) ([]byte, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleViewer)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}
	return k.ReadSchema(ctx, opts.Type)
}

func (t *Tap) CreateSchema(ctx context.Context, opts SchemaOptions) error {
	parsed, err := keg.ParseSchemaDefinition(opts.Data)
	if err != nil {
		return err
	}
	typeName := strings.TrimSpace(parsed.Type)
	if err := keg.ValidSchemaTypeName(typeName); err != nil {
		return fmt.Errorf("schema type is required in schema document: %w", err)
	}

	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	if _, err := k.ReadSchema(ctx, typeName); err == nil {
		return fmt.Errorf("schema %q already exists: %w", typeName, keg.ErrExist)
	} else if !errors.Is(err, keg.ErrNotExist) {
		return fmt.Errorf("unable to check schema %q: %w", typeName, err)
	}
	return k.WriteSchema(ctx, typeName, opts.Data)
}

func (t *Tap) DeleteSchema(ctx context.Context, opts SchemaOptions) error {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	return k.DeleteSchema(ctx, opts.Type)
}

func (t *Tap) Validate(ctx context.Context, opts ValidateOptions) ([]keg.SchemaValidationResult, error) {
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleViewer)
	if err != nil {
		return nil, fmt.Errorf("unable to open keg: %w", err)
	}

	var ids []keg.NodeId
	if len(opts.NodeIDs) == 0 {
		ids, err = k.ListNodes(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to list nodes: %w", err)
		}
	} else {
		for _, raw := range opts.NodeIDs {
			var id keg.NodeId
			k, id, err = t.resolveNodeArg(ctx, k, raw)
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
	}

	results := make([]keg.SchemaValidationResult, 0, len(ids))
	for _, id := range ids {
		result, err := k.ValidateNode(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("unable to validate node %s: %w", id.Path(), err)
		}
		results = append(results, *result)
	}
	return results, nil
}

func (t *Tap) warnSchemaIssues(ctx context.Context, k keg.Keg, id keg.NodeId, stream *toolkit.Stream) {
	if stream == nil {
		if t != nil && t.Runtime != nil {
			stream = t.Runtime.Stream()
		}
	}
	if stream == nil || stream.Err == nil || k == nil {
		return
	}
	result, err := k.ValidateNode(ctx, id)
	if err != nil {
		if errors.Is(err, keg.ErrNotSupported) || errors.Is(err, keg.ErrNotExist) {
			return
		}
		_, _ = fmt.Fprintf(stream.Err, "warning: schema validation failed for node %s: %v\n", id.Path(), err)
		return
	}
	if result == nil || result.Valid {
		return
	}
	for _, issue := range result.Issues {
		field := issue.Field
		if field == "" {
			field = "schema"
		}
		_, _ = fmt.Fprintf(stream.Err, "warning: node %s %s: %s\n", id.Path(), field, issue.Message)
	}
}

func readAllSchemaInput(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("schema input is required")
	}
	return io.ReadAll(r)
}
