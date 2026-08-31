# Codex plugins

Install the baseline Tapper plugin through the official supported installer:

```bash
tap integrate codex
```

The installer extracts the marketplace below the platform user-data directory,
registers it with `codex plugin marketplace add`, and installs
`tapper@tapper-local`. The plugin owns the `tap mcp` registration, a
flight-first Tapper skill, and a `PreToolUse` guardrail that blocks direct
agent use of `tap` and `keg` except harmless help, version, and completion
probes. A `SessionStart` hook restores a concise reminder after startup,
resume, clear, and compaction. The reminder tells the main agent to call
`mcp__tapper__orient` before KEG work, follow the returned flight and KEG
instructions, and apply the MCP-first safety rules; it does not inject the
orientation payload itself and does not run for subagents.

After installing or refreshing the plugin, open Codex `/hooks` and review both
bundled hooks before trusting them again. They invoke `tap hook session-start`
and `tap hook pre-tool-use`; the latter is a guardrail against accidental
direct CLI use, not a complete shell security boundary. The `tap` executable
on `PATH` must remain available and support those hidden commands. Re-run
`tap integrate codex` to receive hook changes, then start a new thread after
approving them so the lifecycle reminder and MCP connection are fresh.

Install the optional developer workflow separately:

```bash
tap integrate codex --plugin tapper-dev
```

`tapper-dev` adds Plan → Code → Review → Commit guidance but no MCP server.
Codex manifests do not currently declare plugin dependencies, so its skill
checks that `mcp__tapper__orient` is available and reports that the baseline
plugin must be installed when it is not.

Preview the exact extraction paths and host commands without writing or
invoking Codex:

```bash
tap integrate codex --dry-run
```

Re-running the installer atomically refreshes the embedded marketplace,
removes legacy packaged Python hooks, and reinstalls from the local source.
Hook changes require review and trust again. A same-named marketplace pointing
elsewhere is reported as a conflict and is never replaced automatically.

Codex currently supports only `--scope user`. `--scope project` and `--scope
local` fail before marketplace extraction or Codex commands; use a user install
until Codex exposes native project scopes.

The baseline plugin distributes only the `tapper` skill; `tapper-dev` remains a
separately installed optional plugin. It ships no separate management skills or
hidden controls. To change roots, run `tap use --flight @namespace/+slug` (or
`tap use +slug`) and start a new thread. Within an existing thread,
authority-bearing calls default to that root and may select an accessible
transitive descendant with `flight`.

If no flight is selected, MCP connects with the complete tool surface and
normal identity-authorized full access. Bare calls can use every accessible KEG
at Codex's real role; explicit `flight` selects one listed real flight without
inheriting no-flight authority. Ask the user to create or choose a
least-privilege flight, run `tap use --flight @namespace/+slug`, and start a
new thread. `session_refresh` cannot narrow the current connection. If only
the recovery tools appear, an explicitly configured root failed to initialize.
If MCP tools are unavailable, report the unavailable connection, ask the user
to reconnect or restart the host session, and never kill or signal host-owned
processes.
