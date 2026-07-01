package keg

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"gopkg.in/yaml.v3"
)

const (
	SchemasDir       = "schemas"
	SchemaFileSuffix = ".schema.yaml"

	// KegSchemaDefinitionSchemaURL is the public JSON Schema used by editor
	// modelines for keg schema definition YAML.
	KegSchemaDefinitionSchemaURL = "https://raw.githubusercontent.com/jlrickert/tapper/main/schemas/keg-schema-definition.json"

	schemaActorHeader = "Tapper-Schema-Actor"
	schemaModeHeader  = "Tapper-Schema-Mode"
)

type ValidationActor string

const (
	ValidationActorHuman   ValidationActor = "human"
	ValidationActorAgent   ValidationActor = "agent"
	ValidationActorAPI     ValidationActor = "api"
	ValidationActorImport  ValidationActor = "import"
	ValidationActorRestore ValidationActor = "restore"
)

type ValidationMode string

const (
	ValidationModeAuto  ValidationMode = ""
	ValidationModeOff   ValidationMode = "off"
	ValidationModeWarn  ValidationMode = "warn"
	ValidationModeBlock ValidationMode = "block"
)

type schemaActorContextKey struct{}
type schemaModeContextKey struct{}

func WithValidationActor(ctx context.Context, actor ValidationActor) context.Context {
	if actor == "" {
		return ctx
	}
	return context.WithValue(ctx, schemaActorContextKey{}, actor)
}

func WithValidationMode(ctx context.Context, mode ValidationMode) context.Context {
	if mode == "" {
		return ctx
	}
	return context.WithValue(ctx, schemaModeContextKey{}, mode)
}

func ValidationActorFromContext(ctx context.Context) ValidationActor {
	if ctx == nil {
		return ""
	}
	actor, _ := ctx.Value(schemaActorContextKey{}).(ValidationActor)
	return actor
}

func ValidationModeFromContext(ctx context.Context) ValidationMode {
	if ctx == nil {
		return ""
	}
	mode, _ := ctx.Value(schemaModeContextKey{}).(ValidationMode)
	return mode
}

func ValidationContextFromHeaders(ctx context.Context, header func(string) string) context.Context {
	if header == nil {
		return ctx
	}
	if actor := ValidationActor(strings.TrimSpace(header(schemaActorHeader))); actor != "" {
		ctx = WithValidationActor(ctx, actor)
	}
	if mode := ValidationMode(strings.TrimSpace(header(schemaModeHeader))); mode != "" {
		ctx = WithValidationMode(ctx, mode)
	}
	return ctx
}

func ValidationHeaderValues(ctx context.Context) map[string]string {
	out := map[string]string{}
	if actor := ValidationActorFromContext(ctx); actor != "" {
		out[schemaActorHeader] = string(actor)
	}
	if mode := ValidationModeFromContext(ctx); mode != "" {
		out[schemaModeHeader] = string(mode)
	}
	return out
}

type SchemaPolicy struct {
	Default ValidationMode `yaml:"default,omitempty" json:"default,omitempty"`
	Human   ValidationMode `yaml:"human,omitempty" json:"human,omitempty"`
	Agent   ValidationMode `yaml:"agent,omitempty" json:"agent,omitempty"`
	API     ValidationMode `yaml:"api,omitempty" json:"api,omitempty"`
	Import  ValidationMode `yaml:"import,omitempty" json:"import,omitempty"`
	Restore ValidationMode `yaml:"restore,omitempty" json:"restore,omitempty"`
}

type SchemaDefinition struct {
	Version  int            `yaml:"version,omitempty" json:"version,omitempty"`
	Type     string         `yaml:"type,omitempty" json:"type,omitempty"`
	Summary  string         `yaml:"summary,omitempty" json:"summary,omitempty"`
	Meta     map[string]any `yaml:"meta,omitempty" json:"meta,omitempty"`
	Markdown MarkdownSchema `yaml:"markdown,omitempty" json:"markdown,omitempty"`
	// Maturity is deprecated legacy compatibility. Prefer property-scoped
	// metadata maturity rows under meta.properties.<property>.maturity.
	Maturity  []MetadataMaturitySchema `yaml:"maturity,omitempty" json:"maturity,omitempty"`
	Relations []RelationSchema         `yaml:"relations,omitempty" json:"relations,omitempty"`
}

