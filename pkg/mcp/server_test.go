package mcp_test

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jlrickert/cli-toolkit/mylog"
	"github.com/jlrickert/cli-toolkit/sandbox"
	"github.com/jlrickert/cli-toolkit/toolkit"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/internal/testkegrepo"
	"github.com/jlrickert/tapper/pkg/keg"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

//go:embed all:data/**
var testdata embed.FS

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	data, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==")
	require.NoError(t, err)
	return data
}

func newTestSandbox(t *testing.T) *sandbox.Sandbox {
	t.Helper()
	return sandbox.NewSandbox(t,
		&sandbox.Options{
			Data: testdata,
			Home: "/home/testuser",
			User: "testuser",
		},
		sandbox.WithFixture("testuser", "~"),
	)
}

func newMemoryTap(t *testing.T, ctx context.Context, rt *toolkit.Runtime) *tapper.Tap {
	t.Helper()
	if _, installed := orientationTestHubs.Load(rt); !installed {
		installOrientationTestHub(t, rt)
		writeUserFlight(t, rt, "")
	}
	hubURL := orientationTestHubFor(t, rt).server.URL
	newKeg := func(alias string) (*keg.LocalKeg, error) {
		repo := testkegrepo.NewMemoryRepository(rt)
		local := keg.NewLocalKeg(repo, rt)
		target := keg.NewApi("home", "local", alias, keg.WithHubURL(hubURL))
		local.SetTarget(&target)
		if err := local.Init(ctx); err != nil {
			return nil, err
		}
		if err := keg.UpdateSettings(ctx, local, func(settings *keg.Settings) {
			settings.Title = strings.ToUpper(alias[:1]) + alias[1:] + " KEG"
			if alias == "personal" {
				settings.Title = "Personal KEG"
			}
			if settings.SchemaPolicy == nil {
				settings.SchemaPolicy = &keg.SchemaPolicy{}
			}
			settings.SchemaPolicy.Strict = false
		}); err != nil {
			return nil, err
		}
		zero := keg.NodeId{ID: 0}
		if err := local.SetContent(ctx, zero, []byte("# Personal Overview\n\nThis is the zero node of the personal KEG.\n")); err != nil {
			return nil, err
		}
		zeroMeta, err := keg.ParseMeta(ctx, []byte("tags:\n  - overview\n"))
		if err != nil {
			return nil, err
		}
		if err := local.SetMeta(ctx, zero, zeroMeta); err != nil {
			return nil, err
		}
		created, err := local.Create(ctx, &keg.CreateOptions{
			Body: []byte("# Hello World\n\nA simple test node that links to [overview](../0).\n"),
			Meta: []byte("tags:\n  - test\n  - hello\n"),
		})
		if err != nil {
			return nil, err
		}
		if created.ID.ID != 1 {
			return nil, fmt.Errorf("seed node id = %d, want 1", created.ID.ID)
		}
		return local, nil
	}
	personal, err := newKeg("personal")
	require.NoError(t, err)
	kegs := map[string]keg.Keg{"personal": personal}
	var kegsMu sync.Mutex

	tap, err := tapper.NewTap(tapper.TapOptions{Runtime: rt})
	require.NoError(t, err)
	store, err := tapper.LoadAuthStore(ctx, rt, tap.PathService.AuthStorePath())
	require.NoError(t, err)
	store.Set(tapper.CanonicalHubURL(hubURL), tapper.AuthEntry{AccessToken: "test-token"})
	require.NoError(t, store.Save(ctx, rt, tap.PathService.AuthStorePath()))
	tap.AuthValidateFn = func(context.Context, *toolkit.Runtime, string, string) (*tapper.WhoAmI, error) {
		return &tapper.WhoAmI{UserID: 1, Username: "testuser", DefaultNamespace: "local", Namespaces: []string{"local"}}, nil
	}
	tap.KegResolver = func(_ context.Context, opts tapper.KegTargetOptions, _ tapper.FlightRole) (keg.Keg, error) {
		alias := strings.TrimSpace(opts.Keg)
		if alias == "" {
			alias = "personal"
		}
		if strings.HasPrefix(alias, "@") {
			if _, tail, ok := strings.Cut(alias, "/"); ok {
				alias = tail
			}
		}
		kegsMu.Lock()
		defer kegsMu.Unlock()
		if existing := kegs[alias]; existing != nil {
			return existing, nil
		}
		created, err := newKeg(alias)
		if err != nil {
			return nil, err
		}
		kegs[alias] = created
		return created, nil
	}
	return tap
}

func newTestSessionWithOpts(t *testing.T, opts ...mcp.ServerOptions) (*sdkmcp.ClientSession, context.Context) {
	t.Helper()
	ctx := context.Background()

	sb := newTestSandbox(t)
	rt := sb.Runtime()

	tap := newMemoryTap(t, ctx, rt)

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, opts...)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	// Connect server in background.
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		<-done
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "test-client",
		Version: "0.1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		session.Close()
	})

	return session, ctx
}

func newTestSession(t *testing.T) (*sdkmcp.ClientSession, context.Context) {
	session, _, ctx := newTestSessionWithRuntime(t)
	return session, ctx
}

// newLocalTestSessionWithRuntime builds the `tap mcp` surface: a server that
// shares a filesystem with its host, so the local-path attachment tools are
// registered. Use it for anything exercising source_path or dest_path.
func newLocalTestSessionWithRuntime(t *testing.T) (*sdkmcp.ClientSession, *toolkit.Runtime, context.Context) {
	t.Helper()
	return newTestSessionWithRuntime(t, mcp.ServerOptions{SharedFilesystem: true})
}

func newTestSessionWithRuntime(t *testing.T, opts ...mcp.ServerOptions) (*sdkmcp.ClientSession, *toolkit.Runtime, context.Context) {
	t.Helper()
	ctx := context.Background()

	sb := newTestSandbox(t)
	rt := sb.Runtime()

	tap := newMemoryTap(t, ctx, rt)

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{}, opts...)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	// Connect server in background.
	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		<-done
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "test-client",
		Version: "0.1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() {
		session.Close()
	})

	return session, rt, ctx
}

