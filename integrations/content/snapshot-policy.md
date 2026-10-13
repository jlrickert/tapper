# Snapshot policy

## Snapshots

`mcp__tapper__snapshot_create` captures a node's current revision. Snapshots
are cheap, deduplicated by content hash, and stored inside the node they
belong to.

**Important: snapshots are stored inside the node. A snapshot does not
protect against `mcp__tapper__node_delete`.** On a hub keg, a removed node
goes to the keg's trash with its snapshots, files, and images:
`mcp__tapper__trash_list` shows it and `mcp__tapper__trash_restore` puts it
back, until the keg's retention purges it. Until then its id stays reserved:
no node can be moved onto it. An archive restore that needs the id moves the
trashed node to a new id first (`displaced_from` in `mcp__tapper__trash_list`
names the old one), and restoring it puts it back at the new id. Elsewhere, removing a node removes
its snapshot history with it. Either way, before a removal you are not sure
of, copy the content somewhere that survives the deletion — read it with
`mcp__tapper__node_read` and keep the result in your working context, or
write it to another node first. If you are not certain the removal is
correct, defer it.

Snapshots do protect in-place edits. Take one before any of:

- `mcp__tapper__node_edit` that rewrites more than a section, pipes in
  generated content, or replaces content the agent did not author.
- `mcp__tapper__node_edit` writing `meta`, which replaces the node's whole
  metadata document and so overwrites existing tags and attributes.
- `mcp__tapper__node_move` — while the node survives the move, a snapshot
  before the rename makes it easy to confirm the move did not lose
  content and to diff against the pre-move state.
- Any chain of tool calls where a mistake partway through would leave
  the node in a state that is hard to reconstruct.

For batch edits across many nodes, snapshot each node that will be
modified before issuing the first write. A single failed tool call in
the middle of a batch is much cheaper to recover from when every
affected node has a restore point.

Recover with:

- `mcp__tapper__snapshot_list` — lists available snapshots for a node,
  most recent first.
- `mcp__tapper__snapshot_read` — reads a prior revision without
  changing the current node.
- `mcp__tapper__snapshot_restore` — recovers the current node from a specific
  snapshot revision.

If you are unsure whether an in-place edit warrants a snapshot, take
one. The cost is negligible. For `node_delete`, a snapshot is not a
recovery path — the trash is, where the keg has one; otherwise preserve the
content some other way first.