type MarkdownSchema struct {
	RequireTitle bool              `yaml:"requireTitle,omitempty" json:"requireTitle,omitempty"`
	Ordered      bool              `yaml:"ordered,omitempty" json:"ordered,omitempty"`
	Sections     []MarkdownSection `yaml:"sections,omitempty" json:"sections,omitempty"`
}

type MarkdownSection struct {
	Heading  string `yaml:"heading" json:"heading"`
	Level    int    `yaml:"level,omitempty" json:"level,omitempty"`
	Required bool   `yaml:"required,omitempty" json:"required,omitempty"`
}

const (
	RelationDirectionLinks     = "links"
	RelationDirectionBacklinks = "backlinks"
)

type RelationSchema struct {
	Name        string                 `yaml:"name,omitempty" json:"name,omitempty"`
	Type        string                 `yaml:"type,omitempty" json:"type,omitempty"`
	Description string                 `yaml:"description,omitempty" json:"description,omitempty"`
	Required    bool                   `yaml:"required,omitempty" json:"required,omitempty"`
	Maturity    []MaturityWeightSchema `yaml:"maturity,omitempty" json:"maturity,omitempty"`
}

type MaturityWeightSchema struct {
	Direction string             `yaml:"direction,omitempty" json:"direction,omitempty"`
	Attribute string             `yaml:"attribute,omitempty" json:"attribute,omitempty"`
	Weight    float64            `yaml:"weight,omitempty" json:"weight,omitempty"`
	Enum      map[string]float64 `yaml:"enum,omitempty" json:"enum,omitempty"`
}

type MetadataMaturitySchema struct {
	Attribute string             `yaml:"attribute,omitempty" json:"attribute,omitempty"`
	Weight    float64            `yaml:"weight,omitempty" json:"weight,omitempty"`
	Enum      map[string]float64 `yaml:"enum,omitempty" json:"enum,omitempty"`
}

type ValidationIssue struct {
	Level   string `json:"level" yaml:"level"`
	Field   string `json:"field,omitempty" yaml:"field,omitempty"`
	Message string `json:"message" yaml:"message"`
}

type SchemaValidationResult struct {
	NodeID string            `json:"node_id,omitempty" yaml:"node_id,omitempty"`
	Type   string            `json:"type,omitempty" yaml:"type,omitempty"`
	Valid  bool              `json:"valid" yaml:"valid"`
	Issues []ValidationIssue `json:"issues,omitempty" yaml:"issues,omitempty"`
}

type NodeValidationPayload struct {
	ID         NodeId
	Content    []byte
	HasContent bool
	Meta       []byte
	HasMeta    bool
}

type SchemaValidationError struct {
	NodeID string
	Type   string
	Issues []ValidationIssue
}

func (e *SchemaValidationError) Error() string {
	if e == nil {
		return ErrSchemaInvalid.Error()
	}
	parts := make([]string, 0, len(e.Issues))
	for _, issue := range e.Issues {
		if issue.Field != "" {
			parts = append(parts, issue.Field+": "+issue.Message)
		} else {
			parts = append(parts, issue.Message)
		}
	}
	if len(parts) == 0 {
		return ErrSchemaInvalid.Error()
	}
	where := "node"
	if e.NodeID != "" {
		where += " " + e.NodeID
	}
	return fmt.Sprintf("%s schema validation failed: %s", where, strings.Join(parts, "; "))
}

func (e *SchemaValidationError) Unwrap() error { return ErrSchemaInvalid }

func IsSchemaInvalid(err error) bool { return errors.Is(err, ErrSchemaInvalid) }

func ParseSchemaDefinition(data []byte) (*SchemaDefinition, error) {
	var schema SchemaDefinition
	if err := yaml.Unmarshal(data, &schema); err != nil {
		return nil, fmt.Errorf("parse schema yaml: %w", err)
	}
	return &schema, nil
}

