package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
)

func listedToolSchemas(t *testing.T) map[string]map[string]any {
	t.Helper()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	out := make(map[string]map[string]any, len(result.Tools))
	for _, tool := range result.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema map[string]any
		require.NoError(t, json.Unmarshal(raw, &schema))
		out[tool.Name] = schema
	}
	return out
}

func resolveSchemaRef(t *testing.T, root, schema map[string]any) map[string]any {
	t.Helper()
	ref, _ := schema["$ref"].(string)
	if ref == "" {
		return schema
	}
	require.True(t, strings.HasPrefix(ref, "#/"), "unsupported schema ref %q", ref)
	var current any = root
	for _, raw := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		part := strings.ReplaceAll(strings.ReplaceAll(raw, "~1", "/"), "~0", "~")
		mapping, ok := current.(map[string]any)
		require.True(t, ok, "schema ref %q traversed a non-object", ref)
		current, ok = mapping[part]
		require.True(t, ok, "schema ref %q is missing %q", ref, part)
	}
	resolved, ok := current.(map[string]any)
	require.True(t, ok, "schema ref %q did not resolve to an object", ref)
	return resolved
}

func schemaProperty(t *testing.T, root, schema map[string]any, name string) map[string]any {
	t.Helper()
	schema = resolveSchemaRef(t, root, schema)
	properties, ok := schema["properties"].(map[string]any)
	require.True(t, ok, "schema has no properties")
	property, ok := properties[name].(map[string]any)
	require.True(t, ok, "schema has no %q property", name)
	return resolveSchemaRef(t, root, property)
}

func schemaArrayItem(t *testing.T, root map[string]any, property string) map[string]any {
	t.Helper()
	array := schemaProperty(t, root, root, property)
	items, ok := array["items"].(map[string]any)
	require.True(t, ok, "%q has no item schema", property)
	return resolveSchemaRef(t, root, items)
}

func requireSchemaField(t *testing.T, schema map[string]any, field string, required bool) {
	t.Helper()
	values, _ := schema["required"].([]any)
	if required {
		require.Contains(t, values, field)
		return
	}
	require.NotContains(t, values, field)
}

func TestMCP_MutationSchemasRequireExpectedHashesAtResourceLocation(t *testing.T) {
	t.Parallel()
	schemas := listedToolSchemas(t)

	for _, tool := range []string{
		"keg_settings_edit", "node_move", "schema_edit", "schema_delete", "flight_edit", "flight_delete",
	} {
		schema, ok := schemas[tool]
		require.True(t, ok, "missing tool %q", tool)
		requireSchemaField(t, schema, "expected_hash", true)
	}

	deleteItem := schemaArrayItem(t, schemas["node_delete"], "nodes")
	requireSchemaField(t, deleteItem, "expected_hash", true)

	// node_edit guards each part with its own token. Which token is required
	// depends on which part is supplied, so neither is schema-required; the
	// handler rejects a supplied part without its token. The combined
	// expected_hash is gone from node_edit entirely.
	editItem := schemaArrayItem(t, schemas["node_edit"], "nodes")
	editProps, ok := editItem["properties"].(map[string]any)
	require.True(t, ok)
	require.NotContains(t, editProps, "expected_hash")
	require.Contains(t, editProps, "expected_content_hash")
	require.Contains(t, editProps, "expected_meta_hash")
	requireSchemaField(t, editItem, "expected_content_hash", false)
	requireSchemaField(t, editItem, "expected_meta_hash", false)

	// create allocates ids, so there is no prior revision to guard and no
	// expected_hash anywhere in its schema.
	createItem := schemaArrayItem(t, schemas["node_create"], "nodes")
	requireSchemaField(t, createItem, "expected_hash", false)
	requireSchemaField(t, schemas["node_create"], "expected_hash", false)
}

func TestMCP_MutationDescriptionsTeachReadMergeRetryProtocol(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	wants := map[string]string{
		"node_edit": "node_read", "node_delete": "node_read", "node_move": "node_read",
		"keg_settings_edit": "keg_settings_read", "schema_edit": "schema_read",
		"schema_delete": "schema_read", "flight_edit": "flight_read", "flight_delete": "flight_read",
	}
	seen := map[string]bool{}
	for _, tool := range result.Tools {
		read, ok := wants[tool.Name]
		if !ok {
			continue
		}
		seen[tool.Name] = true
		require.Contains(t, tool.Description, read)
		require.Contains(t, strings.ToLower(tool.Description), "conflict")
		require.Contains(t, strings.ToLower(tool.Description), "current hash")
	}
	for tool := range wants {
		require.True(t, seen[tool], "missing tool description for %q", tool)
	}
}

