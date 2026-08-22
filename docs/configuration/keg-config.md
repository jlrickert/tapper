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

`schemaPolicy.strict` is a live-write selection rule. When strict is enabled
and a nonzero node create or edit resolves to validation mode `block`, that
write must explicitly select one schema. The selection becomes the completed
node's `meta.type`, and the completed content and metadata are validated
against it. An existing stored type does not satisfy the explicit-selection
requirement. Use `--schema TYPE` with `tap create`, `tap edit`, or metadata
writes.

`human`, `agent`, and `api` each accept `off`, `warn`, or `block`, and continue
to control validation even when strict is enabled. The defaults remain
human=`warn`, agent=`block`, and api=`block`; request-level overrides remain
part of mode resolution. Untyped writes are allowed whenever explicit
selection is not required. Typed nodes are still validated according to the
resolved mode.

Strict does not scan existing nodes when enabled and does not prevent schema
replacement or deletion because of stored nodes. Node 0, imports, archive
restores, snapshot restores, and schema/config operations are exempt from the
selection rule. Newly initialized KEGs still set `strict: true`; older configs
with no `strict` field remain non-strict.

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
