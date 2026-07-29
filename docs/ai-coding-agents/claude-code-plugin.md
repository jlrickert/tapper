# Claude Code plugins

Install the baseline Tapper plugin through the official supported installer:

```bash
tap integrate claude
```

The installer extracts a self-contained Claude marketplace below the platform
user-data directory, registers it with `claude plugin marketplace add`, and
installs `tapper@tapper-local`. The plugin registers `tap mcp`, blocks direct
agent use of the Tapper CLI except harmless help/version/completion probes, and
orients through the active flight and covered KEG instructions. Its guard runs
as `tap hook pre-tool-use`, so the current `tap` binary must remain on `PATH`.

Install the optional developer workflow separately:

```bash
tap integrate claude --plugin tapper-dev
```

`tapper-dev` adds Plan → Code → Review → Commit guidance without duplicating
the MCP registration. Its Claude manifest declares `tapper` as a dependency,
so Claude installs and enables the baseline prerequisite transitively.

Preview without side effects:

```bash
tap integrate claude --dry-run
```

The default scope is `user`. Select Claude's native project scopes when needed:

```bash
tap integrate claude --scope project
tap integrate claude --scope local --plugin tapper-dev
```

`project` writes shared project settings; `local` writes gitignored project
settings. Marketplace registration and plugin install/update use the same
scope, and installations in other scopes are treated independently.

Re-running refreshes the extracted files atomically, removes legacy packaged
Python hooks, and uses Claude's install or update command according to its JSON
plugin state. Review and trust the replacement hook again, then open a fresh
Claude session for the refreshed plugin and MCP connection to take effect. A
`tapper-local` marketplace already pointing elsewhere is an actionable
conflict.

The baseline plugin distributes only the `tapper` skill; `tapper-dev` remains a
separately installed optional plugin. Flight changes use normal Tapper
configuration followed by an explicit `orient` call on the existing MCP
connection. The plugin ships no separate management skills, hidden switch
command, or prompt-expansion hook. If the MCP tools are unavailable, report the
unavailable connection, ask the user to reconnect or restart the host session,
and never kill or signal host-owned processes.
