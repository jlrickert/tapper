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
		"edit": "edits", "meta": "updates", "remove": "nodes",
	} {
		root, ok := schemas[tool]
		require.True(t, ok, "missing tool %q", tool)
		requireSchemaField(t, schemaArrayItem(t, root, array), "expected_hash", true)
	}

	// Metadata reads use node_ids and never need a mutation token. Requiring
	// expected_hash only inside updates keeps that read mode token-free.
	meta := schemas["meta"]
	requireSchemaField(t, meta, "expected_hash", false)
}

func TestMCP_MutationDescriptionsTeachReadMergeRetryProtocol(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	wants := map[string]string{
		"edit": "cat", "meta": "cat", "remove": "cat", "move": "cat",
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
