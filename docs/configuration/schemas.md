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

Select a schema for a node write with `tap create --schema TYPE`, `tap edit
--schema TYPE`, or `tap meta --schema TYPE`. The selected type is persisted as
`meta.type`, then the complete projected node is validated. A conflicting
`type` in attributes, Markdown frontmatter, or metadata is rejected instead of
silently taking precedence. Under `schemaPolicy.strict`, selection is required
only when the resolved actor validation mode is `block`; it remains optional
for `warn`, `off`, non-strict kegs, and node 0.

Metadata property rows can define note maturity scoring under the property's
`maturity` key. The property name is the scored metadata attribute, so each row
only needs a positive `weight` and, for enum values, an optional `enum` score
map:

```yaml
meta:
  type: object
  properties:
    status:
      type: string
      enum: [draft, ready]
      maturity:
        - weight: 1
          enum:
            draft: 0.25
            ready: 1
```

Older top-level `maturity` rows with an explicit `attribute` are still read for
compatibility, but new structured edits should use property-scoped maturity.

Relation rows can also define note maturity scoring under `maturity`. Add an
`attribute` and a positive `weight` to score related target nodes by a numeric
metadata attribute. Set `direction: backlinks` to score incoming links instead
of outgoing links. For enum metadata, add an `enum` map of metadata values to
scores:

```yaml
relations:
  - name: support
    type: evidence
    description: Evidence that supports the note.
    maturity:
      - direction: links
        attribute: status
        weight: 2
        enum:
          draft: 0.25
          ready: 1
```

Snapshot and index operations replay the snapshot timeline and persist the
computed `omega` value into stats when the node's schema contains at least one
weighted maturity rule.
