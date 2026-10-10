# Keg Settings

Keg config is metadata stored in a keg repository itself.

## Purpose And File Location

- Canonical file: `<keg-root>/keg`
- Also recognized: `<keg-root>/keg.yaml`, `<keg-root>/keg.yml`

## View And Edit

```bash
tap keg settings read
tap keg settings read --keg @namespace/name
tap keg settings edit
cat keg.yaml | tap keg settings edit --keg @namespace/name
```

Use `tap keg settings read` commands for keg metadata. Use `tap config` for
user/project resolver settings.

## Field Reference (User-Facing)

Common keg fields:

- `updated`
- `kegv`
- `title`
- `url`
- `creator`
- `state`
- `description`
- `links`
- `indexes`
- `schemaPolicy`

Use `description` for a concise discovery description: orientation uses
it to help agents identify relevant KEGs and does not automatically truncate
it. Use `instructions` for targeted operational guidance. Instructions are
loaded only after an agent explicitly selects the KEG through `keg_settings_read`;
they are not included in aggregate orientation.

`schemaPolicy.strict` is a live-write selection rule. When strict is enabled
and a nonzero node create or edit resolves to validation mode `block`, that
write must explicitly select one schema. The selection becomes the completed
node's `meta.type`, and the completed content and metadata are validated
against it. A `type` declared in submitted metadata does not satisfy the
explicit-selection requirement. Use `--schema TYPE` with `tap node create`,
`tap node edit`, or metadata writes.

An edit that replaces only content leaves metadata alone, so it keeps the
node's stored type instead: it needs no selection and is validated against that
type. This does not cover an untyped node, which has no type to keep, or
content whose frontmatter declares a different type. Metadata-only edits and
edits of both parts still need a selection.

`human`, `agent`, and `api` each accept `off`, `warn`, or `block`, and continue
to control validation even when strict is enabled. The defaults remain
human=`warn`, agent=`block`, and api=`block`; request-level overrides remain
part of mode resolution. Untyped writes are allowed whenever explicit
selection is not required. Typed nodes are still validated according to the
resolved mode.

Strict does not scan existing nodes when enabled and does not prevent schema
replacement or deletion because of stored nodes. Node 0, imports, archive
restores, snapshot restores, and schema/settings operations are exempt from the
selection rule. Newly initialized KEGs still set `strict: true`; older configs
with no `strict` field remain non-strict.

## When To Edit Which Config

- Edit user config for machine defaults, hubs, and credentials.
- Edit project config for repo-specific resolution behavior.
- Edit keg settings for keg metadata and index/link declarations.

## Validation And Safe Editing Tips

- Prefer `tap keg settings edit` to edit with validation.
- Pipe YAML to `tap keg settings edit` when you want non-interactive updates.
- Keep YAML valid and key names consistent.
- Save small changes and re-run `tap keg settings read` to confirm output.
- Use `tap keg info --keg @namespace/name` to confirm a resolved target when
  debugging selection.

Legacy `summary` remains readable when `description` is absent. An explicitly
empty description takes precedence. New settings and archive exports use
`description`, preserving the complete stored text. Index and node-schema
summary fields retain their existing names.
