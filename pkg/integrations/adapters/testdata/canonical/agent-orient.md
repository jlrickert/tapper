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
