# Configuration Examples

These examples use the current config shape: hubs are a name-keyed map, each
with its own default `namespace`, and local kegs live at
`<basePath>/@<namespace>/<name>`. A keg is named by reference — a bare name, an
`@namespace/name` reference, or a path — there is no `kegs` alias map.

## Single Laptop Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: pub
kegMap: []
hubs:
  my-laptop:
    kind: local
    namespace: local
    basePath: ~/Documents/kegs
```

Use this when your local kegs live in one directory and no repo-specific
overrides are needed. A keg named `pub` resolves to
`~/Documents/kegs/@local/pub`.

## Multi-Repo Setup With `kegMap`

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: pub
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
  - alias: work
    pathPrefix: ~/repos/github.com/work
hubs:
  my-laptop:
    kind: local
    namespace: local
    basePath: ~/Documents/kegs
```

This routes different repo roots to different kegs. Each `alias` is a keg
reference (here the bare names `pub` and `work`, which resolve to
`@local/pub` and `@local/work`).

## Project Override Setup

```yaml
# .tapper/config.yaml
defaultKeg: tapper
fallbackKeg: tapper
defaultHub: my-laptop
defaultNamespace: local
kegMap: []
```

This makes the repository default to the `tapper` keg on the local hub
(`<basePath>/@local/tapper`): `defaultKeg: tapper` resolves its namespace from
`defaultNamespace: local`, and the local namespace selects the local hub. Hubs
and credentials cannot be set here — only in user config.

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
    namespace: me
    url: https://keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
```

Use this when references should resolve to API-style hub targets instead of
local file paths. `fallbackKeg: public` resolves its namespace from
`fallbackNamespace: me` and its hub from that namespace, yielding
`keg:@me/public` on the `knut` hub.

## Air-Gapped / SOC2 Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: local
disableDefaultHub: true
hubs:
  my-laptop:
    kind: local
    namespace: local
    basePath: ~/Documents/kegs
```

Use this when the deployment must prove no implicit network calls happen. With
`disableDefaultHub: true` and no remote `hubs` entries, hub-dependent commands
error with `no hub configured; implicit default disabled` instead of silently
reaching `https://atlas.foldwise.ai`.

## Generating A Config

Rather than write any of the above by hand, run `tap bootstrap`:

```bash
tap bootstrap                          # cloud (atlas) — the default
tap bootstrap --kind local             # local hub only
tap bootstrap --kind enterprise --endpoint keg.acme.com
```

Bootstrap writes `fallbackHub`, the built-in local hub (keyed by the machine
hostname), and the `local → <local hub>` namespace mapping, then asks for a
default keg and records it as `fallbackKeg` so plain `tap` commands resolve one
immediately (a project's `defaultKeg` or `kegMap` still overrides it). It does
not write a global `fallbackNamespace`: the namespace comes from the resolved
hub's own `namespace` field (adopted from the hub at login for cloud/enterprise).
See [User Config](user-config.md#tap-bootstrap).