func TestMCP_ToolsList(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{
		"auth_info", "keg_list", "keg_search", "cat", "list", "grep", "tags", "backlinks", "links", "info",
		"keg_settings", "keg_settings_edit", "stats", "create", "edit", "remove", "move",
		"index", "list_indexes", "index_cat", "doctor", "node_history", "node_snapshot",
		"node_snapshot_view", "node_restore", "list_files", "list_images", "delete_file", "delete_image",
		"upload_file", "upload_image", "download_image", "orient", "session_refresh",
		"lock_acquire", "lock_release", "lock_status", "lock_force_release", "list_flights", "flight_show",
		"flight_create", "flight_edit", "flight_delete", "schema_list", "schema_read", "schema_create",
		"schema_edit", "schema_delete", "validate",
	} {
		require.Truef(t, names[want], "agent-safe surface missing %q", want)
	}
	for _, banned := range []string{
		"config", "config_template", "repo_init", "export", "import", "auth_status", "license",
		"download_file", "keg_visibility", "namespace_list", "namespace_create",
	} {
		require.Falsef(t, names[banned], "agent-safe surface exposed %q", banned)
	}
	for _, tool := range res.Tools {
		schema, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		require.NotContains(t, string(schema), "source_path", "tool %q exposes a machine-local upload path", tool.Name)
		require.NotContains(t, string(schema), "dest_path", "tool %q exposes a machine-local download destination", tool.Name)
	}
}

// TestMCP_CommonAgentSafeSurface pins the single common surface. Hosted
// callers inject providers rather than selecting a different registration set.
func TestMCP_CommonAgentSafeSurface(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSessionWithOpts(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	names := make(map[string]bool, len(res.Tools))
	for _, tool := range res.Tools {
		names[tool.Name] = true
	}

	// Common KEG and account tools must be present.
	for _, want := range []string{
		"cat", "list", "grep", "tags", "backlinks", "links", "info", "keg_settings",
		"keg_settings_edit",
		"stats", "create", "edit", "remove", "move", "index",
		"list_indexes", "index_cat", "node_history", "node_snapshot",
		"node_snapshot_view", "node_restore", "orient", "session_refresh",
		"list_files", "list_images", "delete_file", "delete_image",
		"upload_file", "upload_image", "download_image",
		"schema_list", "schema_read", "schema_create", "schema_edit",
		"schema_delete", "validate", "doctor", "keg_list", "keg_search", "auth_info",
		"lock_acquire", "lock_release", "lock_status", "lock_force_release",
		"list_flights", "flight_show", "flight_create", "flight_edit", "flight_delete",
	} {
		require.Truef(t, names[want], "common surface should expose %q", want)
	}

	// Machine-local and tenant-administration tools must be absent.
	for _, banned := range []string{
		"auth_status", "config", "config_template", "license", "repo_init", "integrate", "export", "import", "download_file",
		"keg_grants", "keg_grant", "keg_revoke", "keg_visibility",
		"namespace_list", "namespace_members", "namespace_add_member",
		"namespace_set_role", "namespace_remove_member", "namespace_create",
		"flight_update",
		// meta was folded into edit (writes) and cat meta_only (reads); it
		// must not come back as a third way to touch node metadata.
		"meta",
	} {
		require.Falsef(t, names[banned], "common surface must not expose %q", banned)
	}
}

func TestMCP_Cat(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{"0"},
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "cat returned error: %s", text)
	require.Contains(t, text, "Personal Overview")
}

func TestMCP_KegSettingsEdit_ReplacesValidatedDocument(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)
	expectedHash := readSettingsHash(t, session, ctx, "")

	edit, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "keg_settings_edit",
		Arguments: map[string]any{
			"data":          "kegv: 2025-07\ntitle: Agent Edited\nsummary: complete replacement\n",
			"expected_hash": expectedHash,
		},
	})
	require.NoError(t, err)
	require.False(t, edit.IsError, "keg_settings_edit returned error: %v", edit.Content)
	callOrient(t, ctx, session)

	read, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "keg_settings",
		Arguments: map[string]any{
			"minimal": false,
		},
	})
	require.NoError(t, err)
	require.False(t, read.IsError, "keg_settings returned error: %v", read.Content)
	text := read.Content[0].(*sdkmcp.TextContent).Text
	require.Contains(t, text, "title: Agent Edited")
	require.Contains(t, text, "summary: complete replacement")

	invalid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "keg_settings_edit",
		Arguments: map[string]any{
			"data": "kegv: [\n",
		},
	})
	require.NoError(t, err)
	require.True(t, invalid.IsError)

	read, err = session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings",
		Arguments: map[string]any{"minimal": false},
	})
	require.NoError(t, err)
	require.False(t, read.IsError)
	require.Contains(t, read.Content[0].(*sdkmcp.TextContent).Text, "title: Agent Edited")
}

func TestMCP_CatContentOnly(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "# Personal Overview")
}

func TestMCP_List(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "list returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Personal Overview")
	require.Contains(t, text, "Hello World")
}

func TestMCP_ListIdOnly(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "0")
	require.Contains(t, text, "1")
}

func TestMCP_ListDefaultLimit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Omitting limit should apply the MCP default (50). The test fixture
	// has only 2 nodes, so all are returned — but the important thing is
	// that the call succeeds with the default applied.
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "0")
	require.Contains(t, text, "1")
}

func TestMCP_ListUnlimitedWithNegativeOne(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Passing limit=-1 should request unlimited results (no cap).
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
			"limit":   -1,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	require.Contains(t, text, "0")
	require.Contains(t, text, "1")
}

func TestMCP_ListExplicitLimit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Passing limit=1 should cap at 1 result.
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list",
		Arguments: map[string]any{
			"id_only": true,
			"limit":   1,
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError)
	text := extractText(t, res)
	lines := strings.Split(strings.TrimSpace(text), "\n")
	require.Len(t, lines, 1, "limit=1 should return exactly 1 result")
}

func TestMCP_Grep(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "grep",
		Arguments: map[string]any{
			"query": "Hello",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "grep returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Hello World")
}

func TestMCP_Tags(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "tags",
		Arguments: map[string]any{
			"query": "test",
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "tags returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Hello World")
}

func TestMCP_Backlinks(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "backlinks",
		Arguments: map[string]any{
			"node_ids": []string{"0"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "backlinks returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Hello World")
}

func TestMCP_Links(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "links",
		Arguments: map[string]any{
			"node_ids": []string{"1"},
		},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "links returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Personal Overview")
}

func TestMCP_Info(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "info",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "info returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "node_count")
	require.Contains(t, text, "ref:")
	require.Contains(t, text, "hub: home")
}

func TestMCP_KegSettings(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "keg_settings",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "keg_settings returned error: %v", res.Content)
	text := extractText(t, res)
	require.Contains(t, text, "Personal KEG")
	require.NotContains(t, text, "node_count")
}

func TestMCP_Stats(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "stats",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	// stats may be empty if no stats.json exists, but should not error
	require.False(t, res.IsError, "stats returned error: %v", res.Content)
}

func TestMCP_CatError(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{"999"},
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing node")
}

