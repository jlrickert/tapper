# tapper Documentation

Tapper helps teams build a shared organizational brain: durable project memory
for people and AI agents. Use these docs to set up Tapper, decide how to
structure KEGs, connect agents, and operate the system safely over time.

## Start Here

- New to Tapper: read the project [README](../README.md) first.
- Setting up your machine: [Configuration Overview](configuration/README.md).
- Connecting Claude Code, Codex, or another MCP host:
  [Using Tapper From AI Agents](ai-coding-agents/README.md).
- Designing a maintainable knowledge base:
  [KEG Structure Patterns](keg-structure/README.md).

## Choose Your Path

| You need to... | Read |
| --- | --- |
| Set up hosted Tapper defaults | [Configuration Overview](configuration/README.md) |
| Make a repo resolve to the right team keg | [Project Config](configuration/project-config.md) |
| Understand `@namespace/keg` resolution | [Resolution Order](configuration/resolution-order.md) |
| Connect AI agents to shared memory | [AI Coding Agents](ai-coding-agents/README.md) |
| Capture consistent notes, tags, and links | [KEG Structure Patterns](keg-structure/README.md) |
| Preserve or restore important node states | [Node Snapshots](node-snapshots.md) |
| Back up, migrate, or archive a keg | [Backups And Archives](backups-and-archives.md) |
| Debug setup or resolution failures | [Troubleshooting](configuration/troubleshooting.md) |
| Understand internals before contributing | [Architecture Overview](architecture/README.md) |
| Change Tapper itself: build, test, lint, contribute | [Development](development/README.md) |

## Core Workflows

### Bootstrap A Machine

```bash
tap bootstrap --kind cloud
tap auth login
tap keg create @alice/personal
tap use personal --user
```

For an enterprise deployment, use
`tap bootstrap --kind enterprise --endpoint <url>` instead.

### Work In A Team Keg

```bash
tap namespace create acme
tap keg create @acme/engineering
tap use @acme/engineering
tap node create
tap node search "release plan"
```

Use `tap use @namespace/keg` in a repository to set that repo's default keg in
`.tapper/config.yaml`. Use `tap use @namespace/keg --user` to set your personal
fallback keg.

### Share Access

```bash
tap namespace add-member @teammate editor --namespace acme
tap keg grant @teammate editor --keg @acme/engineering
tap keg visibility private --keg @acme/engineering
tap keg rename @acme/engineering docs
```

Namespace membership handles organization-level access. Keg grants handle
per-keg access when a domain needs a tighter boundary. Adding a member or a
grantee sends an invitation: access starts only when they accept it.

```bash
tap invitation list          # invitations addressed to you
tap invitation accept 42     # or: tap invitation decline 42
tap invitation revoke 42     # withdraw one you sent
```

### Connect An Agent

```bash
tap integrate codex
tap integrate claude
tap integrate opencode
```

Every integration exposes the Tapper MCP server plus host-specific prompts or
skills embedded in `tap`. `tap integrate HOST` is the official supported
installation and upgrade surface. Repeat `--plugin`, for example `--plugin
tapper-dev`, to add optional plugins. `--scope` defaults to `user`; Claude also
supports `project` and `local`, opencode supports `project`, and Codex is
user-only. Agents should use the `mcp__tapper__*` tools rather than reading or
writing KEG files directly.

## Command Quick Reference

### Targeting

- `--keg @namespace/name` - select a keg explicitly.
- `--namespace NAME` - resolve a bare keg name inside a namespace.
- `--hub NAME` - force the hub used for namespace resolution.
- `--flight @namespace/+slug` - apply flight context for orient/MCP.
- `--config PATH` - bypass the user/project config cascade.

### Common Node Operations

- `tap node create` - create a node.
- `tap node read NODE_ID` - display node content and metadata.
- `tap node edit NODE_ID` - edit a node.
- `tap node list` - list indexed nodes.
- `tap node search QUERY` - search node content.
- `tap tag list [EXPR]` - list tags or query tagged nodes.
- `tap node backlinks NODE_ID` - show nodes that link to a node.
- `tap node links NODE_ID` - show outgoing links from a node.

### Keg And Organization Operations

- `tap bootstrap` - create or refresh user-level setup.
- `tap use [@namespace/keg]` - set or inspect project/user keg selection.
- `tap keg create @namespace/name` - create a keg.
- `tap keg list` - list kegs visible on a hub.
- `tap keg grant|grants|revoke` - manage per-keg access.
- `tap keg visibility public|private` - set keg visibility.
- `tap keg rename @namespace/old new` - rename a keg alias in its namespace.
- `tap namespace create|list|members|add-member|set-role|remove-member` -
  manage namespaces and membership. Member management requires the owner role.
- `tap invitation list|accept|decline|revoke` - review invitations to
  namespaces and kegs; access starts only when accepted.
- `tap hub list|status|add|remove|set-default` - manage hub connections.

### Safety And Operations

- `tap snapshot create NODE_ID -m "message"` - capture a node revision.
- `tap snapshot list NODE_ID` - list node revisions.
- `tap snapshot restore NODE_ID REV --yes` - restore a revision.
- `tap archive export -o out.keg.tar.gz` - export a keg archive.
- `tap archive import out.keg.tar.gz` - import a keg archive.
- `tap keg check` - check keg health.
- `tap index rebuild` - rebuild indexes.

## Next Steps

- [Configuration Overview](configuration/README.md)
- [AI Coding Agent Configuration](ai-coding-agents/README.md)
- [KEG Structure Patterns](keg-structure/README.md)
- [Node Snapshots](node-snapshots.md)
- [Backups And Archives](backups-and-archives.md)
- [Query Expressions](query-expressions.md)
- [Output Formats](output-formats.md)
- [Troubleshooting](configuration/troubleshooting.md)
- [Architecture Overview](architecture/README.md)
