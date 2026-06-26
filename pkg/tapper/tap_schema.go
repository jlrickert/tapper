package tapper

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/jlrickert/cli-toolkit/toolkit"
	"github.com/jlrickert/tapper/pkg/keg"
)

type SchemaOptions struct {
	KegTargetOptions
	Type string
	Data []byte
}

type EditSchemaOptions struct {
	KegTargetOptions
	Type   string
	Stream *toolkit.Stream
}

const schemaDefinitionSchemaModeline = "# yaml-language-server: $schema=" + keg.KegSchemaDefinitionSchemaURL + "\n"

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

func (t *Tap) EditSchema(ctx context.Context, opts EditSchemaOptions) error {
	typeName := strings.TrimSpace(opts.Type)
	if err := keg.ValidSchemaTypeName(typeName); err != nil {
		return err
	}
	k, err := t.resolveKegForRole(ctx, opts.KegTargetOptions, FlightRoleEditor)
	if err != nil {
		return fmt.Errorf("unable to open keg: %w", err)
	}
	originalRaw, err := k.ReadSchema(ctx, typeName)
	if err != nil {
		return fmt.Errorf("unable to read schema %q: %w", typeName, err)
	}

	saveSchema := func(data []byte, source string) error {
		if err := validateEditedSchema(typeName, data); err != nil {
			return fmt.Errorf("schema %s is invalid: %w", source, err)
		}
		if err := k.WriteSchema(ctx, typeName, data); err != nil {
			return fmt.Errorf("unable to save edited schema %q: %w", typeName, err)
		}
		return nil
	}

	if opts.Stream != nil && opts.Stream.IsPiped {
		pipedRaw, readErr := io.ReadAll(opts.Stream.In)
		if readErr != nil {
			return fmt.Errorf("unable to read piped input: %w", readErr)
		}
		if len(bytes.TrimSpace(pipedRaw)) > 0 {
			if bytes.Equal(pipedRaw, originalRaw) {
				return nil
			}
			return saveSchema(pipedRaw, "from stdin")
		}
	}

	tempPath, err := newEditorTempFilePath(t.Runtime, schemaEditorTempFilePrefix(k, typeName), ".schema.yaml")
	if err != nil {
		return fmt.Errorf("unable to create temp schema file path: %w", err)
	}
	initialRaw := ensureYAMLSchemaModeline(originalRaw, schemaDefinitionSchemaModeline)
	if err := t.Runtime.WriteFile(tempPath, initialRaw, 0o600); err != nil {
		return fmt.Errorf("unable to write temp schema file: %w", err)
	}
	defer func() {
		_ = t.Runtime.Remove(tempPath, false)
	}()

	if err := editWithLiveSaves(ctx, t.Runtime, tempPath, nil, func(editedRaw []byte) error {
		if bytes.Equal(editedRaw, originalRaw) {
			return nil
		}
		return saveSchema(editedRaw, "after editing")
	}); err != nil {
		return fmt.Errorf("unable to edit schema %q: %w", typeName, err)
	}
	return nil
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

func validateEditedSchema(typeName string, data []byte) error {
	parsed, err := keg.ParseSchemaDefinition(data)
	if err != nil {
		return err
	}
	declared := strings.TrimSpace(parsed.Type)
	if declared == "" {
		return fmt.Errorf("schema must declare type %q: %w", typeName, keg.ErrInvalid)
	}
	if declared != typeName {
		return fmt.Errorf("schema type %q does not match target type %q: %w", declared, typeName, keg.ErrInvalid)
	}
	return nil
}

func ensureYAMLSchemaModeline(data []byte, modeline string) []byte {
	if hasYAMLSchemaModeline(data) {
		return data
	}
	out := make([]byte, 0, len(modeline)+len(data))
	out = append(out, modeline...)
	out = append(out, data...)
	return out
}

func hasYAMLSchemaModeline(data []byte) bool {
	for _, line := range bytes.Split(data, []byte("\n")) {
		trimmed := bytes.TrimSpace(line)
		if len(trimmed) == 0 {
			continue
		}
		if bytes.HasPrefix(trimmed, []byte("# yaml-language-server: $schema=")) {
			return true
		}
		if bytes.HasPrefix(trimmed, []byte("#")) {
			continue
		}
		return false
	}
	return false
}

func schemaEditorTempFilePrefix(k keg.Keg, typeName string) string {
	namespace, kegName := schemaEditorTempNameParts(k)
	return fmt.Sprintf("tap-schema-edit-%s-%s-%s-",
		sanitizeEditorTempSegment(namespace, "unknown"),
		sanitizeEditorTempSegment(kegName, "keg"),
		sanitizeEditorTempSegment(typeName, "schema"),
	)
}

func schemaEditorTempNameParts(k keg.Keg) (string, string) {
	namespace, kegName := logicalKegTempNameParts(k)
	if namespace != "local" || kegName != "keg" || k == nil || k.Target() == nil {
		return namespace, kegName
	}

	file := strings.TrimSpace(k.Target().File)
	if file == "" {
		return namespace, kegName
	}
	clean := filepath.Clean(file)
	name := strings.TrimSpace(filepath.Base(clean))
	parent := strings.TrimSpace(filepath.Base(filepath.Dir(clean)))
	if strings.HasPrefix(parent, "@") && len(parent) > 1 && name != "" && name != "." {
		return strings.TrimPrefix(parent, "@"), name
	}
	return namespace, kegName
}

func readAllSchemaInput(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("schema input is required")
	}
	return io.ReadAll(r)
}
