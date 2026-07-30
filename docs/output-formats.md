# Output Formats

`tap list`, `tap grep`, `tap tags`, `tap links`, and `tap backlinks` all render
node listings through one `--format` template, and the MCP tools of the same
names accept the same string.

## Quick reference

```sh
tap list                                   # id, updated, title (default)
tap list -f '%i %t'                        # id and title
tap list -f '%i\t%{type}\t%{status}'       # metadata columns
tap list -f '%i\t%{tags}'                  # the tag list
tap list -f '%i\t%{.accessCount}'          # a statistics field
tap list -f '100%%'                        # a literal percent
```

The default format is `"%i\t%d\t%t"`.

## Field selectors

Selectors share the vocabulary of [query expressions](query-expressions.md), so
one set of names covers both filtering and display. A selector appears in two
positions, and a bare word means something different in each:

- **Predicate position** (a query expression) — a bare word is a **tag**, because
  a metadata key is always written there as `key=value`.
- **Field position** (a format template) — a bare word is a **metadata key**,
  because there is no value to compare against and a tag is not a field.

Statistics fields carry a leading dot in both positions, so the one selector
that appears in both means the same thing in both.

| Selector | Resolves to | Cost |
| --- | --- | --- |
| `%{id}` | the node id | free |
| `%{title}` | the node title | free |
| `%{.updated}`, `%{.created}`, `%{.accessed}` | index timestamps, RFC3339 | free |
| `%{.hash}`, `%{.lead}`, `%{.accessCount}`, `%{.omega}` | statistics fields | one read per node |
| `%{tags}` | the node's tags, comma separated | one read per node |
| `%{anything-else}` | that metadata key | one read per node |

`id`, `title`, and `tags` are reserved. A node carrying metadata under one of
those keys cannot address it in field position; the intrinsic wins.

### Legacy verbs

The single-letter verbs remain supported as aliases. No new letters are added,
because a single letter cannot address an arbitrary metadata key.

| Verb | Equivalent |
| --- | --- |
| `%i` | `%{id}` |
| `%t` | `%{title}` |
| `%d` | `%{.updated}` |
| `%c` | `%{.created}` |
| `%a` | `%{.accessed}` |
| `%%` | a literal `%` |

An unrecognised `%X` passes through as literal text, so a format containing a
bare percent keeps working.

### Escapes

A shell does not expand `\t` inside double quotes, so `tap` interprets backslash
escapes itself. This is what makes the tab-separated default typeable at a
prompt:

```sh
tap list -f "%{id}\t%{type}\t%{title}"     # real tabs
```

| Escape | Renders |
| --- | --- |
| `\t` | tab |
| `\n` | newline |
| `\r` | carriage return |
| `\\` | a literal backslash |

An unrecognised `\X` passes through untouched, the same rule as `%X`, so a
Windows-style path in a template survives. A real tab — from `$'...'` quoting or
a script — passes through unchanged.

## Per-keg defaults

A keg can declare the columns its listings should show, so a keg whose nodes are
distinguished by `type` and `subkind` displays them without every caller passing
`--format`:

```yaml
# keg
kegv: 2025-07
listFields: [id, type, subkind, title]
```

The same setting drives the node list in Tapper Hub, so a keg looks the same on
both surfaces. Resolution order is `--format` → `listFields` → the built-in
default. Entries use the selector vocabulary above and are validated when the
config is saved, so a typo is reported at that point rather than rendering a
silently blank column.

## Absent values

An absent value renders as the empty string rather than a placeholder, so a
tabular format keeps a stable column count no matter which nodes carry a key.
A sentinel such as `-` would be indistinguishable from a real value.

```sh
tap list -f '%i\t%{type}' | cut -f2      # stays column 2 for every node
```

These all render empty: a metadata key the node does not have, a metadata value
that is not a scalar (a list or a map), an empty tag list, a zero timestamp, and
an absent `omega`.

Two deliberate exceptions:

- **`%{.accessCount}` always renders an integer, including `0`.** `stats.json`
  omits the key when the count is zero, so absent and zero are the same state on
  disk and there is no absence to represent.
- **`%{.omega}` distinguishes absent from zero**, because omega genuinely is
  tri-state. An unset omega renders empty; an omega of `0` renders `0`.

## Cost

Formats naming only `id`, `title`, the three dates, or the legacy verbs — which
includes the default — read nothing beyond the index that a listing already
loads.

Any other selector reads one file per node, for the nodes in the result window
only. Combine with `--limit` on a large keg. The same is true of the equivalent
query predicates: filtering on `entity=plan` or `.hash=` also reads per node.

## Notes

- **Spelling follows the query language, not the on-disk file.** The selector is
  `%{.accessCount}`; the key inside `stats.json` is `access_count`. There is one
  canonical vocabulary and it is the query language's.
- **`%{.created}` reads the index, not `stats.json`**, matching how `.created>…`
  is evaluated in a query. If the index has drifted from `stats.json`, both show
  the index value. Run `tap index rebuild` to reconcile.
- **Control characters in a rendered value collapse to single spaces.** Each
  output line is one node, and a value such as `%{.lead}` can contain newlines.
  Literal text in the template is untouched, so an explicit `\t` separator
  survives.
- **Values are never re-scanned.** A node whose title contains `%c` renders it
  literally.

## See also

- [Query Expressions](query-expressions.md) — the same vocabulary, in predicate
  position.
