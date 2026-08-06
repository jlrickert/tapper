# tapper

Interact with Tapper KEGs (Knowledge Exchange Graphs) through the native MCP
server.

**Call `mcp__tapper__orient` first, in every session, before doing anything
else — including answering the user.** Do not wait until KEG work looks like it
is starting. The active flight carries the instructions describing what this
session is for, so until you orient you cannot know whether the work is KEG
work, which KEGs you may touch, or what the user actually expects of you. A
message as small as "test" is not a reason to defer: orient, then respond with
that context in hand.

After orienting, identify the relevant covered KEGs from their titles and
summaries and call `mcp__tapper__keg_settings` for those KEGs before operating
on them. Treat the active flight, cover, flight instructions, and targeted KEG
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
- **Leave node 0 alone.** It is the keg's placeholder landing node, created with
  the keg itself.

## Node 0

Every keg has a node `0`. It is not an ordinary node and is not yours to write:

- It is the **placeholder** a link to unwritten content lands on, so its content
  is deliberately generic.
- It carries **no `type`**, on purpose. Do not add one, and do not read its
  absence as a defect to repair — a node without a type is normally a schema
  error, and node 0 is the documented exception.
- **Removing it breaks the keg.** Tapper treats a missing node 0 as an
  uninitialized keg, so deleting it makes every other node unreachable.

When you have content to write, create a new node. If node 0 genuinely needs to
change — a keg's landing page is a reasonable thing to want — say so and let the
user decide; do not fold it into unrelated work.

## Flight-first orientation

Every MCP tool accepts an optional `keg` parameter. Use a covered KEG reference
returned by orientation to work across KEGs without changing directories or
restarting the MCP server.

- `mcp__tapper__orient` — returns the active flight, its cover and instructions,
  compact KEG discovery metadata, and canonical safety guidance.
- `mcp__tapper__keg_settings` — returns targeted title, summary, updated
  metadata, and instructions for one or more selected KEGs.
- `mcp__tapper__info` — returns concise diagnostics for a covered KEG.
- `mcp__tapper__keg_list` — lists the KEGs exposed by configured hubs.

## Bootstrapping a session

Orientation is unconditional and comes first, before any other tool call and
before your first reply. It is not a lookup step you reach for once KEG work is
identified — it is how the session learns what it is for. Then load the
selected KEG instructions with `mcp__tapper__keg_settings`.

**Orient again after any context reset**, such as a clear or a compact. The MCP
connection survives those, so the server does not re-initialize and will not
re-send anything on its own — but the flight instructions you were operating
under are gone from your context. Re-orienting is cheap and idempotent, and it
also picks up any configuration change made since you connected. If you cannot
tell whether you have oriented in the current context, you have not; orient.

**The newest orientation wins.** More than one copy can be present at once: the
connection's startup instructions are captured when the server connects and are
never refreshed afterwards, and a compaction summary may carry a paraphrase of
an earlier orientation. Both can be stale, and a stale copy may sit earlier in
your context than the fresh one. Treat the most recent `mcp__tapper__orient`
result as authoritative and discard the others outright rather than reconciling
them — in particular, a startup copy saying KEG tools are locked is wrong once
a later orientation has returned a flight. When no flight is selected, the MCP
server connects in a recovery-only state: KEG tools are locked, while
`mcp__tapper__list_flights` and `mcp__tapper__flight_show` remain available for
discovery. Ask the user to select a flight in Tapper configuration, then call
`mcp__tapper__orient` again.

When there is no flight to select — a fresh machine or account — the session
instead starts on a temporary **bootstrap flight**. Its cover is empty, so the
KEG tools stay locked, but `mcp__tapper__keg_create` and the flight mutation
tools are available so you can create the first KEG and the first flight.
Setting that up is the session's work; do it before anything else. You still
cannot *select* a flight — that stays a human action — so hand the setup back
to the user and call `mcp__tapper__orient` again once they confirm. The
orientation payload names exactly where they should do it.

If `mcp__tapper__orient` is unavailable, report that the Tapper MCP connection
is unavailable, ask the user to reconnect or restart the host session, and
never kill or signal host-owned processes. A flight with an empty cover exposes
no KEGs.
