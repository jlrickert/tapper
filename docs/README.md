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
| Set up local or hosted Tapper defaults | [Configuration Overview](configuration/README.md) |
| Make a repo resolve to the right team keg | [Project Config](configuration/project-config.md) |
| Understand `@namespace/keg` resolution | [Resolution Order](configuration/resolution-order.md) |
| Connect AI agents to shared memory | [AI Coding Agents](ai-coding-agents/README.md) |
| Capture consistent notes, tags, and links | [KEG Structure Patterns](keg-structure/README.md) |
| Preserve or restore important node states | [Node Snapshots](node-snapshots.md) |
| Back up, migrate, or archive a keg | [Backups And Archives](backups-and-archives.md) |
| Debug setup or resolution failures | [Troubleshooting](configuration/troubleshooting.md) |
| Understand internals before contributing | [Architecture Overview](architecture/README.md) |

## Core Workflows

### Bootstrap A Machine

```bash
tap bootstrap --kind local --default-keg @local/personal
tap keg create @local/personal
tap use @local/personal --user
```

For hosted or enterprise deployments, use `tap bootstrap --kind cloud` or
`tap bootstrap --kind enterprise --endpoint <url>`, then `tap auth login`.

### Work In A Team Keg

```bash
tap namespace create acme
tap keg create @acme/engineering
tap use @acme/engineering
tap create
tap grep "release plan"
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
per-keg access when a domain needs a tighter boundary.

### Connect An Agent

```bash
tap integrate codex
tap integrate claude
```

Both integrations expose the Tapper MCP server plus host-specific prompts or
skills. Agents should use the `mcp__tapper__*` tools rather than reading or
writing KEG files directly.

## Command Quick Reference

### Targeting

- `--keg @namespace/name` - select a keg explicitly.
- `--namespace NAME` - resolve a bare keg name inside a namespace.
- `--hub NAME` - force the hub used for namespace resolution.
- `--flight @namespace/+slug` - apply a task-scoped overlay.
- `--config PATH` - bypass the user/project config cascade.

### Common Node Operations

- `tap create` - create a node.
- `tap cat NODE_ID` - display node content and metadata.
- `tap edit NODE_ID` - edit a node.
- `tap list` - list indexed nodes.
- `tap grep QUERY` - search node content.
- `tap tags [EXPR]` - list tags or query tagged nodes.
- `tap backlinks NODE_ID` - show nodes that link to a node.
- `tap links NODE_ID` - show outgoing links from a node.

### Keg And Organization Operations

- `tap bootstrap` - create or refresh user-level setup.
- `tap use [@namespace/keg]` - set or inspect project/user keg selection.
- `tap keg create @namespace/name` - create a keg.
- `tap keg list` - list kegs visible on a hub.
- `tap keg grant|grants|revoke` - manage per-keg access.
- `tap keg visibility public|private` - set keg visibility.
- `tap keg rename @namespace/old new` - rename a keg alias in its namespace.
- `tap namespace create|list|members|add-member|set-role|remove-member` -
  manage namespaces and membership.
- `tap hub list|status|add|remove|set-default` - manage hub connections.

### Safety And Operations

- `tap snapshot create NODE_ID -m "message"` - capture a node revision.
- `tap snapshot history NODE_ID` - list node revisions.
- `tap snapshot restore NODE_ID REV --yes` - restore a revision.
- `tap archive export -o out.keg.tar.gz` - export a keg archive.
- `tap archive import out.keg.tar.gz` - import a keg archive.
- `tap doctor` - check keg health.
- `tap index rebuild` - rebuild indexes.

## Next Steps

- [Configuration Overview](configuration/README.md)
- [AI Coding Agent Configuration](ai-coding-agents/README.md)
- [KEG Structure Patterns](keg-structure/README.md)
- [Node Snapshots](node-snapshots.md)
- [Backups And Archives](backups-and-archives.md)
- [Query Expressions](query-expressions.md)
- [Troubleshooting](configuration/troubleshooting.md)
- [Architecture Overview](architecture/README.md)
