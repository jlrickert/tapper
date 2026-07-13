---
name: tapper-mcp-reset
description: Diagnose and recover a stale or unavailable Tapper MCP connection without killing host-owned processes.
---

# Tapper MCP reset

Use this skill when Tapper tools or the Tapper MCP connection are missing,
stale, or unavailable in the current host session.

## Diagnose first

1. Inspect the tools available in the current session. Do not assume that a
   missing Tapper tool means Tapper is uninstalled.
2. Run the harmless `tap version` probe and report that version. Direct Tapper
   CLI help, version, and completion probes are allowed even in MCP-first host
   sessions.
3. If Tapper tools are callable, use `mcp__tapper__info` to verify KEG access,
   then use `mcp__tapper__orient` to confirm the connection and current
   project/flight context.
4. If Tapper tools are not callable, report that the current MCP connection is
   unavailable. Never kill or signal Codex-, Claude-, or MCP-owned processes.

## Recover the host session

- In Claude Code, ask the user to run `/reload-plugins`. If reloading does not
  restore Tapper, guide them to open a new Claude session.
- In Codex, guide the user to start a new thread. If the plugin is still absent,
  guide them to restart the Codex app.
- After the host reconnects, verify recovery with `mcp__tapper__info` followed
  by `mcp__tapper__orient`.

## Reset versus upgrade

A connection reset only makes the already installed plugin and MCP server
available to a fresh host session. It does not upgrade Tapper or refresh the
embedded plugins. Use `tap integrate HOST` again only when the user explicitly
wants to install, upgrade, or change plugin selection or scope.
