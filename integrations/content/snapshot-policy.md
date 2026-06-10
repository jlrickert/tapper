# Snapshot policy

## Snapshots

`mcp__tapper__node_snapshot` captures a node's current revision. Snapshots
are cheap, deduplicated by content hash, and stored inside the node they
belong to.

**Important: snapshots are stored inside the node. Removing a node
removes its snapshot history with it. A snapshot does not protect
against `mcp__tapper__remove`.** Before a removal, copy the content
somewhere that survives the deletion — read it with `mcp__tapper__cat`
and keep the result in your working context, or write it to another
node first. If you are not certain the removal is correct, defer it.

Snapshots do protect in-place edits. Take one before any of:

- `mcp__tapper__edit` that rewrites more than a section, pipes in
  generated content, or replaces content the agent did not author.
- `mcp__tapper__meta` changes that overwrite existing tags, attributes,
  or frontmatter.
- `mcp__tapper__move` — while the node survives the move, a snapshot
  before the rename makes it easy to confirm the move did not lose
  content and to diff against the pre-move state.
- Any chain of tool calls where a mistake partway through would leave
  the node in a state that is hard to reconstruct.

For batch edits across many nodes, snapshot each node that will be
modified before issuing the first write. A single failed tool call in
the middle of a batch is much cheaper to recover from when every
affected node has a restore point.

Recover with:

- `mcp__tapper__node_history` — lists available snapshots for a node,
  most recent first.
- `mcp__tapper__node_snapshot_view` — reads a prior revision without
  changing the current node.
- `mcp__tapper__node_restore` — recovers the current node from a specific
  snapshot revision.

If you are unsure whether an in-place edit warrants a snapshot, take
one. The cost is negligible. For `remove`, a snapshot is not a
recovery path — preserve the content some other way first.