// --- write tool tests ---

// batchCreateArgs builds a one-node create payload. It accepts the "title" and
// "lead" shorthands the create tool no longer has and folds them into the
// markdown content, so tests that only need *a node to exist* stay readable
// and do not each have to spell out a heading.
func batchCreateArgs(item map[string]any) map[string]any {
	title, hasTitle := item["title"].(string)
	lead, hasLead := item["lead"].(string)
	if hasTitle || hasLead {
		delete(item, "title")
		delete(item, "lead")
		if !hasTitle {
			title = "Node"
		}
		content := "# " + title + "\n"
		if hasLead {
			content += "\n" + lead + "\n"
		}
		item["content"] = content
	}
	item["key"] = "node"
	return map[string]any{"nodes": []any{item}}
}

func batchEditArgs(item map[string]any) map[string]any {
	return map[string]any{"nodes": []any{item}}
}

func batchSnapshotArgs(item map[string]any) map[string]any {
	return map[string]any{"nodes": []any{item}}
}

func TestMCPMutationSchemasRejectLegacySingleItemFields(t *testing.T) {
	session, ctx := newTestSession(t)
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"create", map[string]any{"title": "legacy"}},
		{"edit", map[string]any{"node_id": "0", "content": "# legacy\n"}},
		{"remove", map[string]any{"node_ids": []string{"0"}, "expected_hash": "legacy"}},
		{"node_snapshot", map[string]any{"node_id": "0"}},
	} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tc.name, Arguments: tc.args})
		require.NoError(t, err)
		require.True(t, res.IsError, "%s accepted legacy fields", tc.name)
	}
}

func TestMCPMutationSchemasRejectEmptyAndOversizedArrays(t *testing.T) {
	session, ctx := newTestSession(t)
	oversizedObjects := make([]any, 101)
	oversizedRemovals := make([]any, 101)
	for i := range oversizedObjects {
		oversizedObjects[i] = map[string]any{"key": fmt.Sprintf("node-%d", i)}
		oversizedRemovals[i] = map[string]any{"node_id": fmt.Sprintf("%d", i), "expected_hash": "hash"}
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"create empty", map[string]any{"nodes": []any{}}},
		{"create oversized", map[string]any{"nodes": oversizedObjects}},
		{"edit empty", map[string]any{"nodes": []any{}}},
		{"edit oversized", map[string]any{"nodes": oversizedObjects}},
		{"remove empty", map[string]any{"nodes": []any{}}},
		{"remove oversized", map[string]any{"nodes": oversizedRemovals}},
		{"snapshot empty", map[string]any{"nodes": []any{}}},
		{"snapshot oversized", map[string]any{"nodes": oversizedObjects}},
	} {
		tool := strings.Fields(tc.name)[0]
		if tool == "snapshot" {
			tool = "node_snapshot"
		}
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: tool, Arguments: tc.args})
		require.NoError(t, err)
		require.True(t, res.IsError, "%s accepted invalid array bounds", tc.name)
	}
}

func TestMCPMutationsPreserveAgentSchemaPolicy(t *testing.T) {
	session, ctx := newTestSession(t)
	expectedHash := readSettingsHash(t, session, ctx, "")
	settings, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "keg_settings_edit",
		Arguments: map[string]any{"expected_hash": expectedHash, "data": `kegv: 2025-07
schemaPolicy:
  strict: true
  human: off
  agent: block
  api: off
`},
	})
	require.NoError(t, err)
	require.False(t, settings.IsError, "settings update failed: %s", extractText(t, settings))
	callOrient(t, ctx, session)
	schema, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "schema_create",
		Arguments: map[string]any{"data": `type: task
meta:
  type: object
  required: [type]
  properties:
    type: {const: task}
`},
	})
	require.NoError(t, err)
	require.False(t, schema.IsError, "schema create failed: %s", extractText(t, schema))

	invalid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: map[string]any{"nodes": []any{map[string]any{"key": "invalid", "content": "# Missing type\n"}}},
	})
	require.NoError(t, err)
	require.True(t, invalid.IsError, "MCP write was reclassified as human and bypassed agent:block")
	require.Contains(t, extractText(t, invalid), "explicit schema selection is required")

	valid, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{"nodes": []any{map[string]any{
			"key": "valid", "schema": "task", "content": "# Typed\n", "meta": "type: task\n",
		}}},
	})
	require.NoError(t, err)
	require.False(t, valid.IsError, "typed MCP create failed: %s", extractText(t, valid))
}

func TestMCP_Create(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "New Node",
			"lead":  "A node created via MCP.",
			"meta":  "tags:\n  - mcp-test\n",
		}),
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "create returned error: %s", text)
	require.NotEmpty(t, text)

	// Read it back.
	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{text},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.False(t, readRes.IsError, "cat returned error: %s", readText)
	require.Contains(t, readText, "# New Node")
	require.Contains(t, readText, "A node created via MCP.")
}

// TestMCP_CreateRejectsFrontmatterInContent keeps content and meta from being
// two ways to write the same node metadata.
func TestMCP_CreateRejectsFrontmatterInContent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{"nodes": []any{map[string]any{
			"key":     "node",
			"content": "---\ntags:\n  - sneaky\n---\n\n# Body\n",
		}}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "create accepted frontmatter in content")
	require.Contains(t, extractText(t, res), "meta field")
}

// TestMCP_CreateRejectsSchemaConflictingWithMeta pins the promise made in the
// schema field's description: a type declared in meta is not overridden by a
// different schema selection, it is refused.
func TestMCP_CreateRejectsSchemaConflictingWithMeta(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{"nodes": []any{map[string]any{
			"key":     "node",
			"content": "# Conflicted\n",
			"meta":    "type: note\n",
			"schema":  "task",
		}}},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "create accepted a schema conflicting with meta.type")
	require.Contains(t, extractText(t, res), "conflicts with")
}

// TestMCP_CreateRejectsLegacyStructuredFields pins that title, lead, tags, and
// attrs are gone rather than silently ignored: each was a second way to write
// something content or meta already carries.
func TestMCP_CreateRejectsLegacyStructuredFields(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	for _, field := range []string{"title", "lead", "body", "tags", "attrs"} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
			Name: "create",
			Arguments: map[string]any{"nodes": []any{map[string]any{
				"key": "node", "content": "# Legacy\n", field: "x",
			}}},
		})
		require.NoError(t, err)
		require.Truef(t, res.IsError, "create accepted legacy field %q", field)
	}
}

