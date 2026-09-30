# Relay

> Experimental: the command, configuration, and protocol may change.

`tap relay` offers this machine's model providers, such as a local Ollama, to
your Hub account's model catalog. Hub then routes inference for those models
through the relay. It can also offer the tools of local MCP servers to Hub's
chat; see [MCP servers](#mcp-servers).

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

List MCP servers under `relay.mcp` and the relay forwards their tools to Hub's
chat. A server is either a stdio program the relay starts or a streamable HTTP
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
- Tools reach only Hub's chat, and only for agents that allow relay tools.
  Hub's approval setting applies: a tool the server marks read-only can run
  without asking, so only forward servers whose annotations you trust.
- `shareable: true` lets people you share the server with on Hub call its
  tools. The calls run on this machine with your server's access. Without it
  the relay refuses every call that is not yours, whatever Hub's shares say.
- Tools need Hub support for relay protocol 2. An older Hub gets the models
  alone, and the relay logs that it does not support relayed tools.
- A relay can offer MCP servers without any providers.

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
own. It is available only as a CLI command.