// TestMCP_FlightIsExplainedOnceOnTheParameter pins where flight selection is
// documented. schemaWithFlight adds `flight` to every authority-bearing tool;
// an injected property nothing describes reads as absent (an agent once
// reported it missing from keg_create), so the property carries its own
// description. Tool descriptions do not repeat it: the same sentence on every
// tool buried each tool's own text, and the operating rules explain it once.
func TestMCP_FlightIsExplainedOnceOnTheParameter(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	checked := 0
	for _, tool := range result.Tools {
		require.NotEmptyf(t, strings.TrimSpace(tool.Description), "tool %q has no description", tool.Name)
		require.NotContainsf(t, tool.Description, "Accepts an optional flight",
			"tool %q repeats the flight explanation its parameter carries", tool.Name)
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var object struct {
			Properties map[string]struct {
				Description string `json:"description"`
			} `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(schema, &object))
		flight, ok := object.Properties["flight"]
		if !ok {
			continue
		}
		checked++
		require.Containsf(t, flight.Description, "Omit it",
			"tool %q: the flight property must say what omitting it does", tool.Name)
		require.NotContainsf(t, tool.Description, flight.Description,
			"tool %q repeats its flight property's description", tool.Name)
	}
	require.Greater(t, checked, 0, "no tool exposed a flight property; the injection may have broken")

	instructions := session.InitializeResult().Instructions
	require.Contains(t, instructions, "`flight`", "the operating rules must explain the flight argument once")
}

// TestMCP_WriteToolsStateTheContentContract keeps the two rules an agent
// previously had to discover by triggering them in the descriptions themselves.
func TestMCP_WriteToolsStateTheContentContract(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	byName := map[string]string{}
	for _, tool := range result.Tools {
		byName[tool.Name] = strings.ToLower(tool.Description)
	}

	for _, name := range []string{"node_create", "node_edit"} {
		desc, ok := byName[name]
		require.Truef(t, ok, "missing tool %q", name)
		require.Containsf(t, desc, "frontmatter",
			"%q does not say content must not begin with a frontmatter block", name)
	}
	for _, want := range []string{"expected_content_hash", "expected_meta_hash", "never invalidates"} {
		require.Containsf(t, byName["node_edit"], want,
			"edit does not state that each part of a node has its own hash (%q)", want)
	}
	require.Contains(t, byName["node_read"], "content_hash")
	require.Contains(t, byName["node_read"], "meta_hash")
}

// TestMCP_SchemaSelectionFieldsAreConstrained pins the schema field's rule in
// the advertised input schemas: the same pattern and length bound
// keg.ValidSchemaTypeName enforces, so a client or the SDK refuses a sentence
// before any backend sees it. The guard tokens stay optional.
func TestMCP_SchemaSelectionFieldsAreConstrained(t *testing.T) {
	t.Parallel()
	schemas := listedToolSchemas(t)

	fields := map[string]map[string]any{
		"node_edit":   schemaProperty(t, schemas["node_edit"], schemaArrayItem(t, schemas["node_edit"], "nodes"), "schema"),
		"node_create": schemaProperty(t, schemas["node_create"], schemaArrayItem(t, schemas["node_create"], "nodes"), "schema"),
		"node_merge":  schemaProperty(t, schemas["node_merge"], schemas["node_merge"], "schema"),
	}
	for tool, field := range fields {
		require.Equal(t, keg.SchemaTypeNamePattern, field["pattern"], tool)
		require.EqualValues(t, keg.SchemaTypeNameMaxLength, field["maxLength"], tool)
	}

	editItem := schemaArrayItem(t, schemas["node_edit"], "nodes")
	requireSchemaField(t, editItem, "schema", false)
	requireSchemaField(t, editItem, "expected_content_hash", false)
	requireSchemaField(t, editItem, "expected_meta_hash", false)
}
