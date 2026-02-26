# Resolution Order

This page describes how tapper chooses a keg target.

## 1. Explicit Target Flags Win First

If you pass explicit flags, they take precedence:

- `--path` resolves a keg from a specific filesystem path
- `--project` resolves from project-local locations
- `--keg` resolves an alias

`--keg` cannot be combined with `--project` or `--path`.

## 2. No Explicit Keg Flow

When no explicit target is supplied, tapper resolves in this order:

1. `defaultKeg`
2. `kegMap` match (`pathRegex` first, then longest `pathPrefix`)
3. `fallbackKeg`

## 3. Alias Resolution

For a selected alias, tapper resolves in this order:

1. explicit `kegs` map entry
2. discovered alias from `kegSearchPaths`
3. project-local alias fallback at `./kegs/<alias>` (git root aware)

## 4. Collision Behavior

If the same alias exists in multiple `kegSearchPaths`, later paths in the list take precedence.

## 5. Worked Examples

- In a repo with `.tapper/config.yaml` containing `defaultKeg: tapper`, `tap info` resolves
  `tapper` first.
- If `defaultKeg` is empty and `kegMap` matches the current path to alias `ecw`, `tap info`
  resolves `ecw`.
- If neither default nor map match resolves, `fallbackKeg` is used.
