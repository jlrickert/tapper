# Keg Config

Keg config is metadata stored in a keg repository itself.

## Purpose And File Location

- Canonical file: `<keg-root>/keg`
- Also recognized: `<keg-root>/keg.yaml`, `<keg-root>/keg.yml`

## View And Edit

```bash
tap keg settings
tap keg settings --keg @namespace/name
tap keg settings edit
cat keg.yaml | tap keg settings edit --keg @namespace/name
```

Use `tap keg settings` commands for keg metadata. Use `tap config` for
user/project resolver settings.

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
- `schemaPolicy`

Use `summary` for a concise discovery description: aggregate orientation uses
it to help agents identify relevant KEGs and does not automatically truncate
it. Use `instructions` for targeted operational guidance. Instructions are
loaded only after an agent explicitly selects the KEG through `keg_settings`;
they are not included in aggregate orientation.

`schemaPolicy.strict` is a KEG-wide invariant. In strict mode every nonzero
node must declare a known `type` and satisfy that type's metadata and Markdown
schema. Node 0 may omit `type`; an explicitly typed node 0 is validated. Strict
mode cannot be weakened by actor or request overrides, and complete resulting
state is checked when strictness is enabled, schemas are replaced or deleted,
snapshots are restored, or archives are imported. Newly initialized KEGs set
`strict: true`; older configs with no `strict` field remain non-strict.

When strict mode is disabled, `human`, `agent`, and `api` each accept `off`,
`warn`, or `block`. The defaults are human=`warn`, agent=`block`, and
api=`block`. Non-strict archive imports and snapshot restores retain their
historical schema-enforcement exemption.

## When To Edit Which Config

- Edit user config for machine defaults, hubs, and credentials.
- Edit project config for repo-specific resolution behavior.
- Edit keg config for keg metadata and index/link declarations.

## Validation And Safe Editing Tips

- Prefer `tap keg settings edit` to edit with validation.
- Pipe YAML to `tap keg settings edit` when you want non-interactive updates.
- Keep YAML valid and key names consistent.
- Save small changes and re-run `tap keg settings` to confirm output.
- Use `tap info --keg @namespace/name` to confirm a resolved target when
  debugging selection.
