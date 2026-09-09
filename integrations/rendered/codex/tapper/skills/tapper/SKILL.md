---
name: tapper
description: Orient to Tapper flights and operate on KEGs through MCP-first safety rules.
---

# Operating MCP

Call `mcp__tapper__orient` first and again after context reset. Use its newest
instructions and authority. Then call `mcp__tapper__keg_settings` for each target
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
level. With no root, orientation is search-first; keg_search and flight_search
find identity-readable metadata. Discovery is independent of selection.
flight_show inspects a permission-filtered declared manifest and its hash;
it never activates a session or selects operational authority.

No-root identity authority stays pinned for the connection lifetime. Creating
a flight does not narrow it. To change the root, the user selects it outside
MCP and starts a fresh connection. session_refresh only retries failed initial
activation; it cannot change an active connection.

A failed configured root exposes recovery tools: orient, session_refresh,
list_flights, flight_show, auth_info, keg_search, flight_search, guide. Repair
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

Every authority-bearing tool below accepts an optional top-level `flight`.
When the connection starts without a flight, omission uses normal
identity-authorized full access and an explicit value selects any listed real
flight exactly. With a real pinned root, omission selects that root and an
explicit value selects the root or an accessible flattened descendant.
Authentication,
`session_refresh`, `list_flights`,
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
| `mcp__tapper__list_flights`, `mcp__tapper__flight_show` | Identity-readable discovery; flight_show returns a permission-filtered declared manifest, including explicit empty collections/instructions and hash. Neither tool activates or selects authority. |

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
| `mcp__tapper__cat`                                    | Read one or more nodes. Each structured row pairs `node_id` and `hash` with that node's `content` and `meta`, so a read feeds straight into `edit`. Supports `meta_only`, `content_only`, `stats_only`, and `query` expression selection as an alternative to explicit node IDs. |
| `mcp__tapper__links`                                  | Outbound links from a node.                                                                                                                              |
| `mcp__tapper__backlinks`                              | Inbound links to a node.                                                                                                                                 |
| `mcp__tapper__list_indexes`, `mcp__tapper__index_cat` | Read generated index files (tag index, changelog, and others).                                                                                           |
| `mcp__tapper__keg_settings`                           | Read targeted title, description, updated metadata, and instructions for one or more selected KEGs; batches accept up to 100 canonical references.          |
| `mcp__tapper__keg_search`                             | Case-insensitive literal search across identity-accessible canonical refs, titles, and descriptions. Returns at most 50 rows and never grants operational access. |

Pass `id_only: true` to `grep` and `tags` when you only need IDs for follow-up
reads — it keeps token consumption bounded on large result sets.

## Query expressions

`mcp__tapper__list` (via `query`), `mcp__tapper__tags` (via `query`), and
`mcp__tapper__cat` (via `query`) accept a boolean expression language that
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
| `mcp__tapper__keg_settings_edit` | Call `keg_settings` with `minimal=false`, then replace the complete YAML `data` using its `hash` as `expected_hash`. Requires identity admin and effective flight admin authority when a flight is selected. |

Schema edits and deletes similarly require the hash from `schema_read`. Every
conflict performs no operation: merge the change into returned current content
or refetch with the corresponding read, then retry with the returned current
hash.

A hash covers the resource version read. Mutations may invalidate it, and not
every mutation returns a replacement, so a sequence like edit-then-delete needs a
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

## On-demand discovery and guidance

- `mcp__tapper__flight_search`: literal reference/title/description search over
  readable flights; at most 50 deterministic metadata results, with a truncation
  notice. No instructions or additional access are returned.
- `mcp__tapper__guide`: read canonical guidance by topic: `operating`, `authoring`
  or `linking`, `snapshots`, `tools`, and `troubleshooting`.
- `mcp__tapper__orient`: full active instructions, effective readable KEGs,
  and immediate readable child flights. Orient with a child reference for the
  next level. No-flight orientation points to search without listing resources.

## Response and pagination contracts

Text content is JSON rendered from the same public object as structuredContent.
The message field preserves prose, diagnostics, and legacy formatted output.
Images remain in MCP image content blocks alongside JSON metadata.

