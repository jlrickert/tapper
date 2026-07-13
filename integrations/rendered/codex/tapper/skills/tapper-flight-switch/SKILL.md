---
name: tapper-flight-switch
description: Switch the Tapper flight for this conversation using an explicit per-call, thread-local override.
---

# Tapper flight switch

Use this skill only after the user explicitly asks to switch the active Tapper
flight for the current conversation.

## Switch flights

1. Resolve the requested flight. Use `mcp__tapper__list_flights` when discovery
   is needed, then `mcp__tapper__flight_show` with the requested reference so
   the user can see its title, instructions, and cover.
2. Call `mcp__tapper__orient` with that same explicit reference in `flight`.
   The orientation result establishes the flight instructions and covered KEGs.
3. Retain the exact selected reference in conversation context.
4. Pass that reference in the `flight` field on every subsequent Tapper MCP
   call in this thread, including reads, searches, writes, snapshots, and later
   orientation calls. The flight's cover remains authoritative.

## Thread-local behavior

The override is conversational and call-scoped. Do not write Tapper config and
do not assume mutable MCP session state. A newly opened host session returns to
the project flight configured in `.tapper/config.yaml` unless the user asks to
switch again.

For a persistent project default, the user can run either:

```text
tap use --flight @namespace/+slug
tap use +slug
```

The short form uses the resolved default namespace. Both forms update the
project's `.tapper/config.yaml` for newly opened Codex or Claude sessions.
