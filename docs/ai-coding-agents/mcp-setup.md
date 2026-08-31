# MCP Server Setup

The `tap mcp` command starts a Model Context Protocol server on stdio, exposing
the same agent-safe tools and resources as Tapper Hub's authenticated `/mcp`
endpoint — the one difference being attachment transfers, where `tap mcp` can
also read and write local paths because it runs on your machine. Both publish
connection-pinned authority at initialization. Orientation is a read-only view;
`session_refresh` retries only a broken explicit selection. Without a flight,
the server uses normal identity-authorized full access for the connection
lifetime. This page is the advanced manual path for MCP hosts that are not
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

When no flight is selected, the complete tool inventory is visible. Bare calls
use every identity-accessible KEG at the caller's real role, and explicit
`flight` selects any listed real flight for that call. No-flight authority
never raises Hub ACLs or namespace membership. Create or choose a
least-privilege flight, pin it with `tap use --flight @namespace/+slug` (or
`tap mcp --flight`), and start a new connection. `session_refresh` cannot
narrow the existing connection. Seeing only the six recovery tools means an
explicitly configured root failed to initialize.

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
| `create` | Atomically create 1–100 nodes from `nodes[]`; unique keys support forward/backward `{{node:key}}` body references |
| `edit` | Read with `cat`, then atomically replace 1–100 nodes from `edits[]`; every item requires that node's `expected_hash` |
| `meta` | Read `node_ids[]` without tokens, or read with `cat` and atomically replace metadata through `updates[]`; every update requires that node's `expected_hash` |
| `remove` | Read with `cat`, then atomically delete 1–100 `nodes[]`; every item requires its own `expected_hash` |
| `move` | Read with `cat`, then move a node using its required `expected_hash` |
| `keg_settings_edit` | Read the full document with `keg_settings`, then replace it using its required `expected_hash`; requires admin flight authority and editor/admin KEG access |

Mutation inputs are array-only and each array contains 1–100 items:

```json
{"nodes":[{"key":"plan","title":"Plan","body":"See [task](../{{node:task}})"}]}
{"edits":[{"node_id":"12","content":"# Revised","expected_hash":"...","snapshot_before":true}]}
{"node_ids":["12","13"]}
{"updates":[{"node_id":"12","content":"type: plan\n","expected_hash":"...","snapshot_before":true}]}
{"nodes":[{"node_id":"12","expected_hash":"..."},{"node_id":"13","expected_hash":"..."}]}
{"nodes":[{"node_id":"12","message":"reviewed"}]}
```

The first and last shapes belong to `create` and `node_snapshot`; the middle
shapes cover `edit`, the two mutually exclusive `meta` modes, and `remove`.
Mutation results preserve request order and report `node_id`, the resulting
hash or snapshot revision, and advisory schema validation details when
applicable. A failed batch returns no partial results and commits none of its
changes.

Every protected mutation names the read that supplies its token: `cat` for
node edits, metadata updates, moves, and removals; `keg_settings` for settings;
`schema_read` for schema edits/deletes; and `flight_show` for flight
edits/deletes. A conflict performs no operation and returns the current hash
and, when practical, current content. Merge or refetch, then retry with that
current hash.

### Index, Diagnostics, And Safety

| Tool | Description |
| --- | --- |
| `index`, `list_indexes`, `index_cat` | Rebuild or inspect indexes |
| `doctor` | Check only the selected keg's health (not local Tapper configuration) |
| `node_history`, `node_snapshot`, `node_snapshot_view`, `node_restore` | Manage node snapshots; `node_snapshot` accepts 1–100 nodes atomically |
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
| `keg_list` | List canonical KEGs, effective roles, and winning granting flights for the live pinned-root graph by default or exactly one supplied `flight` |
| `keg_search` | Search all identity-accessible KEG refs, titles, and summaries, including KEGs outside the flight graph; results grant no operational access |
| `auth_info` | Return structured credential-safe `identities[]` and exact pinned-root-context `kegs[]` |

Each identity includes only its hub locator, user ID, username, display name,
default namespace, and namespace names. Tokens, email, scopes, cookies, expiry,
and session data are never returned. Local MCP reports every configured
authenticated Hub identity; hosted MCP reports its single authenticated user.

### Automation And Setup

| Tool | Description |
| --- | --- |
| `import_from_keg` | Import nodes from another keg |
| `orient` | Return a read-only view of current instructions, selectable flights, and KEGs |
| `session_refresh` | Retry activation after a broken explicit selection is repaired; zero arguments and no authority replacement once active |
| `list_flights`, `flight_show` | Discover and inspect visible flights |
| `flight_create`, `flight_edit`, `flight_delete` | Manage Hub-backed flights when the active flight grants `manage_flights` and the identity owns/administers the target namespace; edits/deletes require the hash from `flight_show` |

MCP does not expose Tapper configuration, config templates, repository setup,
archive import/export, raw auth status, license text, keg visibility, or
namespace administration. Those remain external CLI, configuration, or Hub UI
operations.

The five batch mutation modes above intentionally use array-only inputs. Empty
batches, batches over 100 items, duplicate keys/IDs, unknown create
placeholders, missing or stale hashes, invalid schemas, or any persistence
failure reject the entire call. Structured results preserve request order and
include node IDs plus resulting hashes or snapshot revisions. The removed
single-item fields are not accepted by the published MCP schemas.

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

Every authority-bearing MCP tool accepts an optional per-call `flight`.
Omission uses pinned-root authority for operations, while default `orient` and
`keg_list` discovery aggregate the root and accessible descendants. Supplying
the pinned root or a listed descendant selects exactly that flight. The root
reference stays pinned to the connection, and every call reloads its live
graph and authority.

Hosted `/mcp` pins the authenticated account's MCP flight preference when the
connection initializes. Later manifest, relation, cover, identity-role, and ACL
changes are adopted on the next call. Deleting or losing the connection-pinned
root makes that connection permanently unavailable, so selecting a different
root requires a new launch.

## Troubleshooting

### Server Not Responding

Verify Tapper is installed and on `PATH`:

```bash
which tap
tap mcp --help
```

### No Flight Configured

The server connects with identity-authorized full access. Inspect the available
flights through `list_flights` and `flight_show`, then configure a
least-privilege project flight or start the MCP server with an explicit flight:

```bash
tap use --flight @acme/+release-42
tap mcp --flight @acme/+release-42
```

After `tap use`, disconnect and start a new MCP connection. The existing
connection remains on no-flight full access, and `session_refresh` reports
`already_active` with `nextAction:"new_session"`.

If a non-empty configured flight is missing, inaccessible, or unavailable, the
server fails closed into recovery mode. Repair that exact selection outside
MCP, then call `session_refresh` and `orient`; it never falls back to
no-flight full access.

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
