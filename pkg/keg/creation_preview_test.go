package keg_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Fail any attempt to inspect a node or allocate an ID, including node zero
// initialization checks on a fresh LocalKeg instance.
type creationPreviewRepo struct{ keg.Repository }

func (r creationPreviewRepo) CreateSchema(context.Context, string, []byte) error {
	panic("preview created schema")
}

var _ keg.RepositorySchemas = creationPreviewRepo{}

func (r creationPreviewRepo) ReadContent(context.Context, keg.NodeId) ([]byte, error) {
	return nil, fmt.Errorf("preview read a node")
}
func (r creationPreviewRepo) ReadMeta(context.Context, keg.NodeId) ([]byte, error) {
	return nil, fmt.Errorf("preview read metadata")
}
func (r creationPreviewRepo) Next(context.Context) (keg.NodeId, error) {
	return keg.NodeId{}, fmt.Errorf("preview allocated an ID")
}

// Preserve schema storage, which Repository exposes as an optional interface.
func (r creationPreviewRepo) ReadSchema(ctx context.Context, name string) ([]byte, error) {
	return r.Repository.(keg.RepositorySchemas).ReadSchema(ctx, name)
}
func (r creationPreviewRepo) ListSchemas(ctx context.Context) ([]string, error) {
	return r.Repository.(keg.RepositorySchemas).ListSchemas(ctx)
}
func (r creationPreviewRepo) WriteSchema(ctx context.Context, name string, raw []byte) error {
	panic("preview wrote schema")
}
func (r creationPreviewRepo) DeleteSchema(ctx context.Context, name string) error {
	panic("preview deleted schema")
}

func TestCreationPreviewCompleteDraftWithoutNodeReads(t *testing.T) {
	original, ctx := newSchemaSelectionKeg(t)
	require.NoError(t, original.CreateSchema(ctx, "complete", []byte(`type: complete
meta:
  type: object
  required: [owner]
  properties:
    owner: {type: string, minLength: 1}
markdown:
  requireTitle: true
  sections:
    - heading: Notes
      level: 2
      required: true
`)))
	require.NoError(t, original.UpdateSettings(ctx, func(cfg *keg.Settings) {
		cfg.SchemaPolicy = &keg.SchemaPolicy{Strict: true, Human: keg.ValidationModeBlock}
	}))
	k := keg.NewLocalKeg(creationPreviewRepo{original.Repo}, original.Runtime)
	ctx = keg.WithValidationActor(ctx, keg.ValidationActorHuman)
	for _, tc := range []struct {
		name, body, meta, schema string
		valid, parseError        bool
	}{
		{"both valid", "# Draft\n\n## Notes\nText", "owner: me\nextra: {nested: [one, two]}", "complete", true, false},
		{"content only", "# Draft\n\n## Notes\nText", "{}", "complete", false, false},
		{"meta only", "# Draft", "owner: me", "complete", false, false},
		{"conflict", "# Draft\n\n## Notes", "type: task\nowner: me", "complete", false, false},
		{"missing schema", "# Draft\n\n## Notes", "owner: me", "", false, false},
		{"unknown schema", "# Draft", "{}", "missing", false, false},
		{"malformed", "# Draft", "owner: [", "complete", false, true},
		{"frontmatter", "---\nx: y\n---\n# Draft", "{}", "complete", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := k.ValidateNodePayload(ctx, keg.NodeValidationPayload{Create: true, Schema: tc.schema, Content: []byte(tc.body), HasContent: true, Meta: []byte(tc.meta), HasMeta: true})
			if tc.parseError {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.valid, result.Valid)
			require.Empty(t, result.NodeID)
			_, createErr := original.Create(ctx, &keg.CreateOptions{Schema: tc.schema, Body: []byte(tc.body), Meta: []byte(tc.meta)})
			require.Equal(t, tc.valid, createErr == nil)
		})
	}
	_, err := k.ValidateNodePayload(ctx, keg.NodeValidationPayload{Create: true, HasContent: true})
	require.ErrorIs(t, err, keg.ErrInvalid)
}

func TestRemoteCreationPreviewPayload(t *testing.T) {
	fx := NewSandbox(t)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		require.Equal(t, "/validate", r.URL.Path)
		require.Equal(t, http.MethodPost, r.Method)
		var payload map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&payload))
		require.Equal(t, true, payload["create"])
		require.Equal(t, "task", payload["schema"])
		require.Equal(t, "# Draft", payload["content"])
		require.Equal(t, "", payload["meta"], "empty complete metadata must not be omitted")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"valid":true,"type":"task"}`))
	}))
	defer server.Close()
	remote := keg.NewRemoteKeg(server.URL, "token", fx.Runtime())
	result, err := remote.ValidateNodePayload(fx.Context(), keg.NodeValidationPayload{Create: true, Schema: "task", Content: []byte("# Draft"), HasContent: true, HasMeta: true})
	require.NoError(t, err)
	require.True(t, result.Valid)
	require.Equal(t, 1, calls)
}
