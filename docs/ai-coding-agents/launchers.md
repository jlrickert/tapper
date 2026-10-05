# Provider-neutral launcher composition

`tap launch HARNESS` starts Claude Code, Codex, opencode, or pi on a model from
your Hub catalog and binds it to one connection-pinned Hub-backed flight root.

## Models

For Codex, opencode, and pi, Hub is the only inference plane. Claude Code
defaults to split mode (below), which adds Hub's catalog beside your own Claude
models. `--model` names a catalog id: a pooled model
such as `@you/qwen3:8b`, served by your own connected relays or by a pool shared
with you (see [`tap relay`](relay.md)). Each id is one model however many relays
back it; Hub routes each request to one of them. Without `--model` the launch
starts on the first model in your catalog, which Hub orders by the best
`priority` any contributing relay owner gave it. Harness model pickers list the
catalog under the Foldwise provider, for example
`Foldwise (atlas) · @you pool · 32k context`. There is no
local model configuration: the `agent` and `agents` keys are retired and
ignored, and `tap doctor` flags them.

```sh
tap launch claude --model @you/qwen3:8b
tap launch codex
tap launch opencode --dry-run
```

Each harness talks to Hub in the protocol it was built for, and Hub serves
each one:

| Harness | Protocol | Wiring |
| --- | --- | --- |
| Claude Code | Anthropic Messages | Split mode (default): `ANTHROPIC_BASE_URL` set to the forwarder's per-launch path, and a session-only `--settings` `modelPicker` adding your catalog to `/model` with `replaceBuiltInOptions: false`. `--hub`: `ANTHROPIC_BASE_URL`, `ANTHROPIC_AUTH_TOKEN`, and every `ANTHROPIC_*_MODEL` slot set to the model, the picker replaced by your catalog, and an inherited `ANTHROPIC_API_KEY` unset. `--subscription`: no model wiring at all. |
| Codex | OpenAI Responses | A `foldwise` model provider passed with `-c`, with `wire_api = "responses"` and its key in `TAP_LAUNCH_KEY` |
| opencode | OpenAI chat completions | A `foldwise` provider in `OPENCODE_CONFIG_CONTENT` listing the whole catalog |
| pi | OpenAI chat completions | A generated extension loaded with `-e` that registers a `foldwise` provider for the whole catalog. Your `~/.pi/agent` is untouched. |

The harness reaches Hub through a loopback forwarder that `tap launch` runs for
the life of the session. It serves only Hub's inference routes, requires a
per-launch key, and attaches your Hub credential to each request, refreshing a
`tap auth login` token as it nears expiry. The credential never reaches the
harness. `--dry-run` prints the invocation with placeholders for the
forwarder's address, key, and file directory.

### Claude Code: split, hub, and subscription

Claude Code takes one base URL, so split mode routes inside the forwarder.
Claude Code sends every model request to
`http://127.0.0.1:<port>/t/<launch key>/anthropic`. A request naming a Claude
model (`claude-*`, or an alias such as `sonnet`) goes on to Anthropic exactly
as Claude Code sent it, with your own Claude login or `ANTHROPIC_API_KEY`;
`tap launch` adds nothing to it, and that credential never reaches Hub. Any
other model goes to Hub with your Hub credential, which never reaches
Anthropic or Claude Code. Without `--model` or `--agent` the session starts on
Claude Code's own default model, and background work stays on its small Claude
model. If the launching shell set `ANTHROPIC_BASE_URL`, Claude models go there
instead of `https://api.anthropic.com`. If Hub cannot list your models, the
launch warns and continues with Claude models only.

The per-launch key sits in the path because Claude Code's credential in split
mode is your Claude login, not the key. Outside that path the forwarder still
requires the key, and the path opens no routes beyond the inference ones.

`--hub` restores Hub-only inference: every slot on the selected Hub model.
`--subscription` leaves Claude Code's models alone, so Hub supplies only tools
and agents.

### Claude Code: relayed tools and subagents

Relayed MCP tools come through the tapper plugin's `tap mcp`, not through the
launch. The session's `tap mcp` sees `TAP_AGENT`, so Hub offers it the relayed
tools the `--agent` allows (`relay:tools`). Claude Code names them
`mcp__plugin_tapper_tapper__mcp__<owner>__<server>__<tool>`. See
[Relay: Where relayed tools appear](relay.md#where-relayed-tools-appear).

The agent's subagents become Claude Code subagents through `--agents`, named
`<namespace>-<name>`. `--subagents all` offers every agent you can see instead,
and `--subagents none` offers none. Each subagent gets:

- its Hub tools, as `mcp__plugin_tapper_tapper__<tool>` (every hosted tool when
  its list is empty), using `runtime_tools`, so the organization's tool policy
  applies;
- its relayed tools, as `mcp__plugin_tapper_tapper__<name>`, when both it and
  the launch agent allow them;
- the built-ins in `--subagent-builtins`, by default `Read,Grep,Glob`;
- its model, when the launch can reach it; otherwise it inherits the session's.

Hub still checks every call against the launch agent, because the MCP sessions
run as that agent. A subagent's list can narrow what it is offered but never
widen it.

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

## Agents

A session runs one agent. Agents live on the Hub: a namespace-owned model,
instructions, tool allowlist, and flight. The agent brings its flight as the
launch root; `--flight` (or `TAP_FLIGHT`, or the project's configured flight)
overrides it for one session. Pass the agent by reference:

```sh
tap launch opencode --agent @namespace/researcher
tap launch opencode --agent @namespace/researcher --flight @namespace/+other
```

With `--flight` and neither `--agent` nor `--model`, the launch runs your first
catalog model with no Hub agent. An agent with no flight, launched without
`--flight`, has no launch root. The agent's model runs through the Hub (a Hub agent with no model takes
your first catalog model), its instructions reach the harness (opencode gets
them as a primary agent), and the launcher exports `TAP_AGENT=@namespace/name`
so the child's `tap mcp` lists and runs only the agent's tools, with `orient`
and `guide` always available. The allowlist reloads on each tool request. If `tap mcp` cannot load the agent it serves no
other tools. An agent without a memory flight or an explicit flight override
remains locked to recovery tools. `--agent` accepts only a qualified Hub reference; local `agents:` entries remain retired.

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
