Orient to the current tapper keg:

1. Call `mcp__tapper__info` for the tapper version and current configuration.
2. Call `mcp__tapper__keg_info` for the resolved target keg, its path, and node count.
3. Call `mcp__tapper__tags` to list the keg's tag inventory.
4. Call `mcp__tapper__list` with `sort: "updated"` and `limit: 20` to see recent activity.

Summarize what this keg is about and what work has been in flight recently.