func validateSchemaDefinitionForType(typeName string, data []byte) (*SchemaDefinition, error) {
	typeName = strings.TrimSpace(typeName)
	if err := ValidSchemaTypeName(typeName); err != nil {
		return nil, err
	}
	parsed, err := ParseSchemaDefinition(data)
	if err != nil {
		return nil, err
	}
	if err := validateSchemaDefinitionYAMLShape(data); err != nil {
		return nil, err
	}
	if parsed.Type != "" && parsed.Type != typeName {
		return nil, fmt.Errorf("schema type %q does not match target type %q: %w", parsed.Type, typeName, ErrInvalid)
	}
	if len(parsed.Meta) > 0 {
		if _, err := resolveJSONSchema(parsed.Meta); err != nil {
			return nil, fmt.Errorf("invalid meta json schema: %w", err)
		}
	}
	if err := validateMetadataMaturitySchemas(parsed.Maturity); err != nil {
		return nil, err
	}
	if _, err := nestedMetadataMaturitySchemas(parsed.Meta); err != nil {
		return nil, err
	}
	if err := validateRelationSchemas(parsed.Relations); err != nil {
		return nil, err
	}
	return parsed, nil
}

func (s *SchemaDefinition) MetadataMaturityWeights() []MetadataMaturitySchema {
	if s == nil {
		return nil
	}
	weights := append([]MetadataMaturitySchema(nil), s.Maturity...)
	nested, err := nestedMetadataMaturitySchemas(s.Meta)
	if err != nil {
		return weights
	}
	return append(weights, nested...)
}

func validateMetadataMaturitySchemas(weights []MetadataMaturitySchema) error {
	for i, maturity := range weights {
		prefix := fmt.Sprintf("maturity %d", i+1)
		if err := validateMaturityWeight(prefix, maturity.Attribute, maturity.Weight, maturity.Enum); err != nil {
			return err
		}
	}
	return nil
}

