# opencode

Install the baseline Tapper plugin through the official supported installer:

```bash
tap integrate opencode
```

opencode has no plugin marketplace CLI, so this installer works differently
from the Claude and Codex ones. Instead of handing a directory to a host
command, `tap` writes opencode's own configuration: it merges an `mcp.tapper`
server into `opencode.json` and installs the Tapper skills as
`skills/<name>/SKILL.md` directories that opencode discovers on its own.

The MCP entry it writes is:

```json
"tapper": { "type": "local", "command": ["tap", "mcp"], "enabled": true }
```

Everything else in your `opencode.json` — your theme, your model, your other
MCP servers — is preserved. Only the `mcp.tapper` key is tap's.

## Scopes

`--scope` decides which of opencode's two configuration layers is written.

| Scope | Config file | Skills |
|---|---|---|
| `user` (default) | `~/.config/opencode/opencode.json` | `~/.config/opencode/skills/` |
| `project` | `<project>/opencode.json` | `<project>/.opencode/skills/` |

```bash
tap integrate opencode --scope project
```

Project scope writes files that belong in version control, so the whole team
gets the same Tapper wiring by checking them in.

There is no `--scope local`: opencode reads a user config and a project config
and has no gitignored tier between them, so the flag is rejected rather than
silently writing shared project state.

## Optional developer workflow

```bash
tap integrate opencode --plugin tapper-dev
```

`tapper-dev` adds Plan → Code → Review → Commit guidance but no MCP server. It
is the same skill Claude and Codex receive; its content does not vary by host.

## Preview

```bash
tap integrate opencode --dry-run
```

A dry run prints the extraction paths and the files the install would write. It
starts no process and touches nothing. Because opencode is not driven through a
host CLI, the report is a `Would write:` list rather than the `Would run:`
command list the Claude and Codex dry runs produce.

## Re-running

Re-running the installer is how you upgrade, and it is idempotent: the skills
are replaced wholesale so a file an older version shipped does not linger, and
the config merge lands on byte-identical output for an unchanged install.

Two situations stop the installer rather than guessing:

- **A `tapper` MCP server that is not ours.** If `mcp.tapper` already exists and
  does not run `tap`, the install fails instead of replacing it. Rename your
  entry or remove it if you want tap to own that key.
- **A JSONC config.** If you keep `opencode.jsonc`, tap refuses to edit it,
  because Go's JSON encoder cannot round-trip its comments and writing the
  sibling `.json` would shadow settings you believe are live. Add the
  `mcp.tapper` block above to its `mcp` object by hand.

## The guard

opencode gets the same guardrail as Claude and Codex, delivered differently.
Those two read a JSON table of hook commands; opencode has no such table, but
its plugins can block a call from `tool.execute.before` by throwing. So
`tapper-guard` ships for opencode as a self-contained TypeScript module that
shells out to `tap hook pre-tool-use` — the same binary, the same policy, one
implementation for all three hosts.

`tap integrate opencode` installs it by default into opencode's auto-load
plugin directory (the name is singular):

| Scope | Path |
| --- | --- |
| `user` | `~/.config/opencode/plugin/tapper-guard.ts` |
| `project` | `<project>/.opencode/plugin/tapper-guard.ts` |

Nothing is added to your `opencode.json`: opencode loads every module in that
directory at startup. **Restart opencode** after installing or refreshing, or
the change is not loaded.

The guard **fails closed**. If `tap` cannot be reached, exits non-zero, or
returns output that will not parse, the tool call is blocked rather than
allowed — an agent that can disable the guard by breaking it is not a guard.
For that reason `tap integrate opencode` refuses to install the guard unless a
`tap hook`-capable binary is already on `PATH`.

Turn it off with `tap integrate opencode --no-safety`, which **deletes** the
installed module. Unlike Claude and Codex there is no host command to disable a
plugin, and tap wrote that exact file, so skipping the install alone would
leave a guard running with no off switch. The rest of the install — the MCP
server and the `tapper` skill — is untouched.

Two limits worth knowing. `tool.execute.before` is reported not to fire for
tool calls made by subagents spawned through opencode's `task` tool
(https://github.com/anomalyco/opencode/issues/5894), and the guard's shell
parsing allows what it cannot parse. It is a guardrail against accidental
direct CLI use, not a security boundary. See
[Agent Conventions](agent-conventions.md) for the invariants it enforces.

## Launching

`tap launch opencode` starts opencode on a model from your Hub catalog, with
the current flight as a connection-pinned root:

```bash
tap launch opencode --model laptop/ollama/qwen3:8b
```

The launcher declares a `foldwise` provider inline through
`OPENCODE_CONFIG_CONTENT`, which opencode merges after both the global and
project configs, so it applies without replacing either. The provider lists
your whole catalog, with each model's context window where its relay
advertises one, so opencode's model picker can switch between them. See
[Provider-neutral launcher composition](launchers.md) for how the other
harnesses are wired. `tap launch opencode --dry-run` prints exactly what gets
injected.
