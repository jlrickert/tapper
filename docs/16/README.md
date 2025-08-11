# Backlinks index - `dex/backlinks`

A [KEG](../5) index that records incoming links to a node (destination → sources). Where [dex/links](../14) maps outgoing edges (source → destinations), the [dex/backlinks](../16) index provides the reverse mapping: for a given node, which other nodes reference it. This file is useful for "linked from" UI lists, hub pages, graph algorithms, and tooling that needs quick lookup of incoming references.

## Purpose

- Provide a compact mapping: destination node id → list of source node ids.
- Enable quick backlink queries without scanning all README/meta files.
- Support tooling (UIs, indexers, [Zeke](../3) contexts, static site generators) that show "Linked from" lists or compute graph metrics.

## Location

- Typically stored at [dex/backlinks](../16) in the repository layout used by this project (see [dex/links](../14)).

## Format

- Plain text, tab-separated values (TSV).
- One line per destination node.
- Columns:

  1. destination node id — integer node identifier
  2. sources — zero or more source node ids separated by spaces

- Example lines:
  - 3<TAB>1 2 7
  - 10<TAB>
  - 42<TAB>3 5

Notes:

- Use an actual tab character between the destination id and the sources field.
- Source ids are numeric NodeIDs (see [Keg node](../15) for node/ID conventions).
- If a node has no incoming links, include an empty second column or omit the line (prefer including the line with an empty sources column for completeness).

Canonical per-line form:

- "<dest-id>\t<src1> <src2> <src3>\n"

## Semantics

- Destination id: the node being referenced by other nodes.
- Sources: node ids that contain outgoing links to the destination. These represent incoming edges in the [KEG](../5) graph.
- Order of source ids is not semantically significant; keep consistent ordering (e.g., ascending numeric) for reproducible indices.
- The index should be deterministic and stable across runs (same ordering and normalization rules).

## How backlinks are discovered / generated

- Backlinks are typically derived from [dex/links](../14) (source → destinations) by reversing each edge:
  - For each line src<TAB>dst1 dst2..., append src to backlinks[dstN] for each dstN.
- Indexers may compute backlinks on-demand or as part of index generation (for example during `[keg index update](../6)`).
- Discovery of outgoing links (the source of backlinks) follows the [dex/links](../14) rules: scan README/meta for numeric references (../N), explicit node-id tokens, or YAML `links` entries that reference internal nodes.

## Usage

- UI: show "Linked from" / "Referenced by" lists on node pages.
- Automation: find nodes that mention a particular node id for notifications, hub pages, or context building.
- Graph analysis: compute in-degree, find orphaned nodes (no backlinks), or find heavily referenced nodes.
- Quick lookup from the command line:
  - Show sources for node 3:
    - awk -F'\t' '$1==3{print $2}' dex/backlinks
  - List all backlinks:
    - cat dex/backlinks

## Parsing tips for tooling

- Read file line by line; split on the first tab to separate dest id from sources.
- Trim whitespace from both sides.
- Split sources by whitespace into tokens and parse each as an integer NodeID.
- Validate NodeIDs (positive integers) and ignore tokens that cannot be parsed (optionally warn).
- Use stable sorting when producing source lists (e.g., numeric ascending) for reproducible output.

Pseudo parsing logic:

- for each non-empty line L in dex/backlinks:
  - parts := strings.SplitN(L, "\t", 2)
  - dst := parseInt(parts[0])
  - if len(parts) > 1 && strings.TrimSpace(parts[1]) != "":
    - srcs := strings.Fields(parts[1])
    - for each s in srcs: parseInt(s) and append to list

## Maintenance

- Prefer automated index generation (e.g., `[keg index update](../6)`) rather than manual edits.
- When nodes are added/removed or links change, regenerate backlinks as part of the indexer run.
- Write [dex/backlinks](../16) atomically: write to a temp file then rename into place to avoid partial reads by other tools.
- Keep the index sorted (by destination id ascending and sources sorted) for easier diffing and deterministic behavior.

## Implementation notes (Go)

Suggested functions and types:

- func BuildBacklinksIndexFromLinks(links map[int][]int) (map[int][]int, error)

  - Build the reverse mapping from an existing links adjacency map.

- func BuildBacklinksIndex(root string) (map[int][]int, error)

  - Alternatively, scan node directories (or use a KegRepository) to extract outgoing links and build backlinks directly.

- func WriteBacklinksIndex(path string, idx map[int][]int) error

  - Serialize to [dex/backlinks](../16) with deterministic ordering (sort destination ids ascending and source lists ascending) and write atomically.

- func ParseBacklinksIndex(r io.Reader) (map[int][]int, error)
  - Parse an existing [dex/backlinks](../16) into in-memory reverse adjacency lists.

Example Go sketches:

- type BacklinkIndex map[NodeID][]NodeID
- func (bi BacklinkIndex) ToTSV() []byte { /_ deterministic serialization _/ }

Implementation guidance:

- Normalize inputs: remove duplicates from source lists, sort numerically, and validate NodeIDs.
- Prefer building backlinks by reading [dex/links](../14) (ParseLinksIndex -> BuildBacklinksIndexFromLinks) to centralize link discovery logic and avoid duplicating parsing heuristics.
- Write the file atomically and include a header comment if desired (but keep primary format machine-friendly).

## Testing

Unit tests should cover:

- Reverse mapping correctness: given a links map, ensure backlinks map contains the expected sources for each destination.
- Round-trip: Build links -> Build backlinks -> Write -> Parse yields consistent data.
- Deduplication: multiple identical outgoing references from a single source should produce a single source id in backlinks[destination].
- Sorting: verify deterministic order of destinations and sources.
- Handling of invalid/malformed lines: Parse should either return an error or skip with a warning depending on policy.
- Atomic write behavior (temp file + rename) — can be tested by mocking filesystem or using a temp dir.

## Error handling

- Return typed errors (see [Idiomatic Go error handling](../12) and package `keg` sentinels) for IO/backend problems (e.g., BackendError).
- Validate parsing errors and either skip lines with warnings or fail fast depending on indexer policy.
- Report links that reference nonexistent node ids as warnings during index generation (do not silently drop without logging).

## Backlinks and derived indices

- Backlinks are a direct inverse of [dex/links](../14). Tools may compute backlinks on demand from [dex/links](../14) or maintain [dex/backlinks](../16) as a derived index written by the indexer.
- Keeping a separate [dex/backlinks](../16) file is useful for fast lookups in UIs and for tooling that frequently queries inbound edges.

## Best practices

- Keep the index simple: numeric ids only for sources.
- Deduplicate source lists and sort them for determinism.
- Regenerate with tooling rather than hand-editing.
- Validate links when building the index (warn about links to nonexistent nodes).
- Do not store extra metadata in this file; if richer edge metadata is needed, create a separate JSON/YAML index and document it.

## Example (small dex/backlinks)

- [dex/backlinks](../16) contents:
  - 3<TAB>1 2
  - 10<TAB>3
  - 42<TAB>3 7

This indicates node 3 is referenced by nodes 1 and 2; node 10 is referenced by node 3; node 42 is referenced by nodes 3 and 7.

## Related

- Outgoing links index: [dex/links](../14).
- Nodes index: [dex/nodes.tsv](../7).
- Node conventions: [Keg node](../15).
- Error handling patterns: [Idiomatic Go error handling](../12).
