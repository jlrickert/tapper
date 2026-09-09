# Authoring nodes

Read targeted keg_settings instructions and schema_list before writing.
schema_read returns a schema's YAML data and hash. Follow its requirements.

create accepts nodes (1-100), each with a unique key and markdown content
beginning with an H1. meta is a YAML string, not a JSON object. Metadata must
not be embedded as content frontmatter. schema is a sibling of meta; it sets
meta.type and a conflicting type is rejected. Schema selection is required only
when strict policy and agent mode both block.

Example create arguments:
```json
{"nodes":[{"key":"note","content":"# Note\n\nBody","meta":"tags: [example]\n"}]}
```

Before a meaningful edit, call node_snapshot. Read cat with node_ids (or query)
and use each node_id and hash in edit nodes as node_id and expected_hash.
content and meta are separate replacement documents; omit one to preserve it.
Inspect results and validation before proceeding. Re-read before the next
guarded mutation, since not every mutation returns a replacement hash.

For settings use keg_settings minimal=false and retain the complete data and
hash. For schemas use schema_read. For flights use flight_show; cover is a
permission-filtered declared manifest, not effective authority. Do not replace
a filtered collection unless you intend that replacement. Edits with omitted
fields preserve them. On conflict, refetch, merge only the intended change,
and use the freshly returned hash. Never blindly replay a stale write.
