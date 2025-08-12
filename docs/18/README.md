# Node meta (meta.yaml) — how Meta information is handled (keg-meta)

This document explains the purpose, shape, handling, and best practices for node metadata (meta.yaml) used by KEG nodes. It covers human-editable conventions, how indexers and tooling consume meta, and examples using the repo's Go helper type (pkg/keg.Meta) and simple CLI edits.

Summary

- meta.yaml is the canonical machine-readable metadata for a node.
- Keep it small and explicit. The `updated` timestamp is the authoritative freshness signal for indexers.
- Tooling should normalize tags, validate IDs/dates, and write index files atomically.
- Do not store secrets or credentials in meta.yaml.

Recommended meta.yaml shape

- Use simple top-level keys. Minimal example:

```yaml
updated: 2025-08-11 00:00:00Z # ISO 8601 UTC (Z suffix). Keep accurate.
accessed: 2025-08-11 00:00:00Z # ISO 8601 UTC (Z suffix). Keep accurate.
created: 2025-08-11 00:00:00Z # ISO 8601 UTC (Z suffix). Keep accurate.
tags:
  - keg-node
  - docs
```

Field semantics

- updated
  - ISO 8601 UTC timestamp (ending with `Z`) representing last meaningful content/metadata change.
  - Indexers use this to sort and decide freshness. Update it whenever you change content or metadata.
- title
  - Human-friendly title. May include a parenthetical slug used as a canonical tag page indicator (e.g., "Keg tags (keg-tags)").
- summary
  - Optional short paragraph used for hub pages and search snippets.
- tags
  - Discovery tokens. Prefer lowercase, hyphen-separated tokens (e.g., `api-design`, `draft`).
  - Acceptable input shapes: YAML sequence, single string with comma/space-separated tokens. Tooling normalizes to a canonical []string.
- links / authors
  - Optional provenance or external references.

Normalization rules (what tooling should do)

- Trim whitespace and normalize timestamps (parse/emit RFC3339).
- Normalize tags:
  - Lowercase, replace whitespace and commas with hyphens, collapse repeated hyphens, strip leading/trailing separators.
  - Deduplicate and sort for deterministic indices.
- Validate obvious errors (invalid timestamp format, non-numeric node ids in link fields that expect ids).
- Avoid inferring semantics from free-form fields; be conservative when discovering links.

Preserving comments during marshal/unmarshal

