# Linking conventions

## Linking conventions

Tapper supports two link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg:** `keg:ALIAS/NODEID` — bare reference that the index parses
  into a cross-keg edge. Use when linking from one keg to a node in another.

Both forms appear in backlinks. Prefer intra-keg links when the target is in
the same keg.
