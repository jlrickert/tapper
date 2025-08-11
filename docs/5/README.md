# KEG utility (keg)

KEG stands for Knowledge Exchange Graph. It manages KEG nodes and provides a simple CLI for creating, editing, and inspecting nodes.

## CLI Design / Common commands

- list keg nodes
  - keg list
- Register a new node
  - keg create
- Edit node content
  - keg edit <nodeid>
- Edit a specific file in a node
  - keg edit <nodeid> <file>
- Print node content
  - keg cat <nodeid>
- Create a patch note (example integration)
  - zk patch-note "create a patch note that is part of task xyz" | keg c
- Clean: remove empty nodes and compact if uncommitted
  - keg clean
- Commit a keg node
  - keg commit <nodeid> [..nodeid]
- Update the index
  - keg index update

(KEG is intentionally small and focused on a single KEG instance. See the repo docs for implementation details.)
