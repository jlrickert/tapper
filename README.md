# tapper

Tapper is a CLI and MCP server for building a centralized brain across an
organization. It gives teams and AI agents one durable place to store project
context, decisions, handoffs, plans, incidents, links, and working knowledge.

The core unit is a KEG: a Knowledge Exchange Graph made of markdown nodes with
metadata, links, tags, search indexes, and snapshot history. Humans can use it
from the terminal. Agents can use the same memory through MCP.

## Problems Tapper Solves

Organizations lose context in familiar ways:

- new teammates spend days reconstructing project history
- release decisions live in chat threads, tickets, and private notes
- AI agents start each session without the background the last session learned
- cross-team handoffs depend on whoever remembers the details
- documentation explains the code, but not why the system is shaped that way

Tapper turns that scattered memory into a queryable, linked, auditable knowledge
base. A team can capture a decision once, link it to the project, tag the domain,
snapshot important edits, and let both people and agents retrieve it later.

## What You Can Store

Use Tapper for the context that should survive beyond a single conversation or
ticket:

- architecture decisions and tradeoffs
- project onboarding notes
- release plans and post-release findings
- customer, domain, or operational knowledge
- runbooks, debugging notes, and incident timelines
- agent instructions and memory for long-running work
- links between people, systems, repos, features, and decisions

Tapper is not a replacement for source control, issue trackers, or docs sites. It
connects the memory around them.

## Install

### Homebrew

```bash
brew install jlrickert/formulae/tapper
```

Optional project-local profile:

```bash
brew install jlrickert/formulae/keg
```

### From Source

Prerequisite: Go `1.26.0` or newer.

```bash
go install github.com/jlrickert/tapper/cmd/tap@latest
go install github.com/jlrickert/tapper/cmd/keg@latest
```

If needed, add your Go bin directory to `PATH`:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

### Precompiled Binaries

