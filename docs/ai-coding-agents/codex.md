# Codex Install

Tapper ships a bundled Codex integration installed by a single command. The
install tree contains an `AGENTS.md` file, three saved prompts, and an MCP
configuration snippet; copying them into `~/.codex/` wires tapper into Codex
as the agent guide and MCP server.

The install assumes the `tap` binary is already on your `PATH`. Install it
first via Homebrew, `go install`, or the release tarball.

## Install

```bash
tap integrate codex
```

This writes the rendered Codex tree from inside the `tap` binary to
`~/.codex/`. Preview the target paths first:

```bash
tap integrate codex --dry-run
```

The dry run prints the target paths without touching the filesystem. The
five paths are:

```
~/.codex/AGENTS.md
~/.codex/config-snippet.toml
~/.codex/prompts/tapper-orient.md
~/.codex/prompts/tapper-search.md
~/.codex/prompts/tapper-snapshot.md
```

After `tap integrate codex` runs, register the MCP server by merging
`~/.codex/config-snippet.toml` into `~/.codex/config.toml`:

```toml
[mcp_servers.tapper]
command = "tap"
args = ["mcp"]
```

If an `[mcp_servers]` section already exists in your Codex config, merge the
`[mcp_servers.tapper]` entry into it rather than duplicating the header.

Restart Codex to pick up the new `AGENTS.md` and the MCP registration.

## What the install contains

| Artifact                              | Purpose                                                                  |
| ------------------------------------- | ------------------------------------------------------------------------ |
| `AGENTS.md`                           | Codex-facing guide: tapper purpose, rules, link conventions, tool inventory |
| `prompts/tapper-orient.md`            | Saved prompt that calls `mcp__tapper__orient` for a bounded bootstrap    |
| `prompts/tapper-search.md`            | Saved prompt for searching across KEG content                            |
| `prompts/tapper-snapshot.md`          | Saved prompt that snapshots a node before destructive edits              |
| `config-snippet.toml`                 | `[mcp_servers.tapper]` stanza to merge into `~/.codex/config.toml`       |

Saved prompts surface as Codex slash commands; invoke them to run the common
orientation, search, and snapshot-before-edit flows without rewriting the
underlying MCP calls each time.

## What the install does not contain

- **No binary.** The install expects `tap` on `PATH`. If `tap --version` does
  not work in the shell Codex launches, the MCP server will fail to start.
- **No automatic config merge.** The config snippet ships as a separate file;
  merging it into `~/.codex/config.toml` is a manual step so existing MCP
  server entries are not overwritten.
- **No project-specific keg configuration.** Tapper's normal config cascade
  (CLI flags, `TAP_*` env vars, `.tapper/config.yaml`,
  `~/.config/tapper/config.yaml`, defaults) resolves the active keg the same
  way it does for any CLI invocation.

## Update

Re-run the integrate command after upgrading the `tap` binary:

```bash
tap integrate codex
```

The rendered tree is embedded in the binary, so new tapper releases ship
updated `AGENTS.md`, prompts, and the MCP snippet together. Files overwrite
in place; local edits to `~/.codex/AGENTS.md` will be replaced. Keep any
per-user customization in a separate Codex guide file.

## Uninstall

Remove the installed files:

```bash
rm ~/.codex/AGENTS.md
rm ~/.codex/config-snippet.toml
rm -rf ~/.codex/prompts/tapper-*.md
```

Remove the `[mcp_servers.tapper]` section from `~/.codex/config.toml` to
deregister the MCP server. The `tap` binary itself is untouched — remove it
via whichever package manager installed it.

## Override the install location

For non-standard setups, point the installer at a different directory:

```bash
tap integrate codex --target /path/to/codex-config
```

`--target` replaces `~/.codex/` in every written path. Useful for staging
installs, containerized Codex sessions, or running `tap integrate` against a
checked-out dotfiles repo.

## Troubleshooting

**`tapper` MCP server does not appear after install.** Confirm the
`[mcp_servers.tapper]` section is present in `~/.codex/config.toml` (not just
in `config-snippet.toml`). Restart Codex; MCP server registration is picked
up on session start.

**Server is registered but tool calls fail with `command not found` or
similar spawn errors.** The `tap` binary is not on `PATH` in the environment
Codex launches. Confirm `which tap` in the same shell that starts Codex; if
it is missing, extend your shell `PATH` in `~/.zshrc`, `~/.bashrc`, or the
equivalent login file.

**`tap integrate codex` reports `unknown host`.** The binary was built without
the Codex adapter registered. Upgrade to a current Tapper release that ships the
Codex integration.

**Tools return stale data.** The MCP server keeps its in-memory index warm;
re-issue the call. If the staleness persists, a separate `tap` CLI process
likely wrote to the same keg outside the MCP session. See
[Agent Conventions](agent-conventions.md) for why to avoid mixing CLI writes
with a live MCP session.

## See also

- [Claude Code Plugin](claude-code-plugin.md) — the equivalent one-command
  install for Claude Code.
- [Orientation Surface](orient.md) — the shared `orient` payload available
  through the MCP tool, MCP resource, and CLI.
- [MCP Server Setup](mcp-setup.md) — manual MCP registration when
  `tap integrate codex` is not the right fit.
- [Agent Conventions](agent-conventions.md) — the invariants every agent
  should follow against a tapper KEG.
