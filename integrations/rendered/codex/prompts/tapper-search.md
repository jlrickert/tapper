Find nodes in the tapper keg that match a user-provided query.

- For a regex over node content, use `mcp__tapper__grep`.
- For filters by tag, attribute, or stat field, use `mcp__tapper__tags` or `mcp__tapper__list` with a boolean expression (for example `tapper and .created>2026-01-01`).
- Pass `id_only: true` when the result set is large so token usage stays bounded, then read specific nodes with `mcp__tapper__cat`.

Report the node IDs that match and, for each, a one-line summary of how it relates to the query.
