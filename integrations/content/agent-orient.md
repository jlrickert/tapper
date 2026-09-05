# tapper

Interact with Tapper KEGs (Knowledge Exchange Graphs) through the native MCP
server.

**Call `mcp__tapper__orient` first, in every session, before doing anything
else — including answering the user.** Do not wait until KEG work looks like it
is starting. The selected flight carries the instructions describing what this
session is for, so until you orient you cannot know whether the work is KEG
work, which KEGs you may touch, or what the user actually expects of you. A
message as small as "test" is not a reason to defer: orient, then respond with
that context in hand.

After orienting, identify the relevant covered KEGs from their titles and
summaries and call `mcp__tapper__keg_settings` for those KEGs before operating
on them. Treat the selected flight, cover, flight instructions, and targeted KEG
instructions as the authoritative context for the session.

## Rules

- **Use the `mcp__tapper__*` tools for every KEG operation.** They are the
  supported agent surface, return structured results, and participate in
  tapper's index and cache correctly.
- **Never read or write node files directly.** A node is a numbered directory
  containing `README.md`, `meta.yaml`, and `stats.json`. These are tapper's
  internal storage format. Reading them bypasses the index; writing them
  bypasses locking and snapshot history. Always go through
  `mcp__tapper__cat`, `mcp__tapper__edit`, and related tools.
- **Treat the call-selected flight as MCP authority.** The root reference is
  pinned to the connection, but its manifest, transitive
  graph, and authorization are loaded before every authority-bearing call.
  Omit `flight` to use the root, or pass the root or one of the flattened
  descendants returned by orientation. A selected descendant contributes only
  its own instructions and authority; ancestor instructions and permission
  caps are not inherited. No `keg` argument grants authority, and neither does
  `defaultKeg`: naming a KEG chooses a target, and the flight decides whether
  you may reach it. When orientation lists a KEG under "Reachable via
  subflight", every call against it must carry that flight — reads included, so
  `cat`, `links`, and `backlinks` need it just as much as `edit` does.
  Omitting `flight` there returns `ORIENTATION_DENIED`, no matter what `keg`
  says.
- **Handle orientation failures explicitly.** `ORIENTATION_STALE` means
  authority raced between call resolution and Hub validation;
  `ORIENTATION_DENIED` means the selection is outside the accessible graph or lacks the
  requested permission; `ORIENTATION_UNAVAILABLE` is transient; and
  `ORIENTATION_ROOT_UNAVAILABLE` means this session can never replace its lost
  root. Refused operations report `operationPerformed=false` and do not require
  session reorientation. Review current authority before retrying, and never
  replay a mutation automatically.
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

- `mcp__tapper__orient` — read-only discovery returning the connection-pinned
  root, selected flight, ordered breadth-first selectable flights, selected
  path, effective graph KEGs with
  granting-flight provenance, revision, the selected flight's instructions,
  and canonical safety guidance. Omit `flight` for graph discovery or pass a
  canonical root/descendant for an exact projection.
- `mcp__tapper__keg_settings` — returns targeted title, summary, updated
  metadata, and instructions for one or more selected KEGs.
- `mcp__tapper__info` — returns concise diagnostics for a covered KEG.
- `mcp__tapper__keg_list` — lists canonical KEGs, effective roles, and winning
  granting flights for the live pinned-root graph by default or exactly one
  accessible flight when `flight` is supplied.
- `mcp__tapper__keg_search` — searches identity-accessible KEG metadata,
  including KEGs outside the flight graph. Results do not grant KEG access.

## Bootstrapping a session

Orientation is unconditional and comes first, before any other tool call and
before your first reply. It is not a lookup step you reach for once KEG work is
identified — it is how the session learns what it is for. Then load the
selected KEG instructions with `mcp__tapper__keg_settings`.

**Orient again after any context reset**, such as a clear or a compact. The MCP
connection survives those, so the server does not re-initialize and will not
re-send anything on its own — but the flight instructions you were operating
under are gone from your context. Re-orienting is cheap and idempotent, and it
resolves a fresh call-local view without changing session state. If you cannot
tell whether you have oriented in the current context, you have not; orient.

**The newest orientation wins.** A compaction summary may carry a paraphrase of
an earlier orientation, so a stale copy may sit earlier in your context than
the fresh one. Initialization deliberately sends only a minimal directive to
call `orient`; it does not contain flight context. Treat the most recent
`mcp__tapper__orient` result as authoritative and discard older copies outright
rather than reconciling them. When no flight is selected, the connection uses
normal identity-authorized full access and publishes the complete MCP tool
inventory. Bare calls see every identity-accessible KEG at the caller's real
role; this never raises Hub ACLs or namespace membership. An explicit `flight`
selects any listed real flight for that call and uses only its cover,
capabilities, and instructions.

If only `orient`, `session_refresh`, `list_flights`, `flight_show`, `auth_info`, and
`keg_search` appear, flight
authority failed to initialize. A real flight with an empty cover is still
active and publishes the complete registered tool inventory; its KEG calls
simply have no covered targets.

When spawning a native subagent, the controller passes the canonical descendant
reference in the assignment. The subagent must call `mcp__tapper__orient` with
that exact `flight` after startup and again after context compaction. It must
also pass the same `flight` to authority-bearing work calls; omission always
uses the root. Concurrent subagents may use different descendants without
changing shared session state. Merely mentioning ancestor instructions does
not grant or inherit their authority.

No-flight authority is pinned for the connection lifetime. Use it only to
bootstrap a least-privilege flight, then ask the user to pin that flight outside
MCP and start a new connection. `session_refresh` returns `already_active`
with `nextAction:"new_session"`; it cannot narrow the current connection.
Creating a KEG or flight does not change bare-call authority, although a newly
created real flight is immediately available for explicit call-local selection.

Recovery-only mode is reserved for an explicitly configured root that is
missing, inaccessible, invalid, or temporarily unavailable. In that mode only
the recovery tools appear. Fix the configured selection outside MCP, then call
`mcp__tapper__session_refresh` and `mcp__tapper__orient`.

If `mcp__tapper__orient` is unavailable, report that the Tapper MCP connection
is unavailable, ask the user to reconnect or restart the host session, and
never kill or signal host-owned processes. A flight with an empty cover exposes
no KEGs.
