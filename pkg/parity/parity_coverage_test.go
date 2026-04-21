package parity_test

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/jlrickert/cli-toolkit/sandbox"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/cli"
	"github.com/jlrickert/tapper/pkg/mcp"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// tapMethodToSurfaces maps exported Tap method names to their expected CLI
// command and MCP tool name. This is the ground truth for surface coverage.
//
// Methods not present here are intentionally excluded from the parity check
// (e.g., internal helpers, config-only methods, or methods that don't have
// a direct consumer surface).
var tapMethodToSurfaces = map[string]struct {
	CLI string // CLI command path (e.g., "list", "repo init", "index rebuild")
	MCP string // MCP tool name (e.g., "list", "repo_init", "index")
}{
	// Read operations
	"Cat":         {CLI: "cat", MCP: "cat"},
	"List":        {CLI: "list", MCP: "list"},
	"Grep":        {CLI: "grep", MCP: "grep"},
	"Tags":        {CLI: "tags", MCP: "tags"},
	"Backlinks":   {CLI: "backlinks", MCP: "backlinks"},
	"Links":       {CLI: "links", MCP: "links"},
	"ListKegs":    {CLI: "repo list", MCP: "list_kegs"},
	"Info":        {CLI: "config", MCP: "info"},
	"KegInfo":     {CLI: "info", MCP: "keg_info"},
	"Stats":       {CLI: "stats", MCP: "stats"},
	"Dir":         {CLI: "dir", MCP: "dir"},
	"Graph":       {CLI: "graph", MCP: "graph"},
	"ListIndexes": {CLI: "index list", MCP: "list_indexes"},
	"IndexCat":    {CLI: "index get", MCP: "index_cat"},
	"Doctor":      {CLI: "doctor", MCP: "doctor"},

	// Write operations
	"Create": {CLI: "create", MCP: "create"},
	"Edit":   {CLI: "edit", MCP: "edit"},
	"Meta":   {CLI: "meta", MCP: "meta"},
	"Remove": {CLI: "rm", MCP: "remove"},
	"Move":   {CLI: "mv", MCP: "move"},

	// Index operations
	"Index": {CLI: "index rebuild", MCP: "index"},

	// Snapshot operations
	"NodeSnapshot": {CLI: "snapshot create", MCP: "node_snapshot"},
	"NodeHistory":  {CLI: "snapshot history", MCP: "node_history"},
	"NodeRestore":  {CLI: "snapshot restore", MCP: "node_restore"},

	// File operations
	"ListFiles":     {CLI: "file ls", MCP: "list_files"},
	"ListImages":    {CLI: "image ls", MCP: "list_images"},
	"DeleteFile":    {CLI: "file rm", MCP: "delete_file"},
	"DeleteImage":   {CLI: "image rm", MCP: "delete_image"},
	"UploadFile":    {CLI: "file upload", MCP: "upload_file"},
	"DownloadFile":  {CLI: "file download", MCP: "download_file"},
	"UploadImage":   {CLI: "image upload", MCP: "upload_image"},
	"DownloadImage": {CLI: "image download", MCP: "download_image"},

	// Lock operations
	"Lock":        {CLI: "lock acquire", MCP: "lock_acquire"},
	"Unlock":      {CLI: "lock release", MCP: "lock_release"},
	"LockStatus":  {CLI: "lock status", MCP: "lock_status"},
	"ForceUnlock": {CLI: "lock force-release", MCP: "lock_force_release"},

	// Repo management
	"InitKeg":    {CLI: "repo init", MCP: "repo_init"},
	"RemoveRepo": {CLI: "repo rm", MCP: "repo_rm"},

	// Config operations
	"Config":         {CLI: "repo config", MCP: "config"},
	"ConfigTemplate": {CLI: "repo config template", MCP: "config_template"},

	// Archive operations
	// Note: "import" at top level is ImportFromKeg (live keg import).
	// "archive import" is Import (archive import). Different commands,
	// different Tap methods, same word.
	"Export":        {CLI: "archive export", MCP: "export"},
	"Import":        {CLI: "archive import", MCP: "import"},
	"ImportFromKeg": {CLI: "import", MCP: "import_from_keg"},

	// Site and serve (CLI-only or with MCP equivalents)
	"Site":  {CLI: "site", MCP: "site"},
	"Serve": {CLI: "mcp", MCP: "serve"},
}

