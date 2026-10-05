# Tool inventory

Every authority-bearing tool below accepts an optional top-level `flight`.
When the connection starts without a flight, omission uses normal
identity-authorized full access and an explicit value selects any listed real
flight exactly. With a real pinned root, omission selects that root and an
explicit value selects the root or an accessible flattened descendant.
`session_info`, `session_refresh`, `flight_list`, `flight_read`,
`flight_search`, `keg_search`, `namespace_list`, `namespace_search`,
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

## Orientation and management

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__orient` | Read-only view of no-flight identity authority or the pinned real root, an optional exact real-flight selection, revision, available KEGs, and current instructions. |
| `mcp__tapper__session_refresh` | Retry activation only after a broken configured root is repaired. It never replaces active no-flight or real-flight authority; narrowing no-flight access requires a new connection. |
| `mcp__tapper__keg_list`, `mcp__tapper__keg_create` | Discover every identity-accessible KEG at its real role with no flight, or the effective projection of a selected real flight; no-flight creation uses namespace membership while real-flight creation also requires `manage_kegs`. |
| `mcp__tapper__flight_create`, `mcp__tapper__flight_edit`, `mcp__tapper__flight_delete` | Manage Hub flights when the selected flight grants `manage_flights`; edits and deletes require the manifest hash returned by `flight_read`, and normal Hub ACLs still apply. |
| `mcp__tapper__flight_list`, `mcp__tapper__flight_read` | Identity-readable discovery; flight_read returns a permission-filtered declared manifest, including explicit empty collections/instructions and hash. Neither tool activates or selects authority. |

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
| `mcp__tapper__node_search`                                   | Regex search over node content. Supports `ignore_case`, `limit`, `max_lines`, and `id_only`.                                                             |
| `mcp__tapper__tag_list`                                   | List tags or filter nodes by a boolean expression over tags, attributes, and dot-prefix stats fields (for example `tapper and .created>2026-01-01`).     |
| `mcp__tapper__node_list`                                   | List nodes in a keg with optional filters.                                                                                                               |
| `mcp__tapper__node_read`                                    | Read one or more nodes. Each structured row pairs `node_id` and `hash` with that node's `content` and `meta`, so a read feeds straight into `node_edit`. Supports `meta_only`, `content_only`, `stats_only`, and `query` expression selection as an alternative to explicit node IDs. |
| `mcp__tapper__node_links`                                  | Outbound links from a node.                                                                                                                              |
| `mcp__tapper__node_backlinks`                              | Inbound links to a node.                                                                                                                                 |
| `mcp__tapper__index_list`, `mcp__tapper__index_read` | Read generated index files (tag index, changelog, and others).                                                                                           |
| `mcp__tapper__keg_settings_read`                           | Read targeted title, description, updated metadata, and instructions for one or more selected KEGs; batches accept up to 100 canonical references.          |
| `mcp__tapper__keg_search`                             | Case-insensitive literal search across identity-accessible canonical refs, titles, and descriptions. Returns at most 50 rows and never grants operational access. |

Pass `id_only: true` to `node_search` and `tag_list` when you only need IDs for follow-up
reads — it keeps token consumption bounded on large result sets.

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

## Maintenance

| Tool                                                                           | Purpose                                                                                                               |
| ------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------- |
| `mcp__tapper__node_create`                                                          | Atomically create 1–100 nodes. Each is a markdown `content` document plus an optional YAML `meta` document; the title is the content's H1. Nodes in one batch reference each other with `{{node:KEY}}`. |
| `mcp__tapper__node_edit`                                                            | Call `node_read`, then atomically replace `content`, `meta`, or both for 1–100 `nodes[]`. Each part has its own hash: supplied `content` requires `expected_content_hash` (the read's `content_hash`) and supplied `meta` requires `expected_meta_hash` (its `meta_hash`). Editing one part never invalidates the other's hash, and results return the new `content_hash` and `meta_hash`. |
| `mcp__tapper__node_move`                                                            | Call `node_read`, then relocate a node using the combined `hash` from `node_read` as `expected_hash`.                       |
| `mcp__tapper__node_delete`                                                          | Call `node_read`, then atomically remove 1–100 `nodes[]`, each carrying the combined `hash` from `node_read` as `expected_hash`. |
| `mcp__tapper__file_delete`, `mcp__tapper__image_delete`                        | Destructive attachment operations — see the Snapshots section below before calling.                                 |
| `mcp__tapper__snapshot_create`                                                   | Capture a revision before a destructive or large edit.                                                                |
| `mcp__tapper__snapshot_list`, `mcp__tapper__snapshot_read`                 | Inspect read-only prior revisions.                                                                                    |
| `mcp__tapper__snapshot_restore`                                                    | Recover the current node from a prior revision.                                                                       |
| `mcp__tapper__keg_settings_edit` | Call `keg_settings_read` with `minimal=false`, then replace the complete YAML `data` using its `hash` as `expected_hash`. Requires identity admin and effective flight admin authority when a flight is selected. |

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
{"nodes": [{"node_id": "12", "meta": "type: document\n", "expected_meta_hash": "META_HASH_FROM_READ"}]}
```

