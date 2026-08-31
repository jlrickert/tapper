# Agent Conventions

These conventions apply to any AI agent operating against a tapper KEG,
regardless of host (Claude Code, Codex, or any other MCP-speaking agent).
They exist because of how tapper's locking, indexing, and revision
history work — ignoring them produces stale reads, lost writes, or
corrupted indices.

## MCP-first

Prefer the `mcp__tapper__*` MCP tools over the `tap` CLI whenever both
exist. MCP tools:

- Return structured results that agents can consume without parsing
  human-oriented output.
- Participate in tapper's index and cache correctly — an MCP write
  updates the in-memory index; a parallel CLI write does not see that
  update until the next load.
- Coordinate through the MCP server's per-node locks — concurrent MCP
  calls on the same node are serialized.

The CLI remains the right tool for interactive editor sessions,
diagnostics that have no MCP equivalent, and shell-integration tasks.
See the safety rules in the bundled [tapper skill](../../integrations/rendered/claude/tapper/skills/tapper/SKILL.md).

## Never read or write node files directly

A tapper node is a numbered directory:

```
<keg-root>/42/
  README.md        # node content
  meta.yaml        # user-facing metadata
  stats.json       # programmatic stats: hash, timestamps, access count
```

These files are tapper's internal storage format, not an external API.
Reading them bypasses the index. Writing them bypasses per-node
locking and snapshot history.

Always route through tapper's interfaces:

- `mcp__tapper__cat` to read (supports `content_only`, `meta_only`,
  `stats_only`).
- `mcp__tapper__edit` to write content — accepts markdown with
  frontmatter and separates it into `README.md` and `meta.yaml`
  automatically.
- `mcp__tapper__meta` to update metadata without touching content.

## CLI and MCP share Hub concurrency rules

CLI and MCP operations both go through Hub-compatible HTTP APIs. Durable
mutations serialize on the Hub and protected writes require current hashes.
Do not bypass MCP during an agent session: use the native tools so authority,
snapshots, and structured conflict recovery remain visible to the agent.

## Snapshot before large in-place edits; preserve content before remove

`mcp__tapper__node_snapshot` captures the node's current revision.
Snapshots are cheap and content-deduplicated.

**Snapshots are stored inside the node.** `mcp__tapper__remove` deletes
the node directory, which takes its entire snapshot history with it — a
snapshot is not a recovery path for removal. Before removing a node,
copy the content somewhere that survives the deletion: read it with
`mcp__tapper__cat` and keep it in your working context, or write it to
another node first. If you are uncertain the removal is correct, defer.

Snapshots do protect in-place edits. Take one before:

- Any `mcp__tapper__edit` that rewrites more than a section.
- Any `mcp__tapper__meta` overwrite of existing tags or attributes.
- `mcp__tapper__move` (the node survives the move, but a pre-move
  snapshot makes before/after diffs trivial).
- Any multi-tool transformation where a mistake partway through would
  be hard to undo manually.

Recover in-place edits with `mcp__tapper__node_history` (to see
available snapshots) and `mcp__tapper__node_restore` (to roll back).

## Use query expressions for tag and stats filtering

`mcp__tapper__tags` and `mcp__tapper__list` accept a `query` parameter
that is a boolean expression over:

- Bare tag names — `tapper` matches nodes tagged `tapper`.
- Attribute predicates — `key=value` against `meta.yaml` attributes.
- Dot-prefix stats predicates — `.created>2026-01-01` against
  `stats.json` fields.
- Combinations joined with `and`, `or`, parentheses, and `not`.

Examples:

```
tapper and .created>2026-01-01
(feature or plan) and not .deleted
```

Prefer a targeted query over reading many nodes and filtering in your
own code.

## Link conventions

Tapper recognizes two link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the
  current node's directory to the target node's directory.
- **Cross-keg:** `keg:@namespace/keg/NODEID` — fully qualified reference that
  the index parses into a cross-keg edge. Bare names can also be used when the
  active config resolves them unambiguously.

Both forms appear in backlinks. Prefer intra-keg links when the target
is in the same keg — they resolve in any markdown renderer.

## Concurrency expectations

- Per-node writes are serialized. Two tools editing the same node will
  queue, not race.
- Batch operations use the Hub's aggregate transaction boundary. Independent
  single-node operations may run concurrently.
- The index is rebuilt incrementally on write. Searches issued
  immediately after a write see the new state.
- `tap watch` consumes Hub events; it does not discover local files.

## Do not bypass completeness checks

- `mcp__tapper__create` asks the Hub to allocate and create a node atomically.
- `mcp__tapper__remove` runs integrity checks and updates the index. Filesystem
  deletion is not a Tapper operation.
- `mcp__tapper__move` handles ID reassignment and updates backlinks.

If a tool refuses an operation, the refusal is load-bearing — there is a
consistency or authority reason. Do not work around it through direct HTTP or
database access.
