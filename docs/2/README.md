# Keg configuration (keg-config)

[Configuration](../1) specifically related to the [KEG](../5) program. Every keg MUST contain a keg file — a small, opinionated YAML document that describes the keg metadata, links, and indices. Note: Zeke-specific configuration has been moved to a dedicated node — see "Zeke extension to keg configuration" (../24).

## Important rule

- The `updated` line MUST be the first line of the file. Everything else is optional.

## Top-level fields

- updated (required first line)
  - RFC3339 / ISO 8601 UTC timestamp. Example: `2025-08-09T13:01:55Z`.
- kegv (optional)
  - Keg file version identifier (e.g., `2023-01`, `2025-07`).
- title, url, creator, state, summary (optional)
  - Human metadata used by UI and discovery.

## links

- An array of link objects. Each object typically contains:
  - alias: short name
  - url: target URL
- Example:
  ```yaml
  links:
    - alias: jlrickert
      url: https://keg.jlrickert.me/@jlrickert/public
  ```

## indexes

- Optional list of index entries (auto-generated artifacts or pointers used by KEG tooling).
- Example:
  ```yaml
  indexes:
    - file: [dex/changes.md](../8)
      summary: latest changes
    - file: [dex/nodes.tsv](../7)
      summary: all nodes by id
  ```

## Zeke / tooling integration

- The keg file may contain a small `zeke` section for minimal integration hints (for example, a short includes list). However, detailed Zeke-related configuration and include/merge semantics have moved to the dedicated node: "Zeke extension to keg configuration" (../24). Keep the keg-level `zeke` block minimal and use the dedicated node for examples and loader guidance.
- Minimal keg-level reference example (pointing to an external include file):
  ```yaml
  zeke:
    includes:
      docs:
        file: ./docs/keg
        key: zeke
        merge: ["contexts", "roles"]
        merge-prefix: true
  ```
  Use the node at ../24 for recommended patterns, merging rules, and safety notes.

## Notes on semantics and loader behavior

- The keg file is intentionally simple. Tooling should treat the `updated` line as authoritative for freshness.
- When providing integration hints for external tools, prefer explicit `merge` lists and safe defaults (`merge-prefix: true`) to avoid accidental name collisions.
- Document any special keys or conventions near the top of the keg file so maintainers are aware.

## Best practices

- Keep `updated` accurate — tooling relies on it.
- Prefer `merge-prefix: true` when referencing external context sets.
- Avoid embedding secrets in the keg file (use env vars or secure stores).
- If you need authoritative guidance for Zeke includes/merge behavior, see node [24](../24).
