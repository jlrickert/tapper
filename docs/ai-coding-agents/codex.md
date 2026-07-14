# Codex plugins

Install the baseline Tapper plugin from the local marketplace embedded in the
`tap` binary:

```bash
tap integrate codex
```

The installer extracts the marketplace below the platform user-data directory,
registers it with `codex plugin marketplace add`, and installs
`tapper@tapper-local`. The plugin owns the `tap mcp` registration, a
flight-first Tapper skill, and a `PreToolUse` guardrail that blocks direct
agent use of `tap` and `keg` except harmless help, version, and completion
probes. It calls `mcp__tapper__orient`, follows the active flight and
covered-KEG instructions, and applies MCP-first safety rules.

After installing or refreshing the plugin, open Codex `/hooks` and review the
bundled Bash hook before trusting it. The hook invokes
`${PLUGIN_ROOT}/hooks/block-tap-cli.py`; it is a guardrail against accidental
direct CLI use, not a complete shell security boundary. Start a new thread
after approving the hook so both the hook and MCP connection are fresh.

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

Re-running the installer atomically refreshes the embedded marketplace and
reinstalls from the local source. A same-named marketplace pointing elsewhere
is reported as a conflict and is never replaced automatically.

Codex currently supports only `--scope user`. `--scope project` and `--scope
local` fail before marketplace extraction or Codex commands; use a user install
until Codex exposes native project scopes.

The baseline also ships `tapper-mcp-reset`. It diagnoses the running version
and connection and guides users to a new thread or app restart without killing
host-owned processes. Codex has no equivalent user-only prompt hook, so it does
not expose a flight-switching skill. To change flights, run `tap use --flight
@namespace/+slug` (or `tap use +slug`) and open a new thread so MCP reconnects.

If no flight is selected, MCP still connects in recovery-only mode. Codex can
use `list_flights`, `flight_show`, `auth_status`, and `config`, while KEG tools
remain locked. Ask the user to run `tap use --flight @namespace/+slug`, then
start a new thread to reconnect with the normal tool surface.
