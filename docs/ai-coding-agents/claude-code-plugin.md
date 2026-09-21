# Claude Code plugins

Install the baseline Tapper plugin through the official supported installer:

```bash
tap integrate claude
```

The installer extracts a self-contained Claude marketplace below the platform
user-data directory, registers it with `claude plugin marketplace add`, and
installs `tapper@tapper-local` and `tapper-guard@tapper-local`. The baseline
plugin registers `tap mcp` and orients through the active flight, compact KEG
discovery, and targeted settings instructions. It ships no hooks of its own.

`tapper-guard` carries the guard that blocks direct agent use of the Tapper CLI
except harmless help/version/completion probes, and blocks mutation of Tapper
configuration. It runs as `tap hook pre-tool-use`, so the current `tap` binary
must remain on `PATH`. It is a separate plugin so you can turn the enforcement
off without losing the MCP registration or the skill:

```bash
tap integrate claude --no-safety          # do not install the guard
claude plugin disable tapper-guard@tapper-local   # turn off one already installed
```

When `tap hook` runs and cannot decide — empty or malformed input — it exits 2,
which Claude treats as a blocking error, so the guard fails closed. A `tap`
missing from `PATH` entirely is the exception: Claude reports the failed hook
and allows the call, which is why the installer verifies `tap hook` support
before installing.

`--no-safety` only skips the install; it never removes a guard Claude already
has. Asking for `--plugin tapper-guard` together with `--no-safety` is an
error.

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

Scopes are per-host, so `--scope` accepts different values depending on which
host you are installing for. Claude takes all three because its plugin CLI
does; [Codex](codex.md) is user-only because `codex plugin` has no scope flag at
all. Shell completion for `--scope` asks the host you named, so it only ever
suggests values that host accepts.

Re-running refreshes the extracted files atomically, removes legacy packaged
Python hooks, and uses Claude's install or update command according to its JSON
plugin state. Review and trust the replacement hook again, then open a fresh
Claude session for the refreshed plugin and MCP connection to take effect. A
`tapper-local` marketplace already pointing elsewhere is an actionable
conflict.

The baseline plugin distributes only the `tapper` skill; `tapper-dev` remains a
separately installed optional plugin. Root changes use normal Tapper
configuration followed by a new MCP connection. On an existing connection,
governed calls default to that root or select an accessible transitive
descendant with `flight`. The plugin ships no separate management skills, hidden switch
command, or prompt-expansion hook. If the MCP tools are unavailable, report the
unavailable connection, ask the user to reconnect or restart the host session,
and never kill or signal host-owned processes.
