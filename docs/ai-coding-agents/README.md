# Using Tapper From AI Agents

This section documents how to drive tapper from an AI coding agent — what
the MCP server exposes, how to install it in a host that understands the
Model Context Protocol, and the conventions any agent should follow when
operating against a KEG.

Vendor-specific configuration (how to write a `CLAUDE.md`, how Codex
discovers `AGENTS.md`, permission modes, hooks) is covered by the vendors
themselves. This directory focuses on tapper.

## Guides

- [Claude Code Plugin](claude-code-plugin.md) — install tapper as a bundled
  Claude Code plugin: one command to register the MCP server and the
  `tapper` skill.
- [Codex Install](codex.md) — install tapper for Codex: `tap integrate codex`
  drops `AGENTS.md`, saved prompts, and the MCP config snippet into
  `~/.codex/`.
- [Orientation Surface](orient.md) — the tiered `orient` surface shared by
  the `mcp__tapper__orient` tool, `tapper://orient/*` resources, and
  `tap orient`.
- [MCP Server Setup](mcp-setup.md) — manual setup for hosts that do not use
  a bundled integration: `claude mcp add`, JSON config for arbitrary MCP
  clients, and the full 31-tool reference.
- [Agent Conventions](agent-conventions.md) — tapper invariants every agent
  should follow: MCP-first, never edit node files directly, never mix CLI
  writes with a live MCP session, snapshot before destructive edits.

## Quick pick

| You want to… | Read |
|---|---|
| Install tapper in Claude Code in one command | [Claude Code Plugin](claude-code-plugin.md) |
| Install tapper in Codex in one command | [Codex Install](codex.md) |
| Understand the tiered orientation payload | [Orientation Surface](orient.md) |
| Wire tapper into a non-bundled MCP host | [MCP Server Setup](mcp-setup.md) |
| Know which tools the MCP server exposes | [MCP Server Setup — Available Tools](mcp-setup.md#available-tools) |
| Tell an agent how to behave against a KEG | [Agent Conventions](agent-conventions.md) |
