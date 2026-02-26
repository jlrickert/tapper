# Keg Config

Keg config is metadata stored in a keg repository itself.

## Purpose And File Location

- Canonical file: `<keg-root>/keg`
- Also recognized: `<keg-root>/keg.yaml`, `<keg-root>/keg.yml`

## View And Edit

```bash
tap config
tap config --keg <alias>
tap config --project
tap config --path <path>
tap config --edit
```

Use `tap config` commands for keg metadata. Use `tap repo config` for user/project resolver
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

- Prefer `tap config --edit` to edit with validation.
- Keep YAML valid and key names consistent.
- Save small changes and re-run `tap config` to confirm output.
- Use `tap info` to confirm the resolved keg directory when debugging target selection.