func nestedMetadataMaturitySchemas(meta map[string]any) ([]MetadataMaturitySchema, error) {
	root, ok := schemaStringMap(meta)
	if !ok || len(root) == 0 {
		return nil, nil
	}
	props, ok := schemaStringMap(root["properties"])
	if !ok || len(props) == 0 {
		return nil, nil
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []MetadataMaturitySchema
	for _, name := range names {
		prop, ok := schemaStringMap(props[name])
		if !ok {
			continue
		}
		rawMaturity, ok := prop["maturity"]
		if !ok {
			continue
		}
		items, ok := schemaAnySlice(rawMaturity)
		if !ok {
			return nil, fmt.Errorf("meta property %q maturity must be a list: %w", name, ErrInvalid)
		}
		for i, rawItem := range items {
			prefix := fmt.Sprintf("meta property %q maturity %d", name, i+1)
			item, ok := schemaStringMap(rawItem)
			if !ok {
				return nil, fmt.Errorf("%s must be a mapping: %w", prefix, ErrInvalid)
			}
			for key := range item {
				switch key {
				case "weight", "enum":
				default:
					return nil, fmt.Errorf("%s has unsupported field %q: %w", prefix, key, ErrInvalid)
				}
			}
			weight, ok, err := schemaFloat(item["weight"])
			if err != nil {
				return nil, fmt.Errorf("%s has invalid weight: %w", prefix, ErrInvalid)
			}
			if !ok || math.IsNaN(weight) || math.IsInf(weight, 0) || weight <= 0 {
				return nil, fmt.Errorf("%s requires a positive weight: %w", prefix, ErrInvalid)
			}
			enum, err := schemaEnumScores(item["enum"], prefix, true)
			if err != nil {
				return nil, err
			}
			out = append(out, MetadataMaturitySchema{Attribute: name, Weight: weight, Enum: enum})
		}
	}
	return out, nil
}

func validateRelationSchemas(relations []RelationSchema) error {
	for i, rel := range relations {
		for j, maturity := range rel.Maturity {
			prefix := fmt.Sprintf("relation %d maturity %d", i+1, j+1)
			if maturity.Direction != "" {
				if _, ok := normalizeRelationDirection(maturity.Direction); !ok {
					return fmt.Errorf("%s has invalid direction %q: %w", prefix, maturity.Direction, ErrInvalid)
				}
			}
			if err := validateMaturityWeight(prefix, maturity.Attribute, maturity.Weight, maturity.Enum); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateMaturityWeight(prefix, rawAttribute string, weight float64, enum map[string]float64) error {
	if math.IsNaN(weight) || math.IsInf(weight, 0) || weight < 0 {
		return fmt.Errorf("%s has invalid weight %v: %w", prefix, weight, ErrInvalid)
	}
	attribute := strings.TrimSpace(rawAttribute)
	if weight > 0 && attribute == "" {
		return fmt.Errorf("%s weight requires an attribute: %w", prefix, ErrInvalid)
	}
	if attribute != "" && weight <= 0 {
		return fmt.Errorf("%s attribute requires a positive weight: %w", prefix, ErrInvalid)
	}
	if len(enum) > 0 && attribute == "" {
		return fmt.Errorf("%s enum scoring requires an attribute: %w", prefix, ErrInvalid)
	}
	for value, score := range enum {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s enum scoring has an empty value: %w", prefix, ErrInvalid)
		}
		if math.IsNaN(score) || math.IsInf(score, 0) {
			return fmt.Errorf("%s enum value %q has invalid score %v: %w", prefix, value, score, ErrInvalid)
		}
	}
	return nil
}

func schemaStringMap(value any) (map[string]any, bool) {
	switch v := value.(type) {
	case map[string]any:
		return v, true
	case map[any]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			keyString, ok := key.(string)
			if !ok {
				return nil, false
			}
			out[keyString] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func schemaAnySlice(value any) ([]any, bool) {
	switch v := value.(type) {
	case []any:
		return v, true
	default:
		return nil, false
	}
}

func schemaFloat(value any) (float64, bool, error) {
	switch v := value.(type) {
	case nil:
		return 0, false, nil
	case int:
		return float64(v), true, nil
	case int8:
		return float64(v), true, nil
	case int16:
		return float64(v), true, nil
	case int32:
		return float64(v), true, nil
	case int64:
		return float64(v), true, nil
	case uint:
		return float64(v), true, nil
	case uint8:
		return float64(v), true, nil
	case uint16:
		return float64(v), true, nil
	case uint32:
		return float64(v), true, nil
	case uint64:
		return float64(v), true, nil
	case float32:
		return float64(v), true, nil
	case float64:
		return v, true, nil
	case json.Number:
		f, err := v.Float64()
		return f, true, err
	default:
		return 0, true, fmt.Errorf("unsupported numeric type %T", value)
	}
}

func schemaEnumScores(value any, prefix string, requireUnitRange bool) (map[string]float64, error) {
	if value == nil {
		return nil, nil
	}
	raw, ok := schemaStringMap(value)
	if !ok {
		return nil, fmt.Errorf("%s enum scoring must be a mapping: %w", prefix, ErrInvalid)
	}
	out := make(map[string]float64, len(raw))
	for value, rawScore := range raw {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s enum scoring has an empty value: %w", prefix, ErrInvalid)
		}
		score, ok, err := schemaFloat(rawScore)
		if err != nil || !ok || math.IsNaN(score) || math.IsInf(score, 0) {
			return nil, fmt.Errorf("%s enum value %q has invalid score: %w", prefix, value, ErrInvalid)
		}
		if requireUnitRange && (score < 0 || score > 1) {
			return nil, fmt.Errorf("%s enum value %q score must be between 0 and 1: %w", prefix, value, ErrInvalid)
		}
		out[value] = score
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func validateSchemaDefinitionYAMLShape(data []byte) error {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return err
	}
	if len(doc.Content) == 0 {
		return nil
	}
	root := doc.Content[0]
	if root == nil || root.Kind != yaml.MappingNode {
		return nil
	}
	relationFields := map[string]bool{
		"name":        true,
		"type":        true,
		"description": true,
		"required":    true,
		"maturity":    true,
	}
	maturityFields := map[string]bool{
		"direction": true,
		"attribute": true,
		"weight":    true,
		"enum":      true,
	}
	metadataMaturityFields := map[string]bool{
		"attribute": true,
		"weight":    true,
		"enum":      true,
	}

	if maturity := mappingValueInMapping(root, "maturity"); maturity != nil && maturity.Kind == yaml.SequenceNode {
		for i, weight := range maturity.Content {
			if weight == nil || weight.Kind != yaml.MappingNode {
				continue
			}
			for j := 0; j+1 < len(weight.Content); j += 2 {
				key := weight.Content[j]
				if key == nil || key.Kind != yaml.ScalarNode {
					continue
				}
				if !metadataMaturityFields[key.Value] {
					return fmt.Errorf("maturity %d has unsupported field %q: %w", i+1, key.Value, ErrInvalid)
				}
			}
		}
	}

	relations := mappingValueInMapping(root, "relations")
	if relations == nil || relations.Kind != yaml.SequenceNode {
		return nil
	}
	for i, rel := range relations.Content {
		if rel == nil || rel.Kind != yaml.MappingNode {
			continue
		}
		for j := 0; j+1 < len(rel.Content); j += 2 {
			key := rel.Content[j]
			if key == nil || key.Kind != yaml.ScalarNode {
				continue
			}
			if !relationFields[key.Value] {
				return fmt.Errorf("relation %d has unsupported field %q: %w", i+1, key.Value, ErrInvalid)
			}
		}
		maturity := mappingValueInMapping(rel, "maturity")
		if maturity == nil || maturity.Kind != yaml.SequenceNode {
			continue
		}
		for j, weight := range maturity.Content {
			if weight == nil || weight.Kind != yaml.MappingNode {
				continue
			}
			for k := 0; k+1 < len(weight.Content); k += 2 {
				key := weight.Content[k]
				if key == nil || key.Kind != yaml.ScalarNode {
					continue
				}
				if !maturityFields[key.Value] {
					return fmt.Errorf("relation %d maturity %d has unsupported field %q: %w", i+1, j+1, key.Value, ErrInvalid)
				}
			}
		}
	}
	return nil
}

func normalizeRelationDirection(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", RelationDirectionLinks, "link", "linked", "outgoing", "out":
		return RelationDirectionLinks, true
	case RelationDirectionBacklinks, "backlink", "backlinked", "incoming", "in":
		return RelationDirectionBacklinks, true
	default:
		return "", false
	}
}

func ValidSchemaTypeName(typeName string) error {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" || typeName == "." || typeName == ".." {
		return fmt.Errorf("schema type is required: %w", ErrInvalid)
	}
	if filepath.IsAbs(typeName) || strings.ContainsAny(typeName, "/\\\x00") || strings.Contains(typeName, "..") {
		return fmt.Errorf("invalid schema type %q: %w", typeName, ErrInvalid)
	}
	return nil
}

func SchemaFilename(typeName string) (string, error) {
	typeName = strings.TrimSpace(typeName)
	if err := ValidSchemaTypeName(typeName); err != nil {
		return "", err
	}
	return typeName + SchemaFileSuffix, nil
}

func repoSchemas(repo Repository) (RepositorySchemas, bool) {
	store, ok := repo.(RepositorySchemas)
	return store, ok
}

func (k *LocalKeg) ListSchemas(ctx context.Context) ([]string, error) {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return nil, ErrNotSupported
	}
	return store.ListSchemas(ctx)
}

func (k *LocalKeg) ReadSchema(ctx context.Context, typeName string) ([]byte, error) {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return nil, ErrNotSupported
	}
	return store.ReadSchema(ctx, typeName)
}

func (k *LocalKeg) WriteSchema(ctx context.Context, typeName string, data []byte) error {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	if _, err := validateSchemaDefinitionForType(typeName, data); err != nil {
		return err
	}
	return store.WriteSchema(ctx, typeName, data)
}

func (k *LocalKeg) DeleteSchema(ctx context.Context, typeName string) error {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return ErrNotSupported
	}
	return store.DeleteSchema(ctx, typeName)
}