// tapMethodsExcluded lists Tap methods that are intentionally excluded from
// surface coverage checks. These are internal helpers, config accessors, or
// methods that are not meant to be directly exposed as standalone tools.
var tapMethodsExcluded = map[string]string{
	"KegConfigEdit":   "interactive editor; not exposed via MCP",
	"ConfigEdit":      "interactive editor; not exposed via MCP",
	"LookupKeg":       "internal resolution helper; not a user-facing operation",
	"NewServeHandler": "internal HTTP handler factory; used by Serve",
	"DoctorConfig":    "tapper-config health check helper; called by Doctor CLI/MCP surfaces",
	"ConfigExplain":   "shares surface with Config via --explain flag / explain field",
	"Orient":          "MCP tool and Resources live in pkg/mcp; the matching `tap orient` CLI command lands in a later phase alongside `tap integrate`",
}

// TestCoverage_AllTapMethodsHaveBothSurfaces uses reflection to enumerate
// exported methods on *tapper.Tap and verify that each method listed in
// tapMethodToSurfaces has both a registered CLI command and MCP tool.
func TestCoverage_AllTapMethodsHaveBothSurfaces(t *testing.T) {
	t.Parallel()

	// Enumerate exported Tap methods via reflection.
	tapType := reflect.TypeOf(&tapper.Tap{})
	tapMethods := make(map[string]bool)
	for i := 0; i < tapType.NumMethod(); i++ {
		m := tapType.Method(i)
		if m.IsExported() {
			tapMethods[m.Name] = true
		}
	}

	// Verify every mapped method actually exists on Tap.
	for method := range tapMethodToSurfaces {
		require.True(t, tapMethods[method],
			"tapMethodToSurfaces references %q but it is not an exported method on *tapper.Tap", method)
	}

	// Collect MCP tool names from a live server.
	mcpTools := collectMCPToolNames(t)

	// Collect CLI command names from a live command tree.
	cliCommands := collectCLICommandPaths(t)

	// Check each mapped method has both surfaces.
	for method, surfaces := range tapMethodToSurfaces {
		t.Run("surface/"+method, func(t *testing.T) {
			// Check MCP tool exists.
			require.Contains(t, mcpTools, surfaces.MCP,
				"Tap.%s is mapped to MCP tool %q but tool is not registered", method, surfaces.MCP)

			// Check CLI command exists (just verify it's in the known set).
			require.Contains(t, cliCommands, surfaces.CLI,
				"Tap.%s is mapped to CLI command %q but command is not registered", method, surfaces.CLI)
		})
	}

	// Report any Tap methods that are neither mapped nor excluded.
	for method := range tapMethods {
		if _, mapped := tapMethodToSurfaces[method]; mapped {
			continue
		}
		if _, excluded := tapMethodsExcluded[method]; excluded {
			continue
		}
		t.Errorf("Tap.%s is not mapped in tapMethodToSurfaces and not in tapMethodsExcluded — add it to one of them", method)
	}
}

// collectMCPToolNames returns the set of registered MCP tool names.
func collectMCPToolNames(t *testing.T) map[string]bool {
	t.Helper()

	sb := sandbox.NewSandbox(t, &sandbox.Options{
		Data: testdata,
		Home: "/home/testuser",
		User: "testuser",
	}, sandbox.WithFixture("testuser", "~"))
	ctx := sb.Context()

	tap, err := tapper.NewTap(tapper.TapOptions{
		Runtime: sb.Runtime(),
	})
	require.NoError(t, err)

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{})
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()

	done := make(chan error, 1)
	go func() {
		done <- srv.Run(ctx, serverTransport)
	}()
	t.Cleanup(func() {
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("MCP server error: %v", err)
		}
	})

	client := sdkmcp.NewClient(&sdkmcp.Implementation{
		Name:    "coverage-test",
		Version: "0.1",
	}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { session.Close() })

	res, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	tools := make(map[string]bool)
	for _, tool := range res.Tools {
		tools[tool.Name] = true
	}
	return tools
}

// collectCLICommandPaths returns the set of CLI command paths (e.g., "list",
// "repo init", "index rebuild") by walking the Cobra command tree dynamically.
// This uses TapProfile which includes all commands (config, repo, etc.).
func collectCLICommandPaths(t *testing.T) map[string]bool {
	t.Helper()

	// Build the full command tree with TapProfile (includes all commands).
	root := cli.NewRootCmd(&cli.Deps{
		Profile: cli.TapProfile(),
	})

	paths := make(map[string]bool)
	walkCommands(root, "", paths)
	return paths
}

// walkCommands recursively walks a Cobra command tree, collecting command paths
// relative to the root. Leaf commands and parent commands that have their own
// RunE are both included (e.g., "repo config" is both a parent and a runnable
// command).
func walkCommands(cmd *cobra.Command, prefix string, paths map[string]bool) {
	for _, child := range cmd.Commands() {
		path := child.Name()
		if prefix != "" {
			path = prefix + " " + child.Name()
		}
		// Include this command if it is runnable or has subcommands.
		paths[path] = true
		// Recurse into subcommands.
		walkCommands(child, path, paths)
	}
}
