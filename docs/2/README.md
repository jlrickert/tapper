# Keg configuration (keg-config)

[Configuration](../1) specifically related to the [KEG](../2) program. Every
keg MUST contain a keg file — a small, opinionated YAML document that describes the keg metadata, links, indices, and optional tool-specific sections (for example `zeke` configuration used by the [Zeke](../3) AI utility).

## Important rule

- The `updated` line MUST be the first line of the file. Everything else is optional.

## Top-level fields

- updated (required first line)
  - ISO 8601 timestamp. Example: `2025-08-09 13:01:55Z`
- kegv (optional)
  - Keg file version identifier (e.g. `2023-01`)
- title (optional)
  - Human-readable title for the keg
- url (optional)
  - Canonical URL or git SSH URL for the main repository for this keg
- creator (optional)
  - URL or identifier for the keg maintainer/creator
- state (optional)
  - Human status string (e.g. `living`, `archived`)
- summary (optional)
  - Multi-line description; typically a YAML block scalar (|) for longer text

## links

- An array of link objects. Each object typically contains:
  - alias: short name
  - url: target URL
- Example:
  - links:
    - alias: jlrickert
      url: keg.jlrickert.me/@jlrickert/public

## zeke (optional)

- A section used by the Zeke AI utility to declare includes and merge behavior. When present it instructs the zeke config loader how to find and merge contexts/roles from this keg.
- Typical fields under `zeke.includes.<name>`:
  - key: optional key used as the default prefix when merging (e.g., `zeke`)
  - file: path to a single file to include (e.g., `./docs/keg`)
  - find / glob: glob or find pattern used to locate candidate include files (e.g., `**/meta.yaml`)
  - merge: list of top-level keys to merge into the loader’s root config (common values: `["contexts","roles"]`)
  - merge-prefix: boolean (default true). If true the loader will prefix included context names with the include key (or include name). Set to false to flatten names into the root namespace.
- Example:
  ```yaml
  zeke:
    includes:
    docs:
    key: zeke
    find: \*\*/meta.yaml
    merge: ["contexts", "roles"]
    merge-prefix: true
  ```

## indexes (optional)

- An array of index entries—auto-generated files or pointers used by KEG tooling.
- Each entry usually contains:
  - file: path to index (e.g., `dex/changes.md`)
  - summary: short description (e.g., `latest changes`)
- Example:
  - indexes:
    - file: dex/changes.md
      summary: latest changes
    - file: dex/nodes.tsv
      summary: all nodes by id

## Notes on semantics and loader behavior

- The keg file is intentionally simple: the loader/tooling should treat the `updated` line as authoritative for file freshness.
- `zeke.includes` entries are not KEG runtime directives; they are metadata used by Zeke (or other tools) to discover and merge config fragments.
- When a zeke loader merges content from a KEG file:
  - Look for candidate nodes under `contexts` or `zeke.contexts` in included YAML content.
  - Only merge the keys explicitly listed in `merge` to avoid accidental pollution.
  - By default, prefix included context names with the include `key` (or include name) to avoid collisions. Setting `merge-prefix: false` flattens names (use with caution).
  - Warn or fail on name collisions unless an explicit flatten/override is requested.

## Minimal example keg file

- Example (illustrative):

  ```yaml
  updated: 2025-08-09 13:01:55Z
  kegv: 2023-01

  title: KEG Worklog for tapper
  url: git@github.com:jlrickert/tapper.git
  creator: git@github.com:jlrickert/jlrickert.git
  state: living

  summary: |
  👋 Hey there! The KEG community welcomes you. This is an initial
  sample `keg` file. ...

  links:

  - alias: jlrickert
    url: keg.jlrickert.me/@jlrickert/public

  zeke:
  includes:
  docs:
  key: zeke
  find: \*\*/meta.yaml
  merge: ["contexts", "roles"]
  merge-prefix: false

  indexes:

  - file: dex/changes.md
    summary: latest changes
  - file: dex/nodes.tsv
    summary: all nodes by id
  ```

## Best practices

- Keep `updated` accurate — tooling relies on it to detect changes.
- Prefer explicit `merge` lists in `zeke.includes` to avoid accidentally merging unrelated keys.
- Use `merge-prefix: true` (the safe default) to avoid accidental name collisions when incorporating external contexts.
- Document any special keys or conventions used by other tools in a comment near the top of the keg file so maintainers are aware.