func TestMCP_CreateBatchReturnsOrderedStructuredResults(t *testing.T) {
	session, ctx := newTestSession(t)
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: map[string]any{"nodes": []any{
			map[string]any{"key": "first", "content": "# First\n\n[Second](../{{node:second}})\n"},
			map[string]any{"key": "second", "content": "# Second\n\n[First](../{{node:first}})\n"},
		}},
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "batch create returned error: %s", extractText(t, res))
	raw, err := json.Marshal(res.StructuredContent)
	require.NoError(t, err)
	var structured struct {
		Results []struct {
			Key    string `json:"key"`
			NodeID string `json:"node_id"`
			Hash   string `json:"hash"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(raw, &structured))
	require.Len(t, structured.Results, 2)
	require.Equal(t, []string{"first", "second"}, []string{structured.Results[0].Key, structured.Results[1].Key})
	require.NotEmpty(t, structured.Results[0].NodeID)
	require.NotEmpty(t, structured.Results[1].NodeID)
	require.NotEmpty(t, structured.Results[0].Hash)
	require.NotEmpty(t, structured.Results[1].Hash)
}

func TestMCP_CreateWithBody(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	body := "# Custom Title\n\nCustom body content.\n"
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"content": body,
		}),
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "create returned error: %s", text)

	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{text},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.Contains(t, readText, "# Custom Title")
	require.Contains(t, readText, "Custom body content.")
}

func TestMCP_Edit(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node first.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Before Edit",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)
	require.False(t, createRes.IsError)
	expectedHash := readNodeHash(t, session, ctx, nodeID)

	// Edit it.
	editRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"content":       "# After Edit\n\nEdited via MCP.\n",
			"expected_hash": expectedHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, editRes.IsError, "edit returned error: %s", extractText(t, editRes))

	// Read back.
	readRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{nodeID},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	readText := extractText(t, readRes)
	require.Contains(t, readText, "# After Edit")
	require.Contains(t, readText, "Edited via MCP.")
}

// metaOnlyText reads a node's metadata document through cat, which is the only
// metadata read now that the meta tool is gone.
func metaOnlyText(t *testing.T, session *sdkmcp.ClientSession, ctx context.Context, nodeID string) string {
	t.Helper()
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{nodeID}, "meta_only": true},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "cat meta_only returned error: %s", text)
	return text
}

func TestMCP_MetaReadThroughCat(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Node 0 has tags: [overview]
	require.Contains(t, metaOnlyText(t, session, ctx, "0"), "overview")
}

func TestMCP_EditWritesMeta(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Meta Test",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)
	expectedHash := readNodeHash(t, session, ctx, nodeID)

	writeRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"meta":          "tags:\n  - updated\n  - mcp\n",
			"expected_hash": expectedHash,
		}),
	})
	require.NoError(t, err)
	require.False(t, writeRes.IsError, "edit returned error: %s", extractText(t, writeRes))

	readText := metaOnlyText(t, session, ctx, nodeID)
	require.Contains(t, readText, "updated")
	require.Contains(t, readText, "mcp")
}

// TestMCP_EditWritesContentAndMetaTogether covers the case the old two-tool
// split could not express at all: both halves of a node replaced in one call,
// under the single hash that covers them both.
func TestMCP_EditWritesContentAndMetaTogether(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: batchCreateArgs(map[string]any{"title": "Before Both"}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"content":       "# After Both\n\nRewritten body.\n",
			"meta":          "tags:\n  - both\n",
			"expected_hash": readNodeHash(t, session, ctx, nodeID),
		}),
	})
	require.NoError(t, err)
	require.False(t, res.IsError, "edit returned error: %s", extractText(t, res))

	require.Contains(t, metaOnlyText(t, session, ctx, nodeID), "both")

	contentRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{nodeID}, "content_only": true},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, contentRes), "# After Both")
}

// TestMCP_EditRejectsFrontmatterInContent pins the footgun this refactor
// removed: content that opens with a frontmatter delimiter used to be parsed
// as metadata silently.
func TestMCP_EditRejectsFrontmatterInContent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "create",
		Arguments: batchCreateArgs(map[string]any{"title": "Frontmatter Subject"}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       nodeID,
			"content":       "---\ntags:\n  - sneaky\n---\n\n# Body\n",
			"expected_hash": readNodeHash(t, session, ctx, nodeID),
		}),
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "edit accepted frontmatter in content")
	require.Contains(t, extractText(t, res), "meta field")

	require.NotContains(t, metaOnlyText(t, session, ctx, nodeID), "sneaky")
}

// TestMCP_EditRequiresContentOrMeta rejects an item that names a node and a
// hash but asks for no change.
func TestMCP_EditRequiresContentOrMeta(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":       "0",
			"expected_hash": readNodeHash(t, session, ctx, "0"),
		}),
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "edit accepted an item with neither content nor meta")
	require.Contains(t, extractText(t, res), "content or meta is required")
}

// TestMCP_EditRejectsSnapshotBefore pins the removal of the implicit snapshot
// flag: a stale caller must fail loudly rather than quietly lose its snapshot.
func TestMCP_EditRejectsSnapshotBefore(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "edit",
		Arguments: batchEditArgs(map[string]any{
			"node_id":         "0",
			"content":         "# Zero\n",
			"expected_hash":   readNodeHash(t, session, ctx, "0"),
			"snapshot_before": true,
		}),
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "edit accepted snapshot_before")
	require.Contains(t, extractText(t, res), "snapshot_before")
}

func TestMCP_Remove(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "To Be Removed",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)
	expectedHash := readNodeHash(t, session, ctx, nodeID)

	// Remove it.
	removeRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "remove",
		Arguments: map[string]any{
			"nodes": []map[string]any{{
				"node_id":       nodeID,
				"expected_hash": expectedHash,
			}},
		},
	})
	require.NoError(t, err)
	require.False(t, removeRes.IsError, "remove returned error: %s", extractText(t, removeRes))

	// Confirm it's gone.
	catRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{nodeID},
		},
	})
	require.NoError(t, err)
	require.True(t, catRes.IsError, "expected error reading removed node")
}

func TestMCP_Move(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Create a node.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Movable Node",
		}),
	})
	require.NoError(t, err)
	srcID := extractText(t, createRes)
	expectedHash := readNodeHash(t, session, ctx, srcID)

	// Move it to ID 999.
	moveRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "move",
		Arguments: map[string]any{
			"source_id":     srcID,
			"dest_id":       "999",
			"expected_hash": expectedHash,
		},
	})
	require.NoError(t, err)
	require.False(t, moveRes.IsError, "move returned error: %s", extractText(t, moveRes))

	// Old ID is gone.
	oldRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids": []string{srcID},
		},
	})
	require.NoError(t, err)
	require.True(t, oldRes.IsError, "expected error reading old node ID")

	// New ID exists.
	newRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"999"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	newText := extractText(t, newRes)
	require.False(t, newRes.IsError, "cat returned error: %s", newText)
	require.Contains(t, newText, "Movable Node")
}

// --- snapshot and file tool tests ---

func TestMCP_NodeHistory_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_history",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "node_history returned error: %s", text)
	require.Contains(t, text, "no snapshots")
}

func TestMCP_NodeSnapshotAndHistory(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Snapshot node 0.
	snapRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_snapshot",
		Arguments: batchSnapshotArgs(map[string]any{
			"node_id": "0",
			"message": "initial snapshot",
		}),
	})
	require.NoError(t, err)
	snapText := extractText(t, snapRes)
	require.False(t, snapRes.IsError, "node_snapshot returned error: %s", snapText)
	require.Contains(t, snapText, "snapshot rev")

	// Check history.
	histRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_history",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	histText := extractText(t, histRes)
	require.False(t, histRes.IsError, "node_history returned error: %s", histText)
	require.Contains(t, histText, "rev 1")
	require.Contains(t, histText, "initial snapshot")

	viewRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "node_snapshot_view",
		Arguments: map[string]any{
			"node_id": "0",
			"rev":     "1",
		},
	})
	require.NoError(t, err)
	viewText := extractText(t, viewRes)
	require.False(t, viewRes.IsError, "node_snapshot_view returned error: %s", viewText)
	require.Contains(t, viewText, "This is the zero node of the personal KEG.")

	currentRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "cat",
		Arguments: map[string]any{
			"node_ids":     []string{"0"},
			"content_only": true,
		},
	})
	require.NoError(t, err)
	currentText := extractText(t, currentRes)
	require.False(t, currentRes.IsError, "cat returned error: %s", currentText)
	require.Contains(t, currentText, "This is the zero node of the personal KEG.")
}

func TestMCP_ListFiles_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_files",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "list_files returned error: %s", text)
	require.Contains(t, text, "no files")
}

func TestMCP_ListImages_Empty(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_images",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "list_images returned error: %s", text)
	require.Contains(t, text, "no images")
}

// --- index and diagnostics tool tests ---

func TestMCP_ListIndexes(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list_indexes",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "list_indexes returned error: %s", text)
	require.Contains(t, text, "nodes.tsv")
	require.Contains(t, text, "tags")
	require.Contains(t, text, "timeline")
	require.Contains(t, text, "dirty")
}

func TestMCP_IndexCat(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "index_cat",
		Arguments: map[string]any{
			"name": "nodes.tsv",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "index_cat returned error: %s", text)
	require.Contains(t, text, "Personal Overview")
	require.Contains(t, text, "Hello World")

	for _, name := range []string{"timeline", "dirty"} {
		res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
			Name: "index_cat",
			Arguments: map[string]any{
				"name": name,
			},
		})
		require.NoError(t, err)
		text := extractText(t, res)
		require.False(t, res.IsError, "index_cat %s returned error: %s", name, text)
	}
}

func TestMCP_IndexRebuild(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "index",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "index returned error: %s", text)
	require.Contains(t, text, "Indices rebuilt")
}

func TestMCP_Doctor(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "doctor",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "doctor returned error: %s", text)
	// The fixture may or may not have issues; just verify it returns something.
	require.NotEmpty(t, text)
}

// --- lock tool tests ---

func TestMCP_LockAcquireAndRelease(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Acquire lock on node 0.
	acquireRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_acquire",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	token := extractText(t, acquireRes)
	require.False(t, acquireRes.IsError, "lock_acquire returned error: %s", token)
	require.NotEmpty(t, token)

	// Status should show locked.
	statusRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_status",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	statusText := extractText(t, statusRes)
	require.False(t, statusRes.IsError)
	require.Contains(t, statusText, "locked")
	require.Contains(t, statusText, token)

	// Release with correct token.
	releaseRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_release",
		Arguments: map[string]any{
			"node_id": "0",
			"token":   token,
		},
	})
	require.NoError(t, err)
	require.False(t, releaseRes.IsError, "lock_release returned error: %s", extractText(t, releaseRes))

	// Status should show unlocked.
	statusRes2, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_status",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, statusRes2), "unlocked")
}

func TestMCP_LockForceRelease(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Acquire lock.
	acquireRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_acquire",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, acquireRes.IsError)

	// Force release without knowing the token.
	forceRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_force_release",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, forceRes.IsError, "lock_force_release returned error: %s", extractText(t, forceRes))

	// Status should show unlocked.
	statusRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_status",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, statusRes), "unlocked")
}

func TestMCP_LockReleaseTokenMismatch(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	// Acquire lock.
	acquireRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_acquire",
		Arguments: map[string]any{
			"node_id": "0",
		},
	})
	require.NoError(t, err)
	require.False(t, acquireRes.IsError)

	// Release with wrong token should fail.
	releaseRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "lock_release",
		Arguments: map[string]any{
			"node_id": "0",
			"token":   "wrong-token",
		},
	})
	require.NoError(t, err)
	require.True(t, releaseRes.IsError, "expected error for token mismatch")
	require.Contains(t, extractText(t, releaseRes), "mismatch")
}

// --- license tool tests ---

func TestMCP_License(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSessionWithOpts(t)
	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	for _, tool := range res.Tools {
		require.NotEqual(t, "license", tool.Name)
	}
}

func TestMCP_License_Empty(t *testing.T) {
	t.Skip("license is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "license",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "license returned error: %s", text)
	require.Contains(t, text, "no license text available")
}

// --- repo and config tool tests ---

func TestMCP_ToolsList_IncludesNewTools(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.NotContains(t, names, "repo_init")
	require.NotContains(t, names, "config")
	require.NotContains(t, names, "config_template")
}

func TestMCP_Config(t *testing.T) {
	t.Skip("config is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "config",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config returned error: %s", text)
	require.Contains(t, text, "personal")
}

func TestMCP_ConfigUser(t *testing.T) {
	t.Skip("config is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "config",
		Arguments: map[string]any{
			"scope": "user",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config user returned error: %s", text)
	require.Contains(t, text, "defaultKeg")
}

func TestMCP_ConfigInvalidScope(t *testing.T) {
	t.Skip("config is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "config",
		Arguments: map[string]any{
			"scope": "invalid",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for invalid scope")
}

func TestMCP_ConfigTemplate(t *testing.T) {
	t.Skip("config_template is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "config_template",
		Arguments: map[string]any{},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config_template returned error: %s", text)
	require.Contains(t, text, "fallbackHub")
}

func TestMCP_ConfigTemplateProject(t *testing.T) {
	t.Skip("config_template is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "config_template",
		Arguments: map[string]any{
			"scope": "project",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "config_template project returned error: %s", text)
	require.Contains(t, text, "defaultKeg")
}

func TestMCP_RepoInit(t *testing.T) {
	t.Skip("repo_init is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg":   "newkeg",
			"user":  true,
			"title": "New Test KEG",
		},
	})
	require.NoError(t, err)
	text := extractText(t, res)
	require.False(t, res.IsError, "repo_init returned error: %s", text)
	require.Contains(t, text, "initialized keg")
	require.Contains(t, text, "newkeg")
}

func TestMCP_RepoInitMissingAlias(t *testing.T) {
	t.Skip("repo_init is not part of the agent-safe MCP surface")
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "repo_init",
		Arguments: map[string]any{
			"keg": "",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing alias")
}

// --- file transfer tool tests ---

func TestMCP_ToolsList_IncludesFileTransferTools(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}

	require.Contains(t, names, "upload_file")
	require.NotContains(t, names, "download_file")
	require.Contains(t, names, "upload_image")
	require.Contains(t, names, "download_image")
}

// TestMCP_LocalSurfacePublishesLocalPathTransfers pins the `tap mcp` half of
// the split: a shared-filesystem server must publish the full attachment
// round-trip, local paths included. Its hosted counterparts are
// TestMCP_UploadSchemaRejectsLocalSourcePath and
// TestMCP_DownloadImageSchemaRejectsDestPath.
func TestMCP_LocalSurfacePublishesLocalPathTransfers(t *testing.T) {
	t.Parallel()
	session, _, ctx := newLocalTestSessionWithRuntime(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	properties := map[string]map[string]any{}
	for _, tool := range res.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		require.NoError(t, err)
		var schema struct {
			Properties map[string]any `json:"properties"`
		}
		require.NoError(t, json.Unmarshal(raw, &schema))
		properties[tool.Name] = schema.Properties
	}

	for _, want := range []struct{ tool, field string }{
		{"upload_file", "source_path"},
		{"upload_image", "source_path"},
		{"download_file", "dest_path"},
		{"download_image", "dest_path"},
	} {
		require.Contains(t, properties, want.tool, "tap mcp must publish %s", want.tool)
		require.Contains(t, properties[want.tool], want.field,
			"tap mcp %s must accept %s", want.tool, want.field)
	}
}

func TestMCP_UploadAndDownloadFile(t *testing.T) {
	t.Parallel()
	session, rt, ctx := newLocalTestSessionWithRuntime(t)

	// Create a node to attach files to.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "File Test Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	// Write a source file in the sandbox.
	srcPath := "/home/testuser/upload-test.txt"
	require.NoError(t, rt.WriteFile(srcPath, []byte("hello world"), 0o644))

	// Upload the file via source_path.
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":     nodeID,
			"filename":    "test.txt",
			"source_path": srcPath,
		},
	})
	require.NoError(t, err)
	uploadText := extractText(t, uploadRes)
	require.False(t, uploadRes.IsError, "upload_file returned error: %s", uploadText)
	require.Contains(t, uploadText, "uploaded file")

	// List files to verify.
	listRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_files",
		Arguments: map[string]any{
			"node_id": nodeID,
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, listRes), "test.txt")

	// Download the file to a dest_path.
	destPath := "/home/testuser/download-test.txt"
	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_file",
		Arguments: map[string]any{
			"node_id":   nodeID,
			"filename":  "test.txt",
			"dest_path": destPath,
		},
	})
	require.NoError(t, err)
	downloadText := extractText(t, downloadRes)
	require.False(t, downloadRes.IsError, "download_file returned error: %s", downloadText)
	require.Contains(t, downloadText, destPath)

	// Verify the downloaded file contents match.
	got, err := rt.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, "hello world", string(got))
}

func TestMCP_UploadAndDownloadImage(t *testing.T) {
	t.Parallel()
	session, rt, ctx := newLocalTestSessionWithRuntime(t)

	// Create a node to attach images to.
	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Image Test Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	// Write a source image in the sandbox.
	srcPath := "/home/testuser/upload-test.png"
	pngData := tinyPNG(t)
	require.NoError(t, rt.WriteFile(srcPath, pngData, 0o644))

	// Upload the image via source_path.
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_image",
		Arguments: map[string]any{
			"node_id":     nodeID,
			"filename":    "test.png",
			"source_path": srcPath,
		},
	})
	require.NoError(t, err)
	uploadText := extractText(t, uploadRes)
	require.False(t, uploadRes.IsError, "upload_image returned error: %s", uploadText)
	require.Contains(t, uploadText, "uploaded image")

	// List images to verify.
	listRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_images",
		Arguments: map[string]any{
			"node_id": nodeID,
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, listRes), "test.png")

	// Download the image to a dest_path.
	destPath := "/home/testuser/download-test.png"
	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_image",
		Arguments: map[string]any{
			"node_id":   nodeID,
			"filename":  "test.png",
			"dest_path": destPath,
		},
	})
	require.NoError(t, err)
	downloadText := extractText(t, downloadRes)
	require.False(t, downloadRes.IsError, "download_image returned error: %s", downloadText)
	require.Contains(t, downloadText, destPath)

	// Verify the downloaded image contents match.
	got, err := rt.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, pngData, got)
}

func TestMCP_DownloadImageReturnsImageContent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSessionWithOpts(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Hosted Image Download Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	pngData := tinyPNG(t)
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_image",
		Arguments: map[string]any{
			"node_id":     nodeID,
			"filename":    "hosted.png",
			"data_base64": base64.StdEncoding.EncodeToString(pngData),
		},
	})
	require.NoError(t, err)
	require.False(t, uploadRes.IsError, "upload_image returned error: %s", extractText(t, uploadRes))

	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_image",
		Arguments: map[string]any{
			"node_id":  nodeID,
			"filename": "hosted.png",
		},
	})
	require.NoError(t, err)
	require.False(t, downloadRes.IsError, "download_image returned error: %s", extractText(t, downloadRes))
	require.Len(t, downloadRes.Content, 1)
	img, ok := downloadRes.Content[0].(*sdkmcp.ImageContent)
	require.True(t, ok, "download_image content type = %T, want ImageContent", downloadRes.Content[0])
	require.Equal(t, "image/png", img.MIMEType)
	require.Equal(t, pngData, img.Data)

	var structured struct {
		NodeID   string `json:"node_id"`
		Filename string `json:"filename"`
		MIMEType string `json:"mime_type"`
		Size     int    `json:"size"`
	}
	raw, err := json.Marshal(downloadRes.StructuredContent)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &structured))
	require.Equal(t, nodeID, structured.NodeID)
	require.Equal(t, "hosted.png", structured.Filename)
	require.Equal(t, "image/png", structured.MIMEType)
	require.Equal(t, len(pngData), structured.Size)
}

func TestMCP_DownloadImageSchemaRejectsDestPath(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSessionWithOpts(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_image",
		Arguments: map[string]any{
			"node_id":   "0",
			"filename":  "hosted.png",
			"dest_path": "/home/testuser/hosted.png",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected hosted download_image dest_path to fail")
	require.Contains(t, extractText(t, res), "unexpected additional properties")
}

func TestMCP_UploadFileFromBase64(t *testing.T) {
	t.Parallel()
	session, rt, ctx := newLocalTestSessionWithRuntime(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Base64 File Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	payload := []byte("hello from base64")
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":     nodeID,
			"filename":    "raw.txt",
			"data_base64": base64.StdEncoding.EncodeToString(payload),
		},
	})
	require.NoError(t, err)
	require.False(t, uploadRes.IsError, "upload_file returned error: %s", extractText(t, uploadRes))

	destPath := "/home/testuser/base64-download.txt"
	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_file",
		Arguments: map[string]any{
			"node_id":   nodeID,
			"filename":  "raw.txt",
			"dest_path": destPath,
		},
	})
	require.NoError(t, err)
	require.False(t, downloadRes.IsError, "download_file returned error: %s", extractText(t, downloadRes))
	got, err := rt.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestMCP_UploadFileFromEmbeddedResource(t *testing.T) {
	t.Parallel()
	session, rt, ctx := newLocalTestSessionWithRuntime(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Embedded File Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	payload := []byte("hello from embedded resource")
	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id": nodeID,
			"resource": map[string]any{
				"uri":      "file:///tmp/embedded.txt",
				"mimeType": "text/plain",
				"blob":     base64.StdEncoding.EncodeToString(payload),
			},
		},
	})
	require.NoError(t, err)
	require.False(t, uploadRes.IsError, "upload_file returned error: %s", extractText(t, uploadRes))
	require.Contains(t, extractText(t, uploadRes), "embedded.txt")

	destPath := "/home/testuser/embedded-download.txt"
	downloadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_file",
		Arguments: map[string]any{
			"node_id":   nodeID,
			"filename":  "embedded.txt",
			"dest_path": destPath,
		},
	})
	require.NoError(t, err)
	require.False(t, downloadRes.IsError, "download_file returned error: %s", extractText(t, downloadRes))
	got, err := rt.ReadFile(destPath)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestMCP_UploadImageFromEmbeddedResource(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Embedded Image Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	uploadRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_image",
		Arguments: map[string]any{
			"node_id": nodeID,
			"resource": map[string]any{
				"uri":      "file:///tmp/embedded.png",
				"mimeType": "image/png",
				"blob":     base64.StdEncoding.EncodeToString(tinyPNG(t)),
			},
		},
	})
	require.NoError(t, err)
	require.False(t, uploadRes.IsError, "upload_image returned error: %s", extractText(t, uploadRes))
	require.Contains(t, extractText(t, uploadRes), "embedded.png")

	listRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "list_images",
		Arguments: map[string]any{
			"node_id": nodeID,
		},
	})
	require.NoError(t, err)
	require.Contains(t, extractText(t, listRes), "embedded.png")
}

func TestMCP_UploadImageRejectsInvalidImage(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	createRes, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "create",
		Arguments: batchCreateArgs(map[string]any{
			"title": "Invalid Image Node",
		}),
	})
	require.NoError(t, err)
	nodeID := extractText(t, createRes)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_image",
		Arguments: map[string]any{
			"node_id":     nodeID,
			"filename":    "bad.png",
			"data_base64": base64.StdEncoding.EncodeToString([]byte("not an image")),
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected invalid image upload to fail")
	require.Contains(t, extractText(t, res), "invalid image")
}

func TestMCP_UploadRejectsMultipleSources(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":     "0",
			"filename":    "test.txt",
			"source_path": "/home/testuser/a.txt",
			"data_base64": base64.StdEncoding.EncodeToString([]byte("x")),
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected multiple upload sources to fail")
	require.Contains(t, extractText(t, res), "unexpected additional properties")
}

func TestMCP_UploadSchemaRejectsLocalSourcePath(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSessionWithOpts(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":     "0",
			"filename":    "test.txt",
			"source_path": "/home/testuser/upload-test.txt",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected hub surface local upload source to fail")
	require.Contains(t, extractText(t, res), "unexpected additional properties")
}

func TestMCP_UploadFileMissingSource(t *testing.T) {
	t.Parallel()
	// The local surface, so this exercises the unreadable-file path rather than
	// schema rejection of source_path.
	session, _, ctx := newLocalTestSessionWithRuntime(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "upload_file",
		Arguments: map[string]any{
			"node_id":     "0",
			"filename":    "test.txt",
			"source_path": "/nonexistent/path/test.txt",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing source file")
	require.Contains(t, extractText(t, res), "unable to read local file")
}

func TestMCP_DownloadFileNotFound(t *testing.T) {
	t.Parallel()
	session, _, ctx := newLocalTestSessionWithRuntime(t)

	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "download_file",
		Arguments: map[string]any{
			"node_id":   "0",
			"filename":  "nonexistent.txt",
			"dest_path": "/home/testuser/should-not-exist.txt",
		},
	})
	require.NoError(t, err)
	require.True(t, res.IsError, "expected error for missing file")
}

// --- archive tool tests ---

func extractText(t *testing.T, res *sdkmcp.CallToolResult) string {
	t.Helper()
	var parts []string
	for _, c := range res.Content {
		if tc, ok := c.(*sdkmcp.TextContent); ok {
			parts = append(parts, tc.Text)
		}
	}
	return strings.Join(parts, "\n")
}

func TestMCP_ToolAnnotations_AllPresent(t *testing.T) {
	t.Parallel()
	session, ctx := newTestSession(t)

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)
	require.NotEmpty(t, res.Tools)

	// Every tool must have non-nil Annotations.
	for _, tool := range res.Tools {
		require.NotNilf(t, tool.Annotations, "tool %q is missing Annotations", tool.Name)
	}

	// Build a name->tool map for spot checks.
	byName := make(map[string]*sdkmcp.Tool, len(res.Tools))
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}

	// --- read-only tools ---
	readOnlyTools := []string{
		"cat", "list", "grep", "tags", "backlinks", "links",
		"info", "keg_settings", "stats",
		"list_files", "list_images",
		"list_indexes", "index_cat",
		"doctor", "lock_status", "node_history", "node_snapshot_view",
	}
	for _, name := range readOnlyTools {
		tool, ok := byName[name]
		require.Truef(t, ok, "read-only tool %q not found", name)
		require.Truef(t, tool.Annotations.ReadOnlyHint, "tool %q should have ReadOnlyHint=true", name)
		require.NotNilf(t, tool.Annotations.OpenWorldHint, "tool %q should have OpenWorldHint set", name)
		require.Falsef(t, *tool.Annotations.OpenWorldHint, "tool %q should have OpenWorldHint=false", name)
	}

	// --- destructive tools ---
	destructiveTools := []string{
		"remove", "move", "node_restore",
		"delete_file", "delete_image",
		"lock_force_release",
	}
	for _, name := range destructiveTools {
		tool, ok := byName[name]
		require.Truef(t, ok, "destructive tool %q not found", name)
		require.NotNilf(t, tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint set", name)
		require.Truef(t, *tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint=true", name)
	}

	// --- write non-destructive tools ---
	writeTools := []string{
		"create", "edit",
		"node_snapshot",
		"upload_file", "upload_image",
		"lock_acquire", "lock_release",
	}
	for _, name := range writeTools {
		tool, ok := byName[name]
		require.Truef(t, ok, "write tool %q not found", name)
		require.NotNilf(t, tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint set", name)
		require.Falsef(t, *tool.Annotations.DestructiveHint, "tool %q should have DestructiveHint=false", name)
	}

	// --- idempotent tool ---
	indexTool, ok := byName["index"]
	require.True(t, ok, "index tool not found")
	require.NotNil(t, indexTool.Annotations.DestructiveHint, "index should have DestructiveHint set")
	require.False(t, *indexTool.Annotations.DestructiveHint, "index should have DestructiveHint=false")
	require.True(t, indexTool.Annotations.IdempotentHint, "index should have IdempotentHint=true")
	require.NotNil(t, indexTool.Annotations.OpenWorldHint, "index should have OpenWorldHint set")
	require.False(t, *indexTool.Annotations.OpenWorldHint, "index should have OpenWorldHint=false")
}

func TestMCP_InvocationLogging(t *testing.T) {
	t.Parallel()

	_, th := mylog.NewTestLogger(t, slog.LevelDebug)
	lg := slog.New(th)

	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		Logger: lg,
	})

	// Call a known tool to trigger the middleware.
	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "doctor",
	})
	require.NoError(t, err)

	// The middleware logs asynchronously from the server goroutine, so
	// use RequireEntry with a short timeout.
	entry := mylog.RequireEntry(t, th, func(e mylog.LoggedEntry) bool {
		return e.Msg == "invocation"
	}, 2*time.Second)

	require.Equal(t, slog.LevelInfo, entry.Level)
	require.Equal(t, "mcp", entry.Attrs["surface"])
	require.Equal(t, "doctor", entry.Attrs["tool"])
	require.Equal(t, true, entry.Attrs["success"])

	// duration_ms should be present and non-negative. Sandbox tests use a
	// frozen clock, so the value may be 0.
	durationRaw, hasDuration := entry.Attrs["duration_ms"]
	require.True(t, hasDuration, "log entry should include duration_ms")
	durationMs, ok := durationRaw.(int64)
	require.True(t, ok, "duration_ms should be an int64")
	require.GreaterOrEqual(t, durationMs, int64(0), "duration_ms should be non-negative")

	// Client metadata from the test client.
	require.Equal(t, "test-client", entry.Attrs["client.name"])
	require.Equal(t, "0.1", entry.Attrs["client.version"])
}

func TestMCP_InvocationLogging_ToolError(t *testing.T) {
	t.Parallel()

	_, th := mylog.NewTestLogger(t, slog.LevelDebug)
	lg := slog.New(th)

	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		Logger: lg,
	})

	// Call a tool that will return an error result (nonexistent node).
	res, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{"99999"}},
	})
	require.NoError(t, err) // RPC itself succeeds; the tool returns IsError.
	require.True(t, res.IsError, "tool should return an error result")

	entry := mylog.RequireEntry(t, th, func(e mylog.LoggedEntry) bool {
		return e.Msg == "invocation" && e.Attrs["tool"] == "cat"
	}, 2*time.Second)

	require.Equal(t, false, entry.Attrs["success"],
		"invocation log should reflect tool-level failure")
	require.Equal(t, "cat", entry.Attrs["tool"])
}

func TestMCP_InvocationLogging_WithKegAlias(t *testing.T) {
	t.Parallel()

	_, th := mylog.NewTestLogger(t, slog.LevelDebug)
	lg := slog.New(th)

	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{
		Logger: lg,
	})

	// Call a tool with an explicit keg alias in arguments.
	_, _ = session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "list",
		Arguments: map[string]any{"keg": "personal"},
	})

	entry := mylog.RequireEntry(t, th, func(e mylog.LoggedEntry) bool {
		return e.Msg == "invocation" && e.Attrs["tool"] == "list"
	}, 2*time.Second)

	require.Equal(t, "personal", entry.Attrs["keg"],
		"invocation log should include keg alias from tool arguments")
}

type invocationTelemetryRecorder struct {
	mu     sync.Mutex
	events []tapper.InvocationEvent
}

func (r *invocationTelemetryRecorder) Report(event tapper.InvocationEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, event)
}

func (*invocationTelemetryRecorder) Close(context.Context) {}

func (r *invocationTelemetryRecorder) snapshot() []tapper.InvocationEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]tapper.InvocationEvent(nil), r.events...)
}

func TestMCP_InvocationTelemetryReportsExactToolAndOutcome(t *testing.T) {
	reporter := &invocationTelemetryRecorder{}
	session, ctx := newTestSessionWithOpts(t, mcp.ServerOptions{Reporter: reporter})

	_, err := session.CallTool(ctx, &sdkmcp.CallToolParams{Name: "doctor"})
	require.NoError(t, err)
	failed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "cat",
		Arguments: map[string]any{"node_ids": []string{"99999"}, "keg": "sensitive-target"},
	})
	require.NoError(t, err)
	require.True(t, failed.IsError)

	events := reporter.snapshot()
	require.Len(t, events, 2)
	require.Equal(t, tapper.InvocationEvent{Surface: "mcp", Tool: "doctor", Success: true}, events[0])
	require.Equal(t, "mcp", events[1].Surface)
	require.Equal(t, "cat", events[1].Tool)
	require.False(t, events[1].Success)
	require.Empty(t, events[1].Command)
	require.Nil(t, events[1].Interactive)
}
