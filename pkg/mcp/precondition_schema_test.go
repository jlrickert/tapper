package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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
		"keg_settings_edit", "move", "schema_edit", "schema_delete", "flight_edit", "flight_delete",
	} {
		schema, ok := schemas[tool]
		require.True(t, ok, "missing tool %q", tool)
		requireSchemaField(t, schema, "expected_hash", true)
	}

	for tool, array := range map[string]string{
		"edit": "edits", "remove": "nodes",
	} {
		root, ok := schemas[tool]
		require.True(t, ok, "missing tool %q", tool)
		requireSchemaField(t, schemaArrayItem(t, root, array), "expected_hash", true)
	}

	// create allocates ids, so there is no prior revision to guard and no
	// expected_hash anywhere in its schema.
	createItem := schemaArrayItem(t, schemas["create"], "nodes")
	requireSchemaField(t, createItem, "expected_hash", false)
	requireSchemaField(t, schemas["create"], "expected_hash", false)
}

func TestMCP_MutationDescriptionsTeachReadMergeRetryProtocol(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	wants := map[string]string{
		"edit": "cat", "remove": "cat", "move": "cat",
		"keg_settings_edit": "keg_settings", "schema_edit": "schema_read",
		"schema_delete": "schema_read", "flight_edit": "flight_show", "flight_delete": "flight_show",
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

// TestMCP_AuthorityBearingToolsDescribeFlight pins that the injected flight
// property is also documented. schemaWithFlight adds `flight` to every
// authority-bearing tool's schema, but an agent reading descriptions rather
// than raw schemas saw no mention of it and reported the parameter as missing
// from keg_create. A property nothing describes reads as absent.
func TestMCP_AuthorityBearingToolsDescribeFlight(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	checked := 0
	for _, tool := range result.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var object struct {
			Properties map[string]json.RawMessage `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(schema, &object))
		if _, ok := object.Properties["flight"]; !ok {
			continue
		}
		checked++
		require.Containsf(t, strings.ToLower(tool.Description), "flight",
			"tool %q accepts a flight property but never mentions it in its description", tool.Name)
	}
	require.Greater(t, checked, 0, "no tool exposed a flight property; the injection may have broken")
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

	for _, name := range []string{"create", "edit"} {
		desc, ok := byName[name]
		require.Truef(t, ok, "missing tool %q", name)
		require.Containsf(t, desc, "frontmatter",
			"%q does not say content must not begin with a frontmatter block", name)
	}
	require.Contains(t, byName["edit"], "content and metadata together",
		"edit does not state that one hash covers both halves of a node")
}