func (k *LocalKeg) ValidateNode(ctx context.Context, id NodeId) (*SchemaValidationResult, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, err
	}
	node, err := k.getNode(ctx, id)
	if err != nil {
		return nil, err
	}
	return k.validateNodeData(ctx, id, node)
}

func (k *LocalKeg) ValidateNodePayload(ctx context.Context, payload NodeValidationPayload) (*SchemaValidationResult, error) {
	if err := k.checkKegExists(ctx); err != nil {
		return nil, err
	}
	id := payload.ID
	if !id.Valid() {
		return nil, fmt.Errorf("node id is required: %w", ErrInvalid)
	}

	contentBytes := payload.Content
	if !payload.HasContent {
		existing, err := k.Repo.ReadContent(ctx, id)
		if err != nil {
			return nil, err
		}
		contentBytes = existing
	}
	metaBytes := payload.Meta
	if !payload.HasMeta {
		existing, err := k.Repo.ReadMeta(ctx, id)
		if err != nil && !errors.Is(err, ErrNotExist) {
			return nil, err
		}
		metaBytes = existing
	}

	content, err := ParseContent(k.Runtime, contentBytes, MarkdownContentFilename)
	if err != nil {
		return nil, err
	}
	meta, err := ParseMeta(ctx, metaBytes)
	if err != nil {
		return nil, err
	}
	node := &NodeData{ID: id, Content: content, Meta: meta, Stats: &NodeStats{}}
	_ = node.updateMeta(ctx, k.Runtime, nil)
	return k.validateNodeData(ctx, id, node)
}

