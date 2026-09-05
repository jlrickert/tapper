---
name: tapper
description: Orient to Tapper flights and operate on KEGs through MCP-first safety rules.
---

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

Every authority-bearing tool below accepts an optional top-level `flight`.
When the connection starts without a flight, omission uses normal
identity-authorized full access and an explicit value selects any listed real
flight exactly. With a real pinned root, omission selects that root and an
explicit value selects the root or an accessible flattened descendant.
Authentication,
configuration, namespace/license discovery, `session_refresh`, `list_flights`,
`flight_show`, and `keg_search` do not accept `flight`. MCP resources use root authority
while rendering graph-wide discovery.

`flight` is an **operational** parameter, not a discovery-only one: `list`,
`cat`, `create`, `edit`, and `remove` all take it and all honour it. Pass the
**exact canonical name** orientation printed under "Selectable flights",
namespace sigil and `+` included:

```json
{ "flight": "@admin/+mcp-smoke-readonly", "keg": "@admin/mcp-smoke-readonly", "limit": 10 }
```

A bare `mcp-smoke-readonly` or `+mcp-smoke-readonly` is not a canonical name.
Unqualified names resolve against the active KEG, so under a root whose cover is
empty there is nothing to resolve them against and the call fails
`ORIENTATION_DENIED` — which reads like a missing feature but is a name that did
not resolve. Re-read the orientation output and copy the name verbatim.

`keg` never grants authority. It selects a target *within* the authority the
call already has; it cannot reach a KEG the selected flight does not cover.
Naming an uncovered KEG is `ORIENTATION_DENIED`, and that is the access control
working, not a bug. To widen what a call can reach, pass a `flight` that covers
it.

## Orientation and management

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__orient` | Read-only view of no-flight identity authority or the pinned real root, an optional exact real-flight selection, revision, available KEGs, and current instructions. |
| `mcp__tapper__session_refresh` | Retry activation only after a broken configured root is repaired. It never replaces active no-flight or real-flight authority; narrowing no-flight access requires a new connection. |
| `mcp__tapper__keg_list`, `mcp__tapper__keg_create` | Discover every identity-accessible KEG at its real role with no flight, or the effective projection of a selected real flight; no-flight creation uses namespace membership while real-flight creation also requires `manage_kegs`. |
| `mcp__tapper__flight_create`, `mcp__tapper__flight_edit`, `mcp__tapper__flight_delete` | Manage Hub flights when the selected flight grants `manage_flights`; edits and deletes require the manifest hash returned by `flight_show`, and normal Hub ACLs still apply. |
| `mcp__tapper__list_flights`, `mcp__tapper__flight_show` | Ungoverned flight discovery; these tools do not select call authority. |

`keg_list` returns `@namespace/keg<TAB>role<TAB>@namespace/+flight` text
(the final field is empty for no-flight authority) and
structured
`{"kegs":[{"ref":"@namespace/keg","role":"viewer|editor|admin","flights":["@namespace/+flight"]}]}`
rows. Omission is the aggregate selector; supplying `flight` requests an exact
projection. The removed `all` property is rejected by schema validation.
With no flight, aggregate results contain every identity-accessible KEG.
With a real pinned root, they are restricted to that root and its currently
accessible transitive descendants.

## Search and discovery

| Tool                                                  | Purpose                                                                                                                                                  |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__grep`                                   | Regex search over node content. Supports `ignore_case`, `limit`, `max_lines`, and `id_only`.                                                             |
| `mcp__tapper__tags`                                   | List tags or filter nodes by a boolean expression over tags, attributes, and dot-prefix stats fields (for example `tapper and .created>2026-01-01`).     |
| `mcp__tapper__list`                                   | List nodes in a keg with optional filters.                                                                                                               |
| `mcp__tapper__cat`                                    | Read one or more nodes. Each structured row pairs `node_id` and `hash` with that node's `content` and `meta`, so a read feeds straight into `edit`. Supports `meta_only`, `content_only`, `stats_only`, and `tag` expression selection as an alternative to explicit node IDs. |
| `mcp__tapper__links`                                  | Outbound links from a node.                                                                                                                              |
| `mcp__tapper__backlinks`                              | Inbound links to a node.                                                                                                                                 |
| `mcp__tapper__list_indexes`, `mcp__tapper__index_cat` | Read generated index files (tag index, changelog, and others).                                                                                           |
| `mcp__tapper__keg_settings`                           | Read targeted title, summary, updated metadata, and instructions for one or more selected KEGs; batches accept up to 100 canonical references.          |
| `mcp__tapper__keg_search`                             | Case-insensitive literal search across identity-accessible canonical refs, titles, and summaries. Returns at most 50 rows and never grants operational access. |

