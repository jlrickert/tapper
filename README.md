# tapper

`tapper` is a CLI for building knowledge systems with KEGs (Knowledge Exchange
Graphs), including personal knowledge management and agent memory workflows across
domains.

Primary entrypoint:

- `tap` for the full CLI surface

Optional secondary entrypoint:

- `keg` as a pruned, project-focused profile built from the same command system

## Problem This Solves

As notes grow across projects, domains, and tools, context gets fragmented:

- important details are buried in disconnected files
- links between ideas, plans, patches, releases, and people are hard to track
- humans and agents cannot reliably reuse the same memory and structure

`tapper` solves this by storing notes as linked KEG nodes with structured metadata,
predictable config resolution, and CLI workflows for creating, navigating, and
maintaining shared memory.

## Installation

Prerequisite: Go `1.26.0` or newer.

Recommendation: install using the newest release tag (currently `v0.2.0`).

Install binaries from the newest tag:

```bash
go install github.com/jlrickert/tapper/cmd/tap@v0.2.0
go install github.com/jlrickert/tapper/cmd/keg@v0.2.0
```

Precompiled binaries are also published on GitHub Releases:
<https://github.com/jlrickert/tapper/releases> (use the newest tag).

If needed, add your Go bin directory to `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Verify installation:

```bash
tap --help
```

Optional: verify the pruned project-focused binary too:

```bash
keg --help
```

Set up shell completions for `tap`:

```bash
# zsh (current session)
source <(tap completion zsh)

# zsh (persist)
tap completion zsh > "${fpath[1]}/_tap"
```

Optional: install completions for `keg` if you use the pruned binary:

```bash
# zsh (current session)
source <(keg completion zsh)

# zsh (persist)
keg completion zsh > "${fpath[1]}/_keg"
```

If new commands do not appear in tab completion after an upgrade, regenerate the
completion files and reload your shell.

## Quick Start

Run the CLI:

```bash
tap --help
```

Target a keg from the root command:

```bash
tap --keg personal list
tap --path ~/Documents/kegs/pub snapshot history 12
```

Initialize a project-local keg:

```bash
tap repo init --keg tapper --project
```

Use the current working directory instead of git root:

```bash
tap repo init --keg tapper --cwd
tap repo init --keg tapper --path .
```

Create and inspect node history with `tap`:

```bash
tap snapshot create 12 --keg personal -m "before refactor"
tap snapshot history 12 --keg personal
```

Use `keg` only when you specifically want the pruned project-local profile:

```bash
keg snapshot create 12 -m "before refactor"
keg snapshot history 12
```

`tap` is the main command. `keg` exists to prove that the same Cobra tree can
be exposed through a narrower profile with project-local defaults.

```bash
tap snapshot create 12 --keg personal -m "before refactor"
tap snapshot history 12 --keg personal
tap archive export --keg personal -o notes.keg.tar.gz
```

Export and import a keg archive:

```bash
keg archive export -o notes.keg.tar.gz
keg archive import notes.keg.tar.gz
```

Primary `tap` workflow:

```bash
tap archive export --keg personal -o notes.keg.tar.gz
tap archive import notes.keg.tar.gz --keg personal
```

Project-local `keg` workflow:

```bash
keg archive export -o notes.keg.tar.gz
keg archive import notes.keg.tar.gz
```

Archive import overwrites matching node IDs in the target keg instead of
allocating new node IDs.

Snapshot history is included in archives by default. Use `--no-history` when
you want to export only the current node state.

Show merged repo configuration:

```bash
tap repo config
```

## Configuration Quick Map

- User config: `~/.config/tapper/config.yaml`
- Project config: `.tapper/config.yaml`
- Keg config: `<keg-root>/keg`

## Documentation

Project docs live under `docs/`:

- [Documentation Home](docs/README.md)
- [Configuration Overview](docs/configuration/README.md)
- [Architecture Overview](docs/architecture/README.md)
- [CLI And Command Flow](docs/architecture/cli-and-command-flow.md)
- [Service Layer](docs/architecture/service-layer.md)
- [Repository Layer](docs/architecture/repository-layer.md)
- [Testing Architecture](docs/architecture/testing-architecture.md)
- [User Config](docs/configuration/user-config.md)
- [Project Config](docs/configuration/project-config.md)
- [Keg Config](docs/configuration/keg-config.md)
- [Resolution Order](docs/configuration/resolution-order.md)
- [Configuration Examples](docs/configuration/examples.md)
- [Troubleshooting](docs/configuration/troubleshooting.md)
- [KEG Structure Patterns](docs/keg-structure/README.md)
- [Minimum Keg Node](docs/keg-structure/minimum-node.md)
- [Entity And Tag Patterns](docs/keg-structure/entity-and-tag-patterns.md)
- [Domain Separation And Migration](docs/keg-structure/domain-separation-and-migration.md)
- [Example Keg Structures](docs/keg-structure/example-structures.md)
- [Markdown Style Guide](docs/keg-structure/markdown-style-guide.md)

## Config Precedence At A Glance

When no explicit keg target is provided, tapper resolves in this order:

1. `defaultKeg`
2. `kegMap` path match (`pathRegex` first, then longest `pathPrefix`)
3. `fallbackKeg`

Alias lookup then prefers explicit `kegs` entries, then discovered aliases from
`kegSearchPaths`, then project-local alias fallback at `./kegs/<alias>`.

## Troubleshooting

For common errors such as `no keg configured`, `keg alias not found`, and discovery path
issues, see [docs/configuration/troubleshooting.md](docs/configuration/troubleshooting.md).

## Repository Layout

- `cmd/tap` - `tap` entrypoint
- `cmd/keg` - `keg` entrypoint
- `pkg/tapper` - config, resolution, and init services
- `pkg/keg` - KEG primitives and repository implementation
- `kegs/tapper` - repository KEG content
- `docs/` - end-user documentation
