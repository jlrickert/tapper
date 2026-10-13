---
name: tapper
description: Orient to Tapper flights and operate on KEGs through MCP-first safety rules.
---

# Operating MCP

Call `mcp__tapper__orient` first and again after context reset. Use its newest
instructions and authority. Then call `mcp__tapper__keg_settings_read` for each target
KEG before operating there. Use MCP for KEG operations; never bypass it by
reading or writing node storage. Leave placeholder node 0 alone.

The connection pins the Hub and root reference at activation. Live manifests,
relationships, credentials, and permissions reload for each authority-bearing
call. Omit flight to use the root, or pass a canonical accessible descendant
such as @team/+work for that call. A child supplies its own authority and
instructions; ancestor and sibling permissions are not inherited. Pass the same
flight on subsequent operations. keg selects a target and never grants access.

orient returns current active instructions, effective readable KEGs, and
immediate readable children. Orient with a child reference to inspect the next
level. With no root, keg_list and flight_list are the complete lists of what
your identity reads, and explore_kegs and explore_flights search the public
directory. Discovery is independent of selection.
flight_read inspects a permission-filtered declared manifest and its hash;
it never activates a session or selects operational authority.

No-root identity authority stays pinned for the connection lifetime. Creating
a flight does not narrow it. To change the root, the user selects it outside
MCP and starts a fresh connection. session_refresh only retries failed initial
activation; it cannot change an active connection.

A failed configured root exposes recovery tools: orient, session_refresh,
flight_list, flight_read, session_info, guide. Repair
the configured root outside MCP, then retry session_refresh and orient.
An active flight can have no effective KEGs and still expose the full inventory;
KEG denials in that scope do not imply missing tools.

On failure, follow code, message, action, and operationPerformed in the returned
payload. A false outcome means refused; null means unknown, so inspect state
before repeating a mutation. For task-specific help use guide topics authoring,
linking, snapshots, tools, or troubleshooting.

If orient is unavailable, report that the Tapper MCP connection is unavailable
and ask the user to reconnect or restart the host session. Never kill or signal
host-owned processes.

Every tool belongs to one category, and its description opens with the
category name:

- **Session**: who you are and what this session may do. Always available.
- **Memory**: read and write notes in the KEGs this session covers.
- **Explore**: search the Hub's public directory of KEGs, flights, and people.
- **Manage**: change the memory system itself: KEGs, settings, schemas,
  indexes, locks, flights, and agents. Writing a note never needs these.

One rule decides between listing and searching. Your own KEGs, flights,
namespaces, schemas, and tags are short, complete lists: read them with
`keg_list`, `flight_list`, `namespace_list`, `schema_list`, and `tag_list`.
Searching is only for note content (`node_search`) and for the public
directory (`explore_kegs`, `explore_flights`, `explore_people`). Explore never
finds notes and never grants access.

Every authority-bearing tool below accepts an optional top-level `flight`.
When the connection starts without a flight, omission uses normal
identity-authorized full access and an explicit value selects any listed real
flight exactly. With a real pinned root, omission selects that root and an
explicit value selects the root or an accessible flattened descendant.
`session_info`, `session_refresh`, `flight_list`, `flight_read`,
`explore_kegs`, `explore_flights`, `explore_people`, `namespace_list`,
`invitation_list`, `agent_list`, and `agent_read` do not accept `flight`. MCP resources use root
authority while rendering graph-wide discovery.

`flight` is an **operational** parameter, not a discovery-only one: `node_list`,
`node_read`, `node_create`, `node_edit`, and `node_delete` all take it and all honour it. Pass the
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

## Session

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__orient` | Read-only view of no-flight identity authority or the pinned real root, an optional exact real-flight selection, revision, available KEGs, the tool categories, and current instructions. |
| `mcp__tapper__session_refresh` | Retry activation only after a broken configured root is repaired. It never replaces active no-flight or real-flight authority; narrowing no-flight access requires a new connection. |
| `mcp__tapper__session_info` | Credential-free identity and default namespace. Namespace names do not assert administrative membership. |
| `mcp__tapper__namespace_list` | The namespaces you belong to, with your role. |
| `mcp__tapper__invitation_list` | Pending invitations addressed to your user: org memberships and keg grants that take effect only if accepted. Read-only: accepting or declining is your user's decision (Hub account page or `tap invitation`), so tell them rather than acting. |
| `mcp__tapper__guide` | Canonical guidance by topic: `operating`, `authoring` or `linking`, `snapshots`, `tools`, and `troubleshooting`. |

## Memory: your KEGs and flights

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__keg_list` | Every KEG this session can work in, at its real role with no flight, or the effective projection of a selected real flight, with title and description. The list is complete: read it rather than searching. Listings label KEGv2 KEGs. |
| `mcp__tapper__flight_list`, `mcp__tapper__flight_read` | Your readable flights; flight_read returns a permission-filtered declared manifest, including explicit empty collections/instructions and hash. Neither tool activates or selects authority. |

