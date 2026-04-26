# User Config

User config defines machine-wide defaults for tapper.

## Purpose And File Location

- File: `~/.config/tapper/config.yaml`
- Scope: your current user account

## View And Edit

```bash
tap repo config --user
tap repo config edit --user
tap repo config template user
cat config.yaml | tap repo config edit --user
```

## Key Reference

- `fallbackKeg`: last-resort alias when no default/map match resolves
- `defaultKeg`: optional alias used first when no keg flag is provided
- `kegSearchPaths`: ordered directories scanned for discovered file-backed kegs
- `kegs`: explicit alias-to-target map
- `kegMap`: path-based alias mapping (`pathRegex` first, then longest `pathPrefix`)
- `defaultHub`: name of the default entry in `hubs` used by `tap auth login`
  and API-style targets when no hub is specified explicitly
- `disableDefaultHub`: when `true`, suppress the compiled-in `DefaultHubURL`
  fallback (`https://keg.foldwise.ai`) — hub-dependent commands fail with a
  clear error if no other hub is configured. Useful for SOC2-audited
  deployments that need to prove no implicit network targets exist.
- `hubs`: list of named hub definitions (name, url, token/tokenEnv)

## Hub Resolution Chain

`tap auth login` and other hub-dependent commands resolve the target hub
in this order, stopping at the first match:

1. Explicit `--hub URL` flag → use it (canonicalized)
2. `defaultHub: NAME` → look up `NAME` in `hubs` and use its URL
3. Exactly one entry in `hubs` → use it
4. `disableDefaultHub: true` (or `TAP_DISABLE_DEFAULT_HUB=1`) → error
5. Fall back to the compiled-in `DefaultHubURL` (`https://keg.foldwise.ai`)

## Recommended Baseline Config

```yaml
fallbackKeg: pub
kegSearchPaths:
  - ~/Documents/kegs
kegMap:
  - alias: pub
    pathPrefix: ~/repos/github.com
kegs: {}
defaultHub: knut
hubs:
  - name: knut
    url: keg.jlrickert.me
    tokenEnv: KNUT_API_KEY
```

## Common Mistakes

- Empty `kegSearchPaths`: discovered local aliases will not resolve.
- Alias mismatch: `defaultKeg`, `fallbackKeg`, or `kegMap.alias` points to an alias that does
  not exist in `kegs` and is not discoverable from `kegSearchPaths`.
- Missing fallback: no `defaultKeg` plus no `fallbackKeg` can produce `no keg configured`.
