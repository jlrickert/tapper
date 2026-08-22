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
	return k.CreateSchema(ctx, typeName, opts.Data)
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
		return k.ValidateNodes(ctx, keg.ValidateNodesOptions{})
	} else {
		type group struct {
			k         keg.Keg
			ids       []keg.NodeId
			positions []int
		}
		groups := map[string]*group{}
		order := []string{}
		for _, raw := range opts.NodeIDs {
			var id keg.NodeId
			resolved, id, resolveErr := t.resolveNodeArg(ctx, k, raw)
			err = resolveErr
			if err != nil {
				return nil, err
			}
			key := describeKeg(resolved)
			g := groups[key]
			if g == nil {
				g = &group{k: resolved}
				groups[key] = g
				order = append(order, key)
			}
			g.ids = append(g.ids, id)
			g.positions = append(g.positions, len(ids))
			ids = append(ids, id)
		}
		results := make([]keg.SchemaValidationResult, len(ids))
		for _, key := range order {
			g := groups[key]
			batch, batchErr := g.k.ValidateNodes(ctx, keg.ValidateNodesOptions{NodeIDs: g.ids})
			if batchErr != nil {
				return nil, batchErr
			}
			for i, result := range batch {
				results[g.positions[i]] = result
			}
		}
		return results, nil
	}
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

func (t *Tap) warnSchemaValidation(result *keg.SchemaValidationResult, id keg.NodeId, stream *toolkit.Stream) {
	if result == nil || result.Valid {
		return
	}
	if stream == nil && t != nil && t.Runtime != nil {
		stream = t.Runtime.Stream()
	}
	if stream == nil || stream.Err == nil {
		return
	}
	for _, issue := range result.Issues {
		message := issue.Message
		if issue.Field != "" {
			message = issue.Field + ": " + message
		}
		_, _ = fmt.Fprintf(stream.Err, "warning: node %s schema: %s\n", id.Path(), message)
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
