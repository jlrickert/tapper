# Linking conventions

## Linking conventions

Tapper supports two link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg (configured):** `keg:ALIAS/NODEID` — resolves the keg through
  active configuration and is parsed by the index into a cross-keg edge.
- **Cross-keg (fully qualified):** `keg:@NAMESPACE/ALIAS/NODEID` — identifies
  the namespace and keg explicitly and is parsed into a cross-keg edge.

Both forms appear in backlinks. Prefer intra-keg links when the target is in
the same keg.
