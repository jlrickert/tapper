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

Relation rows can also define note maturity scoring. Add `attribute` and a
positive `weight` to score linked target nodes by a numeric metadata attribute.
Set `direction: backlinks` to score incoming links instead of outgoing links.
For enum metadata, add an `enum` map of metadata values to scores:

```yaml
relations:
  - name: support
    type: evidence
    direction: links
    attribute: status
    weight: 2
    enum:
      draft: 0.25
      ready: 1
```

`stats` returns the computed `omega` value when the node's schema contains at
least one weighted relation rule.
