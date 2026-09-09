# Configuration Examples

Save connections and credentials in `~/.config/tapper/config.yaml`:

```yaml
hub: atlas
keg: "@foldwise/dev"
hubs:
  atlas:
    url: https://atlas.foldwise.ai
    tokenEnv: ATLAS_API_KEY
  homelab:
    url: https://hub.example.test
    defaultNamespace: homelab
kegMap:
  - pathPrefix: ~/repos/homelab
    hub: homelab
    keg: "@homelab/dev"
```

Outside the mapped directory, commands use `@foldwise/dev` on Atlas. Inside
it, they use `@homelab/dev` on homelab. Only that Hub is contacted.

A project can pin a saved Hub:

```yaml
# .tapper/config.yaml
hub: atlas
keg: "@foldwise/engineering"
```

If a directory mapping matches, its KEG still wins; this project's Hub wins
over the mapping's Hub. Use `--keg` or `TAP_KEG` to override the KEG and
`--hub` or `TAP_HUB` to override the Hub.

To suppress implicit Atlas fallback, set `disableAtlasHub: true`. Explicitly
configured connections remain available, including an explicit Atlas entry.

Both `tap use @team/notes` and `tap use @team/notes --user` write `keg`, in the
project and user scope respectively. A matching mapping continues to override
that top-level selection. See [Resolution Order](resolution-order.md).
