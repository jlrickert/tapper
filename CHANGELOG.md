# Changelog

All notable changes to this project are documented in this file.

## v0.5.0 - 2026-03-06



### ⚙️ Miscellaneous
- add node_modules to .gitignore
- tidy go.mod after MCP SDK addition
- add docs to Taskfile source watches


### 🐛 Bug Fixes
- make index resilient to missing/malformed node metadata
- make tap list --limit show most recent nodes instead of oldest
- resolve data races and add process-aware node locking
- open real config file in repo config edit instead of temp copy
- show clear error when --path points to nonexistent directory


### 📚 Documentation
- update repo init examples to use --keg flag
- add long descriptions to backlinks, archive, create, mv, and stats commands
- overhaul entity structure examples
- add node snapshots documentation
- Add query expressions documentation
- add MCP server setup guide and update CLAUDE.md


### 🚀 Features
- restructure index commands under `tap index` parent
- add --limit flag to tap list with default of 50
- add tap docs command and AI coding agent documentation
- add tap doctor command for keg health checks
- add --sort flag to tap list with index expansion
- add tap links command for outgoing node links
- add MCP server with read-only KEG tools
- add MCP write tools for node creation and modification
- add MCP index and doctor tools
- add MCP snapshot and file management tools


### 🚜 Refactor
- remove deprecated --alias flag from index subcommands


### 🧪 Testing
- add CLI concurrency tests


## v0.4.0 - 2026-03-04



### ⚙️ Miscellaneous
- refactor release workflow to resolve version before changelog generation
- add TypeScript configuration for graph frontend


### 🐛 Bug Fixes
- preserve snapshot imports and local init paths
- support numeric shorthand after root flags


### 📚 Documentation
- add comprehensive descriptions to keg and tap config JSON schemas
- clarify tapper's role in knowledge systems and problem statement


### 🚀 Features
- Add JSON Schema editor hints to tapper config files
- add interactive graph visualization command
- add node snapshots and archive import/export
- make snapshot and archive commands available to both tap and kegv2
- reorganize snapshot and archive workflows
- include snapshots in archive exports
- move KEG target flags to tap root
- add tap import command for live keg-to-keg node import
- add node ID ValidArgsFunction and completion tests for all phases
- add --query flag with key=value attribute predicate support
- add --query flag to tap rm
- add tap repo rm command to remove a keg alias
- normalize config edit workflows
- simplify repo config commands
- make global keg flags mutually exclusive and drop repo init positional arg


### 🚜 Refactor
- remove deprecated --tags flag from tap import


### 🧪 Testing
- cover unsupported snapshot backends
- add unit and CLI integration tests for tap import


## v0.3.0 - 2026-02-26



### 📚 Documentation
- expand README with quick start and configuration overview
- add installation instructions to README


### 🚀 Features
- return target path from InitKeg and update init command output


## v0.2.0 - 2026-02-26



### ⚙️ Miscellaneous
- add CI/CD automation with testing and release workflows
- migrate release process to GitHub Actions workflow
- improve code documentation and update dependencies
- update .gitignore and upgrade cli-toolkit dependency


### 🐛 Bug Fixes
- align keg defaults and resolver precedence coverage
- correct release workflow version detection


### 📚 Documentation
- improve config error messages and template output


### 🚀 Features
- refactor tap package into focused modules and add file/image management
- add dex/changes.md index and tag-filtered custom indexes
- split index command into list and cat operations, add reindex command
- evolve config schema to support ordered keg search paths
- add project-local keg alias resolution in kegs/ directory


### 🚜 Refactor
- rename command files to cmd_ prefix convention
- rename config edit methods and consolidate temp file utilities


### 🧪 Testing
- expand mv and rm commands with comprehensive test coverage


## v0.1.0 - 2026-02-24



### ⚙️ Miscellaneous
- update go-std to v0.1.0 and use toolkit package
- upgrade cli-toolkit to v0.2.0 and refactor tests to use new sandbox API
- update cli-toolkit to v0.2.1 and refactor project abstraction
- upgrade cli-toolkit to v0.4.0 and cobra to v1.10.2
- add Apache License and update dependencies
- add release automation with goreleaser and git-cliff


### 🐛 Bug Fixes
- normalize and sort meta tags when serializing
- write updated timestamp before title in nodes index
- keg mapping for various commands
- remove context dependencies from service layer
- preserve unknown config fields when updating timestamp


