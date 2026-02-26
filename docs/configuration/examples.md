# Configuration Examples

## Single Laptop Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
kegMap: []
kegs: {}
defaultRegistry: knut
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
  - alias: ecw
    pathPrefix: ~/repos/bitbucket.org
kegs: {}
defaultRegistry: knut
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
defaultRegistry: knut
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

## Registry-Oriented Setup

```yaml
# ~/.config/tapper/config.yaml
fallbackKeg: pub
defaultRegistry: knut
registries:
  - name: knut
    url: keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
kegSearchPaths:
  - ~/Documents/kegs
kegMap: []
kegs:
  pub:
    api: keg:knut:@jlrickert/public
```

Use this when aliases should resolve to API/registry targets instead of local file paths.
