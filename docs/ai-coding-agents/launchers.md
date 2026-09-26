# Provider-neutral launcher composition

`tap launch HARNESS` starts Claude Code, Codex, opencode, or pi on a model from
your Hub catalog and binds it to one connection-pinned Hub-backed flight root.

## Models

Hub is the only inference plane. `--model` names a catalog id, one of the
models your connected relays offer or that are shared with you (see
[`tap relay`](relay.md)). Without it the launch starts on the first model in
your catalog, which Hub orders by the relay owners' `priority`. There is no
local model configuration: the `agent` and `agents` keys are retired and
ignored, and `tap doctor` flags them.

```sh
tap launch claude --model laptop/ollama/qwen3:8b
tap launch codex
tap launch opencode --dry-run
```

Each harness talks to Hub in the protocol it was built for, and Hub serves
each one:

| Harness | Protocol | Wiring |
| --- | --- | --- |
| Claude Code | Anthropic Messages | `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, and every `ANTHROPIC_*_MODEL` slot set to the model. A session-only `--settings` `modelPicker` replaces `/model`'s Claude lineup with your catalog. An inherited `ANTHROPIC_API_KEY` is unset. |
| Codex | OpenAI Responses | A `foldwise` model provider passed with `-c`, with `wire_api = "responses"` and its key in `TAP_LAUNCH_KEY` |
| opencode | OpenAI chat completions | A `foldwise` provider in `OPENCODE_CONFIG_CONTENT` listing the whole catalog |
| pi | OpenAI chat completions | A generated extension loaded with `-e` that registers a `foldwise` provider for the whole catalog. Your `~/.pi/agent` is untouched. |

The harness reaches Hub through a loopback forwarder that `tap launch` runs for
the life of the session. It serves only Hub's inference routes, requires a
per-launch key, and attaches your Hub credential to each request, refreshing a
`tap auth login` token as it nears expiry. The credential never reaches the
harness. `--dry-run` prints the invocation with placeholders for the
forwarder's address, key, and file directory.

The child also gets `TAP_HARNESS` and `TAP_MODEL`. Tapper reports them in
orientation and telemetry as the session's identity, and they select nothing.

## Flight root

Set `flight: @namespace/+slug` in Tapper configuration (including a
directory-specific `kegMap` rule) or export
`TAP_FLIGHT=@namespace/+slug`; the launcher validates that the namespace routes
to a remote Hub before starting the harness. Flights are always Hub-backed.
Project flight overrides the mapped flight; both override the user baseline.
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