Pass `id_only: true` to `grep` and `tags` when you only need IDs for follow-up
reads — it keeps token consumption bounded on large result sets.

## Query expressions

`mcp__tapper__list` (via `query`), `mcp__tapper__tags` (via `query`), and
`mcp__tapper__cat` (via `tag`) accept a boolean expression language that
filters nodes. Three predicate kinds combine with the standard boolean
operators:

| Predicate   | Example                                  | Matches                   |
| ----------- | ---------------------------------------- | ------------------------- |
| Tag         | `golang`                                 | nodes tagged `golang`     |
| Attribute   | `status=done`, `status!=draft`           | values in `meta.yaml`     |
| Stats field | `.created>2026-01-01`, `.accessCount>=5` | values in `stats.json`    |

Operators: `and` (`&&`), `or` (`||`), `not` (`!`), plus parentheses for
grouping. Precedence is `not` > `and` > `or`, so `a or b and not c`
parses as `a or (b and (not c))`.

Examples:

- `tapper and .created>2026-01-01` — tapper-tagged nodes created this year
- `(golang or rust) and status=done` — done nodes tagged with either language
- `status=draft or not shipped` — drafts or anything not shipped

Prefer a targeted query over reading many nodes and filtering in your own
code; the index does the work in O(matches) rather than O(total).

## Maintenance

