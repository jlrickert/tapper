# Provider-neutral launcher composition

`tap launch` binds an agent process to one connection-pinned Hub-backed flight root.
Set `flight: @namespace/+slug` in Tapper configuration or export
`TAP_FLIGHT=@namespace/+slug`; the launcher validates that the namespace routes
to a remote Hub before starting the harness. Flights are always Hub-backed.
For a one-shot root that does not rewrite shared configuration, pass the global
flag directly:

```sh
tap launch claude --flight @namespace/+slug
```

Every authority-bearing MCP call reloads the root's live graph without allowing
shared configuration to redirect the running process to another root. The
controller may select any identity-accessible flattened descendant explicitly;
that flight contributes independent instructions and authority for that call.

The launcher specification is deliberately provider-neutral:

1. Choose an agent host and model command.
2. Resolve and validate the configured canonical Hub-backed launch root.
3. Connect the host to that process over its supported MCP transport.
4. Require initialization followed by `orient` before KEG work.
5. Keep durable task and plan state in ordinary runtime-interpreted KEG notes;
   do not create a local agent-session registry.
6. On restart, initialize again and recover durable work from those notes.

Multiple launcher-bound processes may use different flights in the same
project without rewriting shared configuration. This stronger pinned-root mode is
recommended when preventing accidental self-expansion matters. Config-driven
mode remains convenient for deliberate temporary switching. Neither mode
replaces operating-system sandboxing or separate credentials.

Host composition should follow each provider's native configuration surface:

- [Codex configuration](https://learn.chatgpt.com/docs/config-file/config-reference#configtoml)
- [Claude Code MCP configuration](https://code.claude.com/docs/en/mcp)
- [Ollama launch composition](https://ollama.com/blog/launch)

The launcher is experimental.