### 📚 Documentation
- add initial documentation and sample config for KEG project
- add meta, content, node, and links documentation
- add Tapper, KEG CLI, Zeke extension, and storage docs
- Improve MemoryRepo docs and simplify tests
- add CLI design patterns and update Tapper docs


### 🚀 Features
- add versioned KEG config management with env var expansion
- add KEG docs for indices/tags/links and bump config to v2
- add Dex index parsing and repository abstraction
- add core keg package (repo, meta, dex, content, errors, tests)
- add tapper config resolution and tests
- add user config mappings and improve keg target resolution
- add initial keg CLI scaffolding and internal helpers
- add test helpers, NormalizeTags, and link resolver updates
- implement FsRepo and modernize KegRepository API
- add tap config and keg URL utilities
- introduce KegTarget and refactor keg/user config handling
- support scalar and mapping forms for KegUrl in YAML
- add errors package and improve content parsing
- add registry shorthand and normalize keg target parsing
- add memory target and improve keg init and filesystem repo
- add app/cli runner, init command, and keg FS updates
- add tap CLI entrypoint and refactor Runner/init plumbing
- add create command, interactive streams, and registry scheme
- enable creation of user and registry kegs on local machine
- refactor keg initialization to support multiple target types
- add cat command to display node content with metadata
- add config command to display and edit configuration
- add info command to display and edit keg metadata
- allow cat and info commands to map to the correct keg
- default to cat subcommand
- Add repo list subcommand
- global config updates and config templates
- add user and project config edit subcommand
- add dir subcommand
- add list subcommand
- add stats to track programmatic node metadata
- add node level locking
- add CLI profiles and project-local keg resolution
- improve error handling and reporting for project keg discovery
- add output mode flags for cat command
- move title and tags to appropriate metadata layers
- add move and remove node commands with link rewriting
- Add reverse listing and preserve custom keg config sections
- Add node directory support to dir command
- support piped stdin as initial draft for info edit command
- add stats command to display node statistics
- add edit command for nodes with editor support
- Add backlinks command to list nodes that reference a target node
- add alias 'e' for edit command
- add grep command for searching node content
- add tags command to list and filter by tags
- add boolean tag expression support to tags command
- Add meta command for reading and editing node metadata
- add edit mode to cat command
- skip editor when piped input is provided
- support bulk node removal and interactive create with live editing
- add multi-node support and tag filtering to cat command
- inject node ID into all multi-node cat output modes


### 🚜 Refactor
- reorganize keg internals and add deterministic index builders
- centralize editor runner and add ISO8601 helper
- export index fields and add Dex.GetNode
- simplify index builders and add serialization tests
- split monolithic config into focused files and modernize types
- unify Dex indexes and migrate to new index types
- simplify keg Meta to typed fields and YAML node
- use std utilities and terrs in filesystem and memory repos
- split and normalize tag parsing utilities
- consolidate keg package and modernize repository APIs
- update dependencies and restructure package layout
- move keg target parsing into pkg/keg_url
- reorganize keg, tap and tapper internals and tests
- restructure keg internals and Node/Meta models
- reorganize keg internals and improve init/update flows
- consolidate Project config, replace fixtures, bump go-std
- restructure tap project and config; bump dependencies
- migrate filesystem repo to use toolkit and project packages
- migrate to cli-toolkit and add markdown frontmatter support
- migrate to cli-toolkit from go-std package
- simplify CLI initialization and improve config management
- rename init methods and add keg alias resolution
- encapsulate Config fields with getter and setter methods
- remove duplicate internal packages and consolidate to cli-toolkit
- update keg repo type inference with sensible defaults
- rename methods and improve init command flag handling
- reorganize app logic into tapper package and add index command
- variable and file name updates
- rename Meta and Content types to NodeMeta and NodeContent
- update CLI and runtime dependency injection throughout codebase
- pass runtime to repo constructors
- update cli-toolkit API usage and fix command flags
- move tag management from NodeMeta to NodeStats
- replace --type flag with destination-specific flags
- remove redundant keg existence validation
- remove fmt package and error logging statements
- Store Runtime in Keg and Node to simplify runtime access
- restructure config and info commands into separate repo and keg namespaces
- convert config edit subcommand to flag


### 🧪 Testing
- use scalar keg URLs in user config tests to preserve comments
- add Meta parsing, hash and comment-preservation tests
- convert create command tests to table-driven format
- add test case for nonexistent node in cat command
- add access count tracking to node stats
- add dir command path expansion tests
- add test for list command id-only flag
- Add info edit command tests and refactor temp file handling
- remove stats injection from default frontmatter output
- Add live save tests and implement live editor with validation

