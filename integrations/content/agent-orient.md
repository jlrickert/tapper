# Operating MCP

Call `mcp__tapper__orient` first and again after context reset. Use its newest
instructions and authority. Then call `mcp__tapper__keg_settings` for each target
KEG before operating there. Use MCP for KEG operations; never bypass it by
reading or writing node storage. Leave placeholder node 0 alone.

The connection pins the Hub and root reference at activation. Live manifests,
relationships, credentials, and permissions reload for each authority-bearing
call. Omit flight to use the root, or pass a canonical accessible descendant
such as @team/+work for that call. A child supplies its own authority and
instructions; ancestor and sibling permissions are not inherited. Pass the same
flight on subsequent operations. keg selects a target and never grants access.

orient returns current active instructions, effective readable KEGs, and
immediate readable children. Orient with a child reference to inspect the next
level. With no root, orientation is search-first; keg_search and flight_search
find identity-readable metadata. Discovery is independent of selection.
flight_show inspects a permission-filtered declared manifest and its hash;
it never activates a session or selects operational authority.

No-root identity authority stays pinned for the connection lifetime. Creating
a flight does not narrow it. To change the root, the user selects it outside
MCP and starts a fresh connection. session_refresh only retries failed initial
activation; it cannot change an active connection.

A failed configured root exposes recovery tools: orient, session_refresh,
list_flights, flight_show, auth_info, keg_search, flight_search, guide. Repair
the configured root outside MCP, then retry session_refresh and orient.
An active flight can have no effective KEGs and still expose the full inventory;
KEG denials in that scope do not imply missing tools.

On failure, follow code, message, action, and operationPerformed in the returned
payload. A false outcome means refused; null means unknown, so inspect state
before repeating a mutation. For task-specific help use guide topics authoring,
linking, snapshots, tools, or troubleshooting.

If orient is unavailable, report that the Tapper MCP connection is unavailable
and ask the user to reconnect or restart the host session. Never kill or signal
host-owned processes.