Download from [GitHub Releases](https://github.com/jlrickert/tapper/releases).

Verify installation:

```bash
tap --help
```

## Start On One Machine

This path creates a local knowledge base and makes plain `tap` commands resolve
to it.

```bash
tap bootstrap --kind local --default-keg @local/personal
tap keg create @local/personal
tap use @local/personal --user
```

Create your first memory:

```bash
tap create
```

Write a short note in your editor, for example a project decision or onboarding
fact. Then read, list, and search it:

```bash
tap list
tap cat 1
tap grep "decision"
```

The important thing is the workflow: capture context close to the work, then
retrieve it by ID, link, tag, or search when a human or agent needs it later.

## Set Up A Shared Organization Brain

For a team or company, use a shared hub and namespace so everyone works against
the same organizational memory.

```bash
tap bootstrap --kind cloud
tap auth login
tap namespace create acme
tap keg create @acme/engineering
tap use @acme/engineering
```

Add teammates to the namespace or grant access to a specific keg:

```bash
tap namespace add-member @teammate editor --namespace acme
tap keg grant @teammate editor --keg @acme/engineering
tap keg rename @acme/engineering docs
```

Typical team layout:

- `@acme/engineering` for architecture, releases, incidents, and repo context
- `@acme/product` for product decisions, customer knowledge, and roadmap context
- `@acme/ops` for runbooks, operational notes, and incident follow-up

Use separate kegs when access boundaries or domains differ. Use links when one
domain needs to point at another.

## Connect AI Agents

Tapper exposes the same memory to AI agents through MCP. The agent can search,
read, create, edit, snapshot, and orient itself against a keg without scraping
files directly.

### Codex and Claude Code

```bash
tap integrate codex
tap integrate claude
```

`tap integrate HOST` is the official supported installation, refresh, upgrade,
plugin-selection, and scope surface. Each command extracts the host-native
marketplace embedded in `tap`, registers the local source, and installs the
baseline `tapper` plugin. The `tap` executable on `PATH` must be current enough
to provide the Go-backed `tap hook` commands used by the plugin. No GitHub
checkout or manual MCP configuration is required. Repeat `--plugin` to add
optional plugins, for example `tap integrate claude --plugin tapper-dev`.
Requested plugins keep their order and duplicates are ignored. Activation
defaults to `--scope user`; Claude also supports `project` (shared project
settings) and `local` (gitignored project settings), while Codex currently
supports only `user`. Use `--dry-run` to inspect every extraction path and host
command without side effects.

Refreshing is atomic and removes legacy packaged Python hooks. Review and trust
the replacement hooks again, then start a fresh Codex thread or Claude session
so the new hook commands and MCP connection take effect. Manual MCP setup is
reserved for generic hosts without a native Tapper plugin.

See [Using Tapper From AI Agents](docs/ai-coding-agents/README.md) for setup,
agent conventions, flight-first orientation, and manual MCP configuration.

## The Daily Workflow

Capture something worth remembering:

```bash
tap create --keg @acme/engineering
```

Find related context:

```bash
tap grep "billing migration" --keg @acme/engineering
tap tags "release and payments" --keg @acme/engineering
tap backlinks 42 --keg @acme/engineering
```

Preserve an important state before changing it:

```bash
tap snapshot create 42 --keg @acme/engineering -m "before release edits"
tap snapshot history 42 --keg @acme/engineering
```

Move a repository or session onto the right shared memory:

```bash
tap use @acme/engineering
tap use @acme/engineering --flight @acme/+release-42
```

Export a keg for backup or migration:

```bash
tap archive export --keg @acme/engineering -o engineering.keg.tar.gz
tap archive import engineering.keg.tar.gz --keg @acme/engineering
```

## Concepts

- **KEG**: a Knowledge Exchange Graph, usually scoped to a team, project, or
  domain.
- **Node**: one markdown memory entry with metadata, tags, links, stats, and
  optional files/images.
- **Hub**: where kegs live. A hub can be local, hosted, or enterprise.
- **Namespace**: the owner or organization segment in references such as
  `@acme/engineering`.
- **Flight**: task context that can cap kegs for MCP sessions and inject
  agent instructions for a task.
- **MCP**: the protocol Tapper uses to expose KEG operations to AI agents.

## Documentation

Start here:

- [Documentation Home](docs/README.md)
- [Configuration Overview](docs/configuration/README.md)
- [Using Tapper From AI Agents](docs/ai-coding-agents/README.md)
- [KEG Structure Patterns](docs/keg-structure/README.md)
- [Backups And Archives](docs/backups-and-archives.md)
- [Node Snapshots](docs/node-snapshots.md)
- [Query Expressions](docs/query-expressions.md)

For contributors and internals:

- [Architecture Overview](docs/architecture/README.md)
- [CLI And Command Flow](docs/architecture/cli-and-command-flow.md)
- [Service Layer](docs/architecture/service-layer.md)
- [Repository Layer](docs/architecture/repository-layer.md)
- [Testing Architecture](docs/architecture/testing-architecture.md)

## Configuration At A Glance

Tapper resolves a keg reference through this chain:

1. explicit CLI flags such as `--keg`, `--namespace`, and `--hub`
2. project config in `.tapper/config.yaml`
3. user config in `~/.config/tapper/config.yaml`
4. built-in defaults, unless disabled

A keg reference is usually a bare name or `@namespace/name`. The namespace
selects the hub, and the hub selects the backend. Local kegs live at
`<basePath>/@<namespace>/<name>`.

Only user config may define hubs and credentials. Project config can select
defaults for a repository, but it cannot introduce a new hub target or token.
See [Resolution Order](docs/configuration/resolution-order.md).

`--flight` is a task context for orientation and MCP sessions.
Direct CLI commands still use normal keg authorization and do not have their
access reduced by flight cover caps.

Persist a project flight for newly opened agent sessions with either `tap use
--flight @namespace/+slug` or the default-namespace shorthand `tap use +slug`.
Local MCP connections pin that flight at connection time; MCP tool calls cannot
override it. Claude users can switch one live connection through the bundled
human-only command, while Codex users change the project flight and reconnect.

## Release And Contribution Notes

`main` is the GitHub default branch and the release branch. The `next` branch is
used for upcoming release work when active. Use the repository's current branch
or maintainer guidance when opening a PR.

Releases are cut by dispatching the
[Release workflow](.github/workflows/release.yml), which writes the changelog
commit and tag, then runs GoReleaser.

## Repository Layout

- `cmd/tap` - full CLI entrypoint
- `cmd/keg` - project-local CLI profile
- `pkg/tapper` - config, resolution, hub, and service layer
- `pkg/keg` - KEG primitives and repository implementation
- `pkg/mcp` - MCP server and tool surface
- `docs/` - user and contributor documentation
