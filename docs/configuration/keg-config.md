# Keg Config

Keg config is metadata stored in a keg repository itself.

## Purpose And File Location

- Canonical file: `<keg-root>/keg`
- Also recognized: `<keg-root>/keg.yaml`, `<keg-root>/keg.yml`

## View And Edit

```bash
tap keg settings
tap keg settings --keg <alias>
tap keg settings --project
tap keg settings --path <path>
tap keg settings edit
cat keg.yaml | tap keg settings edit --path <path>
```

Use `tap keg settings` commands for keg metadata. Use `tap config` for user/project resolver
settings.

## Field Reference (User-Facing)

Common keg fields:

- `updated`
- `kegv`
- `title`
- `url`
- `creator`
- `state`
- `summary`
- `links`
- `indexes`

## When To Edit Which Config

- Edit user config for machine defaults and discovery paths.
- Edit project config for repo-specific resolution behavior.
- Edit keg config for keg metadata and index/link declarations.

## Validation And Safe Editing Tips

- Prefer `tap keg settings edit` to edit with validation.
- Pipe YAML to `tap keg settings edit` when you want non-interactive updates.
- Keep YAML valid and key names consistent.
- Save small changes and re-run `tap keg settings` to confirm output.
- Use `tap info` to confirm the resolved keg directory when debugging target selection.
