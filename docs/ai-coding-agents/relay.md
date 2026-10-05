# Relay

> Experimental: the command, configuration, and protocol may change.

`tap relay` offers this machine's model providers, such as a local Ollama, to
your Hub account's model catalog. Hub then routes inference for those models
through the relay. It can also offer the tools of local MCP servers to Hub,
see [MCP servers](#mcp-servers), and offers the coding agents installed on
this machine as tools, see [Built-in servers](#built-in-servers).

```sh
tap relay
tap relay --name workstation
```

It is registered alongside `tap integrate` and `tap launch`, so it is available
wherever those commands are.

## How it works

- The relay dials out to Hub at `/api/v1/relay/connect` over a WebSocket and
  stays connected, so no inbound port is needed.
- It authenticates with the token from `tap auth login` for each Hub it serves.
- Hub sends inference requests down the connection. The relay forwards them to
  the configured provider as OpenAI chat-completions calls and streams the
  result back. Hub never chooses provider URLs or headers.
- Provider credentials stay on this machine; configuration names the
  environment variable, never the key.
- It runs in the foreground until interrupted and reconnects if the connection
  drops. Disconnecting the relay from a Hub's Account → Relay page stops it
  for that Hub; run it again to reconnect.

## Several hubs

By default the relay serves one Hub: `--hub`, else the selected `hub`, else
the first configured hub. List hubs under `relay.hubs` to serve them all from
one process, each over its own connection:

```yaml
relay:
  hubs: [work, personal]
```

- Every listed Hub sees every provider and model. Each of them can send your
  providers prompts, and for a hosted provider that spends your API key.
- Each provider's `maxConcurrent` is its limit across all hubs. When a
  provider is full, whichever Hub asks next for one of its models hears
  `overloaded`. Hub answers its caller with 429, or tries another relay for a
  pooled model. The relay's other providers are unaffected.
- Models are listed once and a change reaches every Hub.
- A Hub that rejects the relay (a revoked token, an incompatible version, or its
  owner disconnecting it) stops being served; the others carry on.
  `tap relay` exits once no Hub is left.
- `--hub` narrows the relay to that one Hub even when `relay.hubs` is set.

## Configuration

Providers live in the user config `relay` block, which can also turn the
relay off with `enabled: false`. Each provider sets its own `maxConcurrent`
(default 4): a local GPU and a hosted API have different capacity, and one
filling up does not block the other. Hub is told the sum of the providers'
limits, capped at 64. A provider's `priority` (1–99, lower preferred) ranks
its models on Hub: they list first, so chat defaults to them, and a pool tries
them first. Set `priority: 1` on a free local provider to prefer it over a
paid one. See
[User Config: Relay providers](../configuration/user-config.md#relay-providers).

Each model also carries its context limits and reasoning mode where the
provider reports them: Ollama through `/api/show` and `/api/ps`, OpenRouter
through its model list. `models.metadata` fills in or corrects them for
providers that report nothing. A model with a fixed context is its own model:
declare it under `models.variants` (on Ollama the relay creates it with that
`num_ctx`). A metadata change reaches Hub on the next catalog refresh, like a
new model.

Chat models also say whether they call tools and accept images:
- Ollama reports both through `/api/show`.
- OpenRouter reports them through `supported_parameters` and `architecture.input_modalities`.
- `models.metadata` can set `tools: true|false` and `vision: true|false` for any provider.

Hub shows these as badges on agent model lists and sends tool and image requests only to models that support them.

Hub pools models by a **canonical name**, so the same weights at the same precision served by different providers or relays are one model:
- `@<you>/qwen3.6-35b`, whether it runs as Ollama's `qwen3.6:35b-mlx` or LM Studio's `qwen/qwen3.6-35b`.
- `@<you>/qwen3.6-35b-a3b-8bit` for Ollama's `qwen3.6:35b-a3b-mxfp8` and `qwen3.6:35b-a3b-q8_0` alike; the `nvfp4` build is a separate `qwen3.6-35b-a3b-4bit`.
- The relay derives the name: it lowercases, drops a source prefix and `:latest`, strips trailing packaging parts (`mlx`, `gguf`, …), and rewrites a trailing quantization part (`q4_k_m`, `mxfp8`, `bf16`, …) as its bits per weight (`4bit`, `8bit`, `16bit`).
- Set `canonical:` in `models.metadata` when the derived name pools the wrong models together, or keeps apart ones that belong together.

A model does one job. Chat models serve chat completions, speech models
(`models.transcription`) serve `/audio/transcriptions`, and embedding models
serve `/embeddings`. Ollama marks its embedding models itself; for other
providers, list them under `models.embeddings`. `tap launch` leaves speech and
embedding models out of a harness's model list.

## MCP servers

List MCP servers under `relay.mcp` and the relay forwards their tools to Hub.
A server is either a stdio program the relay starts or a streamable HTTP
server that is already running:

```yaml
relay:
  mcp:
    everything:
      command: npx
      args: ["-y", "@modelcontextprotocol/server-everything"]
    notes:
      url: http://127.0.0.1:3000/mcp
      headersFromEnv: {Authorization: NOTES_MCP_AUTH}
      tools:
        deny: ["delete_*"]
```

- The relay starts every server when it starts, lists its tools, and sends the
  list to each Hub. It re-lists when a server announces a change, and on the
  catalog refresh for servers that do not. A server that exits drops out of
  the list and is restarted, backing off from one second to a minute.
- Hub calls a tool by the server and tool names the relay listed, with a JSON
  object of arguments. It cannot name a command, URL, header, or environment
  variable. Cancelling the call in Hub cancels it on the server.
- A stdio server gets `PATH`, `HOME`, `USER`, `SHELL`, `TMPDIR`, locale
  settings, the variables named in `envFrom`, and the literal `env` values.
  Hub tokens and provider API keys are not passed unless you name them.
  `inheritEnv: true` passes the whole environment instead. The server runs in
  its own process group, which the relay kills when it stops. Its stderr goes
  to the relay's log at debug level.
- Each server has its own `maxConcurrent` (default 4) across all hubs, and a
  `timeout` per call (default `2m`). A full server answers `overloaded`; a
  call past its timeout ends with `timeout`.
- Relayed tools reach Hub's chat, Hub's `/mcp/relay` endpoint, and every
  `tap mcp`, including the Claude Code plugin's server; see
  [Where relayed tools appear](#where-relayed-tools-appear). In chat, every
  relayed call asks for approval unless the approval setting is `full`,
  whatever the server's annotations say.
- Who may call a server is decided on Hub: you enable it there, and share it
  from there. A shared call runs on this machine with your server's access.
- Tools need Hub support for relay protocol 2. An older Hub gets the models
  alone, and the relay logs that it does not support relayed tools.
- A relay can offer MCP servers without any providers.
- On relay protocol 3, Hub names the app each request came from (its chat,
  `tap launch <harness>`, or its API), and the relay passes it to OpenRouter
  providers as `X-Title` and `HTTP-Referer` so OpenRouter's logs show it. No
  other provider is told.

## Where relayed tools appear

Hub names a relayed tool `mcp__<owner>__<server>__<tool>` and offers it in
three places:

- **Hub's chat**, to agents that allow relay tools (`relay:tools`).
- **Hub's `/mcp/relay` endpoint**, for any MCP client with a Hub token.
- **Every `tap mcp`**, including the server the Claude Code plugin starts. It
  connects to `/mcp/relay` in the background and serves the relayed tools
  next to the KEG tools; see
  [MCP Server Setup: Relayed tools](mcp-setup.md#relayed-tools).

A server serves no one until you enable it on Hub's Relay page. Once enabled,
it serves agents in your personal namespace. To lend it to others, share it
from the Relay page into a namespace: an organization, or another person's
personal namespace. That namespace's owner or an admin accepts the share, and
from then on it serves agents in that namespace. Tool calls run on your
machine, so a share never takes effect until it is accepted.

Hub scopes relayed tools by the namespace of the agent asking. With an agent
(`?agent=@ns/name` on `/mcp/relay`, or `TAP_AGENT` for `tap mcp`, which
`tap launch --agent` sets) it offers the servers granted to that agent's
namespace, gated by the agent's `relay:tools`. Without an agent it offers
those granted to your personal namespace: your own enabled servers and the
shares you accepted. Set `TAP_RELAY_TOOLS=off` to keep a `tap mcp` from
offering them.

The namespace in a tool's name is always its owner's username:
`mcp__<owner>__<server>__<tool>`, wherever it was shared.

### Why don't I see a shared server?

A server another person runs shows up for an agent only when all of these
hold:

1. Its owner enabled it on Hub's Relay page.
2. Its owner shared it into the agent's namespace.
3. That namespace's owner or an admin accepted the share. For your personal
   namespace, that is you.
4. For an organization, its owner is still a member.
5. Their relay is online.

For tools a whole organization should have, run them on a relay owned by a
service user who is a member, not on a person's laptop, so they do not depend
on one person's machine being on.

## Built-in servers

Besides the servers in `relay.mcp`, the relay can offer its own. All are off
until `relay.runners` turns them on, and each is offered only when its program
is on `PATH`:

| Server | Runs | Turned on by |
|---|---|---|
| `claude` ("Claude Code") | `tap runner serve --runner claude` | `claude.run: true` |
| `codex` ("Codex") | `tap runner serve --runner codex` | `codex.run: true` |
| `opencode` ("opencode") | `tap runner serve --runner opencode` | `opencode.run: true` |
| `pi` ("pi") | `tap runner serve --runner pi` | `pi.run: true` |
| `claude-tools` ("Claude Code tools") | `claude mcp serve`, in the first root | `claude.tools: true` |

Each is a server of its own, so you enable and share them on Hub one by one.
They act as you, on this machine, and inherit the relay's whole environment,
since the agents need their own credentials and configuration. A relay with no
providers and no `relay.mcp` still starts when a built-in server is turned on
and available.

Configure them under `relay.runners`:

```yaml
relay:
  runners:
    claude:
      run: true            # the claude server: claude_code_run
      tools: true          # the claude-tools server: claude mcp serve
    codex: {run: true}     # likewise opencode and pi
    roots: [~/src, ~/work] # where a delegated task may work; default ~
    timeout: 30m           # longest one task may run
    maxConcurrent: 2       # tasks running at once, per server
  mcp:
    claude-tools:
      enabled: false       # a relay.mcp entry with a built-in's name replaces it
```

Only `claude` has `tools`: no other harness has an MCP serve mode, and the
relay refuses `tools` on `codex`, `opencode`, or `pi`. A `relay.mcp` entry with
a built-in server's name replaces that server, and `enabled: false` on it
removes the server.

**Sharing the built-ins.** A shared call to a runner or to `claude-tools` runs
a coding agent, or a shell, with the relay user's access inside `roots`.
Share them only from a host built for it, such as a locked-down VM or
container that serves as an organization's coding worker. Narrow `roots` to
its workspace and run the relay as an unprivileged user:

```yaml
relay:
  runners:
    claude: {run: true}
    roots: [/workspace]
```

### Runner tools

Each runner server has one tool: `claude_code_run`, `codex_run`,
`opencode_run` or `pi_run`. Each takes:

| Argument | |
|---|---|
| `task` | The instruction, as you would type it. Required. |
| `cwd` | Absolute directory to work in. Required; it must exist and, with symlinks resolved, be under one of the roots. |
| `session` | A session id from an earlier result, to continue that conversation. |
| `model` | A model in the runner's own naming. |

The tool runs the agent non-interactively in `cwd` (`claude -p`,
`codex exec`, `opencode run`, `pi --mode json`) and returns its final reply,
as text headed by the session id and as structured content
`{runner, session, reply, is_error}`. A run that fails, or exits non-zero, is
an error result carrying the end of its stderr. Replies over 1 MiB are
truncated.

Each agent applies its own permission settings, and the tools add no bypass
flags. Claude Code's print mode denies anything that would need an
interactive prompt, `codex exec` runs in a read-only sandbox unless your
config grants more, and opencode and pi follow their own configuration.

`tap runner serve` serves every installed agent, or with `--runner NAME`
just that one. It reads these variables; the relay sets the first two from
`relay.runners`:

| Variable | Effect |
|---|---|
| `TAP_RUNNER_ROOTS` | Directories a task's `cwd` must be under, as a `PATH`-style list. Defaults to `$HOME`. |
| `TAP_RUNNER_TIMEOUT` | Longest one task may run, as a Go duration. Defaults to `30m`. |
| `TAP_RUNNER_DEPTH` | At 1 or more, every call is refused. Each agent a runner starts gets it incremented, so a delegated agent cannot delegate again. |

Agents a runner starts also get `TAP_RELAY_TOOLS=off`, so their own `tap mcp`
does not offer relayed tools.

## Limits

Limits: 32 servers, 128 tools per server, 512 tools in all. A tool whose name
is not letters, digits, `.`, `_` and `-`, or whose description or input schema
is too large, is skipped with a warning. A result larger than 4 MiB reaches Hub
as an error result.

| Flag | Effect |
|---|---|
| `--name` | Relay name shown in Hub; overrides `relay.name` |
| `--hub` | Serve only this Hub, overriding `relay.hubs` |

## Surfaces

The relay is a long-running foreground process, so it has no MCP tool of its
own. It is available only as a CLI command. `tap runner serve` is an MCP server
the relay starts, not a command to run by hand.
