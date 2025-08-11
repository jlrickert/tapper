# Links index - `dex/links`

A [KEG](../5) index that records outgoing links from a [node](../15) to other nodes (by numeric id). This index provides a fast, machine-readable mapping of which nodes reference which other nodes and is useful for building graphs, [backlink](../0) lookups, hub pages, and automation.

## Purpose

- Provide a compact mapping: source node id → list of destination node ids.
- Enable quick graph queries (outgoing edges) without scanning all README/meta files.
- Support tooling (indexers, UIs, [Zeke](../3) contexts, static site generators) that need link graphs or backlink computation.

## Location

- Typically stored at [dex/links](../14) in the repository layout used by this project.

## Format

- Plain text, tab-separated values (TSV).
- One line per source node.
- Columns:
  1. source node id — integer node identifier (unique)
  2. destinations — zero or more destination node ids separated by spaces (or a single destination)
- Example lines:
  - 3<TAB>2 5 42
  - 10<TAB>
  - 7<TAB>3

Notes:

- Use an actual tab character between the source id and the destinations field.
- Destination ids are numeric NodeIDs (see [Keg node](../15) for node/ID conventions).
- If a node has no outgoing links, include an empty second column or omit the line entirely (prefer including the line with an empty destinations column for completeness).

Recommended canonical form per line:

- "<source-id>\t<dst1> <dst2> <dst3>\n"

## Semantics

- Source id: the [node](../15) that contains the outgoing references.
- Destinations: node ids that the source links to. These represent logical edges in the [KEG](../5) graph.
- Order of destination ids is not semantically significant; keep consistent ordering (e.g., ascending or appearance order) for reproducible indices.
- The index should be deterministic and stable across runs for the same inputs (same ordering rules and normalization).

## How links are discovered

- Indexers typically scan `README.md` and `meta.yaml` (see [Keg node](../15)) for references to other nodes. Common patterns:
  - Relative link to a node directory: ../N
  - Numeric node references in content (explicit "node N" tokens)
- The discovery implementation should be conservative: only include destination ids when a clear link/reference to a numeric node id is found.
- Optionally, indexers may extract links from YAML `links` entries in `meta.yaml` if they reference internal nodes by id.

## Usage

- Graph queries: build adjacency lists for visualization or analysis.
- Backlink generation: compute reverse mapping (destination → list of sources) to show incoming links.
- UI features: "Linked from" lists on node pages.
- Automation: find nodes that mention a given node id to update contexts, tags, or notifications (use with [Zeke](../3) or other automation).

Common CLI examples:

- Rebuild the index:
  - [keg index update](../5) (or repo-specific indexer)
- Show destinations for node 3:
  - awk -F'\t' '$1==3{print $2}' dex/links
- List all edges:
  - cat dex/links

## Parsing tips for tooling

- Read file line by line; split on the first tab to separate source id from destinations.
- Trim whitespace from both sides.
- Split destinations by whitespace (spaces or tabs) into tokens and parse each as an integer NodeID.
- Validate NodeIDs (positive integers) and ignore tokens that cannot be parsed (optionally warn).
- Use stable sorting when producing destinations (e.g., numeric ascending) for reproducible output.

Pseudo parsing logic:

- for each non-empty line L in dex/links:
  - parts := strings.SplitN(L, "\t", 2)
  - src := parseInt(parts[0])
  - if len(parts) > 1 && strings.TrimSpace(parts[1]) != "":
    - dsts := strings.Fields(parts[1])
    - for each d in dsts: parseInt(d) and append to list

## Maintenance

- Prefer automated index generation ([keg index update](../5)) rather than manual edits.
- When nodes are added/removed/renamed or README/meta content changes, update the `updated` timestamp for the node (see [Keg node](../15)) and regenerate indexes.
- Write the [dex/links](../14) file atomically: write to a temp file then rename into place to avoid partial reads by other tools.
- Keep the index sorted (by source id ascending) for easier diffing and deterministic behavior.

## Implementation notes (Go)

Suggested functions and types:

- func BuildLinksIndex(root string) (map[int][]int, error)

  - Scan node directories under root (or use a KegRepository) and extract outgoing numeric links from README/meta.
  - Return a map[srcID][]dstIDs.

- func WriteLinksIndex(path string, idx map[int][]int) error

  - Serialize to [dex/links](../14) with deterministic ordering (sort source ids and destination lists) and write atomically.

- func ParseLinksIndex(r io.Reader) (map[int][]int, error)

  - Parse existing [dex/links](../14) into in-memory adjacency lists.

- Example Go sketches:
  - type LinkIndex map[NodeID][]NodeID
  - func (li LinkIndex) ToTSV() []byte { /_ deterministic serialization _/ }

## Testing:

- Unit tests should exercise:
  - Extraction of links from various `README.md` and `meta.yaml` patterns (see [Keg node](../15)).
  - Round-trip: Build -> Write -> Parse yields the same adjacency map.
  - Atomic write behavior (temp file + rename).
  - Handling of malformed lines or invalid ids (should not panic; return error or skip with warning).

## Error handling:

- Return typed errors (see [Idiomatic Go error handling](../12)) for IO/backend problems (e.g., BackendError).
- Validate parsing errors and either skip lines with warnings or fail fast depending on indexer policy.

## Backlinks and derived indices

- Backlink index (destination → list of source ids) is a common derived index built from [dex/links](../14). Tools can compute backlinks on-demand or during index generation and write a separate [dex/backlinks](../0) file if needed.
- Example backlink generation: iterate over [dex/links](../14) and for each src→dst append src to backlinks[dst], then sort lists and write backlink file.

## Best practices

- Keep the index simple and focused: numeric ids only for destinations.
- Prefer deterministic serialization (sorted keys and values) for reproducible diffs.
- Regenerate with tooling rather than hand-editing.
- Validate links when building the index (e.g., warn about links to nonexistent nodes).
- Do not store extra metadata in this file; if you need richer edge metadata (alias, title, URL), create a different index format (JSON/YAML) and document it separately.

## Example (small dex/links)

- dex/links contents:
  - 1<TAB>3 5
  - 2<TAB>3
  - 3<TAB>10 42
  - 10<TAB>

This indicates node 1 links to nodes 3 and 5; node 2 links to 3; node 3 links to 10 and 42; node 10 has no outgoing links (explicit empty field).
