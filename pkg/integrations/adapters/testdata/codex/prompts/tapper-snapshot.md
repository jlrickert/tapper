Before a non-trivial edit to a tapper node, capture a snapshot:

1. Call `mcp__tapper__node_snapshot` with the target node ID.
2. Make the edit with `mcp__tapper__edit` or `mcp__tapper__meta`.
3. Verify the result with `mcp__tapper__cat`.

To recover, list prior revisions with `mcp__tapper__node_history` and roll back with `mcp__tapper__node_restore`.

Snapshots do NOT protect against `mcp__tapper__remove`. Before any destructive operation, read the content with `mcp__tapper__cat` and keep it in the working context or copy it into another node first.
