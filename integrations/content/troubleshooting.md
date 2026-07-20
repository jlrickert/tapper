# Troubleshooting

## Troubleshooting

- **Stale search results.** The index is rebuilt on write. If a tool returns
  stale data, re-issue the call or read the index directly via
  `mcp__tapper__list_indexes` and `mcp__tapper__index_cat`.
- **Missing older node IDs.** Use `mcp__tapper__list` with an increased
  `limit` — the default page size may be hiding older nodes.

## See also

- `mcp__tapper__info` reports concise diagnostics for the resolved KEG;
  `mcp__tapper__keg_settings` reports its configuration. Use
  `mcp__tapper__orient` to establish the session's flight and KEG context.
- The tapper documentation in the source repository covers configuration
  precedence and the server's concurrency model.
