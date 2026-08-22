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
	"Cat":           {CLI: "cat", MCP: "cat"},
	"List":          {CLI: "list", MCP: "list"},
	"Grep":          {CLI: "grep", MCP: "grep"},
	"Tags":          {CLI: "tags", MCP: "tags"},
	"Backlinks":     {CLI: "backlinks", MCP: "backlinks"},
	"Links":         {CLI: "links", MCP: "links"},
	"Info":          {CLI: "info", MCP: "info"},
	"KegSettings":   {CLI: "keg settings", MCP: "keg_settings"},
	"KegConfigEdit": {CLI: "keg settings edit", MCP: "keg_settings_edit"},
	"Stats":         {CLI: "stats", MCP: "stats"},
	"ListIndexes":   {CLI: "index list", MCP: "list_indexes"},
	"IndexCat":      {CLI: "index get", MCP: "index_cat"},
	"Doctor":        {CLI: "doctor", MCP: "doctor"},

	// Write operations
	"Create": {CLI: "create", MCP: "create"},
	"Edit":   {CLI: "edit", MCP: "edit"},
	"Meta":   {CLI: "meta", MCP: "meta"},
	"Remove": {CLI: "rm", MCP: "remove"},
	"Move":   {CLI: "mv", MCP: "move"},

	// Index operations
	"Index": {CLI: "index rebuild", MCP: "index"},

	// Schema operations (type-based keg schemas). Full CRUD + validation is
	// exposed on both MCP surfaces; schema mutation resolves at editor role,
	// consistent with node writes.
	"ListSchemas":  {CLI: "schema list", MCP: "schema_list"},
	"ReadSchema":   {CLI: "schema get", MCP: "schema_read"},
	"CreateSchema": {CLI: "schema create", MCP: "schema_create"},
	"EditSchema":   {CLI: "schema edit", MCP: "schema_edit"},
	"DeleteSchema": {CLI: "schema rm", MCP: "schema_delete"},
	"Validate":     {CLI: "validate", MCP: "validate"},

	// Snapshot operations
	"NodeSnapshot":     {CLI: "snapshot create", MCP: "node_snapshot"},
	"NodeHistory":      {CLI: "snapshot history", MCP: "node_history"},
	"NodeSnapshotView": {CLI: "snapshot view", MCP: "node_snapshot_view"},
	"NodeRestore":      {CLI: "snapshot restore", MCP: "node_restore"},

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
	// Note: "import" at top level is ImportFromKeg (live keg import).
	// "archive import" is Import (archive import). Different commands,
	// different Tap methods, same word. Only the live-keg one has an MCP peer.
	"ImportFromKeg": {CLI: "import", MCP: "import_from_keg"},

	// Flights (keg restriction + agent instructions)
	"ListFlights":  {CLI: "flight list", MCP: "list_flights"},
	"GetFlight":    {CLI: "flight show", MCP: "flight_show"},
	"CreateFlight": {CLI: "flight create", MCP: "flight_create"},
	"EditFlight":   {CLI: "flight edit", MCP: "flight_edit"},
	"DeleteFlight": {CLI: "flight delete", MCP: "flight_delete"},

	// Keg discovery (hub-side). HubListKegs backs `tap keg list`; on MCP the
	// same listing is filtered through the session's active flight cover.
	"HubListKegs": {CLI: "keg list", MCP: "keg_list"},

	// Agent orientation remains shared. Native plugin installation is an
	// intentionally CLI-only host operation (see tapMethodsExcluded).
	"Orient": {CLI: "orient", MCP: "orient"},
}

