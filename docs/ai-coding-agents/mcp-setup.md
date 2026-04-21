# MCP Server Setup

The `tap mcp` command starts a Model Context Protocol server on stdio,
exposing all KEG operations as tools. This page is the advanced / manual
path — registering `tap mcp` with an MCP host that is not using the
Claude Code plugin, and the full tool reference.

Most users should start with the one-command installs in the project
README's [Using With AI Agents](../../README.md#using-with-ai-agents)
section. `tap integrate claude` and `tap integrate codex` cover
everything on this page automatically and also deliver the bundled
skill, prompts, and orientation content. Come back here if you want
fine-grained control (a different host, a non-default keg, a custom
transport) or you prefer a manual JSON config.

## Quick Start

### Claude Code (manual)

```bash
claude mcp add --transport stdio tapper -- tap mcp
```

This adds `tapper` to your Claude Code MCP configuration. The full KEG
tool surface becomes available immediately. To target a specific default
keg:

```bash
claude mcp add --transport stdio tapper -- tap mcp --keg notes
```

### Generic MCP host (JSON config)

For any MCP host that reads a JSON configuration file, add:

```json
{
  "mcpServers": {
    "tapper": {
      "command": "tap",
      "args": ["mcp"]
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
      "args": ["mcp", "--keg", "notes"]
    }
  }
}
```

## Available Tools

The MCP server registers 31 tools organized by category.

### Read (11 tools)

| Tool         | Description                              |
| ------------ | ---------------------------------------- |
| `cat`        | Read content of one or more nodes        |
| `list`       | List nodes with optional query filtering |
| `grep`       | Full-text search across node content     |
| `tags`       | List tags or find nodes by query         |
| `backlinks`  | Find nodes linking to a given node       |
| `links`      | List outgoing links from a node          |
| `list_kegs`  | List available keg aliases               |
| `info`       | Show tapper info and environment         |
| `keg_info`   | Read keg configuration                   |
| `stats`      | Show node statistics                     |
| `dir`        | Show keg directory path                  |

### Write (5 tools)

| Tool     | Description                         |
| -------- | ----------------------------------- |
| `create` | Create a new node                   |
| `edit`   | Replace node content                |
| `meta`   | Read or write node metadata (YAML)  |
| `remove` | Delete a node                       |
| `move`   | Move a node to a different ID       |

### Index (3 tools)

| Tool           | Description                      |
| -------------- | -------------------------------- |
| `index`        | Rebuild all indexes              |
| `list_indexes` | List available index names       |
| `index_cat`    | Read contents of a named index   |

### Diagnostics (1 tool)

| Tool     | Description                          |
| -------- | ------------------------------------ |
| `doctor` | Check keg health and report issues   |

### Snapshots (3 tools)

| Tool            | Description                              |
| --------------- | ---------------------------------------- |
| `node_history`  | List snapshot revisions for a node       |
| `node_snapshot` | Create a snapshot of a node's state      |
| `node_restore`  | Restore a node to a previous revision    |

### Locking (4 tools)

| Tool                 | Description                                 |
| -------------------- | ------------------------------------------- |
| `lock_acquire`       | Acquire a cross-process lock on a node      |
| `lock_release`       | Release a cross-process lock on a node      |
| `lock_status`        | Check the lock state of a node              |
| `lock_force_release` | Unconditionally remove a lock on a node     |

### Files (4 tools)

| Tool           | Description                          |
| -------------- | ------------------------------------ |
| `list_files`   | List file attachments for a node     |
| `list_images`  | List image attachments for a node    |
| `delete_file`  | Delete a file attachment             |
| `delete_image` | Delete an image attachment           |

## Keg Targeting

Every tool accepts an optional `keg` parameter to override the server
default. This enables multi-keg workflows without restarting the
server:

- **Server default:** set via `tap mcp --keg ALIAS` at startup.
- **Per-tool override:** pass `"keg": "other-alias"` in any tool call.
- **Fallback:** if neither is set, tapper resolves the target via the
  standard config cascade (CLI flags, `TAP_*` env vars, project
  config, user config, keg search paths).

## Troubleshooting

### Server not responding

Verify tapper is installed and on `PATH`:

```bash
which tap
tap mcp --help
```

### Logs

The MCP server uses stdio for JSON-RPC, so diagnostic output goes to
stderr by default. Use `--log-file` to capture logs to disk:

```bash
claude mcp add --transport stdio tapper -- tap mcp --log-file /tmp/tap-mcp.log
```

### Testing manually

Send a `tools/list` request to verify the server starts correctly:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | tap mcp
```

## See also

- [Claude Code Plugin](claude-code-plugin.md) — one-command install
  that wires both the MCP server and the `tapper` skill.
- [Agent Conventions](agent-conventions.md) — rules an agent should
  follow when operating against a tapper KEG.
