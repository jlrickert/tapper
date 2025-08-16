# Node meta (meta.yaml) — how Meta information is handled (keg-meta)

Concise summary: `meta.yaml` is the canonical, machine-readable metadata for a KEG node. It is small, declarative, and treated as the authoritative source for timestamps, tags, titles, and lightweight provenance. Indexers and tooling rely on the `updated` timestamp to detect changes.

## Summary

- `meta.yaml` is the canonical machine-readable metadata for a node.
- Keep it small and explicit. The `updated` timestamp is the authoritative freshness signal for indexers.
- Tooling should normalize tags, validate timestamps/IDs, and write meta and index files atomically.
- Do not store secrets or credentials in `meta.yaml`.

## File name & location

- File: `meta.yaml` in the node directory (e.g., `docs/<id>/meta.yaml`).
- Tooling reads `meta.yaml` (and `README.md` for content) to build indices and other derived artifacts.

## Recommended meta.yaml shape

Use simple top-level keys. Minimal example:

```yaml
updated: 2025-08-11T00:00:00Z # RFC3339 / ISO8601 UTC (Z suffix)
created: 2025-08-11T00:00:00Z
accessed: 2025-08-11T00:00:00Z
title: Idiomatic Go error handling
summary: Short reference for letting callers pattern-match on errors using errors. Is and errors. As.
tags:
  - best-practices
  - errors
  - golang
links:
  - 12
authors:
  - https://github.com/jlrickert
```

## Field semantics

- updated

  - RFC3339 UTC timestamp (ending with `Z`) representing the last meaningful content or metadata change. Indexers use this to sort and decide freshness. Update it whenever you change content or metadata.

- title

  - Human-friendly title. May include a parenthetical slug used as a canonical tag page indicator (e.g., `Keg tags (keg-tags)`).

- summary

  - Optional short paragraph used for hub pages and search snippets. Keep it brief.

- tags

  - Discovery tokens. Prefer lowercase, hyphen-separated tokens (e.g., `api-design`, `draft`). Acceptable input shapes: YAML sequence, single string with comma/space-separated tokens. Tooling normalizes to a canonical `[]string`.

- links / authors
  - Optional provenance or external references. For link/token resolution rules see the canonical link documentation: [Keg links](../21).

## Normalization rules (what tooling should do)

- Trim whitespace and normalize timestamps (parse/emit RFC3339).
- Normalize tags:
  - Lowercase; replace whitespace and commas with hyphens; collapse repeated hyphens; strip leading/trailing separators.
  - Deduplicate and sort for deterministic indices.
- Validate obvious errors (invalid timestamp format, non-numeric node ids where numeric IDs are expected).
- Be conservative when discovering links from free-form fields; prefer explicit YAML `links` entries or README numeric references for index generation.

## Preserving comments during marshal/unmarshal

