# Configuration Examples

These examples use the current config shape: hubs are a name-keyed map, each
with its own default `namespace`, and local kegs live at
`<basePath>/@<namespace>/<name>`.

## Single Laptop Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: pub
kegMap: []
kegs: {}
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
kegs: {}
hubs:
  my-laptop:
    kind: local
    namespace: local
    basePath: ~/Documents/kegs
```

This routes different repo roots to different aliases.

## Project Override Setup

```yaml
# .tapper/config.yaml
defaultKeg: tapper
fallbackKeg: tapper
defaultHub: my-laptop
defaultNamespace: local
kegMap: []
kegs:
  tapper:
    hub: local
    namespace: local
    name: tapper
```

This makes the repository default to the `tapper` keg on the local hub
(`<basePath>/@local/tapper`). Hubs and credentials cannot be set here — only in
user config.

## Hub-Oriented Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: knut
fallbackNamespace: me
fallbackKeg: pub
kegMap: []
kegs:
  pub:
    hub: knut
    namespace: me
    name: public
hubs:
  knut:
    kind: remote
    namespace: me
    url: https://keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
```

Use this when aliases should resolve to API-style hub targets instead of local
file paths. Because the `knut` hub sets `namespace: me`, references against it
omit their namespace and still resolve under `@me`.

## Air-Gapped / SOC2 Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackHub: my-laptop
fallbackNamespace: local
fallbackKeg: local
disableDefaultHub: true
kegs:
  local:
    hub: my-laptop
    namespace: local
    name: local
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

Bootstrap writes the fallback slots and the built-in local hub (keyed by the
machine hostname); see [User Config](user-config.md#tap-bootstrap).
