# User Config

User config defines machine-wide defaults for tapper.

## Purpose And File Location

- File: `~/.config/tapper/config.yaml`
- Scope: your current user account

## View And Edit

```bash
tap repo config --user
tap repo config edit --user
tap repo config --template --user
```

## Key Reference

- `fallbackKeg`: last-resort alias when no default/map match resolves
- `defaultKeg`: optional alias used first when no keg flag is provided
- `kegSearchPaths`: ordered directories scanned for discovered file-backed kegs
- `kegs`: explicit alias-to-target map
- `kegMap`: path-based alias mapping (`pathRegex` first, then longest `pathPrefix`)
- `defaultRegistry`: default registry name for registry/API style targets
- `registries`: registry definitions (name, url, token/tokenEnv)

## Recommended Baseline Config

```yaml
fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs: {}
defaultRegistry: knut
registries:
  - name: knut
    url: keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
```

## Common Mistakes

- Empty `kegSearchPaths`: discovered local aliases will not resolve.
- Alias mismatch: `defaultKeg`, `fallbackKeg`, or `kegMap.alias` points to an alias that does
  not exist in `kegs` and is not discoverable from `kegSearchPaths`.
- Missing fallback: no `defaultKeg` plus no `fallbackKeg` can produce `no keg configured`.