type schemaWriteOperation string

const (
	schemaWriteCreate  schemaWriteOperation = "create"
	schemaWriteUpdate  schemaWriteOperation = "update"
	schemaWriteImport  schemaWriteOperation = "import"
	schemaWriteRestore schemaWriteOperation = "restore"
)

func (k *LocalKeg) validateForWrite(ctx context.Context, op schemaWriteOperation, id NodeId, node *NodeData) error {
	result, err := k.validateNodeData(ctx, id, node)
	if err != nil {
		return err
	}
	return k.enforceSchemaValidationResult(ctx, op, result)
}

func (k *LocalKeg) validateForWriteWithSchemas(ctx context.Context, op schemaWriteOperation, id NodeId, node *NodeData, store RepositorySchemas) error {
	result, err := k.validateNodeDataWithSchemas(ctx, id, node, store)
	if err != nil {
		return err
	}
	return k.enforceSchemaValidationResult(ctx, op, result)
}

func (k *LocalKeg) enforceSchemaValidationResult(ctx context.Context, op schemaWriteOperation, result *SchemaValidationResult) error {
	if result == nil {
		return nil
	}
	if result.Valid {
		return nil
	}
	mode := k.effectiveValidationMode(ctx, op)
	switch mode {
	case ValidationModeOff, ValidationModeWarn:
		return nil
	default:
		return &SchemaValidationError{NodeID: result.NodeID, Type: result.Type, Issues: result.Issues}
	}
}

func (k *LocalKeg) effectiveValidationMode(ctx context.Context, op schemaWriteOperation) ValidationMode {
	if mode := normalizeValidationMode(ValidationModeFromContext(ctx)); mode != ValidationModeAuto {
		return mode
	}
	var policy *SchemaPolicy
	if cfg, err := k.Repo.ReadConfig(ctx); err == nil && cfg != nil {
		policy = cfg.SchemaPolicy
	}
	if policy != nil {
		switch op {
		case schemaWriteImport:
			if mode := normalizeValidationMode(policy.Import); mode != ValidationModeAuto {
				return mode
			}
		case schemaWriteRestore:
			if mode := normalizeValidationMode(policy.Restore); mode != ValidationModeAuto {
				return mode
			}
		}
		actor := ValidationActorFromContext(ctx)
		switch actor {
		case ValidationActorHuman:
			if mode := normalizeValidationMode(policy.Human); mode != ValidationModeAuto {
				return mode
			}
		case ValidationActorAgent:
			if mode := normalizeValidationMode(policy.Agent); mode != ValidationModeAuto {
				return mode
			}
		case ValidationActorAPI:
			if mode := normalizeValidationMode(policy.API); mode != ValidationModeAuto {
				return mode
			}
		}
		if mode := normalizeValidationMode(policy.Default); mode != ValidationModeAuto {
			return mode
		}
	}

	switch op {
	case schemaWriteImport, schemaWriteRestore:
		return ValidationModeBlock
	}
	switch ValidationActorFromContext(ctx) {
	case ValidationActorHuman:
		return ValidationModeWarn
	case ValidationActorAgent, ValidationActorAPI:
		return ValidationModeBlock
	default:
		return ValidationModeBlock
	}
}