- User comments and lightweight annotations in meta.yaml are valuable (notes, hints, ownership comments). Tooling SHOULD preserve comments when parsing and writing meta.yaml.
- ParseMeta and ToYAML (or any marshal/unmarshal helpers) should aim to preserve comments and non-semantic formatting when possible. Prefer working with the YAML AST (gopkg.in/yaml.v3's yaml.Node) so HeadComment, LineComment, and FootComment attached to nodes are retained across a round trip.
- When updating only a subset of fields (e.g., adding/removing tags or updating timestamps), prefer editing the YAML AST and re-serializing rather than re-encoding data structures via Marshal/Unmarshal that discard comments and ordering.
- If an implementation must normalize values (for deterministic indices), apply normalization to the AST nodes corresponding to the affected fields and leave unrelated node comments intact.
- Be aware that simple Marshal/Unmarshal round-trips (encoding into map[string]any and then yaml.Marshal) typically lose comments and original ordering. Deep-copy helpers that rely on Marshal/Unmarshal will also lose comments; consider using node-based deep-copy logic or apply targeted modifications to the parsed yaml.Node.
- If comment preservation is impossible for a particular backend or implementation, document that behavior clearly and avoid surprises: users should be warned that tool-driven edits will strip comments.

How indexers discover metadata

- Indexers read meta.yaml. They collect:
  - updated → node updated time
  - title → human label
  - tags → tag → node id mappings
  - links → outgoing link edges (when referencing internal node ids)
- Indexers should treat missing files as absent nodes, not fatal errors, and write dex files atomically (temp file + rename).

Common edit workflows

- Manually edit meta.yaml in your editor and update the `updated` timestamp.
- Use small CLI helpers (yq) to operate programmatically.

Examples (yq)

- Add a tag:
  yq -i '.tags = (.tags // []) + ["my-tag"] | .tags |= unique' docs/42/meta.yaml
- Remove a tag:
  yq -i '.tags |= (. // []) | del(.tags[] | select(.=="my-tag"))' docs/42/meta.yaml
- Set updated to now (bash):
  yq -i --arg ts "$(date -u +%Y-%m-%dT%H:%M:%SZ)" '.updated = $ts' docs/42/meta.yaml

Go API usage (pkg/keg.Meta)

- Parse meta.yaml:

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
_ = meta.AddTag("My-Tag")      // normalizes to "my-tag" and persists in meta.Data
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

- Serialize and write back:

```go
out, _ := meta.ToYAML()
os.WriteFile("docs/42/meta.yaml.tmp", out, 0644)
os.Rename("docs/42/meta.yaml.tmp", "docs/42/meta.yaml") // atomic-ish
```

Notes about Meta helper behavior

- ParseMeta returns ErrMetaNotFound if file is empty/whitespace.
- Meta.Tags() accepts several input shapes (array, string) and always returns a canonical []string.
- AddTag/RemoveTag normalize tokens and maintain a canonical tags slice on meta.Data.
- Meta.Set lets you create nested maps along a path and delete a final key by passing nil as value. If an intermediate pathcomponent exists but is not a map, Set returns an error — do not overwrite non-map intermediate values accidentally.
- Meta.ToYAML normalizes tags for stable output.
- ParseMeta and Meta.ToYAML SHOULD preserve comments when possible. Implementations that currently use a simple Marshal/Unmarshal round-trip (or helpers like deepCopyMap that use yaml.Marshal/unmarshal) will lose comments; prefer AST-based approaches (yaml.Node) or targeted in-place edits to avoid stripping user comments and annotations.

Examples & edge cases

- Tags in a single string:
  - meta.yaml: tags: "Zeke, Draft"
  - meta.Tags() → ["draft", "zeke"]
- Empty or missing tags:
  - meta.Tags() returns nil (Meta.NormalizeTags will set tags to an empty []string if you prefer a deterministic key)
- Deleting a tag that does not exist is a no-op.
- If you need additional per-tag metadata (description, canonical node id), consider generating a secondary JSON/YAML index(dex/tags.json) rather than stuffing it into the simple dex/tags flat list.

Best practices

- Keep meta.yaml focused: small, declarative metadata only.
- Update `updated` whenever content or metadata changes so indexers can detect changes.
- Normalize tags on write so indices are deterministic and diffs are meaningful.
- Avoid storing secrets or credentials in meta.yaml — use environment variables or secure stores.
- Write meta changes atomically (write to temp file then rename) to avoid races.
- If your node is intended to be the canonical page for a tag, include the slug in the title (e.g., "Keg tags (keg-tags)") and include that slug in tags.
- Preserve user comments and annotations when parsing and serializing meta.yaml. Prefer AST/node-based edits that retain comments (gopkg.in/yaml.v3's yaml.Node) or clearly document when comments will be stripped.

Indexing & tooling considerations

- Indexers should:
  - Read meta.yaml, parse `updated` and `tags`.
  - Respect normalization rules and deduplicate ids in indices.
  - Prefer nodes with a parenthetical slug in the title when selecting canonical tag pages.
  - Emit warnings for suspicious tokens (spaces, potential secrets).
- Keep index serialization deterministic (sort tags and ids) to make diffs stable and reduce churn.

Quick checklist before committing meta changes

- [ ] Did you update `updated` to a correct RFC3339 UTC timestamp?
- [ ] Are tags normalized and intentional?
- [ ] No secrets or credentials are present?
- [ ] If scripts depend on a slug, did you include the slug in the title and tags?
- [ ] Did you write the file atomically (temp + rename) if using a script?
- [ ] Will your tooling preserve comments/annotations when editing meta.yaml? If not, document that behavior.

Appendix — small meta.yaml example

```yaml
updated: 2025-08-11 00:12:29Z
title: Idiomatic Go error handling
summary: Short reference for letting callers pattern-match on errors using errors.Is and errors.As.
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
