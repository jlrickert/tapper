# Tool inventory

## Search and discovery

| Tool                                                  | Purpose                                                                                                                                                  |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__grep`                                   | Regex search over node content. Supports `ignore_case`, `limit`, `max_lines`, and `id_only`.                                                             |
| `mcp__tapper__tags`                                   | List tags or filter nodes by a boolean expression over tags, attributes, and dot-prefix stats fields (for example `tapper and .created>2026-01-01`).     |
| `mcp__tapper__list`                                   | List nodes in a keg with optional filters.                                                                                                               |
| `mcp__tapper__cat`                                    | Read one or more node bodies. Supports `meta_only`, `content_only`, `stats_only`, and `tag` expression selection as an alternative to explicit node IDs. |
| `mcp__tapper__links`                                  | Outbound links from a node.                                                                                                                              |
| `mcp__tapper__backlinks`                              | Inbound links to a node.                                                                                                                                 |
| `mcp__tapper__graph`                                  | Graph traversal for multi-hop relationships.                                                                                                             |
| `mcp__tapper__list_indexes`, `mcp__tapper__index_cat` | Read generated index files (tag index, changelog, and others).                                                                                           |

Pass `id_only: true` to `grep` and `tags` when you only need IDs for follow-up
reads — it keeps token consumption bounded on large result sets.

## Maintenance

| Tool                                                                           | Purpose                                                                                                               |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__create`                                                          | Allocate a new numbered node. Accepts title, lead, tags, and attributes at creation time.                             |
| `mcp__tapper__edit`                                                            | Write content to a node. Markdown frontmatter in the payload is written to `meta.yaml`; the body becomes `README.md`. |
| `mcp__tapper__meta`                                                            | Update a node's metadata without touching content.                                                                    |
| `mcp__tapper__move`                                                            | Relocate a node or rename its ID.                                                                                     |
| `mcp__tapper__remove`, `mcp__tapper__delete_file`, `mcp__tapper__delete_image` | Destructive operations — see the Snapshots section below before calling.                                             |
| `mcp__tapper__node_snapshot`                                                   | Capture a revision before a destructive or large edit.                                                                |
| `mcp__tapper__node_history`, `mcp__tapper__node_restore`                       | Inspect or roll back to prior revisions.                                                                              |
