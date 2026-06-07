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

### Homebrew (macOS and Linux)

```bash
brew install jlrickert/formulae/tapper
```

Optional: install the pruned project-local binary too:

```bash
brew install jlrickert/formulae/keg
```

Shell completions for zsh, bash, and fish are installed automatically.

### From source

Prerequisite: Go `1.26.0` or newer.

```bash
go install github.com/jlrickert/tapper/cmd/tap@latest
go install github.com/jlrickert/tapper/cmd/keg@latest
```

If needed, add your Go bin directory to `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Set up shell completions manually:

```bash
# zsh (persist)
tap completion zsh > "${fpath[1]}/_tap"
```

### Precompiled binaries

Download from [GitHub Releases](https://github.com/jlrickert/tapper/releases).

Verify installation:

```bash
tap --help
```

## Quick Start

Create your first keg and start taking notes in under a minute.

**1. Initialize a keg**

```bash
tap init --keg personal --user
```

Creates a keg on your local hub at `<basePath>/@<namespace>/personal` — under
the platform user-data directory when no local hub `basePath` is configured —
and registers the alias in your user config.

> Tip: run `tap bootstrap` first to write a sensible user config (the fallback
> hub + namespace and the built-in local hub), or `tap config edit` to set
> `fallbackKeg` so later commands don't need `--keg`. Without configuration,
> tapper picks sensible platform defaults.

**2. Create a node**

```bash
tap create --keg personal
```

Opens your editor with a frontmatter template. Write your note, save, and
close. Set `fallbackKeg` in user config to skip the `--keg` flag on later
commands.

**3. View and edit a node**

```bash
tap cat 1 --keg personal
```

On a terminal this opens the node in your editor for viewing and editing.

**4. List all nodes**

```bash
tap list --keg personal
```

**5. Search**

```bash
tap grep "first" --keg personal
```

That's it — you have a working knowledge base. See [More Examples](#more-examples)
for snapshots, archives, and automation workflows.

## Using With AI Agents

Tapper ships integration bundles for Claude Code and Codex. Pick the one-command
install that matches your agent. For advanced setups — manual MCP registration,
JSON config for other hosts, per-tool keg targeting — see
[MCP Server Setup](docs/ai-coding-agents/mcp-setup.md).

### Claude Code

```bash
claude plugin marketplace add jlrickert/tapper@main
claude plugin install tapper@jlrickert-tapper
```

`claude plugin marketplace add` resolves a bare `owner/repo` against the
repository's GitHub default branch, which for tapper is `dev` (development
surface). Pin with `@main` — `main` always equals the most recent tagged
release. See [Branching Model](#branching-model). The `@jlrickert-tapper`
argument on the `install` line is a marketplace name, not a git ref — it
is unaffected by this pin.

Installs tapper as a Claude Code plugin: the MCP server registration, the
bundled `/tapper` skill, and the orientation prompts in one step. Verify with
`claude /mcp` — `tapper` should appear in the server list.

Prefer a local install without the marketplace? `tap integrate claude` writes
the same plugin tree to `~/.claude/plugins/tapper/`; preview target paths
first with `--dry-run`.

### Codex

```bash
tap integrate codex
```

Writes `~/.codex/AGENTS.md`, saved prompts under `~/.codex/prompts/`, and
`~/.codex/config-snippet.toml`. Merge the config snippet into your
`~/.codex/config.toml` to register the MCP server.

Both install paths expose the full KEG tool surface over MCP — read, write,
search, index, snapshot, lock — plus a tiered `orient` tool that lets an agent
bootstrap against any keg in a bounded token budget. See
[Using Tapper From AI Agents](docs/ai-coding-agents/README.md) for the full
reference.

## More Examples

Target a specific keg from any command:

```bash
tap --keg personal list
tap --path ~/Documents/kegs/pub snapshot history 12
```

Initialize a project-local keg:

```bash
tap init --keg tapper --project
```

Create and inspect node history:

```bash
tap snapshot create 12 --keg personal -m "before refactor"
tap snapshot history 12 --keg personal
```

Export and import a keg archive:

```bash
tap archive export --keg personal -o notes.keg.tar.gz
tap archive import notes.keg.tar.gz --keg personal
```

Archive import overwrites matching node IDs in the target keg instead of
allocating new node IDs. Snapshot history is included by default; use
`--no-history` to export only the current node state.

List the kegs on a hub, qualified as `@namespace/keg`:

```bash
tap hub list                  # kegs on the local hub
tap hub list --hub atlas      # (remote listing not yet supported)
```

Scope a session to a flight — a restriction on which kegs are available plus a
block of agent instructions:

```bash
tap flight list               # discover flights
tap flight show release-42    # inspect allowed kegs + instructions
tap --flight release-42 list  # --flight composes with --keg/--project/--path/--cwd
```

The `keg` binary provides the same commands with project-local defaults:

```bash
keg snapshot create 12 -m "before refactor"
keg archive export -o notes.keg.tar.gz
```

### Automation and scripting

Commands accept piped stdin for non-interactive use:

```bash
echo "Automated note" | tap create --keg personal
tap cat 1 --keg personal --content-only
```

When stdin is piped, no editor is launched. Use `--content-only`,
`--stats-only`, or `--meta-only` with `tap cat` to get machine-readable
output.

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
- [Flights](docs/configuration/flights.md)
- [Troubleshooting](docs/configuration/troubleshooting.md)
- [KEG Structure Patterns](docs/keg-structure/README.md)
- [Minimum Keg Node](docs/keg-structure/minimum-node.md)
- [Domain Separation And Migration](docs/keg-structure/domain-separation-and-migration.md)
- [Example Keg Structures](docs/keg-structure/example-structures.md)
- [Markdown Style Guide](docs/keg-structure/markdown-style-guide.md)
- [Using Tapper From AI Agents](docs/ai-coding-agents/README.md)
- [Claude Code Plugin](docs/ai-coding-agents/claude-code-plugin.md)
- [MCP Server Setup](docs/ai-coding-agents/mcp-setup.md)
- [Agent Conventions](docs/ai-coding-agents/agent-conventions.md)

## Config Precedence At A Glance

When no explicit keg target is provided, tapper resolves the alias in this order:

1. `defaultKeg`
2. `kegMap` path match (`pathRegex` first, then longest `pathPrefix`)
3. `fallbackKeg`

The alias is looked up in the merged `kegs` map and resolved to a target. An
omitted hub resolves `defaultHub` → `fallbackHub` → sole hub → compiled-in
`atlas`; an omitted namespace resolves per-hub `namespace` → `defaultNamespace`
→ `fallbackNamespace` → `@local`. Local kegs live at `<basePath>/@<namespace>/<name>`.

Config itself cascades: the user config is the base, every `.tapper/config.yaml`
from the workspace root up to `/` is walked and merged (deeper wins), then env
vars (`TAP_*`) and CLI flags. Only the user config may define `hubs{}` and
credentials; those are stripped from project configs. See
[Resolution Order](docs/configuration/resolution-order.md).

## Troubleshooting

For common errors such as `no keg configured`, `keg alias not found`, and discovery path
issues, see [docs/configuration/troubleshooting.md](docs/configuration/troubleshooting.md).

## Branching Model

This repo uses a `dev` (development) + `main` (release-only) split:

- `dev` — default branch. All development commits and PRs target `dev`. CI runs on
  pushes and PRs against `dev`.
- `main` — release-only. Advances only when the Release workflow runs; `main` HEAD
  always equals a tagged release.

To contribute, branch off `dev` and open PRs against `dev`. Releases are cut by
dispatching the [Release workflow](.github/workflows/release.yml), which opens a
short-lived `release/$VERSION` PR into `main`, merges it via rebase, tags the
commit, and runs goreleaser. Direct pushes to `main` are blocked by a repository
ruleset.

## Repository Layout

- `cmd/tap` - `tap` entrypoint
- `cmd/keg` - `keg` entrypoint
- `pkg/tapper` - config, resolution, and init services
- `pkg/keg` - KEG primitives and repository implementation
- `kegs/tapper` - repository KEG content
- `docs/` - end-user documentation
