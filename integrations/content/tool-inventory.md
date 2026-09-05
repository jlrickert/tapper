# Tool inventory

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
