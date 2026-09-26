# User Config

User configuration lives at `~/.config/tapper/config.yaml`. It provides the
baseline selection and is the only file allowed to define saved Hub
connections and credentials.

```yaml
hub: atlas
keg: "@foldwise/dev"
hubs:
  atlas:
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
  homelab:
    url: https://hub.example.test
kegMap:
  - pathPrefix: ~/repos/homelab
    hub: homelab
    keg: "@homelab/dev"
```

Use `tap bootstrap` for initial setup, `tap config edit --user` to edit this
file, and `tap config --user` to inspect it. `tap use @namespace/keg --user`
writes its `keg`; `tap hub set-default NAME --user` writes its `hub`.
A matching mapping can override those baseline selections. See
[Resolution Order](resolution-order.md) for the complete precedence rules.

## Fields

- `hub`: selected saved connection name.
- `keg`: KEG reference, such as `@namespace/name`, `keg:@namespace/name`, or a
  bare name used with an explicit `--namespace`.
- `kegMap`: startup-directory rules containing any combination of `keg`, `hub`,
  and `flight`, with at least one default and `pathPrefix` or `pathRegex`.
  Retired `alias` values are preserved but ignored; use `keg` instead.
- `hubs`: named connections with `url` and
  `tokenEnv` or `token`. Login credentials can also come from the AuthStore.
- `relay`: providers that [`tap relay`](../ai-coding-agents/relay.md) offers
  to Hub. User config only; see [Relay providers](#relay-providers).
- `flight`: baseline Flight context; directory mappings, project config, `TAP_FLIGHT`, and
  `--flight` can override it.
- `disableAtlasHub`: disables the implicit Atlas fallback. Explicit saved
  connections, including Atlas, remain available.
- `disableTelemetry`: disables invocation reporting.

The environment selections are `TAP_HUB`, `TAP_KEG`, and `TAP_FLIGHT`. Retired default/fallback
Hub and KEG keys, their environment overrides, namespace-to-Hub mappings, and
Hub `kind` no longer affect selection. Unknown and retired YAML and comments
survive configuration rewrites, except `updated`: this retired timestamp is
ignored when read and removed on serialization. Malformed YAML fails. Invalid unused Hub and
mapping entries do not prevent unrelated operations; selected invalid values
fail clearly.

## Relay providers

The `relay` block configures [`tap relay`](../ai-coding-agents/relay.md). Only
user config may define it: a `relay` block in project config is removed with a
warning, like `hubs`.

```yaml
relay:
  enabled: true                 # optional; false turns the relay off
  name: laptop                  # optional; defaults to the hostname
  hubs: [atlas, work]           # optional; serve these hubs at once
  providers:
    ollama:
      kind: ollama
      maxConcurrent: 2          # optional; default 4
      priority: 1               # optional; lower is preferred
      models:
        allow: ["qwen3*"]
    openrouter:
      kind: openrouter
      auth: apiKey
      apiKeyEnv: OPENROUTER_API_KEY
      maxConcurrent: 16
      priority: 2
    speech:                     # a local Whisper server for dictation
      kind: openai-compatible
      baseUrl: http://127.0.0.1:8000/v1
      models:
        transcription: ["whisper-1"]
```

- `enabled`: `false` makes `tap relay` refuse to start, keeping the rest of
  the block for later. Omitted means enabled.
- `name`: relay name shown in Hub. Defaults to the sanitized hostname.
- `hubs`: names from `hubs` to serve at once, each over its own connection
  with the same providers. Omitted means the one Hub login resolution picks;
  `--hub` narrows to one. Provider limits are shared across them. See
  [Several hubs](../ai-coding-agents/relay.md#several-hubs).
- `providers`: keyed by the provider name advertised to Hub.
  - `kind`: `ollama`, `openai`, `openrouter`, or `openai-compatible`. Defaults
    to the provider's key.
  - `baseUrl`: OpenAI-compatible API root. Defaults per kind (Ollama:
    `http://127.0.0.1:11434/v1`); required for `openai-compatible`.
  - `auth`: `none` or `apiKey`. Defaults per kind.
  - `apiKeyEnv`: name of the environment variable holding the API key. The
    key is read locally and never sent to Hub.
  - `maxConcurrent`: this provider's in-flight requests across all hubs,
    1–64. Defaults to 4. A full provider answers `overloaded` without
    affecting the others.
  - `priority`: this provider's rank on Hub, 1–99, lower preferred. Hub lists
    the provider's models ahead of lower-ranked ones, so chat defaults to them,
    and a pool tries the higher-ranked source first when several can serve a
    model. Omitted means unranked, after every ranked provider. Use it to
    prefer a free local provider over a paid hosted one.
  - `models.allow` / `models.deny`: glob filters over the provider's model
    list. An empty `allow` offers every model not denied.
  - `models.transcription`: model ids or globs that turn speech into text
    through the provider's `/audio/transcriptions` endpoint. Hub offers them
    for dictation, not chat. Exact ids are offered even when the provider's
    `/models` list omits them, as many speech servers do.

## Bootstrap

`tap bootstrap` writes a user `hub` and its connection definition. Deployment
options remain `--kind cloud` for Atlas or `--kind enterprise --endpoint URL`.
These bootstrap deployment options are separate from the retired Hub `kind`.
Login and bootstrap never save namespace defaults. Bootstrap uses the selected
namespace to create and save a qualified KEG reference.

Interactive bootstrap offers a KEG and Flight from the selected Hub and stores
`keg` and an optional user `flight`. Project selection and directory mappings
still apply on later invocations. With no Flight selected, MCP uses normal
identity-authorized access within its pinned Hub.

## Inspection and telemetry

`tap hub list` reads saved connection definitions locally. Remote KEG, Flight,
namespace, and identity discovery contacts only the selected Hub. Live
`tap auth status` checks that Hub; `--offline` can inspect all saved logins
without contacting any of them.

Invocation telemetry goes to `/api/v1/telemetry/invocations` on the active Hub,
using an existing login. It reports the surface, command/tool name, duration,
success, optional CLI interactivity, version, and agent identity. Arguments,
content, paths, errors, credentials, and KEG/node identifiers are not uploaded.
Reporting is best effort. Disable it with `disableTelemetry: true` or
`TAP_DISABLE_TELEMETRY=1`.
