package keg

import (
	"context"
	"fmt"
	"sort"
	"strings"
)

// SchemaSetValidationError reports every node violating a proposed strict
// schema/config state.
type SchemaSetValidationError struct {
	Results []SchemaValidationResult `json:"results"`
}

func (e *SchemaSetValidationError) Error() string {
	if e == nil || len(e.Results) == 0 {
		return ErrSchemaInvalid.Error()
	}
	details := make([]string, 0, len(e.Results))
	for _, result := range e.Results {
		details = append(details, (&SchemaValidationError{
			NodeID: result.NodeID,
			Type:   result.Type,
			Issues: result.Issues,
		}).Error())
	}
	return fmt.Sprintf("strict schema validation failed for %d node(s): %s", len(e.Results), strings.Join(details, "; "))
}
func (e *SchemaSetValidationError) Unwrap() error { return ErrSchemaInvalid }

type schemaOverlayStore struct {
	base    RepositorySchemas
	replace map[string][]byte
	deleted map[string]bool
}

func (s *schemaOverlayStore) ListSchemas(ctx context.Context) ([]string, error) {
	base, err := s.base.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, name := range base {
		if !s.deleted[name] {
			set[name] = true
		}
	}
	for name := range s.replace {
		if !s.deleted[name] {
			set[name] = true
		}
	}
	out := make([]string, 0, len(set))
	for name := range set {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, nil
}
func (s *schemaOverlayStore) ReadSchema(ctx context.Context, name string) ([]byte, error) {
	if s.deleted[name] {
		return nil, ErrNotExist
	}
	if data, ok := s.replace[name]; ok {
		return cloneBytes(data), nil
	}
	return s.base.ReadSchema(ctx, name)
}
func (s *schemaOverlayStore) WriteSchema(context.Context, string, []byte) error {
	return ErrNotSupported
}
func (s *schemaOverlayStore) CreateSchema(context.Context, string, []byte) error {
	return ErrNotSupported
}
func (s *schemaOverlayStore) DeleteSchema(context.Context, string) error { return ErrNotSupported }

func (k *LocalKeg) strictEnabled(ctx context.Context) bool {
	cfg, err := k.Repo.ReadConfig(ctx)
	return err == nil && cfg != nil && cfg.SchemaPolicy != nil && cfg.SchemaPolicy.Strict
}

func (k *LocalKeg) validateCompleteStrict(ctx context.Context, store RepositorySchemas, proposed map[NodeId]*NodeData) error {
	ids, err := k.Repo.ListNodes(ctx)
	if err != nil {
		return err
	}
	invalid := make([]SchemaValidationResult, 0)
	for _, id := range ids {
		node := proposed[id]
		if node == nil {
			exists, err := k.nodeExistsWithContent(ctx, id)
			if err != nil {
				return err
			}
			if !exists {
				continue
			}
			node, err = k.getNode(ctx, id)
			if err != nil {
				return err
			}
		}
		result, err := k.validateNodeDataWithSchemaPolicy(ctx, id, node, store, true)
		if err != nil {
			return err
		}
		if result != nil && !result.Valid {
			invalid = append(invalid, *result)
		}
	}
	if len(invalid) > 0 {
		return &SchemaSetValidationError{Results: invalid}
	}
	return nil
}

func (k *LocalKeg) validateStrictSchemaChange(ctx context.Context, store RepositorySchemas, replace map[string][]byte, deleted map[string]bool) error {
	if !k.strictEnabled(ctx) {
		return nil
	}
	return k.validateCompleteStrict(ctx, &schemaOverlayStore{base: store, replace: replace, deleted: deleted}, nil)
}
