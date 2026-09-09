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
  bare name whose namespace comes from the namespace defaults.
- `kegMap`: startup-directory rules containing `keg`, optional `hub`, and
  `pathPrefix` or `pathRegex`. `alias` is accepted as a spelling of `keg`;
  `keg` wins when both occur.
- `hubs`: named connections with `url`, optional `defaultNamespace`, and
  `tokenEnv` or `token`. Login credentials can also come from the AuthStore.
- `defaultNamespace` and `fallbackNamespace`: namespace defaults for bare names.
- `flight`: baseline Flight context; project config, `TAP_FLIGHT`, and
  `--flight` can override it.
- `agent`: model and telemetry identity, independent of Flight selection.
- `disableAtlasHub`: disables the implicit Atlas fallback. Explicit saved
  connections, including Atlas, remain available.
- `disableTelemetry`: disables invocation reporting.

The environment selections are `TAP_HUB` and `TAP_KEG`. Retired default/fallback
Hub and KEG keys, their environment overrides, namespace-to-Hub mappings, and
Hub `kind` no longer affect selection. Unknown and retired YAML and comments
survive configuration rewrites. Malformed YAML fails. Invalid unused Hub and
mapping entries do not prevent unrelated operations; selected invalid values
fail clearly.

## Bootstrap

`tap bootstrap` writes a user `hub` and its connection definition. Deployment
options remain `--kind cloud` for Atlas or `--kind enterprise --endpoint URL`.
These bootstrap deployment options are separate from the retired Hub `kind`.
The Hub's `defaultNamespace` is populated from login's identity probe.

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
