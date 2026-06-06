# Keg Config

Keg config is metadata stored in a keg repository itself.

## Purpose And File Location

- Canonical file: `<keg-root>/keg`
- Also recognized: `<keg-root>/keg.yaml`, `<keg-root>/keg.yml`

## View And Edit

```bash
tap settings
tap settings --keg <alias>
tap settings --project
tap settings --path <path>
tap settings edit
cat keg.yaml | tap settings edit --path <path>
```

Use `tap settings` commands for keg metadata. Use `tap config` for user/project resolver
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

- Prefer `tap settings edit` to edit with validation.
- Pipe YAML to `tap settings edit` when you want non-interactive updates.
- Keep YAML valid and key names consistent.
- Save small changes and re-run `tap settings` to confirm output.
- Use `tap info` to confirm the resolved keg directory when debugging target selection.
