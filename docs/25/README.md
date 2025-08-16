# Keg storage layout (storage-layout)

This document defines the authoritative on-disk storage layout and key invariants (timestamps, indices, atomic-write rules) for [KEG](../5) [content](../19).

For developer-facing implementation notes (including the keg Go package and repository layout guidance) see the Repository layout node: [Repository layout (repository-layout)](../26).

## Purpose

- Provide a consistent, storage-agnostic mapping for KEG artifacts so indexers, tooling, and implementations behave predictably.
- Document on-disk layout, index semantics (dex/), timestamp invariants, atomic-write guidance, locking, and attachment conventions.

## Relation to other docs

See the focused canonical docs for details (these pages each have a single responsibility):

- Node layout & conventions: [Keg node (keg-node)](../15)
- Node metadata: [Node meta (meta.yaml)](../18)
- README/content conventions: [Keg content (keg-content)](../19)
- Index formats & indexer overview: [Keg index (keg-index)](../6) and [Nodes index (dex/nodes.tsv)](../7)
- Links / backlinks / tags indices: [dex/links](../14), [dex/backlinks](../16), [dex/tags](../17)
- Link/token resolution: [Keg links (keg-link)](../21)
- Developer / repository layout & implementation notes: [Repository layout (repository-layout)](../26)

## Canonical filesystem layout

Recommended repository tree:

- <repo-root>/
  - docs/ — node directory root (recommended)
    - docs/<id>/ — node directory (decimal id)
      - README.md — content (first H1 → title) — see [keg-content](../19)
      - meta.yaml — canonical metadata — see [meta.yaml](../18)
      - attachments/ — optional large files
      - images/ — images referenced by README
  - dex/ — generated indices (see [keg-index](../6))
    - nodes.tsv — id → timestamp → title (see [nodes.tsv](../7))
    - tags — tag → node ids (see [dex/tags](../17))
    - links — src → dst ids (see [dex/links](../14))
    - backlinks — dst → src ids (see [dex/backlinks](../16))
    - changes.md — human changelog (see [dex/changes.md](../8))
  - docs/keg or ./keg — repository keg file (config, aliases) — see [keg-config](../2)
  - .tapper/ — optional Tapper overrides (see [tapper-config](../22))
  - cmd/, pkg/, internal/ — code (if present)
  - .keg-lock — optional repo-level lock for multi-file updates

## File & naming rules

- Node directories are decimal integers (0, 1, 42). NodeID.Path() == "42" — see [pkg/keg docs](../26) for implementation notes.
- Primary content: README.md with the first H1 as the canonical title (see [keg-content](../19)). The Lead Paragraph immediately after the title should be a short summary sentence.
- Metadata: meta.yaml is authoritative for timestamps, tags, summary, and authors (see [meta.yaml](../18)). Do not store secrets in meta.yaml.

## Timestamps & freshness

- The `updated` field in meta.yaml MUST be RFC3339 / ISO8601 UTC (ending with `Z`) and kept accurate; indexers rely on this value rather than file mtimes.
- Update `updated` whenever content or metadata changes so indexers detect freshness.

## Index files — semantics & determinism

- Produce deterministic index output:
  - Normalize and sort keys consistently (tags lexicographically; node id lists numeric ascending).
  - Deduplicate lists.
- Indexer responsibilities:
  - Read meta.yaml and README.md to extract title, tags, and numeric outgoing links (ParseContent heuristics).
  - Build in-memory maps and serialize deterministically into dex/.
- Always write indices atomically (temp file + rename) and include a trailing newline.

## Atomic writes

- Pattern:
  1. Write to dex/<name>.tmp (or docs/<id>/meta.yaml.tmp).
  2. fsync file and parent directory when possible.
  3. Rename to final path (os.Rename) — atomic on POSIX filesystems.
- If atomic rename is not available (some network filesystems), document the limitation and provide alternate safeguards.

## Locking & concurrency

- Use a short-lived repo-level lock for multi-file operations (create index, move nodes, bulk updates).
- Simple file-lock approach: create `.keg-lock` with retries/backoff; return ErrLockTimeout on timeout (see pkg/keg sentinels in developer docs at [../26](../26)).
- Avoid long operations while holding the lock; prefer per-node locks when possible.

## Attachments & large binaries

- Store attachments/images under node directories (attachments/, images/). For very large files prefer external object stores or Git LFS and store references in the node.
- Never embed credentials or secrets in attachments or meta.yaml.

## Validation rules for indexers

- meta.yaml must be parseable and `updated` a valid RFC3339 timestamp (see [meta.yaml](../18)).
- Normalize tags (lowercase, hyphen-separated), dedupe, and sort.
- Reject or warn on URLs that embed credentials.
- Warn about links that reference nonexistent node IDs rather than silently dropping them.

## Storage-agnostic mapping

Logical → storage mapping (example):

- Node content → docs/<id>/README.md
- Node meta → docs/<id>/meta.yaml
- Attachments → docs/<id>/attachments/<name>
- Indices → dex/<name>

Backends (S3, DB, object store) must provide an atomic-write strategy for indices or document equivalent guarantees and return typed errors that callers can inspect (see developer docs at [../26](../26)).

## Implementation notes — Go (brief)

- See the developer-focused guidance in the Repository layout node: [Repository layout (repository-layout)](../26) for Go-specific types and functions (FsRepo, KegRepository, index builders, error sentinels, Meta helpers). That node contains detailed examples and API sketches for implementers.
- Key expectations for Go implementations:
  - Provide ReadMeta/WriteMeta, ReadContent/WriteContent, WriteIndex with atomic semantics.
  - Wrap IO/backend issues in typed errors (BackendError, NodeNotFoundError) so callers can use errors.Is / errors.As.
  - Preserve comments in meta.yaml where feasible (AST-based edits) or document comment-loss behavior.

## Examples

Indexer flow (tags):

1. Walk docs/\* → parse meta.yaml (see [meta.yaml](../18)).
2. Normalize tags and collect node ids per tag.
3. Sort tags and id lists; serialize dex/tags deterministically.
4. Atomically replace dex/tags.

Atomic write (POSIX) example:

```bash
tmp="$(mktemp dex/tags.tmp.XXXX)"
printf "%s\n" "zeke 3 10 45" >"$tmp"
mv "$tmp" dex/tags
```

## Security & best practices

- Do not commit secrets or credentials to meta, keg, or dex files.
- Preserve comments in meta.yaml when possible; if tooling strips comments, document that behavior.
- Add CI checks: deterministic index generation, meta.yaml timestamp format, and secret scanning.
- Prefer automation for index generation and enforce via CI (regenerate & diff).

---

If you want, I can also produce a small companion meta.yaml or a trimmed developer-focused markdown file extracted from the content above (for single-responsibility separation), or prepare a git patch to replace docs/25/README.md. Which would you prefer?
