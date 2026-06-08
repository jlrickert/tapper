Read or list nodes from a tapper keg other than the active default.

Every tapper MCP tool accepts an optional `keg` parameter that overrides the server's current target. Use it instead of restarting the server or switching directories.

1. Call `mcp__tapper__hub_list` to see which kegs the configured hubs expose (qualified as `@namespace/keg`).
2. Call `mcp__tapper__keg_info` with `keg: "REF"` to confirm the resolved path and node count of the target keg.
3. Call `mcp__tapper__cat` with `keg: "REF"` and the node IDs to read. Pass `content_only: true` or `meta_only: true` to bound the payload.
4. For searches, call `mcp__tapper__list` or `mcp__tapper__grep` with the same `keg` override.

Report the node IDs read, the keg they came from, and a one-line summary of each.