- User comments and lightweight annotations in `meta.yaml` are valuable. Tooling SHOULD preserve comments when parsing and writing `meta.yaml` where feasible.
- Prefer editing the YAML AST (gopkg.in/yaml.v3's `yaml.Node`) so `HeadComment`, `LineComment`, and `FootComment` attached to nodes are retained across a round trip.
- When updating only a subset of fields (e.g., tags or timestamps), edit the AST and re-serialize the affected nodes rather than performing a full `Marshal`/`Unmarshal` that will drop comments and ordering.
- If preserving comments is impossible for a particular backend, document that behavior and warn users that tool-driven edits will strip comments.

## How indexers discover metadata

- Indexers read `meta.yaml` to collect:
  - `updated` → node updated time
  - `title` → human label
  - `tags` → tag → node id mappings
  - `links` → outgoing link edges (when referencing internal node ids)
- Treat missing `meta.yaml` as an absent file rather than a fatal error. Indexers should write dex files atomically (write to a temp file then rename).

## Common edit workflows

- Manual: edit `meta.yaml` in your editor and update the `updated` timestamp.
- CLI: use small helpers (`yq`, or `pkg/keg.Meta` helpers) to add/remove tags or set timestamps programmatically.

### Examples (yq)

- Add a tag:
  yq -i '.tags = (.tags // []) + ["my-tag"] | .tags |= unique' docs/42/meta.yaml

- Remove a tag:
  yq -i '.tags |= (. // []) | del(.tags[] | select(.=="my-tag"))' docs/42/meta.yaml

- Set updated to now (bash):
  yq -i --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.updated = $ts' docs/42/meta.yaml

## Go API usage (pkg/keg.Meta)

- Parse `meta.yaml`:

```go
b, _ := os.ReadFile("docs/42/meta.yaml")
meta, err := keg.ParseMeta(b)
if err != nil {
    // handle ErrMetaNotFound or parse error
}
```

- Inspect tags:

```go
tags := meta.Tags() // normalized, deduped, sorted []string
```

- Add / remove tags:

```go
_ = meta.AddTag("My-Tag")   // normalizes to "my-tag"
_ = meta.RemoveTag("old-tag")
```

- Set nested values, delete keys, and read values:

```go
_ = meta.Set("some-value", "zeke", "config", "key")
v, ok := meta.Get("zeke", "config", "key")
_ = meta.Delete("zeke", "config", "key")
```

- Update timestamp:

```go
meta.SetUpdated(time.Now())
t := meta.GetUpdated()
```

- Serialize and write back (atomic-ish):

```go
out, _ := meta.ToYAML()
os.WriteFile("docs/42/meta.yaml.tmp", out, 0644)
os.Rename("docs/42/meta.yaml.tmp", "docs/42/meta.yaml")
```

## Notes about Meta helper behavior

- `ParseMeta` returns `ErrMetaNotFound` if file is empty or whitespace.
- `Meta.Tags()` accepts several input shapes (array, string) and always returns a canonical `[]string`.
- `AddTag`/`RemoveTag` normalize tokens and maintain a canonical tags slice on `meta.Data`.
- `Meta.Set` writes nested maps and will return an error if an intermediate path component is not a map; callers should avoid overwriting non-map intermediates inadvertently.
- `Meta.ToYAML` normalizes tags for stable output.
- `ParseMeta` and `Meta.ToYAML` SHOULD preserve comments when possible. Implementations that use simple `Marshal`/`Unmarshal` will lose comments; prefer AST-based approaches for comment-preserving edits.

## Examples & edge cases

- Tags in a single string:

  - `meta.yaml`: `tags: "Zeke, Draft"`
  - `meta.Tags()` → `["draft", "zeke"]`

- Empty or missing tags:

  - `meta.Tags()` returns `nil` (normalizers may opt to emit an empty `[]` on write for determinism).

- Deleting a tag that does not exist is a no-op.

- If you need per-tag metadata (description, canonical node id), generate a secondary index (e.g., `dex/tags.json`) rather than packing structured metadata into the flat `dex/tags` file.

## Best practices

- Keep `meta.yaml` focused: small, declarative metadata only.
- Update `updated` whenever content or metadata changes so indexers can detect changes.
- Normalize tags on write so indices are deterministic and diffs are meaningful.
- Avoid storing secrets or credentials in `meta.yaml` — use environment variables or secure stores.
- Write meta changes atomically (temp file + rename) to avoid races.
- If a node should be the canonical page for a tag, include the slug in the title (e.g., `Keg tags (keg-tags)`) and include that slug in `tags`.
- Preserve user comments and annotations when editing `meta.yaml`; prefer AST/node-based edits that retain comments (`yaml.Node`), or clearly document when comments will be stripped.

## Indexing & tooling considerations

- Indexers should:
  - Read `meta.yaml` and parse `updated`, `title`, and `tags`.
  - Normalize tokens and deduplicate ids when building tag/link indices.
  - Prefer nodes whose title ends with a parenthetical slug when selecting canonical tag pages.
  - Emit warnings for suspicious tokens (spaces, punctuation, or potential secrets).
- Keep index serialization deterministic (sort tags, ids, destination lists) to make diffs stable and reduce churn.

## Quick checklist before committing meta changes

- [ ] Did you update `updated` to a correct RFC3339 UTC timestamp?
- [ ] Are tags normalized and intentional?
- [ ] No secrets or credentials are present?
- [ ] If scripts depend on a slug, did you include the slug in the title and tags?
- [ ] Did you write the file atomically (temp + rename) if using a script?
- [ ] Will your tooling preserve comments/annotations when editing `meta.yaml`? If not, document that behavior.

## Appendix — small meta.yaml example

```yaml
updated: 2025-08-11T00:12:29Z
title: Idiomatic Go error handling
summary: Short reference for letting callers pattern-match on errors using errors. Is and errors. As.
tags:
  - golang
  - errors
  - best-practices
links:
  - alias: pkg-keg
    url: ../12
authors:
  - https://github.com/jlrickert
```

## Related canonical docs

- Keg content (README conventions): [keg content](../19)
- Keg links (alias/token resolution): [keg links](../21)
- Keg node / node layout: [keg node](../15)
- Tags & tag docs: [keg tags](../10)
- Index formats (dex/\*): [keg index](../6), [nodes index](../7), [changes index](../8), [links index](../14), [backlinks index](../16), [tags index](../17)
