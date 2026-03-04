# tapper Documentation

This documentation is for end users and contributors working with tapper.

`tap` is the primary CLI. `kegv2` is a narrower project-focused profile built
from the same command tree.

## Audience And Goals

Use these docs if you need to:

- set up tapper on your machine
- configure project-level defaults for a repository
- inspect or edit a keg's own configuration
- troubleshoot keg resolution behavior
- design sustainable KEG structure patterns

## Choose Your Path

- I want to set up my machine: [User Config](configuration/user-config.md)
- I want this repo to have defaults: [Project Config](configuration/project-config.md)
- I want to inspect or edit a keg: [Keg Config](configuration/keg-config.md)
- I want to design entities/tags and node conventions:
  [KEG Structure Patterns](keg-structure/README.md)
- I want to understand node revision history:
  [Node Snapshots](node-snapshots.md)
- I want to understand internals and code flow:
  [Architecture Overview](architecture/README.md)
- I want to configure AI coding agents (Claude Code, Codex):
  [AI Coding Agent Configuration](ai-coding-agents/README.md)

If you are unsure where to start, read [Configuration Overview](configuration/README.md).

## Command Quick Reference

### Global keg targeting flags (mutually exclusive)

- `--keg ALIAS` — target a keg by alias
- `--project` — target the project-local keg
- `--cwd` — target the keg in the current working directory
- `--path PATH` — target a keg by filesystem path

### Node operations

- `tap cat NODE_ID` — print node content
- `tap create` — create a new node (reads stdin)
- `tap edit NODE_ID` — replace node content (reads stdin)
- `tap meta NODE_ID` — show or replace node metadata (reads stdin)
- `tap stats NODE_ID` — show node statistics
- `tap rm NODE_ID` — remove a node
- `tap mv SRC DST` — move/renumber a node
- `tap list` — list all nodes (supports [`--query`](query-expressions.md))
- `tap grep QUERY` — search node content
- `tap tags [EXPR]` — list tags or nodes matching a tag expression (supports [`--query`](query-expressions.md))
- `tap backlinks NODE_ID` — show nodes linking to a given node

### Keg operations

- `tap dir [NODE_ID]` — print keg or node directory path
- `tap index` — rebuild keg indices
- `tap reindex` — full reindex of all nodes
- `tap info` — show keg diagnostics
- `tap config` — show active keg config
- `tap config edit` — edit active keg config (reads stdin)
- `tap graph` — output keg link graph
- `tap import FILE` — import nodes from a file

### Attachments

- `tap file ls|upload|download|rm` — manage node file attachments
- `tap image ls|upload|download|rm` — manage node image attachments

### Snapshots and archives

- `tap snapshot create NODE_ID -m "message"` — capture a node snapshot
- `tap snapshot history NODE_ID` — list node snapshot history
- `tap snapshot restore NODE_ID REV --yes` — restore a node snapshot
- `tap archive export -o out.keg.tar.gz` — export a keg archive
- `tap archive import out.keg.tar.gz` — import a keg archive

Snapshot history is included in archives by default. Use `--no-history` to omit
it.

### Repository management

- `tap repo init [--keg ALIAS]` — initialize a keg with repo config
- `tap repo rm ALIAS` — remove a keg alias
- `tap repo list` — list configured keg aliases
- `tap repo config` — show merged repo config
- `tap repo config --user|--project` — show user or project config
- `tap repo config edit --user|--project` — edit user or project config (reads stdin)
- `tap repo config template user|project` — print starter config templates

Use the project-local profile when you want that narrowed workflow:
`kegv2 snapshot|archive ...`

## Common Scenarios

- Single-user local setup: [Configuration Examples](configuration/examples.md#single-laptop-setup)
- Team/project override setup:
  [Configuration Examples](configuration/examples.md#project-override-setup)
- Resolution and precedence details:
  [Resolution Order](configuration/resolution-order.md)
- Structuring entities/tags for long-lived notes:
  [Entity And Tag Patterns](keg-structure/entity-and-tag-patterns.md)
- Starting layout examples by domain:
  [Example Keg Structures](keg-structure/example-structures.md)
- Writing consistent KEG note markdown:
  [Markdown Style Guide](keg-structure/markdown-style-guide.md)
- Understanding command and service internals:
  [Architecture Overview](architecture/README.md)

## Next Steps

- [Configuration Overview](configuration/README.md)
- [KEG Structure Patterns](keg-structure/README.md)
- [Node Snapshots](node-snapshots.md)
- [Query Expressions](query-expressions.md)
- [Architecture Overview](architecture/README.md)
- [AI Coding Agent Configuration](ai-coding-agents/README.md)
- [Markdown Style Guide](keg-structure/markdown-style-guide.md)
- [Troubleshooting](configuration/troubleshooting.md)
