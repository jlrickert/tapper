# Claude Code plugins

Install the baseline Tapper plugin from the local marketplace embedded in the
`tap` binary:

```bash
tap integrate claude
```

The installer extracts a self-contained Claude marketplace below the platform
user-data directory, registers it with `claude plugin marketplace add`, and
installs `tapper@tapper-local`. The plugin registers `tap mcp`, blocks direct
agent use of the Tapper CLI except harmless help/version/completion probes, and
orients through the active flight and covered KEG instructions.

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

Re-running refreshes the extracted files atomically and uses Claude's install
or update command according to its JSON plugin state. A `tapper-local`
marketplace already pointing elsewhere is an actionable conflict.

The baseline also ships `tapper-mcp-reset` and the user-only
`/tapper:tapper-flight-switch REF` command. Use the
reset skill to diagnose the running version and connection, then run
`/reload-plugins` or open a new session without killing host-owned processes.
The flight command is hidden from the model. Its prompt-expansion hook calls a
hidden control on the existing MCP connection, requests human confirmation,
and atomically switches only that connection without writing config or starting
a model turn.
