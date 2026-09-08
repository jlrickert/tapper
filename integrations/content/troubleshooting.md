# Recovery

Read message and action in either text JSON or structuredContent. Both supply
the same operational information, including error diagnostics and recovery.

- Validation errors: correct the named field using the input schema. meta is
  YAML text; mutations use nodes arrays. Do not retry unchanged arguments.
- PRECONDITION_REQUIRED: cat, schema_read, full keg_settings, or flight_show
  supplies the matching expected_hash. Snapshot before meaningful node edits.
- CONFLICT: operationPerformed=false; refetch, merge the intended change, and
  retry with the current hash. currentContent is diagnostic current content,
  not necessarily the replacement-document format accepted by edit.
- ORIENTATION_DENIED: inspect orient and select an accessible flight with the
  required authority; keg alone cannot grant access.
- ORIENTATION_UNAVAILABLE: transient lookup failure; retry a read first.
  ORIENTATION_ROOT_UNAVAILABLE: the pinned root is lost; restore it or have
  the user select a root and start a new connection.
- UNAUTHORIZED: have the user repair authentication to the same Hub, then
  orient. FORBIDDEN: valid credentials lack permission; login cannot grant it.
- operationPerformed=null: the write outcome is unknown. Inspect state before
  retrying, including possible creations; do not assume nothing happened.
- Missing listing rows: follow next_offset until null. For metadata search,
  refine a query marked truncated. grep limits matched lines; cat reads full
  bodies. A tool absent from tools/list is unavailable, not a failed operation.
- Suspected stale indexes: inspect list_indexes/index_cat and doctor. Rebuild
  with index only when warranted and authorized; reads never repair indexes.
