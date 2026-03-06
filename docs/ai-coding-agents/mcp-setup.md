# MCP Server Setup

The `tap mcp` command starts a Model Context Protocol (MCP) server on stdio,
exposing all KEG operations as tools. Once configured, AI agents can read,
search, create, and manage notes without per-command permission prompts.

## Quick Start

### Claude Code

Register the MCP server with a single command:

```bash
claude mcp add --transport stdio tapper -- tap mcp
```

This adds tapper to your Claude Code MCP configuration. All 27 KEG tools become
available immediately.

To target a specific default keg:

```bash
claude mcp add --transport stdio tapper -- tap mcp --keg dev
```

### Manual Configuration

For agents that use a JSON config file, add the following to your MCP settings:

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
      "args": ["mcp", "--keg", "dev"]
    }
  }
}
```

## Available Tools

The MCP server registers 27 tools organized by category:

### Read (11 tools)

| Tool         | Description                              |
| ------------ | ---------------------------------------- |
| `cat`        | Read content of one or more nodes        |
| `list`       | List nodes with optional query filtering |
| `grep`       | Full-text search across node content     |
| `tags`       | List tags or find nodes by tag           |
| `backlinks`  | Find nodes linking to a given node       |
| `links`      | List outgoing links from a node          |
| `list_kegs`  | List available keg aliases               |
| `info`       | Show current keg info                    |
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

### Files (4 tools)

| Tool           | Description                          |
| -------------- | ------------------------------------ |
| `list_files`   | List file attachments for a node     |
| `list_images`  | List image attachments for a node    |
| `delete_file`  | Delete a file attachment             |
| `delete_image` | Delete an image attachment           |

## Keg Targeting

Every tool accepts an optional `keg` parameter to override the server default.
This enables multi-keg workflows without restarting the server:

- **Server default**: set via `tap mcp --keg ALIAS` at startup
- **Per-tool override**: pass `"keg": "other-alias"` in any tool call
- **Fallback**: if neither is set, uses the standard tapper config resolution
  (project config, user config, keg search paths)

## Troubleshooting

### Server not responding

Verify tapper is installed and on PATH:

```bash
which tap
tap mcp --help
```

### Logs

The MCP server uses stdio for JSON-RPC, so diagnostic output goes to stderr by
default. Use `--log-file` to capture logs:

```bash
claude mcp add --transport stdio tapper -- tap mcp --log-file /tmp/tap-mcp.log
```

### Testing manually

Send a `tools/list` request to verify the server starts correctly:

```bash
echo '{"jsonrpc":"2.0","id":1,"method":"tools/list"}' | tap mcp
```
