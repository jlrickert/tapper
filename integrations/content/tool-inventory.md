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

## Query expressions

`mcp__tapper__list` (via `query`), `mcp__tapper__tags` (via `query`), and
`mcp__tapper__cat` (via `tag`) accept a boolean expression language that
filters nodes. Three predicate kinds combine with the standard boolean
operators:

| Predicate   | Example                                  | Matches                   |
| ----------- | ---------------------------------------- | ------------------------- |
| Tag         | `golang`                                 | nodes tagged `golang`     |
| Attribute   | `status=done`, `status!=draft`           | values in `meta.yaml`     |
| Stats field | `.created>2026-01-01`, `.accessCount>=5` | values in `stats.json`    |

Operators: `and` (`&&`), `or` (`||`), `not` (`!`), plus parentheses for
grouping. Precedence is `not` > `and` > `or`, so `a or b and not c`
parses as `a or (b and (not c))`.

Examples:

- `tapper and .created>2026-01-01` — tapper-tagged nodes created this year
- `(golang or rust) and status=done` — done nodes tagged with either language
- `status=draft or not shipped` — drafts or anything not shipped

Prefer a targeted query over reading many nodes and filtering in your own
code; the index does the work in O(matches) rather than O(total).

`mcp__tapper__import` also accepts the expression via `tag_query` for
selecting source nodes to import.

## Maintenance

| Tool                                                                           | Purpose                                                                                                               |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__create`                                                          | Allocate a new numbered node. Accepts title, lead, tags, and attributes at creation time.                             |
| `mcp__tapper__edit`                                                            | Write content to a node. Markdown frontmatter in the payload is written to `meta.yaml`; the body becomes `README.md`. |
| `mcp__tapper__meta`                                                            | Update a node's metadata without touching content.                                                                    |
| `mcp__tapper__move`                                                            | Relocate a node or rename its ID.                                                                                     |
| `mcp__tapper__remove`, `mcp__tapper__delete_file`, `mcp__tapper__delete_image` | Destructive operations — see the Snapshots section below before calling.                                             |
| `mcp__tapper__node_snapshot`                                                   | Capture a revision before a destructive or large edit.                                                                |
| `mcp__tapper__node_history`, `mcp__tapper__node_snapshot_view`                 | Inspect read-only prior revisions.                                                                                    |
| `mcp__tapper__node_restore`                                                    | Recover the current node from a prior revision.                                                                       |
| `mcp__tapper__keg_settings_edit`                                               | Replace the complete validated KEG YAML document. Requires an `admin` flight cover or `full_access` plus editor/admin identity access; it never edits Tapper user/project configuration. |