For list, grep, tags, links, and backlinks, follow next_offset until null,
keeping the other arguments unchanged. has_more=null means another page is
uncertain; a final nonempty page can be followed by an empty page. Pages are
live, not a stable snapshot; reverse reverses each page. limit defaults to 50
(0 means default; -1 unlimited). grep max_lines defaults to 3 per node; -1
returns all matching lines. Use cat for complete bodies.

keg_search returns warnings, partial, and truncated. flight_search returns
truncated. Refine a truncated metadata query; neither search has a cursor.
Both search the connection-pinned Hub, never other configured Hubs.

Supported flight capabilities are manage_flights, manage_kegs, and delete_kegs; full_access
is rejected. Declared cover is not effective authority: inspect orient for the
permission-checked relationship expansion. Creating or inspecting a flight
does not activate it or change the connection's pinned root.

## Other registered tools

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__auth_info` | Credential-free identity and default namespace. Namespace names do not assert administrative membership. |
| `mcp__tapper__schema_list`, `mcp__tapper__schema_read` | Schema names and YAML data plus hash. |
| `mcp__tapper__schema_create`, `mcp__tapper__schema_edit`, `mcp__tapper__schema_delete` | Schema lifecycle; requires admin, with current hashes for edit/delete. |
| `mcp__tapper__validate` | Schema validation findings. |
| `mcp__tapper__index` | Index rebuild; requires editor. |
| `mcp__tapper__info`, `mcp__tapper__stats`, `mcp__tapper__doctor` | KEG diagnostics, node statistics, health findings. |
| `mcp__tapper__lock_acquire`, `mcp__tapper__lock_status`, `mcp__tapper__lock_release`, `mcp__tapper__lock_force_release` | Advisory locks; acquisition returns a private token for release. edit does not accept a lock token. |
| `mcp__tapper__list_files`, `mcp__tapper__list_images` | Stored attachment names. |
| `mcp__tapper__upload_file`, `mcp__tapper__upload_image` | Inline base64/data URI/embedded resource uploads; local stdio also accepts source_path. Link the returned stored filename. |
| `mcp__tapper__download_image` | Image content block and metadata; local stdio optionally accepts dest_path. |
| `mcp__tapper__download_file` | Local stdio only: writes an explicit destination path. Absent on hosted MCP. |

Configuration, namespace administration, license, archive, and video tools are
not registered here. Features present elsewhere in Tapper/Hub are not thereby
callable over MCP. Use tools/list for this connection's available inventory.

Flight cover inputs accept role strings with default depth 2. Custom depth is
readable in flight_show but cannot be set with flight_create/flight_edit. Omit
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

`delete_file` and `delete_image` document `filename` as the attachment name.
Legacy `name` is accepted. One nonempty name is required; when both fields are
supplied they must match exactly, or the call is rejected before mutation.

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

Tapper supports these link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg (same namespace):** `[title](keg:ALIAS/NODEID)` — names a KEG
  in the source namespace.
- **Settings alias:** `[title](keg:~ALIAS/NODEID)` — explicitly resolves an
  alias declared in the source KEG settings. The tilde distinguishes a settings
  alias from a KEG name.
- **Cross-keg (fully qualified):**
  `[title](keg:@NAMESPACE/ALIAS/NODEID)` — identifies the namespace and keg
  explicitly and is parsed into a cross-keg edge.

Indexed links appear in backlinks when readable. Prefer intra-keg links when the target is in
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

Read message and action in either text JSON or structuredContent. Both supply
the same operational information, including error diagnostics and recovery.

- Validation errors: correct the named field using the input schema. meta is
  YAML text; mutations use nodes arrays. Do not retry unchanged arguments.
- PRECONDITION_REQUIRED: cat, schema_read, full keg_settings, or flight_show
  supplies the matching expected_hash. Snapshot before meaningful node edits.
- CONFLICT: operationPerformed=false; refetch, merge the intended change, and
  retry with the current hash. currentContent is diagnostic current content,
  not necessarily the replacement-document format accepted by edit.
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
  refine a query marked truncated. grep limits matched lines; cat reads full
  bodies. A tool absent from tools/list is unavailable, not a failed operation.
- Suspected stale indexes: inspect list_indexes/index_cat and doctor. Rebuild
  with index only when warranted and authorized; reads never repair indexes.