// tapMethodsExcluded lists Tap methods that are intentionally excluded from
// surface coverage checks. These are internal helpers, config accessors, or
// methods that are not meant to be directly exposed as standalone tools.
var tapMethodsExcluded = map[string]string{
	"CreateBatch":       "MCP batch backing operation; CLI create remains a one-node command",
	"EditBatch":         "MCP batch backing operation; CLI edit remains a one-node command",
	"MetaBatch":         "MCP batch backing operation; CLI meta remains a one-node command",
	"NodeSnapshotBatch": "MCP batch backing operation; CLI snapshot create remains a one-node command",
	"ConfigEdit":        "interactive editor; not exposed via MCP",
	"AuthRefreshAll":    "startup credential renewal invoked by the CLI root command (covers `tap` and `tap mcp`); not a user-facing operation",
	"UpdateFlight":      "underlying partial-update operation used by MCP flight_edit; CLI users use `flight edit`",
	"LookupKeg":         "internal resolution helper; not a user-facing operation",
	"ResolveNodeRef":    "internal node-reference resolver shared by surfaces; not a user-facing operation",
	"WatchNode": "streaming, not request/response: CLI surface is `tap watch` (long-lived stream); " +
		"MCP surface is the resources/subscribe protocol capability (not a tool), wired via " +
		"SubscribeHandler in pkg/mcp/server.go. Payload parity is impossible — MCP notifications " +
		"are spec-thin (URI only) while the CLI stream carries kind/field — but both surfaces " +
		"observe the same Tap.WatchNode events",
	"DoctorConfig":          "tapper-config health check helper; called by Doctor CLI/MCP surfaces",
	"ConfigExplain":         "shares surface with Config via --explain flag / explain field",
	"ReadImage":             "underlying byte-read helper used by image download surfaces; not a standalone operation",
	"AuthLogout":            "security: MCP agents must not be able to revoke hub credentials; CLI-only by design",
	"Integrate":             "CLI-only native plugin installation via the official `tap integrate` surface",
	"HubList":               "lists local hub connections (config inspection); CLI-only via `tap hub list`",
	"HubAdd":                "security: writes hub connections (incl. token refs) to user config; CLI-only by design",
	"HubRemove":             "security: mutates hub connections in user config; CLI-only by design",
	"HubSetDefault":         "writes the default hub to project/user config; CLI-only by design",
	"Bootstrap":             "CLI-only onboarding; writes user config + drives interactive login, not an MCP operation",
	"NamespaceCreate":       "UI-only namespace creation handoff; MCP must not advertise namespace creation",
	"NamespaceMembers":      "UI-only user/role inspection for now; MCP must not expose namespace member rosters",
	"NamespaceAddMember":    "UI-only user/role management for now; MCP must not mutate namespace members",
	"NamespaceSetRole":      "UI-only user/role management for now; MCP must not mutate namespace member roles",
	"NamespaceRemoveMember": "UI-only user/role management for now; MCP must not mutate namespace members",
	"KegGrants":             "UI-only user/role inspection for now; MCP must not expose keg grants",
	"KegGrant":              "UI-only user/role management for now; MCP must not mutate keg grants",
	"KegRevoke":             "UI-only user/role management for now; MCP must not mutate keg grants",
	"KegRename":             "UI-only alias management for now; MCP must not rename kegs",
	"SetBootstrapNamespace": "CLI-only bootstrap step; adopts the hub's default namespace after login, not an MCP operation",
	"SetHubDefaultNamespaceByURL": "CLI-only auth/bootstrap helper; adopts the hub's default namespace after login, " +
		"not a standalone user-facing operation",
	"SetFallbackKeg":       "CLI-only bootstrap step; persists the chosen keg as the user-level fallback after login, not an MCP operation",
	"SetBootstrapFlight":   "CLI-only bootstrap step; validates and persists the user-level flight baseline, not an MCP operation",
	"Use":                  "writes the project/user keg + flight to config; CLI-only config management by design",
	"UseStatus":            "CLI-only summary of the resolved keg/flight context; config inspection via `tap use`",
	"ActiveFlightName":     "internal pure read of the explicit flight or the loaded cascade's selection; backs Orient and MCP session adoption rather than being an operation of its own",
	"ActiveAgentName":      "internal pure read of the `tap launch` agent driving the process; reported in orientation and telemetry rather than being an operation of its own",
	"OrientationForFlight": "internal session-orientation builder used by initialize, orient, and the orient resource",
	// Dropped from MCP when the surface was unified behind providers: these
	// operate on machine-local Tapper state or perform tenant administration,
	// neither of which an agent should reach through either transport.
	"AuthStatus":     "replaced on MCP by the credential-free auth_info tool; `tap auth status` renders local token state and has no agent-safe peer",
	"Config":         "reads the local Tapper config cascade; configuration is an external CLI concern, not an MCP operation",
	"ConfigTemplate": "emits starter config files for a human to edit; CLI-only setup step",
	"InitKeg":        "provisions a keg destination on local disk or a hub; CLI-only setup step",
	"Export":         "writes a keg archive to the local filesystem; CLI-only bulk operation",
	"Import":         "reads a keg archive from the local filesystem; CLI-only bulk operation (import_from_keg covers the agent-safe node-level path)",
	"KegVisibility":  "UI-only visibility management; MCP must not flip a keg between public and private",
	"NamespaceList":  "namespace discovery folded into auth_info's identity payload; the standalone tool was tenant-administration shaped",
	"License":        "prints bundled license text; CLI-only via `tap version --license`",
	"Graph":          "deprecated and disabled on MCP: renders a standalone HTML page an agent cannot display, and returning ~8KB of markup as tool text is pure context cost; `tap graph --output` remains until the feature is removed",
	// Experimental launcher. Starting a process on the operator's machine is
	// not an agent operation and must not become an MCP tool.
	"Launch":        "CLI-only: starts an agent harness as a local subprocess; MCP must never spawn processes on its host",
	"ResolveLaunch": "pure resolution half of Launch, exposed so a dry run and a real run cannot drift",
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

	srv := mcp.NewServer(tap, "test", mcp.KegDefaults{
		KegTargetOptions: tapper.KegTargetOptions{Flight: "@local/+parity"},
	}, mcp.ServerOptions{SharedFilesystem: true})
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
// RunE are both included (e.g., "config" is both a parent and a runnable
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
