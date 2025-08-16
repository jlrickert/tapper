# Zeke extension to keg configuration

Lead: Dedicated guidance for embedding Zeke (the Zeke AI utility) include/merge hints in a KEG keg file. This node explains the `zeke` sec

## Purpose

- Describe how a keg file can declare Zeke-related includes so Zeke loaders and other tools can discover and merge contexts/roles safely a
- Provide examples, recommended defaults, and loader behavior expectations.

## Where this lives in a keg file

- Top-level key: `zeke`
- Typical structure:
  ```yaml
  zeke:
    includes:
      <name>:
        key: <optional-prefix>
        file: <path> # OR glob/find
        find: <glob-pattern>
        merge: ["contexts", "roles"] # list of top-level keys to merge
        merge-prefix: true # default true
  ```

## Include fields (explanations)

- key
  - Optional short prefix used when `merge-prefix: true`. If omitted, the include name is used as the prefix.
- file
  - Path to a single YAML file to include (e.g., `./docs/keg`).
- find / glob
  - Glob or find pattern used to locate multiple candidate files (e.g., `**/meta.yaml`).
- merge
  - List of top-level keys from the included content that should be merged into the loader’s root config. Typical values: `["contexts","ro
  - Be explicit: prefer enumerating keys rather than "all".
- merge-prefix
  - Boolean (default: true). When true the loader prefixes included context names with the include `key` (or include name) to avoid collis

## Merging behavior & safety

- Discover candidate files from `file` or `find`/`glob`.
- Parse included YAML and locate candidate merge targets under:
  - top-level `contexts`
  - `zeke.contexts` (KEG-style embedding)
- For each requested `merge` target:
  - If `merge-prefix: true` (safe default), prefix included item names using `key` (or the include name) e.g. `zeke.design`.
  - If `merge-prefix: false`, flatten names into the root namespace. Loader should warn or require an explicit opt-in because flattening c
- Name collisions:
  - Warn by default.
  - Fail or require explicit override only if the loader is configured to be strict.
- Only the keys listed in `merge` should be merged — do not merge other top-level keys from included files unless explicitly requested.

## Recommended example (keg file excerpt)

```yaml
updated: 2025-08-09T13:01:55Z
zeke:
  includes:
    docs:
      key: zeke
      file: ./docs/keg
      merge: ["contexts", "roles"]
      merge-prefix: true
    shared:
      find: ../shared/**/zeke.yaml
      merge: ["contexts"]
      merge-prefix: false # explicit flatten; use with caution
```

## Project zeke.yaml vs keg includes

- Zeke supports user/project config files (e.g., `~/.config/zeke/zeke.yaml`, `GIT_PROJECT/zeke.yaml`).
- When a KEG keg file supplies `zeke.includes`, prefer using the keg-file includes for repository-local composition of KEG-hosted contexts
- Loading precedence is implementation-specific: loaders should document whether project zeke.yaml, user config, or keg includes are merge

## Loader recommendations

- Resolve `file` and `find` paths relative to the repository or include source.
- Parse YAML and locate `contexts` or `zeke.contexts` candidates.
- Perform merges only for keys listed in `merge`.
- Apply prefixing by default to avoid collisions.
- Provide diagnostics:
  - list what was merged and from where
  - report name collisions and whether items were renamed or skipped
- Do not perform automatic network fetches for remote includes without explicit user action.
- Validate included content (syntax, basic shape) and surface parse errors clearly.

## Examples: zeke.yaml snippets (for contexts)

- Example included content (the included file might be a KEG-style keg file or a zeke.yaml):

  ```yaml
  contexts:
    design:
      files:
        - "docs/design/"
      shell:
        - "git rev-parse --abbrev-ref HEAD"
    commit-agent:
      role: gitcommit
      api: openai
  roles:
    gitcommit:
      - git comment agent
  ```

- After merging (with `key: zeke`, `merge-prefix: true`), loader exposes contexts:
  - `zeke.design`
  - `zeke.commit-agent`
  - roles merged and available under prefixed names or merged role tables depending on loader policy.

## Best practices

- Keep `merge` explicit — prefer `["contexts","roles"]` rather than a catch-all.
- Use `merge-prefix: true` unless you have a controlled reason to flatten names.
- Put large or frequently-changing context sets in dedicated KEG nodes or files, and reference them via `file` or `find`.
- Avoid embedding secrets in included files. Use environment variables referenced by the config (and expand them at load time).
- Use diagnostics and a `zeke doctor`-style command to validate merges before use.

## Troubleshooting & diagnostics

- If contexts are missing:
  - Verify include paths and globs resolve to real files.
  - Check `merge` contains "contexts".
  - Check `merge-prefix` behavior — the context may be available under a prefixed name.
- If collisions occur:
  - Inspect loader logs/dry-run output and adjust `key` or `merge-prefix`.
- For parsing problems:
  - Validate YAML syntax in included files; loaders should report parse errors with file and line info.

## Migration note

- If you previously kept extensive Zeke configuration directly in the top-level keg file, move the heavy content into a dedicated file/node and reference it from the keg `zeke.includes` entry. The guidance, examples, and safe defaults documented here are the authoritative location for KEG → Zeke integration patterns.

## Related

- [Zeke AI utility docs](../3)
- [Keg configuration (keg-config)](../2)
- [Keg content & README conventions (title/lead paragraph rules)](../19)
