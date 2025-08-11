# Tags index - `dex/tags`

A [KEG](../5) index that maps tag → list of node ids. This file provides a fast, machine-readable lookup of which nodes carry a given tag and is used by indexers, CLIs, UIs, and automation (for example to build tag hub pages, create Zeke contexts, or drive tag-based workflows).

## Purpose

- Provide a compact mapping: tag → node ids
- Enable quick discovery of nodes by tag without scanning every node's meta.yaml
- Drive automation: tag hubs, Zeke contexts, publication pipelines, and tests
- Support tooling that expects stable, deterministic index output for reproducible diffs

## Location

- Typically stored at dex/tags in the repository layout used by this project (see [Keg index (keg-index)](../6)).

## Format

- Plain text, space-separated values (one line per tag).
- Columns:

  1. tag — lowercase, hyphen-separated token (no leading #)
  2. node ids — zero or more numeric node ids separated by spaces

- Example lines:
  - zeke 3 10 45
  - draft 10 12 87
  - api-design 2 14

Notes:

- Use a single line per tag with the tag first, then a single space, then the list of node ids (space-separated). Trailing newline at end of file.
- If a tag has no nodes, the tag may be omitted or included with an empty list (prefer omission for clarity).
- Tag tokens should be normalized (lowercase, hyphen-separated) to make lookups predictable.

Canonical per-line form:

- "<tag> <id1> <id2> <id3>\n"

## Semantics

- Tag: a discovery token used to group nodes (see [Keg tags (keg-tags)](../10)).
- Node ids: numeric NodeID values referencing nodes that include the tag in their meta.yaml (see [Keg node](../15)).
- Order of node ids is not semantically significant, but maintain a deterministic sorting policy (recommended: numeric ascending or updated-desc grouping) to make diffs stable.

## How the index is generated

- Indexers read each node's meta.yaml (and optionally top-of-README front-matter) and collect tags listed under the `tags` field.
- For each discovered tag, append the node id to that tag's line in the index.
- Recommended normalization:

  - Trim whitespace
  - Convert tags to lowercase
  - Replace spaces with hyphens (or otherwise normalize tokenization rules used in this repo)
  - Deduplicate node ids per tag
  - Sort node ids deterministically (numeric ascending)

- Preferred workflow:
  1. Scan nodes → build in-memory map[tag]set(ids)
  2. Convert sets to sorted slices
  3. Serialize lines in sorted tag order (lexicographic)
  4. Write to a temp file and rename atomically into dex/tags

## Usage

- Find node ids for a tag:
  - awk '$1=="zeke"{for(i=2;i<=NF;i++)print $i}' dex/tags
- List all tags:
  - awk '{print $1}' dex/tags
- Generate a hub page (example pseudo):
  - read dex/tags, for each id lookup title from dex/nodes.tsv or node meta, render list
- Build a Zeke context that includes all nodes for tag `zeke`:
  - read dex/tags → fetch node contents → assemble context

CLI examples:

- Regenerate tags index:
  - keg index update (or repo-specific indexer)
- Show nodes for tag `draft`:
  - grep '^draft ' dex/tags | cut -d' ' -f2-
- List tags:
  - cut -d' ' -f1 dex/tags

## Parsing tips for tooling

- Read file line by line.
- For each non-empty line:
  - Split on the first space (or whitespace) to get tag and remainder.
  - Tokenize the remainder by whitespace to produce node id strings.
  - Parse each id as integer (NodeID) and validate (positive integer).
- Skip blank lines and ignore lines beginning with `#` (if you choose to allow a header comment); prefer no comments for machine-friendliness.

Pseudo parsing logic:

- for each non-empty line L in dex/tags:
  - parts := strings.Fields(L)
  - if len(parts) == 0 { continue }
  - tag := parts[0]
  - ids := []NodeID{}
  - for p in parts[1:]:
    - id := parseInt(p)
    - if parse ok: append to ids
  - normalize tag and ids as required

## Maintenance

- Prefer automated index generation rather than manual edits.
- When a node's tags change, update the node's `updated` timestamp and run the indexer (`keg index update`) to regenerate dex/tags.
- Write the index atomically: write to a temporary file and then rename into place to avoid partial reads by other tools.
- Keep the index sorted (tags lexicographically and id lists numerically) for reproducible diffs.

## Implementation notes (Go)

Suggested API surface and behavior:

- Types:

  - type TagIndex map[string][]NodeID

- Functions:

  - func BuildTagsIndex(root string) (TagIndex, error)
    - Scan node directories or use a KegRepository to load meta.yaml for each node.
    - Extract tags and populate a map[tag]set(ids).
  - func WriteTagsIndex(path string, idx TagIndex) error
    - Serialize deterministically: sort tags, for each tag sort ids ascending, join with spaces, write atomically.
  - func ParseTagsIndex(r io.Reader) (TagIndex, error)
    - Parse existing dex/tags into TagIndex.
  - func (ti TagIndex) ToTSV() []byte
    - Deterministic serialization helper.

- Example sketches:
  - Ensure normalization (strings.ToLower, replace spaces with hyphens if needed).
  - Remove duplicates (use map[int]struct{} while building).
  - Use os.CreateTemp + os.Rename for atomic writes.

Error handling:

- Return typed errors (see [Idiomatic Go error handling](../12)), e.g., NewBackendError for IO problems.
- Validate parsing errors and either fail or skip with warnings depending on indexer policy.

## Testing

Unit tests should cover:

- Extraction:
  - Tags parsed from various `meta.yaml` shapes (empty, missing, single string, array).
- Normalization:
  - Case folding, hyphenation, duplicate removal.
- Round-trip:
  - Build -> Write -> Parse yields same TagIndex.
- Determinism:
  - Order of tags and ids is deterministic across runs for same inputs.
- Atomic write:
  - Simulate writing to a temp directory and assert final file exists and is complete.
- Malformed lines:
  - Parse should gracefully handle invalid ids (skip with warning or error based on policy).

Example test ideas:

- Create three temp nodes with overlapping tags, run BuildTagsIndex, assert mapping contains expected ids with no duplicates and sorted order.

## Best practices

- Keep tag tokens meaningful and stable.
- Prefer lowercase, hyphen-separated tokens (e.g., api-design, zeke, draft).
- Normalize tags consistently at index time to avoid duplicates (e.g., "Zeke" vs "zeke").
- Avoid embedding metadata or secondary data in this file — keep it simple (tag → ids).
- Use the canonical tag page convention: when a node's title ends with "(tag-slug)" and the slug appears in meta.yaml tags, treat that node as the canonical tag documentation (see [Organization of this documentation (docs)](../11) and [Keg tags (keg-tags)](../10)).
- Validate tags during indexing and warn on suspicious tokens (spaces, punctuation, or potential secrets).

## Examples

Sample dex/tags contents:

- zeke 3 10 45
- keg 5 10 15 42
- draft 10 12 87
- api 2 14 22

Interpretation:

- Tag `zeke` is attached to nodes 3, 10, and 45.
- Tag `keg` is attached to nodes 5, 10, 15, and 42.

## Related indices and docs

- Tag documentation: [Keg tags (keg-tags)](../10)
- Canonical tag page convention: [Organization of this documentation (docs)](../11)
- Nodes index: [dex/nodes.tsv](../7)
- Links index: [dex/links](../14)
- Backlinks index: [dex/backlinks](../16)
- Index generation tooling: [Keg index (keg-index)](../6)
- Error handling patterns: [Idiomatic Go error handling](../12)

## Notes

- This index is intentionally minimal and machine-focused. If you need richer tag metadata (descriptions, canonical page id, counts), consider generating a supplement index (JSON/YAML) that includes those fields and document it separately.
