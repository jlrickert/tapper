package keg_test

import (
	"testing"

	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
)

// changeSchema is the schema from tapper#91: a required field typed integer.
const changeSchema = `type: change
meta:
  type: object
  required: [type, contract_count]
  properties:
    type: {const: change}
    contract_count: {type: integer, minimum: 1}
markdown:
  requireTitle: true
`

// TestCreate_IntegerFrontmatterSatisfiesSchema is the issue's reproduction, end
// to end through the real create path rather than at the metadata layer alone.
// Before the fix this failed with:
//
//	validating /properties/contract_count: type: 1 has type "string", want "integer"
//
// which was a closed loop: the node could not be created, and meta could not
// repair it because meta validates the whole node including markdown sections.
func TestCreate_IntegerFrontmatterSatisfiesSchema(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.CreateSchema(ctx, "change", []byte(changeSchema)))

	body := "---\ntype: change\ncontract_count: 1\n---\n\n# Widen the contract\n\nBody text.\n"
	results, err := k.CreateNodes(ctx, []keg.NodeCreate{
		{Key: "n", Schema: "change", Body: []byte(body)},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	if v := results[0].Validation; v != nil {
		require.True(t, v.Valid, "schema validation failed: %+v", v)
	}

	// And the value is stored as a number, not a quoted string.
	meta, err := k.GetMeta(ctx, results[0].ID)
	require.NoError(t, err)
	require.Contains(t, meta.ToYAML(), "contract_count: 1")
	require.NotContains(t, meta.ToYAML(), `contract_count: "1"`)
}

// TestUpdate_IntegerFrontmatterSurvivesEdit covers the edit half of the issue:
// a body carrying inline frontmatter runs through the same metadata path.
func TestUpdate_IntegerFrontmatterSurvivesEdit(t *testing.T) {
	fx := NewSandbox(t)
	ctx := fx.Context()
	k := keg.NewLocalKeg(newTestMemoryRepo(fx.Runtime()), fx.Runtime())
	require.NoError(t, k.Init(ctx))
	require.NoError(t, k.CreateSchema(ctx, "change", []byte(changeSchema)))

	results, err := k.CreateNodes(ctx, []keg.NodeCreate{{
		Key:    "n",
		Schema: "change",
		Body:   []byte("---\ntype: change\ncontract_count: 1\n---\n\n# Title\n\nBody.\n"),
	}})
	require.NoError(t, err)

	_, err = k.UpdateNode(ctx, keg.NodeUpdateOptions{
		ID:         results[0].ID,
		Schema:     "change",
		Content:    []byte("---\ntype: change\ncontract_count: 7\n---\n\n# Title\n\nEdited.\n"),
		HasContent: true,
		// Writes are guarded; carry the hash the create returned.
		ExpectedHash: results[0].Hash,
	})
	require.NoError(t, err)

	meta, err := k.GetMeta(ctx, results[0].ID)
	require.NoError(t, err)
	require.Contains(t, meta.ToYAML(), "contract_count: 7")
	require.NotContains(t, meta.ToYAML(), `contract_count: "7"`)
}