func normalizeValidationMode(mode ValidationMode) ValidationMode {
	switch ValidationMode(strings.ToLower(strings.TrimSpace(string(mode)))) {
	case ValidationModeOff:
		return ValidationModeOff
	case ValidationModeWarn:
		return ValidationModeWarn
	case ValidationModeBlock:
		return ValidationModeBlock
	default:
		return ValidationModeAuto
	}
}

func (k *LocalKeg) validateNodeData(ctx context.Context, id NodeId, node *NodeData) (*SchemaValidationResult, error) {
	store, ok := repoSchemas(k.Repo)
	if !ok {
		return &SchemaValidationResult{NodeID: id.Path(), Valid: true}, nil
	}
	return k.validateNodeDataWithSchemas(ctx, id, node, store)
}

func (k *LocalKeg) validateNodeDataWithSchemas(ctx context.Context, id NodeId, node *NodeData, store RepositorySchemas) (*SchemaValidationResult, error) {
	result := &SchemaValidationResult{NodeID: id.Path(), Valid: true}
	if node == nil || store == nil {
		return result, nil
	}
	types, err := store.ListSchemas(ctx)
	if err != nil {
		return nil, err
	}
	if len(types) == 0 {
		return result, nil
	}

	typeName, hasType := nodeType(node)
	result.Type = typeName
	if !hasType || strings.TrimSpace(typeName) == "" {
		result.Issues = append(result.Issues, ValidationIssue{Level: "error", Field: "meta.type", Message: "missing required type"})
		result.Valid = false
		return result, nil
	}

	rawSchema, err := store.ReadSchema(ctx, typeName)
	if err != nil {
		if errors.Is(err, ErrNotExist) {
			result.Issues = append(result.Issues, ValidationIssue{Level: "error", Field: "meta.type", Message: fmt.Sprintf("unknown type %q", typeName)})
			result.Valid = false
			return result, nil
		}
		return nil, err
	}
	schema, err := ParseSchemaDefinition(rawSchema)
	if err != nil {
		return nil, err
	}
	if schema.Type != "" && schema.Type != typeName {
		result.Issues = append(result.Issues, ValidationIssue{Level: "error", Field: "schema.type", Message: fmt.Sprintf("schema declares type %q but node uses %q", schema.Type, typeName)})
	}
	result.Issues = append(result.Issues, validateMetaAgainstSchema(ctx, node.Meta, schema.Meta)...)
	result.Issues = append(result.Issues, validateMarkdownAgainstSchema(node.Content, schema.Markdown)...)
	result.Valid = len(result.Issues) == 0
	return result, nil
}

func nodeType(node *NodeData) (string, bool) {
	if node == nil || node.Meta == nil {
		return "", false
	}
	return node.Meta.Get("type")
}

func validateMetaAgainstSchema(ctx context.Context, meta *NodeMeta, rawSchema map[string]any) []ValidationIssue {
	if len(rawSchema) == 0 {
		return nil
	}
	resolved, err := resolveJSONSchema(rawSchema)
	if err != nil {
		return []ValidationIssue{{Level: "error", Field: "schema.meta", Message: err.Error()}}
	}
	data, err := nodeMetaJSONValue(ctx, meta)
	if err != nil {
		return []ValidationIssue{{Level: "error", Field: "meta", Message: err.Error()}}
	}
	if err := resolved.Validate(data); err != nil {
		return []ValidationIssue{{Level: "error", Field: "meta", Message: err.Error()}}
	}
	return nil
}

