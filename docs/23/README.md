# Tapper CLI commands (tapper-cli)

## CLI commands (tap CLI)

Recommended command set and behavior:

- `tap repo set <alias|url|path> [--persist]`

  - Default action: write to repo-local git config (`git config --local tap.keg <value>`)
  - `--persist`: write `.tapper/local.yaml` instead (visible file)
  - Validate input before writing (no creds, well-formed URL)
  - Confirm success and show effective resolution

- `tap repo unset`

  - Remove git config: `git config --local --unset tap.keg`
  - Remove `.tapper/local.yaml` if `--persist` was used or confirm before deleting

- `tap repo show`

  - Print full resolution chain for current directory showing each candidate source and which wins

- `tap repo list [--scan <dir>]`

  - List known repo→keg mappings (scan repos for `.tapper` or read a list maintained in `~/.config/tapper`)

- `tap resolve [<path>]`

  - Dry-run: show which KEG would be used for the given path and why

- `tap repo fetch <mapping>` (optional)
  - Helper to clone or fetch a remote KEG (explicit action only)

UX notes:

- The default `tap repo set` should favor `git config` unless `--persist` is passed.
- Always print which source was last updated and where the mapping is stored.
- Provide `--show-all` or `--verbose` to reveal all candidate sources.

## Security considerations

- Treat all external URLs as untrusted input (validate scheme, disallow credentials).
- Do not auto-clone remote KEGs without explicit user permission. Provide a `tap fetch` command for explicit fetch/clone.
- Limit config file permissions where appropriate (e.g., 0644 is typical; avoid storing private keys).
- Warn users in UI/docs that repo-local `.tapper/local.yaml` is ignored by default; if they commit it, it becomes project-visible.
