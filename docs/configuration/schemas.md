# Keg Note Schemas

Keg note schemas live in a keg under `schemas/<type>.schema.yaml` and are
managed with `tap schema`.

## Edit

```bash
tap schema edit --keg @namespace/name task
cat task.schema.yaml | tap schema edit --keg @namespace/name task
```

`tap schema edit TYPE` opens the stored YAML in your editor. If the stored file
does not already include one, the temporary editor file starts with a
`yaml-language-server` schema modeline for `schemas/keg-schema-definition.json`.
The modeline is editor guidance; piped YAML with or without comments/modelines
parses the same way.

Schema documents declare the note `type`, an optional `summary`, optional
metadata JSON Schema under `meta`, markdown structure rules under `markdown`,
and relation requirements under `relations`.