| Tool                                                                           | Purpose                                                                                                               |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__create`                                                          | Atomically create 1–100 nodes. Each is a markdown `content` document plus an optional YAML `meta` document; the title is the content's H1. Nodes in one batch reference each other with `{{node:KEY}}`. |
| `mcp__tapper__edit`                                                            | Call `cat`, then atomically replace `content`, `meta`, or both for 1–100 `nodes[]`; every item requires that node's returned hash, and one hash covers content and metadata together. |
| `mcp__tapper__move`                                                            | Call `cat`, then relocate a node using its required returned hash.                                                    |
| `mcp__tapper__remove`                                                          | Call `cat`, then atomically remove 1–100 `nodes[]`, each carrying its own required returned hash.                     |
| `mcp__tapper__delete_file`, `mcp__tapper__delete_image`                        | Destructive attachment operations — see the Snapshots section below before calling.                                 |
| `mcp__tapper__node_snapshot`                                                   | Capture a revision before a destructive or large edit.                                                                |
| `mcp__tapper__node_history`, `mcp__tapper__node_snapshot_view`                 | Inspect read-only prior revisions.                                                                                    |
| `mcp__tapper__node_restore`                                                    | Recover the current node from a prior revision.                                                                       |
| `mcp__tapper__keg_settings_edit`                                               | Call `keg_settings`, then replace the complete validated KEG YAML using its required returned hash. Requires an `admin` flight cover or `full_access` plus editor/admin identity access. |

Schema edits and deletes similarly require the hash from `schema_read`. Every
conflict performs no operation: merge the change into returned current content
or refetch with the corresponding read, then retry with the returned current
hash.

A hash covers exactly one write. Every successful write returns a new one and
invalidates the hash you sent, so a sequence like edit-then-delete needs a
fresh read between the two calls rather than a reused token. Node ids are
per-keg counters as well: node 4 in one keg is unrelated to node 4 in another.

### Writing a node

A node is two documents and nothing else: `content`, the markdown body whose H1
is the title, and `meta`, the complete metadata document. Three placement rules
cover most first-attempt failures:

- `schema` is a property of the item itself, a sibling of `meta` — never a key
  inside the metadata.
- `meta` is a **YAML string**, not a JSON object. `"type: document\n"` is
  right; `{"type": "document"}` is not.
- `content` must not open with a `---` frontmatter block. Metadata has one
  home, and that is `meta`.

Use `schema_list` to see the names a keg accepts, then:

```json
{"nodes": [{"key": "a1", "content": "# Title\n\nBody", "meta": "type: document\n", "schema": "document"}]}
```

`edit` takes the same two documents per item plus that node's current hash from
`cat`, and either document may be omitted to leave it untouched:

```json
{"nodes": [{"node_id": "12", "content": "# Revised\n\nBody", "expected_hash": "HASH_FROM_CAT"}]}
```

`remove` carries only ids and hashes:

```json
{"nodes": [{"node_id": "12", "expected_hash": "HASH_FROM_CAT"}]}
```

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
- `mcp__tapper__edit` writing `meta`, which replaces the node's whole
  metadata document and so overwrites existing tags and attributes.
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
- `mcp__tapper__node_snapshot_view` — reads a prior revision without
  changing the current node.
- `mcp__tapper__node_restore` — recovers the current node from a specific
  snapshot revision.

If you are unsure whether an in-place edit warrants a snapshot, take
one. The cost is negligible. For `remove`, a snapshot is not a
recovery path — preserve the content some other way first.

Tapper supports two link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg (configured):** `[title](keg:ALIAS/NODEID)` — resolves the keg
  through active configuration and is parsed by the index into a cross-keg
  edge.
- **Cross-keg (fully qualified):**
  `[title](keg:@NAMESPACE/ALIAS/NODEID)` — identifies the namespace and keg
  explicitly and is parsed into a cross-keg edge.

Both forms appear in backlinks. Prefer intra-keg links when the target is in
the same keg. A bare `keg:` reference in node prose is plain text: it does not
create a graph link or backlink. Bare references remain valid as CLI arguments,
configuration values, schema values, and tool parameters.

Linking across kegs is ordinary authoring, but *copying* nodes across them is
not an agent operation: no tool moves or duplicates nodes between kegs. Read
the source with `mcp__tapper__cat` and `mcp__tapper__create` the node in the
target, which also lets you adjust its links deliberately. Bulk transfer
between kegs is an operator task the user runs outside MCP.

## Attachments

A node's uploaded files and images live in two directories inside the node's
own directory, so they are linked relative to it — the same base the `../NODEID`
form above counts from:

- **File:** `[label](./assets/FILE)` — anything uploaded with
  `mcp__tapper__upload_file`.
- **Image:** `![alt](./images/IMAGE)` — anything uploaded with
  `mcp__tapper__upload_image`.

**Both directory names are plural**: `assets/` and `images/`, never `asset/` or
`image/`. Uploading succeeds regardless of how you later write the link, so a
singular path fails silently as a broken reference rather than as an error.

Use `mcp__tapper__list_files` and `mcp__tapper__list_images` to get the exact
stored names; the upload may normalize the filename you supplied.

## Secret handling

- Never store credentials, API tokens, private keys, session cookies, customer
  secrets, or unredacted sensitive production data in a KEG.
- Do not paste secrets into node content, metadata, links, snapshots, files, or
  images. Snapshot history is durable and does not make secret storage safe.
- When evidence contains sensitive values, record a redacted description and a
  safe reference to the authorized system that owns the secret.
- If a secret is discovered in a KEG, stop before copying or editing it further
  and follow the user's incident and credential-rotation process.

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
