# Provider-neutral launcher composition

A future launcher can bind an agent process to one immutable flight identity by
starting Tapper as `tap mcp --flight @namespace/+slug`. Orientation still
refreshes that flight's latest manifest and instructions, but shared
configuration cannot redirect the process to another flight.

The launcher specification is deliberately provider-neutral:

1. Choose an agent host and model command.
2. Start a dedicated Tapper MCP process with a static `--flight` argument.
3. Connect the host to that process over its supported MCP transport.
4. Require initialization/orientation before KEG work.
5. Keep durable task and plan state in ordinary runtime-interpreted KEG notes;
   do not create a local agent-session registry.
6. On restart, initialize again and recover durable work from those notes.

Multiple launcher-bound processes may use different flights in the same
project without rewriting shared configuration. This stronger immutable mode is
recommended when preventing accidental self-expansion matters. Config-driven
mode remains convenient for deliberate temporary switching. Neither mode
replaces operating-system sandboxing or separate credentials.

Host composition should follow each provider's native configuration surface:

- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-reference#configtoml)
- [Claude Code MCP configuration](https://code.claude.com/docs/en/mcp)
- [Ollama launch composition](https://ollama.com/blog/launch)

This document specifies composition only; Tapper does not implement the
launcher in this change.
