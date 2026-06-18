# Claude Code Plugin

Tapper ships as a Claude Code plugin. Installing it registers the `tap mcp`
server and the `tapper` skill in one step, without manual `claude mcp add`
commands or hand-authored skill files.

The plugin assumes the `tap` binary is already on your `PATH`. Install it
first via Homebrew, `go install`, or the release tarball.

## Install

Tapper is distributed through a self-hosted Claude Code marketplace. Add
the marketplace, then install the plugin:

```bash
claude plugin marketplace add jlrickert/tapper@main
claude plugin install tapper@jlrickert-tapper
```

The first command registers the marketplace manifest at
`.claude-plugin/marketplace.json` in the tapper repo. The second
installs the plugin named `tapper` from that marketplace.

After install, confirm the MCP server and the skill are wired up:

```bash
claude /mcp              # should list `tapper`
```

In a new Claude Code session, typing `/tapper` should surface the
`/tapper` skill in the slash menu.

## What the plugin contains

| Artifact | Purpose |
|---|---|
| `.claude-plugin/plugin.json` | Plugin manifest — name, version, description, author. |
| `.claude-plugin/.mcp.json` | Registers `tap mcp` as the `tapper` MCP server on stdio. |
| `skills/tapper/SKILL.md` | The `tapper` skill — conventions for MCP-first KEG workflows. |

All three live under `integrations/rendered/claude/` in the tapper repository,
produced from canonical content by the Claude adapter. The plugin is a thin
wrapper that points Claude Code at them.

The same rendered tree is embedded in the `tap` binary. Agents that prefer
not to install the plugin can reach equivalent orientation content through
the `mcp__tapper__orient` tool or the `tapper://orient/claude/tier-<n>`
MCP resources — see [Orientation Surface](orient.md).

## What the plugin does not contain

- No binary. The plugin expects `tap` on `PATH`. If `tap --version` does
  not work in your shell, the MCP server will fail to start.
- No project-specific keg configuration. Tapper's normal five-tier config
  cascade (CLI flags, `TAP_*` env vars, `.tapper/config.yaml`,
  `~/.config/tapper/config.yaml`, defaults) resolves the active keg the
  same way it does for any CLI invocation.
- No methodology or workflow content. The bundled skill documents the
  tapper MCP surface and the conventions for using it — it does not
  prescribe how to structure notes, tag them, or link them.

## Update

```bash
claude plugin update tapper
```

Plugin versions track the tapper binary release. After `brew upgrade tap`
(or equivalent), run the update above to pick up any changes to the
skill or MCP registration.

## Uninstall

```bash
claude plugin uninstall tapper
```

This removes the plugin, the `tapper` MCP server registration, and the
`/tapper` skill from Claude Code. The `tap` binary itself is untouched —
remove it via whichever package manager installed it.

## Troubleshooting

**`claude /mcp` does not list `tapper` after install.** Restart Claude
Code. MCP server registration is picked up on session start.

**`tapper` is listed but tools fail with a `command not found` or
similar spawn error.** The `tap` binary is not on `PATH` in the
environment Claude Code launches. Confirm `which tap` in the same
shell that starts Claude Code; if it is missing, extend your shell
`PATH` in `~/.zshrc`, `~/.bashrc`, or the equivalent login file.

**Tools return stale data.** The MCP server keeps its in-memory index
warm; re-issue the call. If the staleness persists, it almost always
means a separate `tap` CLI process wrote to the same keg outside the
MCP session. See [Agent Conventions](agent-conventions.md) for why to
avoid mixing CLI writes with a live MCP session.

## Issues

File issues at [github.com/jlrickert/tapper/issues](https://github.com/jlrickert/tapper/issues).
Include the output of `tap --version`, `claude --version`, and the
`claude /mcp` listing.
