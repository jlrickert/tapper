# Troubleshooting

## "no keg configured"

Cause:

- `defaultKeg` and `fallbackKeg` are unset, and no explicit target was provided.

Fix:

- Set at least `fallbackKeg` in user or project config.
- Or run commands with an explicit target (`--keg`, `--project`, or `--path`).

## "keg alias not found"

Cause:

- Alias does not exist in `kegs` and is not discoverable from `kegSearchPaths`.

Fix:

- Add alias under `kegs:` or ensure a matching local keg exists under a search path.
- Verify alias spelling in `defaultKeg`, `fallbackKeg`, and `kegMap` entries.

## Unexpected Keg Selected

Cause:

- Precedence selected a different target than expected.

Fix:

- Check `defaultKeg`, `kegMap`, and `fallbackKeg` values.
- Verify path matches for `kegMap` (`pathRegex` before `pathPrefix`).
- Remember later `kegSearchPaths` entries win on alias collisions.

## "kegSearchPaths not defined"

Cause:

- No discovery paths are configured and no explicit `kegs` mapping resolved.

Fix:

- Add `kegSearchPaths` to user config, for example:

```yaml
kegSearchPaths:
  - ~/Documents/kegs
```

## Debug Checklist

```bash
# Show merged config
tap repo config

# Inspect user and project configs separately
tap repo config --user
tap repo config --project

# Show active keg config (resolved target)
tap config

# Confirm resolution for a specific alias
tap info --keg <alias>

# Force project-local resolution
tap info --project
```
