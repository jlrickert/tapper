# Linking conventions

Tapper supports these link forms in node bodies:

- **Intra-keg:** `[title](../NODEID)` — relative path from the current node's
  directory to the target node's directory. Renders as a link in markdown
  tooling and is resolvable by the index.
- **Cross-keg (same namespace):** `[title](keg:ALIAS/NODEID)` — names a KEG
  in the source namespace.
- **Settings alias:** `[title](keg:~ALIAS/NODEID)` — explicitly resolves an
  alias declared in the source KEG settings. The tilde distinguishes a settings
  alias from a KEG name.
- **Cross-keg (fully qualified):**
  `[title](keg:@NAMESPACE/ALIAS/NODEID)` — identifies the namespace and keg
  explicitly and is parsed into a cross-keg edge.

Indexed links appear in backlinks when readable. Prefer intra-keg links when the target is in
the same keg. A bare `keg:` reference in node prose is plain text: it does not
create a graph link or backlink. Bare references remain valid as CLI arguments,
configuration values, schema values, and tool parameters.

Linking across kegs is ordinary authoring, but *copying* nodes across them is
not an agent operation: no tool moves or duplicates nodes between kegs. Read
the source with `mcp__tapper__cat` and `mcp__tapper__create` the node in the
target, which also lets you adjust its links deliberately. Bulk transfer
between kegs is an operator task the user runs outside MCP.

## Attachments

A node's uploaded files and images live in two directories inside the node's
own directory, so they are linked relative to it — the same base the `../NODEID`
form above counts from:

- **File:** `[label](./assets/FILE)` — anything uploaded with
  `mcp__tapper__upload_file`.
- **Image:** `![alt](./images/IMAGE)` — anything uploaded with
  `mcp__tapper__upload_image`.

**Both directory names are plural**: `assets/` and `images/`, never `asset/` or
`image/`. Uploading succeeds regardless of how you later write the link, so a
singular path fails silently as a broken reference rather than as an error.

Use `mcp__tapper__list_files` and `mcp__tapper__list_images` to get the exact
stored names; the upload may normalize the filename you supplied.
