# Relay

> Experimental: the command, configuration, and protocol may change.

`tap relay` offers this machine's model providers, such as a local Ollama, to
your Hub account's model catalog. Hub then routes inference for those models
through the relay.

```sh
tap relay
tap relay --name laptop
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

| Flag | Effect |
|---|---|
| `--name` | Relay name shown in Hub; overrides `relay.name` |
| `--hub` | Serve only this Hub, overriding `relay.hubs` |

## Surfaces

The relay is a long-running foreground process, so it has no MCP tool. It is
available only as a CLI command.
