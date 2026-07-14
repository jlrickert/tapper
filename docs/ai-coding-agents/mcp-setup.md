# MCP Server Setup

The `tap mcp` command starts a Model Context Protocol server on stdio, exposing
KEG operations as tools. With an active flight, the local full surface pins it
for the lifetime of each MCP connection. Without one, the server starts in a
recovery-only state so the host can inspect flights safely. This page is the
advanced manual path for MCP hosts that are not using the bundled Claude Code
or Codex integrations.

Most users should use the official one-command installs in the project README's
[Connect AI Agents](../../README.md#connect-ai-agents) section. `tap integrate
claude` and `tap integrate codex` install native plugins from a local
marketplace embedded in the binary. Manual configuration below is supported
for generic MCP hosts that do not have a native Tapper plugin.

If an installed Tapper MCP connection goes stale, do not kill host-owned
processes. Claude users should try `/reload-plugins` and then a new session;
Codex users should start a new thread and then restart the app if needed.
Verify recovery with Tapper `info` and `orient`. Re-running `tap integrate` is
for installation or upgrades, not connection reset.

## Quick Start

Persist a project flight before connecting:

```bash
tap use --flight @acme/+release-42
# shorthand when the default namespace is already configured
tap use +release-42
```

### Claude Code Manual Setup

```bash
claude mcp add --transport stdio tapper -- tap mcp --flight @acme/+release-42
```

This adds `tapper` to your Claude Code MCP configuration. To target a specific
default organization keg:

```bash
claude mcp add --transport stdio tapper -- tap mcp --keg @acme/engineering
```

### Generic MCP Host

For any MCP host that reads a JSON configuration file, add:

```json
{
  "mcpServers": {
    "tapper": {
      "command": "tap",
      "args": ["mcp", "--flight", "@acme/+release-42"]
    }
  }
}
```

With a default keg:

```json
{
  "mcpServers": {
    "tapper": {
      "command": "tap",
      "args": ["mcp", "--keg", "@acme/engineering"]
    }
  }
}
```

## Available Tools

The MCP server exposes the same operating surface as the CLI. Exact tool
availability follows the installed Tapper version; inspect your MCP host's tool
list for the live surface.

When no flight is selected, the visible list is intentionally restricted to
`orient`, `list_flights`, `flight_show`, `auth_status`, and `config`. `orient`
and any guessed KEG-tool call explain that KEG tools are locked and direct the
agent to inspect flights through MCP, ask the user to run `tap use --flight
@namespace/+slug`, and reconnect. Claude's hidden, human-confirmed flight
switch control can expand the existing connection to the normal surface.

### Read

| Tool | Description |
| --- | --- |
| `cat` | Read content of one or more nodes |
| `list` | List nodes with optional query filtering |
| `grep` | Full-text search across node content |
| `tags` | List tags or find nodes by query |
| `backlinks` | Find nodes linking to a node |
| `links` | List outgoing links from a node |
| `info` | Show tapper info and environment |
| `keg_info` | Read keg configuration |
| `stats` | Show node statistics |

### Write

| Tool | Description |
| --- | --- |
| `create` | Create a node |
| `edit` | Replace node content |
| `meta` | Read or write node metadata |
| `remove` | Delete a node |
| `move` | Move a node to a different ID |

### Index, Diagnostics, And Safety

| Tool | Description |
| --- | --- |
| `index`, `list_indexes`, `index_cat` | Rebuild or inspect indexes |
| `doctor` | Check keg health |
| `node_history`, `node_snapshot`, `node_snapshot_view`, `node_restore` | Manage node snapshots |
| `lock_acquire`, `lock_release`, `lock_status`, `lock_force_release` | Coordinate cross-process node locks |

### Files And Images

| Tool | Description |
| --- | --- |
| `list_files`, `upload_file`, `download_file`, `delete_file` | Manage file attachments |
| `list_images`, `upload_image`, `download_image`, `delete_image` | Manage image attachments |

### Organization And Keg Administration

| Tool | Description |
| --- | --- |
| `keg_list` | List visible kegs on a hub |
| `keg_visibility` | Set keg visibility |
| `namespace_list` | Inspect namespaces |
| `auth_status` | Inspect authentication state |

User and role management tools are intentionally not exposed over MCP for now;
manage namespace members and keg grants through the hub UI.

### Automation And Setup

| Tool | Description |
| --- | --- |
| `repo_init` | Initialize a keg destination |
| `config`, `config_template` | Read config or starter templates |
| `import_from_keg` | Import nodes from another keg |
| `export`, `import` | Export or import keg archives |
| `graph` | Render a keg graph |
| `orient` | Return the shared KEG system orientation payload |
| `list_flights`, `flight_show` | Discover and inspect visible flights |
| `flight_create`, `flight_edit`, `flight_delete` | Manage other flights when the active flight grants `manage_flights` and the identity owns/administers the target namespace |
| `license` | Read bundled license text |

## Keg Targeting

Every KEG-oriented tool accepts an optional `keg` parameter to override the
server default. This enables multi-keg workflows without restarting the server:

- **Server default:** set via `tap mcp --keg @namespace/name` at startup.
- **Per-tool override:** pass `"keg": "@namespace/other"` in a tool call.
- **Fallback:** if neither is set, Tapper resolves the target through the normal
  config cascade: flags, project config, user config, and built-in defaults.

Use the per-tool `keg` parameter for cross-keg work. Do not restart the MCP
server just to switch between organization kegs.

There is no model-visible per-call `flight` parameter. The active flight is
server-owned connection state. Claude's bundled plugin provides a human-only
switch command; other hosts must reconnect after changing their project flight.

## Troubleshooting

### Server Not Responding

Verify Tapper is installed and on `PATH`:

```bash
which tap
tap mcp --help
```

### No Flight Configured

The server connects in recovery-only mode rather than failing startup. Inspect
the available flights through `list_flights` and `flight_show`, then configure
a project flight or start the MCP server with an explicit flight:

```bash
tap use --flight @acme/+release-42
tap mcp --flight @acme/+release-42
```

After `tap use`, reconnect the MCP host. A flight that is explicitly selected
but missing or invalid still fails startup so a stale configuration cannot
silently downgrade into recovery mode.

### Logs

The MCP server uses stdio for JSON-RPC, so diagnostic output goes to stderr by
default. Use `--log-file` to capture logs to disk:

```bash
claude mcp add --transport stdio tapper -- tap mcp --log-file /tmp/tap-mcp.log
```

### Testing Manually

Send a `tools/list` request to verify the server starts correctly:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | tap mcp
```

## See Also

- [Claude Code Plugin](claude-code-plugin.md)
- [Codex Install](codex.md)
- [Orientation Surface](orient.md)
- [Agent Conventions](agent-conventions.md)
