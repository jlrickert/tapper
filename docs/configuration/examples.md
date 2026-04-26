# Configuration Examples

## Single Laptop Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
kegMap: []
kegs: {}
defaultHub: knut
```

Use this when your local kegs live in one directory and no repo-specific overrides are needed.

## Multi-Repo Setup With `kegMap`

```yaml
# ~/.config/tapper/config.yaml
fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
  - alias: work
    pathPrefix: ~/repos/github.com/work
kegs: {}
defaultHub: knut
```

This routes different repo roots to different aliases.

## Project Override Setup

```yaml
# .tapper/config.yaml
defaultKeg: tapper
fallbackKeg: tapper
kegMap: []
kegs:
  tapper:
    file: kegs/tapper
kegSearchPaths:
  - kegs
defaultHub: knut
```

This makes the repository default to `kegs/tapper`.

## Project-Local Alias Under `kegs/<alias>`

If an alias is not explicitly configured, tapper can still resolve a project-local keg at:

```text
./kegs/<alias>/keg
```

Example:

```bash
tap info --keg tapper
```

## Hub-Oriented Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackKeg: pub
defaultHub: knut
hubs:
  - name: knut
    url: keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
kegSearchPaths:
  - ~/Documents/kegs
kegMap: []
kegs:
  pub:
    hub: knut
    user: jlrickert
    keg: public
```

Use this when aliases should resolve to API-style hub targets instead of local file paths.

## Air-Gapped / SOC2 Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackKeg: local
disableDefaultHub: true
kegSearchPaths:
  - ~/Documents/kegs
kegs:
  local:
    file: ~/Documents/kegs/local
```

Use this when the deployment must prove no implicit network calls happen.
With `disableDefaultHub: true` and no `hubs` entries, hub-dependent commands
(like `tap auth login` with no `--hub`) error with `no hub configured;
implicit default disabled` instead of silently reaching `https://keg.foldwise.ai`.
