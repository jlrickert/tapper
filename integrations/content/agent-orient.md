# tapper

Interact with Tapper KEGs (Knowledge Exchange Graphs) through the native MCP
server. At the start of KEG work, call `mcp__tapper__orient` and treat the
returned active flight, cover, flight instructions, and covered-KEG
instructions as the authoritative context for the session.

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
- **Treat the active flight as MCP authority.** It determines the instructions
  and KEGs available to the agent. `defaultKeg` does not grant authority for an
  MCP session.

## Flight-first orientation

Every MCP tool accepts an optional `keg` parameter. Use a covered KEG reference
returned by orientation to work across KEGs without changing directories or
restarting the MCP server.

- `mcp__tapper__orient` — returns the active flight, its cover and instructions,
  covered KEG instructions, and canonical safety guidance.
- `mcp__tapper__info` — returns concise diagnostics for a covered KEG.
- `mcp__tapper__keg_list` — lists the KEGs exposed by configured hubs.

## Bootstrapping a session

Call `mcp__tapper__orient` first. Follow the pinned flight and KEG instructions
it returns. When no flight is selected, the local MCP server connects in a
recovery-only state: KEG tools are locked, while `mcp__tapper__list_flights`
and `mcp__tapper__flight_show` remain available for discovery. Ask the user to
select a flight in Tapper configuration, then reconnect. A flight with an empty
cover exposes no KEGs.
