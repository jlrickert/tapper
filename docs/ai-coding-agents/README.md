# Using Tapper From AI Agents

This section documents how AI coding agents use Tapper as shared project
memory: how to install the MCP server, how agents should orient themselves, and
which conventions keep the KEG safe for humans and agents at the same time.

Vendor-specific project configuration, permission modes, and general plugin
behavior are covered by the vendors themselves. This directory focuses on
Tapper's embedded native plugins. Use `tap integrate codex` or `tap integrate
claude` as the official supported installation and upgrade path; use manual MCP
configuration only for generic hosts without a native Tapper plugin.

## Guides

- [Claude Code Plugin](claude-code-plugin.md) — install tapper as a bundled
  Claude Code plugin: one command to register the MCP server and the
  `tapper` skill.
- [Codex plugins](codex.md) — install baseline `tapper` and optional
  `tapper-dev` from the embedded local marketplace.
- [Orientation Surface](orient.md) — the shared `orient` payload exposed by
  the `mcp__tapper__orient` tool, the `tapper://orient` resource, and
  `tap orient`.
- [MCP Server Setup](mcp-setup.md) — manual setup for hosts that do not use
  a bundled integration: `claude mcp add`, JSON config for arbitrary MCP
  clients, and the tool categories exposed over MCP.
- [Provider-neutral launcher composition](launchers.md) — future pinned-root
  `--flight` process binding across agent hosts and runtimes.
- [Agent Conventions](agent-conventions.md) — tapper invariants every agent
  should follow: MCP-first, never edit node files directly, never mix CLI
  writes with a live MCP session, snapshot before destructive edits.

## Quick pick

| You want to… | Read |
|---|---|
| Install tapper in Claude Code in one command | [Claude Code Plugin](claude-code-plugin.md) |
| Install tapper in Codex in one command | [Codex Install](codex.md) |
| Understand the orientation payload | [Orientation Surface](orient.md) |
| Wire tapper into a non-bundled MCP host | [MCP Server Setup](mcp-setup.md) |
| Know which tools the MCP server exposes | [MCP Server Setup — Available Tools](mcp-setup.md#available-tools) |
| Tell an agent how to behave against a KEG | [Agent Conventions](agent-conventions.md) |
