# Sandbox keg fixtures

Each subdirectory here is a complete keg tree that can be copied into a
running sandbox via `task sandbox:populate -- <name>`. Fixtures land at
`~/.local/share/tapper/kegs/<name>/` inside the container.

## Adding a fixture

1. Create a directory under `test-env/fixtures/<name>/`.
2. Populate it with a valid keg tree:
   - `keg` config file (kegv: 2023-01)
   - `0/README.md` for the zero node
   - additional numbered nodes as desired
3. Run `task sandbox:populate -- --list` to confirm it appears.

The fixture format mirrors the on-disk keg layout. Tapper auto-derives
`meta.yaml` and `stats.json` for nodes that don't include them, so a
hand-authored fixture only strictly needs the `keg` file plus
`<id>/README.md` per node.
