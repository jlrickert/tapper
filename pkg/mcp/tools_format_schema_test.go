package mcp_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/keg"
)

// schemaProperties decodes the description of each property in a tool's input
// schema. The schema is carried as an opaque value, so it is inspected through
// its JSON form rather than a concrete type.
func schemaProperties(t *testing.T, schema any) map[string]string {
	t.Helper()
	if schema == nil {
		return nil
	}
	raw, err := json.Marshal(schema)
	require.NoError(t, err)

	var decoded struct {
		Properties map[string]struct {
			Description string `json:"description"`
		} `json:"properties"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))

	out := make(map[string]string, len(decoded.Properties))
	for name, prop := range decoded.Properties {
		out[name] = prop.Description
	}
	return out
}

// formatToolNames are the tools whose output is rendered through the shared
// listing formatter, and which therefore must advertise the shared vocabulary.
var formatToolNames = []string{"list", "grep", "tags", "backlinks", "links"}

// TestMCP_FormatSchemaAdvertisesSharedVocabulary pins the generated tool
// schemas to the vocabulary the formatter actually implements.
//
// The struct tags carrying these descriptions must be literals, so they are
// duplicated from keg.FormatVocabularyDescription. This test is what keeps the
// copy honest: before it existed, the schemas advertised %i, %d, and %t while
// the implementation had also supported %c and %a for some time, so two working
// verbs were undiscoverable to agents.
func TestMCP_FormatSchemaAdvertisesSharedVocabulary(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	seen := map[string]bool{}
	for _, tool := range res.Tools {
		description, ok := schemaProperties(t, tool.InputSchema)["format"]
		if !ok {
			continue
		}
		seen[tool.Name] = true
		require.Truef(t,
			strings.HasPrefix(description, keg.FormatVocabularyDescription),
			"tool %q format description drifted from keg.FormatVocabularyDescription:\n got: %s\nwant prefix: %s",
			tool.Name, description, keg.FormatVocabularyDescription)
	}

	for _, name := range formatToolNames {
		require.Truef(t, seen[name], "tool %q exposes no format property", name)
	}
}

// TestMCP_FormatSchemaDescriptionIsReflectorSafe guards a sharp edge in schema
// generation: a description whose first whitespace-delimited token contains an
// equals sign is rejected by the reflector, and AddTool turns that into a panic
// at server construction, taking down the whole MCP server rather than one
// tool. Documenting a "type=" or "status=" selector is exactly the case that
// would trip it.
func TestMCP_FormatSchemaDescriptionIsReflectorSafe(t *testing.T) {
	t.Parallel()

	first, _, _ := strings.Cut(keg.FormatVocabularyDescription, " ")
	require.NotContainsf(t, first, "=",
		"description must not begin with a token containing '='; got %q", first)
}
