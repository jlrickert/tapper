# Configuration Examples

These examples use the current remote-only config shape. Hubs are a name-keyed
map, each with its own `defaultNamespace`. A KEG is named by a bare name or an
`@namespace/name` reference; there is no `kegs` alias map.

## Hosted Cloud Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: atlas
fallbackNamespace: me
fallbackKeg: pub
kegMap: []
hubs:
  atlas:
    kind: remote
    defaultNamespace: me
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
```

Bootstrap normally writes this shape and adopts the authenticated user's home
namespace from the Hub.

## Multi-Repo Setup With `kegMap`

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: atlas
fallbackNamespace: me
fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
  - alias: work
    pathPrefix: ~/repos/github.com/work
hubs:
  atlas:
    kind: remote
    defaultNamespace: me
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
```

This routes different repo roots to different kegs. Each `alias` is a keg
reference (here the bare names `pub` and `work`, which select remote KEGs based
on workspace path).

## Project Override Setup

```yaml
# .tapper/config.yaml
defaultKeg: tapper
fallbackKeg: tapper
defaultHub: atlas
defaultNamespace: acme
kegMap: []
```

This makes the repository default to `keg:@acme/tapper` on the configured
`atlas` Hub. Hubs and credentials cannot be set here — only in user config.

## Hub-Oriented Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: knut
fallbackNamespace: me
fallbackKeg: public
kegMap: []
hubs:
  knut:
    kind: remote
    defaultNamespace: me
    url: https://keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
```

Use this for an enterprise Hub. `fallbackKeg: public` resolves its namespace from
`fallbackNamespace: me` and its hub from that namespace, yielding
`keg:@me/public` on the `knut` hub.

## Air-Gapped / SOC2 Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: enterprise
fallbackNamespace: acme
fallbackKeg: private
disableAtlasHub: true
hubs:
  enterprise:
    kind: remote
    defaultNamespace: acme
    url: https://tapper.acme.internal
    tokenEnv: TAPPER_ENTERPRISE_TOKEN
```

Use this when the deployment must prove Tapper never contacts the compiled-in
Atlas endpoint. With `disableAtlasHub: true`, resolution stays on explicitly
configured enterprise Hubs.

## Generating A Config

Rather than write any of the above by hand, run `tap bootstrap`:

```bash
tap bootstrap                          # cloud (atlas) — the default
tap bootstrap --kind enterprise --endpoint keg.acme.com
```

Bootstrap writes `fallbackHub`, then asks for a default KEG and records it as
`fallbackKeg` so plain `tap` commands resolve one immediately (a project's
`defaultKeg` or `kegMap` still overrides it). The namespace comes from the
resolved Hub's own `defaultNamespace`, adopted from whoami at login.
See [User Config](user-config.md#tap-bootstrap).
