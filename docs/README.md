# tapper Documentation

This documentation is for end users and contributors working with tapper.

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
- I want to understand internals and code flow:
  [Architecture Overview](architecture/README.md)

If you are unsure where to start, read [Configuration Overview](configuration/README.md).

## Command Quick Reference

- Show merged config: `tap repo config`
- Show user config: `tap repo config --user`
- Show project config: `tap repo config --project`
- Print starter templates: `tap repo config --template --user|--project`
- Edit user/project config: `tap repo config edit --user|--project`
- Show active keg config: `tap config`
- Edit active keg config: `tap config --edit`

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
- [Architecture Overview](architecture/README.md)
- [Markdown Style Guide](keg-structure/markdown-style-guide.md)
- [Troubleshooting](configuration/troubleshooting.md)