`keg_list` returns `@namespace/keg<TAB>role<TAB>@namespace/+flight<TAB>title<TAB>description`
text (the flight field is empty for no-flight authority, and empty trailing
fields are dropped) and structured
`{"kegs":[{"ref":"@namespace/keg","role":"viewer|editor|admin","title":"…","description":"…","flights":["@namespace/+flight"]}]}`
rows. Omission is the aggregate selector; supplying `flight` requests an exact
projection. The removed `all` property is rejected by schema validation.
With no flight, aggregate results contain every identity-accessible KEG.
With a real pinned root, they are restricted to that root and its currently
accessible transitive descendants.

## Memory: finding notes

| Tool                                                  | Purpose                                                                                                                                                  |
| ----------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__node_search`                                   | Find the nodes most relevant to a query, ranked by the Hub across every KEG the call may read, one `keg`, or the listed `kegs`. Takes plain terms, `"quoted phrases"`, and `-exclusions`. Hits carry `ref`, `score`, `title`, matched `fields`, and a `snippet` with highlights; follow `next_cursor`. |
| `mcp__tapper__node_grep`                                   | Regex match over node content lines in one KEG, in node id order, for exact identifiers, symbols, and audits. Supports `ignore_case`, `limit`, `max_lines`, and `id_only`. |
| `mcp__tapper__tag_list`                                   | List tags or filter nodes by a boolean expression over tags, attributes, and dot-prefix stats fields (for example `tapper and .created>2026-01-01`).     |
| `mcp__tapper__node_list`                                   | List nodes in a keg with optional filters.                                                                                                               |
| `mcp__tapper__node_read`                                    | Read one or more nodes. Each structured row pairs `node_id` and `hash` with that node's `content` and `meta`, so a read feeds straight into `node_edit`. Supports `meta_only`, `content_only`, `stats_only`, and `query` expression selection as an alternative to explicit node IDs. |
| `mcp__tapper__node_links`                                  | Outbound links from a node.                                                                                                                              |
| `mcp__tapper__node_backlinks`                              | Inbound links to a node.                                                                                                                                 |
| `mcp__tapper__index_list`, `mcp__tapper__index_read` | Read generated index files (tag index, changelog, and others).                                                                                           |
| `mcp__tapper__keg_settings_read`                           | Read targeted title, description, updated metadata, and instructions for one or more selected KEGs; batches accept up to 100 canonical references.          |

Start topic lookups with `node_search`; reach for `node_grep` when you need
every line matching an exact pattern. Pass `id_only: true` to `node_grep` and
`tag_list` when you only need IDs for follow-up reads — it keeps token
consumption bounded on large result sets.

## Query expressions

`mcp__tapper__node_list` (via `query`), `mcp__tapper__tag_list` (via `query`), and
`mcp__tapper__node_read` (via `query`) accept a boolean expression language that
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

## Memory: writing notes

| Tool                                                                           | Purpose                                                                                                               |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__node_create`                                                          | Atomically create 1–100 nodes. Each is a markdown `content` document plus an optional YAML `meta` document; the title is the content's H1. Nodes in one batch reference each other with `{{node:KEY}}`. |
| `mcp__tapper__node_edit`                                                            | Call `node_read`, then atomically replace `content`, `meta`, or both for 1–100 `nodes[]`. Each part has its own hash: supplied `content` requires `expected_content_hash` (the read's `content_hash`) and supplied `meta` requires `expected_meta_hash` (its `meta_hash`). Editing one part never invalidates the other's hash, and results return the new `content_hash` and `meta_hash`. For a small change to a long note, send `patch` (a unified diff of the changed lines with a little context) instead of `content`; it needs `expected_content_hash` too, and a patch that no longer fits is refused with the lines it expected. A patch keeps the body's line endings, and `\ No newline at end of file` may only end a hunk that reaches the end of the body. |
| `mcp__tapper__node_move`                                                            | Call `node_read`, then relocate a node using the combined `hash` from `node_read` as `expected_hash`. A trashed node's id stays reserved until it is restored or purged. |
| `mcp__tapper__node_delete`                                                          | Call `node_read`, then atomically remove 1–100 `nodes[]`, each carrying the combined `hash` from `node_read` as `expected_hash`. |
| `mcp__tapper__node_merge`                                                           | Deduplicate: `node_read` both, then write the merged body into the target (`content` or `patch`, optional `meta`). Links to the source are repointed at the target and the source goes to the trash as merged. Guard the source with its combined `hash`, the target with `content_hash` (and `meta_hash` with meta). |
| `mcp__tapper__keg_replace`                                                          | Manage. Find and replace across a keg in one atomic write (literal or `regex`, narrowed by `query`, optionally in `include_meta`). Run it with `dry_run` first to see every node that would change. A node the write would refuse (schemas, metadata that must parse, a body opening with frontmatter) is reported with its error, without its bodies, and left alone while the rest are written; `dry_run` reports the same errors. At most 500 matching nodes per call, refused ones included. |
| `mcp__tapper__trash_list`, `mcp__tapper__trash_restore`                            | Where the keg keeps a trash, list removed nodes and why they went, and restore 1–100 of them at their ids with their history and assets. An entry an archive restore moved to make way shows `displaced_from` (the id it was trashed at) and restores at its new id. |
| `mcp__tapper__file_delete`, `mcp__tapper__image_delete`                        | Destructive attachment operations — see the Snapshots section below before calling.                                 |
| `mcp__tapper__snapshot_create`                                                   | Capture a revision before a destructive or large edit.                                                                |
| `mcp__tapper__snapshot_list`, `mcp__tapper__snapshot_read`                 | Inspect read-only prior revisions.                                                                                    |
| `mcp__tapper__snapshot_restore`                                                    | Recover the current node from a prior revision.                                                                       |
| `mcp__tapper__keg_settings_edit` | Manage. Call `keg_settings_read` with `minimal=false`, then replace the complete YAML `data` using its `hash` as `expected_hash`. Requires identity admin and effective flight admin authority when a flight is selected. |

Schema edits and deletes similarly require the hash from `schema_read`. Every
conflict performs no operation: merge the change into returned current content
or refetch with the corresponding read, then retry with the returned current
hash — for `node_edit`, the returned `currentContentHash` or `currentMetaHash`
of the part being written.

A hash covers the resource version read. `node_read` returns three per node:
`content_hash` and `meta_hash` for `node_edit`, and the combined `hash` for
`node_delete` and `node_move`. Mutations may invalidate a hash, and not every
mutation returns a replacement for every hash, so a sequence like
edit-then-delete needs a fresh read between the two calls rather than a reused
token. Node ids are
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

`node_edit` takes the same two documents per item, each guarded by its own hash
from `node_read`, and either document may be omitted to leave it untouched:

```json
{"nodes": [{"node_id": "12", "content": "# Revised\n\nBody", "expected_content_hash": "CONTENT_HASH_FROM_READ"}]}
{"nodes": [{"node_id": "12", "meta": "type: document\n", "expected_meta_hash": "META_HASH_FROM_READ", "schema": "document"}]}
```

On a strict keg (the default for new kegs) every `node_create` item and every
`node_edit` item that supplies `meta` needs `schema`; `type` in `meta` alone does
not count. A content-only edit keeps the node's current type and is validated
against it, so it needs no `schema`.

`node_delete` carries only ids and the combined hashes:

```json
{"nodes": [{"node_id": "12", "expected_hash": "HASH_FROM_READ"}]}
```

## Explore: the public directory

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__explore_kegs` | Public KEGs on the Hub, matched literally by reference, title, or description. |
| `mcp__tapper__explore_flights` | Public flights on the Hub. Metadata only, never instructions. |
| `mcp__tapper__explore_people` | People and org namespaces on the Hub, matched by name or display name. Role is set where you already belong. |

Each takes a required, non-empty `query` and returns `ref<TAB>name<TAB>description<TAB>role`
text and structured `{"kind":"kegs","results":[{"ref","name","description","role"}],"truncated":false}`.
Results are capped; refine a truncated query, there is no cursor. A result
grants nothing: a public KEG outside this session's flight still needs a
flight that covers it before any Memory tool can read it. Explore asks the
connection-pinned Hub, never other configured Hubs.

## Manage: KEGs and flights

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__keg_create` | Create a KEG; no-flight creation uses namespace membership while real-flight creation also requires `manage_kegs`. Takes an optional immutable `kegv`: `2025-07` (KEGv1, the default) or `2026-09` (KEGv2 preview, where the Hub enables it). |
| `mcp__tapper__flight_create`, `mcp__tapper__flight_edit`, `mcp__tapper__flight_delete` | Manage Hub flights when the selected flight grants `manage_flights`; edits and deletes require the manifest hash returned by `flight_read`, and normal Hub ACLs still apply. |

## Manage: agents

Hub agents are a namespace-owned model, description, markdown instructions, and
tool allowlist. A flight names the agents that run on it. Members of a
namespace read its agents; owners and admins change them. Agent refs are
`@namespace/name`.

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__agent_list`, `mcp__tapper__agent_read` | Identity-readable agents in one namespace or every namespace you belong to; `agent_read` adds instructions and `effective_tools`. Neither accepts `flight`. |
| `mcp__tapper__agent_create`, `mcp__tapper__agent_edit`, `mcp__tapper__agent_delete` | Manage agents in a namespace you own or administer. A selected real flight must also grant `manage_flights`. `agent_edit` keeps omitted fields; an edit applies to open sessions on their next call. |

An agent's `tools` hold tool group ids and single tool names; empty means
every tool. Groups follow the categories: Memory is `keg:read`, `keg:write`,
and `flight:read`; Explore is `explore`; Manage is `keg:admin`,
`schema:admin`, `keg:maintain`, `flight:admin`, `agent:read`, and
`agent:admin`. The Session tools (`orient`, `guide`, `session_info`,
`session_refresh`, `namespace_list`, and `invitation_list`) are always
available.

`orient` gives the full active instructions, effective readable KEGs, and
immediate readable child flights. Orient with a child reference for the next
level. No-flight orientation points to `keg_list` and `flight_list` without
listing resources.

## Response and pagination contracts

Text content is JSON rendered from the same public object as structuredContent.
The message field preserves prose, diagnostics, and legacy formatted output.
Images remain in MCP image content blocks alongside JSON metadata.

For node_list, node_grep, tag_list, node_links, and node_backlinks, follow
next_offset until null,
keeping the other arguments unchanged. has_more=null means another page is
uncertain; a final nonempty page can be followed by an empty page. Pages are
live, not a stable snapshot; reverse reverses each page. limit defaults to 50
(0 means default; -1 unlimited). node_grep max_lines defaults to 3 per node; -1
returns all matching lines. Use node_read for complete bodies.

node_search pages with an opaque cursor instead: repeat the same query with
next_cursor as cursor until next_cursor is absent (text output ends with a
`next cursor:` line while more remain). An omitted or 0 limit lets the Hub
choose the page size; -1 is not accepted. Hits are ordered by score, then KEG, then node id;
mode reports `keyword` when the Hub ranked without semantic matching. A KEG
outside the flight's cover is never searched, and a Hub without ranked search
fails with a message pointing at node_grep.

The explore tools return truncated and have no cursor; refine the query.

Supported flight capabilities are manage_flights, manage_kegs, and delete_kegs; full_access
is rejected. Declared cover is not effective authority: inspect orient for the
permission-checked relationship expansion. Creating or inspecting a flight
does not activate it or change the connection's pinned root.

## Memory: schemas, diagnostics, locks, and attachments

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__schema_list`, `mcp__tapper__schema_read` | Schema names and YAML data plus hash. Call `schema_list` before writing in a KEG and pick a type it lists. |
| `mcp__tapper__schema_validate` | Schema validation findings. |
| `mcp__tapper__keg_info`, `mcp__tapper__node_stats` | KEG diagnostics and node statistics. |
| `mcp__tapper__lock_acquire`, `mcp__tapper__lock_status`, `mcp__tapper__lock_release` | Advisory locks; acquisition returns a private token for release. node_edit does not accept a lock token. |
| `mcp__tapper__file_list`, `mcp__tapper__image_list` | Stored attachment names. |
| `mcp__tapper__file_upload`, `mcp__tapper__image_upload` | Inline base64/data URI/embedded resource uploads; local stdio also accepts source_path. Link the returned stored filename. |
| `mcp__tapper__image_download` | Image content block and metadata; local stdio optionally accepts dest_path. |
| `mcp__tapper__file_download` | Local stdio only: writes an explicit destination path. Absent on hosted MCP. |

## Manage: schemas and repair

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__schema_create`, `mcp__tapper__schema_edit`, `mcp__tapper__schema_delete` | Schema lifecycle; requires admin, with current hashes for edit/delete. Writing a note never needs these. |
| `mcp__tapper__index_rebuild` | Index rebuild; requires editor. |
| `mcp__tapper__keg_check` | KEG health findings. |
| `mcp__tapper__lock_force_release` | Break a lock another session holds. |

Configuration, namespace administration, license, archive, and video tools are
not registered here. Features present elsewhere in Tapper/Hub are not thereby
callable over MCP. Use tools/list for this connection's available inventory.

Flight cover inputs accept role strings with default depth 2. Custom depth is
readable in flight_read but cannot be set with flight_create/flight_edit. Omit
cover on partial edits to preserve existing entries and depths.


`mcp__tapper__keg_delete` permanently deletes an empty or populated KEG and all
its data, including snapshots. Supply an explicit canonical `keg` such as
`@team/disposable` and, optionally, the standard call-local `flight`.
No-flight calls require identity admin permission. Flight-scoped deletion also
requires `delete_kegs` and effective admin cover from that selected flight.
`manage_kegs` alone cannot delete; deletion does not require `manage_kegs`.
No `expected_hash` is accepted: the settings hash does not cover a whole KEG.
Success returns `keg` and `deleted: true` in matching text and structured JSON.
Missing KEGs return the normal not-found error.

`file_delete` and `image_delete` document `filename` as the attachment name.
Legacy `name` is accepted. One nonempty name is required; when both fields are
supplied they must match exactly, or the call is rejected before mutation.

## Snapshots

`mcp__tapper__snapshot_create` captures a node's current revision. Snapshots
are cheap, deduplicated by content hash, and stored inside the node they
belong to.

**Important: snapshots are stored inside the node. A snapshot does not
protect against `mcp__tapper__node_delete`.** On a hub keg, a removed node
goes to the keg's trash with its snapshots, files, and images:
`mcp__tapper__trash_list` shows it and `mcp__tapper__trash_restore` puts it
back, until the keg's retention purges it. Until then its id stays reserved:
no node can be moved onto it. An archive restore that needs the id moves the
trashed node to a new id first (`displaced_from` in `mcp__tapper__trash_list`
names the old one), and restoring it puts it back at the new id. Elsewhere, removing a node removes
its snapshot history with it. Either way, before a removal you are not sure
of, copy the content somewhere that survives the deletion — read it with
`mcp__tapper__node_read` and keep the result in your working context, or
write it to another node first. If you are not certain the removal is
correct, defer it.

Snapshots do protect in-place edits. Take one before any of:

- `mcp__tapper__node_edit` that rewrites more than a section, pipes in
  generated content, or replaces content the agent did not author.
- `mcp__tapper__node_edit` writing `meta`, which replaces the node's whole
  metadata document and so overwrites existing tags and attributes.
- `mcp__tapper__node_move` — while the node survives the move, a snapshot
  before the rename makes it easy to confirm the move did not lose
  content and to diff against the pre-move state.
- Any chain of tool calls where a mistake partway through would leave
  the node in a state that is hard to reconstruct.

For batch edits across many nodes, snapshot each node that will be
modified before issuing the first write. A single failed tool call in
the middle of a batch is much cheaper to recover from when every
affected node has a restore point.

Recover with:

- `mcp__tapper__snapshot_list` — lists available snapshots for a node,
  most recent first.
- `mcp__tapper__snapshot_read` — reads a prior revision without
  changing the current node.
- `mcp__tapper__snapshot_restore` — recovers the current node from a specific
  snapshot revision.

If you are unsure whether an in-place edit warrants a snapshot, take
one. The cost is negligible. For `node_delete`, a snapshot is not a
recovery path — the trash is, where the keg has one; otherwise preserve the
content some other way first.

Tapper supports these link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg (same namespace):** `[title](keg:ALIAS/NODEID)` — names a KEG
  in the source namespace.
- **Settings alias:** `[title](keg:~ALIAS/NODEID)` — explicitly resolves an
  alias declared in the source KEG settings `aliases`. The tilde distinguishes
  a settings alias from a KEG name. An alias keeps links working when the KEG
  it names moves: only the alias's target changes.
- **Cross-keg (fully qualified):**
  `[title](keg:@NAMESPACE/ALIAS/NODEID)` — identifies the namespace and keg
  explicitly and is parsed into a cross-keg edge. `NODEID` may be `0`, the
  KEG's root node, to link the KEG as a whole. The `@` is required:
  `keg:NAMESPACE/ALIAS/NODEID` is not a link, and `keg_check` reports it.

On a Hub, a flight that covers a KEG also covers the KEGs its notes link to,
as far as the cover entry's depth reaches, so a cross-keg link can widen what
an agent under that flight may read. A flight blocks a KEG by listing it with
role `none`.

Indexed links appear in backlinks when readable. Prefer intra-keg links when the target is in
the same keg. A bare `keg:` reference in node prose is plain text: it does not
create a graph link or backlink. Bare references remain valid as CLI arguments,
configuration values, schema values, and tool parameters.

Linking across kegs is ordinary authoring, but *copying* nodes across them is
not an agent operation: no tool moves or duplicates nodes between kegs. Read
the source with `mcp__tapper__node_read` and `mcp__tapper__node_create` the node in the
target, which also lets you adjust its links deliberately. Bulk transfer
between kegs is an operator task the user runs outside MCP.

## Attachments

A node's uploaded files and images live in two directories inside the node's
own directory, so they are linked relative to it — the same base the `../NODEID`
form above counts from:

- **File:** `[label](./assets/FILE)` — anything uploaded with
  `mcp__tapper__file_upload`.
- **Image:** `![alt](./images/IMAGE)` — anything uploaded with
  `mcp__tapper__image_upload`.

**Both directory names are plural**: `assets/` and `images/`, never `asset/` or
`image/`. Uploading succeeds regardless of how you later write the link, so a
singular path fails silently as a broken reference rather than as an error.

Use `mcp__tapper__file_list` and `mcp__tapper__image_list` to get the exact
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

Read message and action in either text JSON or structuredContent. Both supply
the same operational information, including error diagnostics and recovery.

- Validation errors: correct the named field using the input schema. meta is
  YAML text; mutations use nodes arrays. Do not retry unchanged arguments.
- PRECONDITION_REQUIRED: node_read, schema_read, full keg_settings_read, or
  flight_read
  supplies the matching expected_hash. node_edit needs expected_content_hash
  for supplied content and expected_meta_hash for supplied meta (node_read's
  content_hash and meta_hash). Snapshot before meaningful node edits.
- CONFLICT: operationPerformed=false; refetch, merge the intended change, and
  retry with the current hash (for node_edit, currentContentHash or
  currentMetaHash of the part being written). currentContent is diagnostic current content,
  not necessarily the replacement-document format accepted by node_edit.
- ORIENTATION_DENIED: inspect orient and select an accessible flight with the
  required authority; keg alone cannot grant access.
- ORIENTATION_UNAVAILABLE: transient lookup failure; retry a read first.
  ORIENTATION_ROOT_UNAVAILABLE: the pinned root is lost; restore it or have
  the user select a root and start a new connection.
- UNAUTHORIZED: have the user repair authentication to the same Hub, then
  orient. FORBIDDEN: valid credentials lack permission; login cannot grant it.
- operationPerformed=null: the write outcome is unknown. Inspect state before
  retrying, including possible creations; do not assume nothing happened.
- Missing listing rows: follow next_offset until null. For metadata search,
  refine a query marked truncated. node_search pages with next_cursor;
  node_grep limits matched lines; node_read reads full bodies. A tool absent
  from tools/list is unavailable, not a failed operation.
- "ranked search unavailable" (NOT_SUPPORTED): the Hub predates ranked
  search. Use node_grep with a regex for the terms, one KEG at a time.
- Suspected stale indexes: inspect index_list/index_read and keg_check.
  Rebuild with index_rebuild only when warranted and authorized; reads never repair indexes.