`node_delete` carries only ids and the combined hashes:

```json
{"nodes": [{"node_id": "12", "expected_hash": "HASH_FROM_READ"}]}
```

## Agents

Hub agents are a namespace-owned model, description, markdown instructions, and
tool allowlist. A flight names the agents that run on it. Members of a
namespace read its agents; owners and admins change them. Agent refs are
`@namespace/name`.

| Tool | Purpose |
| --- | --- |
| `mcp__tapper__agent_list`, `mcp__tapper__agent_read` | Identity-readable agents in one namespace or every namespace you belong to; `agent_read` adds instructions and `effective_tools`. Neither accepts `flight`. |
| `mcp__tapper__agent_create`, `mcp__tapper__agent_edit`, `mcp__tapper__agent_delete` | Manage agents in a namespace you own or administer. A selected real flight must also grant `manage_flights`. `agent_edit` keeps omitted fields; an edit applies to open sessions on their next call. |

An agent's `tools` hold tool group ids (`keg:read`, `keg:write`, `keg:admin`,
`flight:read`, `flight:admin`, `agent:read`, `agent:admin`, `discover`) and
single tool names; empty means every tool. `orient`, `guide`, `session_info`,
and `session_refresh` are always available.

## On-demand discovery and guidance

- `mcp__tapper__namespace_list`: the namespaces you belong to, with your role.
- `mcp__tapper__namespace_search`: people and org namespaces visible on the
  Hub, not only your own, matched by name or display name; empty query browses.
  Discovery only: a match grants nothing. Follow up with `keg_search` and
  `flight_search`.
- `mcp__tapper__invitation_list`: pending invitations addressed to your user,
  org memberships and keg grants that take effect only if accepted. Read-only:
  accepting or declining is your user's decision (Hub account page or
  `tap invitation`), so tell them rather than acting.

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

For node_list, node_search, tag_list, node_links, and node_backlinks, follow
next_offset until null,
keeping the other arguments unchanged. has_more=null means another page is
uncertain; a final nonempty page can be followed by an empty page. Pages are
live, not a stable snapshot; reverse reverses each page. limit defaults to 50
(0 means default; -1 unlimited). node_search max_lines defaults to 3 per node; -1
returns all matching lines. Use node_read for complete bodies.

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
| `mcp__tapper__session_info` | Credential-free identity and default namespace. Namespace names do not assert administrative membership. |
| `mcp__tapper__schema_list`, `mcp__tapper__schema_read` | Schema names and YAML data plus hash. |
| `mcp__tapper__schema_create`, `mcp__tapper__schema_edit`, `mcp__tapper__schema_delete` | Schema lifecycle; requires admin, with current hashes for edit/delete. |
| `mcp__tapper__schema_validate` | Schema validation findings. |
| `mcp__tapper__index_rebuild` | Index rebuild; requires editor. |
| `mcp__tapper__keg_info`, `mcp__tapper__node_stats`, `mcp__tapper__keg_check` | KEG diagnostics, node statistics, health findings. |
| `mcp__tapper__lock_acquire`, `mcp__tapper__lock_status`, `mcp__tapper__lock_release`, `mcp__tapper__lock_force_release` | Advisory locks; acquisition returns a private token for release. node_edit does not accept a lock token. |
| `mcp__tapper__file_list`, `mcp__tapper__image_list` | Stored attachment names. |
| `mcp__tapper__file_upload`, `mcp__tapper__image_upload` | Inline base64/data URI/embedded resource uploads; local stdio also accepts source_path. Link the returned stored filename. |
| `mcp__tapper__image_download` | Image content block and metadata; local stdio optionally accepts dest_path. |
| `mcp__tapper__file_download` | Local stdio only: writes an explicit destination path. Absent on hosted MCP. |

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
