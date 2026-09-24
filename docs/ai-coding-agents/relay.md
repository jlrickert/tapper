# Relay

> Experimental: the command, configuration, and protocol may change.

`tap relay` offers this machine's model providers, such as a local Ollama, to
your Hub account's model catalog. Hub then routes inference for those models
through the relay.

```sh
tap relay
tap relay --name laptop --max-concurrent 2
```

It is registered alongside `tap integrate` and `tap launch`, so it is available
wherever those commands are.

## How it works

- The relay dials out to Hub at `/api/v1/relay/connect` over a WebSocket and
  stays connected, so no inbound port is needed.
- It authenticates with the token from `tap auth login` for the selected Hub.
- Hub sends inference requests down the connection. The relay forwards them to
  the configured provider as OpenAI chat-completions calls and streams the
  result back. Hub never chooses provider URLs or headers.
- Provider credentials stay on this machine; configuration names the
  environment variable, never the key.
- It runs in the foreground until interrupted and reconnects if the connection
  drops. Disconnecting the relay from Hub's Account → Relay page stops it;
  run it again to reconnect.

## Configuration

Providers live in the user config `relay` block. See
[User Config: Relay providers](../configuration/user-config.md#relay-providers).

| Flag | Effect |
|---|---|
| `--name` | Relay name shown in Hub; overrides `relay.name` |
| `--max-concurrent` | Maximum in-flight requests (default 4) |

## Surfaces

The relay is a long-running foreground process, so it has no MCP tool. It is
available only as a CLI command.
