# tapper

`tapper` is a CLI for building knowledge systems with KEGs (Knowledge Exchange
Graphs), including personal knowledge management and agent memory workflows across
domains. It provides two entrypoints:

- `tap` for repo, config, and node workflows
- `kegv2` for project-keg focused workflows

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
go install github.com/jlrickert/tapper/cmd/kegv2@v0.2.0
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
kegv2 --help
```

Set up shell completions:

```bash
# zsh (current session)
source <(tap completion zsh)
source <(kegv2 completion zsh)

# zsh (persist)
tap completion zsh > "${fpath[1]}/_tap"
kegv2 completion zsh > "${fpath[1]}/_kegv2"
```

## Quick Start

Run the CLI:

```bash
tap --help
```

Initialize a project-local keg:

```bash
tap repo init tapper --project
```

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
- `cmd/kegv2` - `kegv2` entrypoint
- `pkg/tapper` - config, resolution, and init services
- `pkg/keg` - KEG primitives and repository implementation
- `kegs/tapper` - repository KEG content
- `docs/` - end-user documentation
