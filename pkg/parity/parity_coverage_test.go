package parity_test

import (
	"reflect"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/jlrickert/tapper/pkg/cli"
	"github.com/jlrickert/tapper/pkg/tapper"
)

// tapMethodToSurfaces maps exported Tap method names to their expected CLI
// command and MCP tool name. This is the ground truth for surface coverage.
//
// Methods not present here are intentionally excluded from the parity check
// (e.g., internal helpers, config-only methods, or methods that don't have
// a direct consumer surface).
var tapMethodToSurfaces = map[string]struct {
	CLI string   // CLI command path (e.g., "list", "repo init", "index rebuild")
	MCP []string // MCP tool names covering it (e.g., "list", "repo_init", "index")
}{
	// Read operations
	"Cat":             {CLI: "cat", MCP: []string{"cat"}},
	"List":            {CLI: "list", MCP: []string{"list"}},
	"Grep":            {CLI: "grep", MCP: []string{"grep"}},
	"Tags":            {CLI: "tags", MCP: []string{"tags"}},
	"Backlinks":       {CLI: "backlinks", MCP: []string{"backlinks"}},
	"Links":           {CLI: "links", MCP: []string{"links"}},
	"Info":            {CLI: "info", MCP: []string{"info"}},
	"KegSettings":     {CLI: "keg settings", MCP: []string{"keg_settings"}},
	"KegSettingsEdit": {CLI: "keg settings edit", MCP: []string{"keg_settings_edit"}},
	"Stats":           {CLI: "stats", MCP: []string{"stats"}},
	"ListIndexes":     {CLI: "index list", MCP: []string{"list_indexes"}},
	"IndexCat":        {CLI: "index get", MCP: []string{"index_cat"}},
	"Doctor":          {CLI: "doctor", MCP: []string{"doctor"}},

	// Write operations
	"Create": {CLI: "create", MCP: []string{"create"}},
	"Edit":   {CLI: "edit", MCP: []string{"edit"}},
	// tap meta reads and writes; on MCP those halves live in different tools —
	// cat meta_only reads, edit writes — so the metadata capability is present
	// on both surfaces without a tool of its own.
	"Meta":   {CLI: "meta", MCP: []string{"cat", "edit"}},
	"Remove": {CLI: "rm", MCP: []string{"remove"}},
	"Move":   {CLI: "mv", MCP: []string{"move"}},

	// Index operations
	"Index": {CLI: "index rebuild", MCP: []string{"index"}},

	// Schema operations (type-based keg schemas). Full CRUD + validation is
	// exposed on both MCP surfaces; schema mutation resolves at editor role,
	// consistent with node writes.
	"ListSchemas":  {CLI: "schema list", MCP: []string{"schema_list"}},
	"ReadSchema":   {CLI: "schema get", MCP: []string{"schema_read"}},
	"CreateSchema": {CLI: "schema create", MCP: []string{"schema_create"}},
	"EditSchema":   {CLI: "schema edit", MCP: []string{"schema_edit"}},
	"DeleteSchema": {CLI: "schema rm", MCP: []string{"schema_delete"}},
	"Validate":     {CLI: "validate", MCP: []string{"validate"}},

	// Snapshot operations
	"NodeSnapshot":     {CLI: "snapshot create", MCP: []string{"node_snapshot"}},
	"NodeHistory":      {CLI: "snapshot history", MCP: []string{"node_history"}},
	"NodeSnapshotView": {CLI: "snapshot view", MCP: []string{"node_snapshot_view"}},
	"NodeRestore":      {CLI: "snapshot restore", MCP: []string{"node_restore"}},

	// File operations
	"ListFiles":     {CLI: "file ls", MCP: []string{"list_files"}},
	"ListImages":    {CLI: "image ls", MCP: []string{"list_images"}},
	"DeleteFile":    {CLI: "file rm", MCP: []string{"delete_file"}},
	"DeleteImage":   {CLI: "image rm", MCP: []string{"delete_image"}},
	"UploadFile":    {CLI: "file upload", MCP: []string{"upload_file"}},
	"DownloadFile":  {CLI: "file download", MCP: []string{"download_file"}},
	"UploadImage":   {CLI: "image upload", MCP: []string{"upload_image"}},
	"DownloadImage": {CLI: "image download", MCP: []string{"download_image"}},

	// Lock operations
	"Lock":        {CLI: "lock acquire", MCP: []string{"lock_acquire"}},
	"Unlock":      {CLI: "lock release", MCP: []string{"lock_release"}},
	"LockStatus":  {CLI: "lock status", MCP: []string{"lock_status"}},
	"ForceUnlock": {CLI: "lock force-release", MCP: []string{"lock_force_release"}},

	// Flights (keg restriction + agent instructions)
	"ListFlights":  {CLI: "flight list", MCP: []string{"list_flights"}},
	"GetFlight":    {CLI: "flight show", MCP: []string{"flight_show"}},
	"CreateFlight": {CLI: "flight create", MCP: []string{"flight_create"}},
	"EditFlight":   {CLI: "flight edit", MCP: []string{"flight_edit"}},
	"DeleteFlight": {CLI: "flight delete", MCP: []string{"flight_delete"}},

	// Keg discovery (hub-side). HubListKegs backs `tap keg list`; on MCP the
	// same listing is filtered through the session's active flight cover.
	"HubListKegs": {CLI: "keg list", MCP: []string{"keg_list"}},

	// Agent orientation remains shared. Native plugin installation is an
	// intentionally CLI-only host operation (see tapMethodsExcluded).
	"Orient": {CLI: "orient", MCP: []string{"orient"}},
}

