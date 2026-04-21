---
name: tapper
description: Interact with tapper KEGs via the mcp__tapper__* MCP interface — search, navigate, create, and maintain notes. MCP-first workflow.
---

# tapper

Interact with tapper KEGs (Knowledge Exchange Graphs) — numbered repositories
of markdown nodes with metadata, links, and indexed views. This skill describes
the conventions an agent should follow when operating against a tapper KEG.

## Rules

- **Use the `mcp__tapper__*` tools for every KEG operation.** They are the
  supported agent surface, return structured results, and participate in
  tapper's index and cache correctly.
- **Never read or write node files directly.** A node is a numbered directory
  containing `README.md`, `meta.yaml`, and `stats.json`. These are tapper's
  internal storage format. Reading them bypasses the index; writing them
  bypasses locking and snapshot history. Always go through
  `mcp__tapper__cat`, `mcp__tapper__edit`, `mcp__tapper__meta`, and related
  tools.

## Keg selection

Every MCP tool accepts an optional `keg` parameter naming the keg alias. Leave
it empty to use the configured default; set it to target another keg. To
discover which alias is active:

- `mcp__tapper__keg_info` — resolves the active keg and reports its path and
  node count.
- `mcp__tapper__list_kegs` — lists every configured keg alias.

## Bootstrapping a session

When starting work against an unfamiliar keg, these tools give a compact
orientation:

- `mcp__tapper__info` — tapper version and environment summary.
- `mcp__tapper__list_kegs` — configured keg aliases.
- `mcp__tapper__keg_info` — resolved target keg, path, node count.
- `mcp__tapper__tags` — tag inventory for the target keg (call with no
  arguments to list all tags).

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

## Snapshots

`mcp__tapper__node_snapshot` captures a node's current revision. Snapshots
are cheap, deduplicated by content hash, and stored inside the node they
belong to.

**Important: snapshots are stored inside the node. Removing a node
removes its snapshot history with it. A snapshot does not protect
against `mcp__tapper__remove`.** Before a removal, copy the content
somewhere that survives the deletion — read it with `mcp__tapper__cat`
and keep the result in your working context, or write it to another
node first. If you are not certain the removal is correct, defer it.

Snapshots do protect in-place edits. Take one before any of:

- `mcp__tapper__edit` that rewrites more than a section, pipes in
  generated content, or replaces content the agent did not author.
- `mcp__tapper__meta` changes that overwrite existing tags, attributes,
  or frontmatter.
- `mcp__tapper__move` — while the node survives the move, a snapshot
  before the rename makes it easy to confirm the move did not lose
  content and to diff against the pre-move state.
- Any chain of tool calls where a mistake partway through would leave
  the node in a state that is hard to reconstruct.

For batch edits across many nodes, snapshot each node that will be
modified before issuing the first write. A single failed tool call in
the middle of a batch is much cheaper to recover from when every
affected node has a restore point.

Recover with:

- `mcp__tapper__node_history` — lists available snapshots for a node,
  most recent first.
- `mcp__tapper__node_restore` — rolls a node back to a specific snapshot
  revision.

If you are unsure whether an in-place edit warrants a snapshot, take
one. The cost is negligible. For `remove`, a snapshot is not a
recovery path — preserve the content some other way first.

## Linking conventions

Tapper supports two link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg:** `keg:ALIAS/NODEID` — bare reference that the index parses
  into a cross-keg edge. Use when linking from one keg to a node in another.

Both forms appear in backlinks. Prefer intra-keg links when the target is in
the same keg.

## Troubleshooting

- **Stale search results.** The index is rebuilt on write. If a tool returns
  stale data, re-issue the call or read the index directly via
  `mcp__tapper__list_indexes` and `mcp__tapper__index_cat`.
- **Missing older node IDs.** Use `mcp__tapper__list` with an increased
  `limit` — the default page size may be hiding older nodes.

## See also

- `mcp__tapper__info` reports the tapper version, build, and current
  configuration; a good starting point for any session.
- The tapper documentation in the source repository covers configuration
  precedence and the server's concurrency model.