func resolveJSONSchema(raw map[string]any) (*jsonschema.Resolved, error) {
	normalized := normalizeYAMLJSON(raw)
	data, err := json.Marshal(normalized)
	if err != nil {
		return nil, err
	}
	var schema jsonschema.Schema
	if err := json.Unmarshal(data, &schema); err != nil {
		return nil, err
	}
	return schema.Resolve(nil)
}

func nodeMetaJSONValue(ctx context.Context, meta *NodeMeta) (map[string]any, error) {
	if meta == nil {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := yaml.Unmarshal([]byte(meta.ToYAML()), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = map[string]any{}
	}
	if tags := meta.Tags(); len(tags) > 0 {
		out["tags"] = tags
	}
	_ = ctx
	return normalizeYAMLJSON(out).(map[string]any), nil
}

func normalizeYAMLJSON(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[k] = normalizeYAMLJSON(v)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for k, v := range x {
			out[fmt.Sprint(k)] = normalizeYAMLJSON(v)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, v := range x {
			out[i] = normalizeYAMLJSON(v)
		}
		return out
	default:
		return x
	}
}

func validateMarkdownAgainstSchema(content *NodeContent, schema MarkdownSchema) []ValidationIssue {
	if content == nil {
		return []ValidationIssue{{Level: "error", Field: "markdown", Message: "missing markdown content"}}
	}
	var issues []ValidationIssue
	if schema.RequireTitle && strings.TrimSpace(content.Title) == "" {
		issues = append(issues, ValidationIssue{Level: "error", Field: "markdown.title", Message: "missing H1 title"})
	}
	if len(schema.Sections) == 0 {
		return issues
	}
	headings := markdownHeadings(content.Body)
	positions := make(map[int]int, len(schema.Sections))
	for sectionIdx, section := range schema.Sections {
		section.Heading = strings.TrimSpace(section.Heading)
		if section.Heading == "" {
			continue
		}
		for headingIdx, heading := range headings {
			if section.Level > 0 && heading.Level != section.Level {
				continue
			}
			if strings.EqualFold(heading.Text, section.Heading) {
				positions[sectionIdx] = headingIdx
				break
			}
		}
		if section.Required {
			if _, ok := positions[sectionIdx]; !ok {
				issues = append(issues, ValidationIssue{Level: "error", Field: "markdown.sections", Message: fmt.Sprintf("missing required section %q", section.Heading)})
			}
		}
	}
	if schema.Ordered {
		last := -1
		for i, section := range schema.Sections {
			pos, ok := positions[i]
			if !ok {
				continue
			}
			if pos < last {
				issues = append(issues, ValidationIssue{Level: "error", Field: "markdown.sections", Message: fmt.Sprintf("section %q appears out of order", section.Heading)})
				break
			}
			last = pos
		}
	}
	return issues
}

type markdownHeading struct {
	Level int
	Text  string
}

func markdownHeadings(body string) []markdownHeading {
	var headings []markdownHeading
	scanner := bufio.NewScanner(strings.NewReader(body))
	inFence := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "```") || strings.HasPrefix(line, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence || !strings.HasPrefix(line, "#") {
			continue
		}
		level := 0
		for level < len(line) && level < 6 && line[level] == '#' {
			level++
		}
		if level == 0 || level >= len(line) || line[level] != ' ' {
			continue
		}
		text := strings.TrimSpace(line[level:])
		text = strings.TrimSpace(strings.TrimRight(text, "#"))
		if text == "" {
			continue
		}
		headings = append(headings, markdownHeading{Level: level, Text: text})
	}
	return headings
}

func schemaTypesFromFiles(names []string) []string {
	out := make([]string, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if !strings.HasSuffix(name, SchemaFileSuffix) {
			continue
		}
		typeName := strings.TrimSuffix(name, SchemaFileSuffix)
		if typeName != "" {
			out = append(out, typeName)
		}
	}
	sort.Strings(out)
	return out
}

func schemaTypeFilesFromMap(m map[string][]byte) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return schemaTypesFromFiles(names)
}
