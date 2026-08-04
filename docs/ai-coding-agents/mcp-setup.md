# MCP Server Setup

The `tap mcp` command starts a Model Context Protocol server on stdio, exposing
the same agent-safe tools and resources as Tapper Hub's authenticated `/mcp`
endpoint — the one difference being attachment transfers, where `tap mcp` can
also read and write local paths because it runs on your machine. Both publish
immutable flight authority at initialization and on explicit orientation.
Without one, the server starts in a recovery-only state so the host can inspect
flights safely. This page is the advanced manual path for MCP hosts that are not
using the bundled Claude Code or Codex integrations.

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

The MCP server exposes a shared agent surface rather than every machine-local
CLI capability. Exact tool availability follows the installed Tapper version
and the active flight; inspect your MCP host's tool list for the live surface.

When no flight is selected, the visible list is intentionally restricted to
`orient`, `list_flights`, `flight_show`, and `auth_info`. `orient`
and any guessed KEG-tool call explain that KEG tools are locked and direct the
agent to inspect flights through MCP, ask the user to run `tap use --flight
@namespace/+slug`, and call `orient` again on the same connection.

### Read

| Tool | Description |
| --- | --- |
| `cat` | Read content of one or more nodes |
| `list` | List nodes with optional query filtering |
| `grep` | Full-text search across node content |
| `tags` | List tags or find nodes by query |
| `backlinks` | Find nodes linking to a node |
| `links` | List outgoing links from a node |
| `info` | Show concise diagnostics for the resolved keg |
| `keg_settings` | Read targeted minimal config for one or more selected KEGs; `minimal=false` reads one complete config |
| `stats` | Show node statistics |

### Write

| Tool | Description |
| --- | --- |
| `create` | Create a node |
| `edit` | Replace node content |
| `meta` | Read or write node metadata |
| `remove` | Delete a node |
| `move` | Move a node to a different ID |
| `keg_settings_edit` | Replace the complete validated KEG YAML document; requires admin flight authority and editor/admin KEG access |

### Index, Diagnostics, And Safety

| Tool | Description |
| --- | --- |
| `index`, `list_indexes`, `index_cat` | Rebuild or inspect indexes |
| `doctor` | Check only the selected keg's health (not local Tapper configuration) |
| `node_history`, `node_snapshot`, `node_snapshot_view`, `node_restore` | Manage node snapshots |
| `lock_acquire`, `lock_release`, `lock_status`, `lock_force_release` | Coordinate cross-process node locks |

### Files And Images

| Tool | Description |
| --- | --- |
| `list_files`, `upload_file`, `download_file`, `delete_file` | Manage file attachments |
| `list_images`, `upload_image`, `download_image`, `delete_image` | Manage image attachments |

The transfer tools come in two variants, chosen by whether the server shares a
filesystem with the agent driving it.

Local `tap mcp` runs on your machine, so a path names the same file on both
sides. It publishes the full round-trip: `upload_file` and `upload_image` accept
`source_path` and `file:` URIs alongside `data_base64`, data URIs, and embedded
resources; `download_file` writes to `dest_path`; and `download_image` takes an
optional `dest_path`, returning the image as MCP content when you omit it.

Hosted `/mcp` shares no filesystem with the agent, so a path there would name
the server's own disk. Its uploads accept only embedded resources, data URIs,
and base64 bytes; `download_image` always returns MCP image content; and
`download_file` is not registered. Those fields are absent from the published
schema rather than refused at call time, so a hosted agent never has the
vocabulary to ask.

### Discovery And Identity

| Tool | Description |
| --- | --- |
| `keg_list` | List identity-authorized kegs filtered through the active flight |
| `auth_info` | Return structured credential-safe `identities[]` and flight-filtered `kegs[]` |

Each identity includes only its hub locator, user ID, username, display name,
default namespace, and namespace names. Tokens, email, scopes, cookies, expiry,
and session data are never returned. Local MCP reports every configured
authenticated Hub identity; hosted MCP reports its single authenticated user.

### Automation And Setup

| Tool | Description |
| --- | --- |
| `import_from_keg` | Import nodes from another keg |
| `graph` | Render a keg graph |
| `orient` | Return the shared KEG system orientation payload |
| `list_flights`, `flight_show` | Discover and inspect visible flights |
| `flight_create`, `flight_edit`, `flight_delete` | Manage Hub-backed flights when the active flight grants `manage_flights` and the identity owns/administers the target namespace |

MCP does not expose Tapper configuration, config templates, repository setup,
archive import/export, raw auth status, license text, keg visibility, or
namespace administration. Those remain external CLI, configuration, or Hub UI
operations.

`import_from_keg` requires editor identity and flight authority on the source
when `leave_stubs` is requested, because that option rewrites source nodes.
Both transports also publish `tapper://orient` and the
`tapper://node/{node_id}{?keg}` resource template.

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
server-owned session state. Config-driven servers adopt configuration changes
only through explicit orientation; `tap mcp --flight` stays bound to that
identity for its process lifetime.

Hosted `/mcp` instead selects the authenticated account's global MCP flight
preference. A successful self-edit adopts the exact returned flight immediately;
a self-delete enters recovery immediately. Mutation tools disappear as soon as
the adopted flight no longer grants `manage_flights`.

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

After `tap use`, call `orient` on the existing session. A failed ordinary
refresh keeps the last valid authority; a blank selection intentionally enters
recovery mode. If local configuration still names a flight deleted through MCP,
`orient` reports the stale external reference until configuration is changed.

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
