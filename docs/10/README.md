# Keg tags (keg-tags)

Tags in [KEG](../2) are lightweight labels that group related nodes for discovery and automation. Use tags to categorize notes, mark lifecycle state (draft/published), indicate technology/language, or mark items for an automated workflow.

## Why tags exist

- Fast grouping: find all nodes about the same topic or that share a purpose.
- Automation: tooling (indexers, search, zeke contexts) can operate on tag groups.
- Cross-cutting organization: tags let one node appear in many logical collections.

## Tag semantics

- Tags are short, case-insensitive identifiers attached to a node’s metadata.
- Tags do not replace titles or slugs — they are orthogonal labels used for discovery.
- Prefer lowercase, hyphen-separated tokens (e.g., api-design, zeke, draft).

## How to add tags

- Canonical place: node meta file (docs/<id>/meta.yaml) or top of README front-matter.
- Example meta.yaml:
  updated: 2025-08-09 18:33:17Z
  title: Keg tags (keg-tags)
  tags:
  - keg
  - tags
  - metadata
- Use your editor or a tool to add/remove tags and update the updated timestamp.

## Command-line examples

- Add a tag (example using yq):
  yq -i '.tags = (.tags // []) + ["my-tag"] | .tags |= unique' docs/10/meta.yaml
- Remove a tag:
  yq -i '.tags |= (. // []) | del(.tags[] | select(.=="my-tag"))' docs/10/meta.yaml
- List tags:
  awk '{print $1}' dex/tags
- List nodes for a tag:
  awk '/^mytag /{for(i=2;i<=NF;i++)print $i}' dex/tags

## Tag index (dex/tags)

- The tag index is a small, generated file mapping tag → node ids. Recommended format:
  tag id1 id2 id3
- Example:
  zeke 3 10 45
  draft 10 12 87
- Regenerate the index with the KEG indexer (keg index update) or the ku helper (\_ku_index). The index is the primary fast-lookup for tag-based commands.

## Tooling & automation ideas

- CLI helpers: keg tag add/rm, keg nodes --tag <tag>, keg tag hub <tag> (generate hub page).
- Automation: generate a README per tag listing node titles and summaries (good for a tag landing page
  ).
- Zeke integration: use tags to build contexts (e.g., a "zeke" context that includes all nodes with the zeke tag).

## Implementation notes (Go)

- Recommended types and functions:
  - type Meta { Updated time.Time; Title string; Tags []string }
  - func LoadMeta(path string) (\*Meta, error)
  - func BuildTagsIndex(root string) (map[string][]int, error)
  - Write index atomically to dex/tags.
- Tests: unit tests that create sample nodes and assert the index contents and ordering.

## Best practices

- Keep tags meaningful and stable.
- Use tag namespaces only when necessary (pkg:zeke, lang:go).
- Update the node's updated timestamp when modifying tags to ensure indexes are regenerated properly.
- Avoid embedding secrets in meta files that may be surfaced by tag pages or search.

## Examples / use cases

- Find all draft design notes: ku tags draft
- Create a "tag hub" page for the tag "api" that lists all api-tagged nodes with a one-line summary.
- Use tags to bootstrap a zeke context ("include all nodes with tag zeke in the zeke-design context").
