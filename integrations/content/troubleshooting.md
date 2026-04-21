# Troubleshooting

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