// tapMethodsExcluded lists Tap methods that are intentionally excluded from
// surface coverage checks. These are internal helpers, config accessors, or
// methods that are not meant to be directly exposed as standalone tools.
var tapMethodsExcluded = map[string]string{
	"CreateBatch":       "MCP batch backing operation; CLI create remains a one-node command",
	"EditBatch":         "MCP batch backing operation; CLI edit remains a one-node command",
	"NodeSnapshotBatch": "MCP batch backing operation; CLI snapshot create remains a one-node command",
	"CatViews":          "structured accessor behind Cat; MCP cat uses it to return per-node precondition hashes without re-reading",
	"NodeHash":          "explicit CLI read-before-write helper; MCP reads return the same token in structured content",
	"SchemaHash":        "explicit CLI read-before-write helper; MCP schema_read returns the same token",
	"KegSettingsHash":   "explicit CLI read-before-write helper; MCP keg_settings returns the same token",
	"ConfigEdit":        "interactive editor; not exposed via MCP",
	"AuthRefreshAll":    "startup credential renewal invoked by the CLI root command (covers `tap` and `tap mcp`); not a user-facing operation",
	"UpdateFlight":      "underlying partial-update operation used by MCP flight_edit; CLI users use `flight edit`",
	"LookupKeg":         "internal resolution helper; not a user-facing operation",
	"ResolveNodeRef":    "internal node-reference resolver shared by surfaces; not a user-facing operation",
	"OrientationKegs":   "internal MCP authority helper; keg_list is the governed user-facing discovery surface",
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
	"SetFallbackKeg":           "CLI-only bootstrap step; persists the chosen keg as the user-level fallback after login, not an MCP operation",
	"SetBootstrapFlight":       "CLI-only bootstrap step; validates and persists the user-level flight baseline, not an MCP operation",
	"Use":                      "writes the project/user keg + flight to config; CLI-only config management by design",
	"UseStatus":                "CLI-only summary of the resolved keg/flight context; config inspection via `tap use`",
	"ActiveFlightName":         "internal pure read of the explicit flight or the loaded cascade's selection; backs Orient and MCP session adoption rather than being an operation of its own",
	"ActiveAgentName":          "internal pure read of the `tap launch` agent driving the process; reported in orientation and telemetry rather than being an operation of its own",
	"OrientationKegsForFlight": "internal authority projection used by MCP providers to compute a revision before rendering once",
	"IdentityKegCatalog":       "internal identity metadata projection used by MCP providers for graph discovery and ungoverned keg_search",
	// Dropped from MCP when the surface was unified behind providers: these
	// operate on machine-local Tapper state or perform tenant administration,
	// neither of which an agent should reach through either transport.
	"AuthStatus":     "replaced on MCP by the credential-free auth_info tool; `tap auth status` renders local token state and has no agent-safe peer",
	"Config":         "reads the local Tapper config cascade; configuration is an external CLI concern, not an MCP operation",
	"ConfigTemplate": "emits starter config files for a human to edit; CLI-only setup step",
	"InitKeg":        "provisions a keg destination on local disk or a hub; CLI-only setup step",
	"Export":         "writes a keg archive to the local filesystem; CLI-only bulk operation",
	"Import":         "reads a keg archive from the local filesystem; CLI-only bulk operation",
	"KegVisibility":  "UI-only visibility management; MCP must not flip a keg between public and private",
	"NamespaceList":  "namespace discovery folded into auth_info's identity payload; the standalone tool was tenant-administration shaped",
	"License":        "prints bundled license text; CLI-only via `tap version --license`",
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
			// Check every mapped MCP tool exists.
			require.NotEmpty(t, surfaces.MCP, "Tap.%s maps to no MCP tool", method)
			for _, tool := range surfaces.MCP {
				require.Contains(t, mcpTools, tool,
					"Tap.%s is mapped to MCP tool %q but tool is not registered", method, tool)
			}

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
	env := newParityEnv(t)
	res, err := env.session.ListTools(env.ctx, nil)
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
