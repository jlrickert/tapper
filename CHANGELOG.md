# Changelog

All notable changes to this project are documented in this file.

## v0.31.0 - 2026-07-15



### 🚜 Refactor
- **config:** remove legacy keg settings


## v0.30.0 - 2026-07-14



### 🐛 Bug Fixes
- **integrations:** align Codex plugin manifests


### 🚀 Features
- **integrations:** generalize native plugin installation
- **mcp:** enforce flight-gated sessions
- **flights:** add full access capability
- **flights:** add recovery bootstrap flow
- **integrations:** harden Codex MCP workflows
- **integrations:** run plugin hooks through tap


### 🚜 Refactor
- **flights:** replace revisions with manifest hashes
- **orient:** make shared payload MCP-first
- **mcp:** keep plugin installation CLI-only


## v0.29.0 - 2026-07-10



### 🐛 Bug Fixes
- **release:** keep automatic versions on v0
- **auth:** keep device codes out of verification URLs


### 🚀 Features
- **orient:** simplify orient payload


## v0.28.2 - 2026-07-07



### ⚙️ Miscellaneous
- **release:** add existing tag recovery mode


## v0.28.1 - 2026-07-07



### 🐛 Bug Fixes
- **auth:** refresh cached hub credentials
- **auth:** show all stored hubs in status


### 🚜 Refactor
- **auth:** share hub credential refresh flow


## v0.28.0 - 2026-07-01



### 🐛 Bug Fixes
- **keg:** touch config consistently on dex writes


### 🚀 Features
- add snapshot indexes and computed omega
- **keg:** add snapshot omega indexes and source hashes
- **schemas:** support nested metadata maturity weights


## v0.27.0 - 2026-06-28



### 📚 Documentation
- **flight:** cover caps apply to MCP, not web


### 🚀 Features
- **flight:** scope cover caps to MCP/web, not direct CLI


## v0.26.0 - 2026-06-27



### 📚 Documentation
- trim flight manifest schema description


### 🚀 Features
- **mcp:** expose keg schema tools on MCP surfaces
- add schema-backed editor manifests
- name flight and schema editor temp files consistently
- lock mcp to project flight
- include schemas in keg archives


## v0.25.0 - 2026-06-25



### 🚀 Features
- **schemas:** add type-based keg schemas
- add schema edit command
- **keg:** add remote alias rename command


### 🚜 Refactor
- **keg:** move schema repository capability


## v0.24.0 - 2026-06-21



### 🐛 Bug Fixes
- **mcp:** disable UI-only admin tools


### 🚀 Features
- replace flight update with editor-based flight edit


## v0.23.0 - 2026-06-18



### ⚙️ Miscellaneous
- **test-env:** migrate sandbox compose from docker to podman
- gitignore local task.md scratch notes


### 🐛 Bug Fixes
- align remote keg URL with hub and fix tap resolution
- keep local node ids bare in the dex
- render edit frontmatter as yaml and ignore self-save events
- reject path traversal in keg asset names


### 📚 Documentation
- update guidance and integrations for namespace addressing
- describe the Keg interface architecture in CLAUDE.md
- refresh namespace-centric onboarding guides


### 🚀 Features
- address kegs by (hub, namespace, name); drop kegSearchPaths
- **cli:** add tap bootstrap onboarding (local/cloud/enterprise)
- **auth:** redesign tap auth login (device flow + token paste)
- gh-style tap auth status with live validation
- **auth:** silent token refresh and device-login UX fixes
- layered config, per-hub namespaces, flights, and qualified refs
- default bootstrap namespace to the user's hub namespace
- namespace-centric keg addressing and keg: reference scheme
- stream live node events with editor reverse sync
- add tap watch command for live node events
- resolve note-relative links and asset paths when rendering nodes
- name editor temp files after the keg and node being edited
- define the Keg interface and lift business ops into LocalKeg
- add shared remote error-code table for the hub wire protocol
- add RemoteKeg, the one-request-per-operation hub client
- manage hub-backed flights with role-capped covers
- split system and user indexes
- split hub admin into tap keg and tap namespace commands
- require bootstrap, infer namespaces, drop tap site
- **namespace:** hand off creation to hub UI
- **bootstrap:** add guided setup and first keg creation
- **mcp:** add KegResolver seam and SurfaceHub tool curation


### 🚜 Refactor
- move repo config to top-level, keg config to settings
- drop repo and list-kegs command surface
- resolve kegs through the namespace-centric chain
- unify repository events behind ctx-scoped Watch
- drop deprecated tag filters and legacy input formats
- rename Keg to LocalKeg and split keg.go by concern
- route pkg/tapper through the Keg interface


### 🧪 Testing
- migrate keg fixtures to @local namespace layout
- update suites for namespace-addressed kegs


## v0.22.1 - 2026-05-26



### ⚙️ Miscellaneous
- **deps:** bump cli-toolkit, mcp go-sdk, fsnotify, and transitives


### 📚 Documentation
- align CLAUDE.md branching model and MCP tool count with reality


## v0.22.0 - 2026-04-30



### ⚙️ Miscellaneous
- **release:** split release into PR and publish workflows
- **release:** rework to manual dev→main PR flow + tag-push publish
- consolidate release-publish into release workflow


### 🐛 Bug Fixes
- **release-pr:** tolerate git-cliff failure on first release


### 🚀 Features
- **config:** canonicalize index file paths to bare form
- **cli:** promote tap repo init to top-level tap init
- **cli:** tighten tap init — alias regex, platform default, drop tap dir
- **cli:** TTY prompt for tap init, path-free user surfaces
- **sandbox:** isolate from host, add fixtures + dotfiles packages
- **integrations:** add PreToolUse hook blocking direct tap/keg CLI use


## v0.21.0 - 2026-04-28



### ⚙️ Miscellaneous
- trigger on dev branch instead of main
- **release:** merge release commit via PR instead of pushing to main
- bump cli-toolkit to v1.5.1


### 📚 Documentation
- document dev/main branching model
- pin Claude plugin marketplace install to @main


### 🚀 Features
- **test-env:** add work-mode sandbox with local cli-toolkit


## v0.20.0 - 2026-04-26



### ⚙️ Miscellaneous
- add isolated Ubuntu docker sandbox for local testing
- scrub personal references introduced by the hub rename pass
- disable auth subcommand to unblock unrelated work


### 🐛 Bug Fixes
- strip id field from persisted meta.yaml
- canonicalize @ sigil in hub: shorthand
- allow auth cli
- pin embed drift test to embedded plugin version


### 📚 Documentation
- lead Claude Code install with plugin marketplace
- document hub resolution chain and finish registry → hub rename


### 🚀 Features
- add auth store module and state path accessor
- add tap auth login with OAuth2 PKCE loopback flow
- discover OAuth2 endpoints via RFC 8414 metadata
- add tap auth status, logout, and MCP auth_status tool
- consult auth store when resolving remote keg tokens
- add OAuth 2.0 device authorization grant flow to tap auth login
- add default hub mechanism with five-step resolveLoginHubURL chain
- resolve active keg in tap orient instead of printing a placeholder hint


### 🚜 Refactor
- consolidate auth logout and hub resolution in service layer
- rename registry → hub across tapper config, kegurl, and CLI
- rename Target.User to Namespace, split BasicAuthUser, rename Keg to KegName
- fold pkg/keg_url into pkg/keg


### 🧪 Testing
- cover ResolveLoginHubURL chain and TAP_DISABLE_DEFAULT_HUB env var


## v0.19.0 - 2026-04-22



### ⚙️ Miscellaneous
- drive plugin manifest version from the release workflow


### 🐛 Bug Fixes
- prevent stale MCP reads and shadow-reservation existence races
- eliminate wall-clock polling from in-memory lock waits


### 📚 Documentation
- lead with tap integrate for AI agent setup and route manual MCP registration to the advanced guide
- document orient surface and codex install path
- document the tapper query expression language in installed skills


### 🚀 Features
- replace --dest flag with positional dest arg and add stdout mode on download
- scaffold .claude-plugin/ manifest and placeholder
- ship Claude Code plugin with bundled tapper skill and MCP server
- embed integration content trees in the tap binary
- add tiered orient endpoint with MCP tool and resource URIs
- ship codex install tree with agents guide, prompts, and mcp config
- install tapper integrations and print orientation payloads from the command line
- add shell completion for tap orient --tier/--flight and tap integrate --target


### 🚜 Refactor
- author editor integrations via canonical content and adapters
- route orient payload through the shared tap API
- push host-path map behind Adapter.OrientPath and close codex test gap
- promote --flight to a root persistent flag mutually exclusive with --keg selectors


## v0.18.1 - 2026-04-03



### 🐛 Bug Fixes
- resolve 6 bugs and 7 refactoring issues across pkg/keg and pkg/tapper
- resolve fsRepoWatcher channel race between Emit sends and loop close
- prevent ABBA deadlock in Close by releasing fw.mu before unregisterWatcher
- replace base64 encoding with file-path handles in MCP file/image tools
- correct download tool annotations and complete image parity test
- skip bare directories in Index and fix dex pointer race


## v0.18.0 - 2026-04-01



### 🐛 Bug Fixes
- resolve Go idiom violations from dev/646 audit
- update completion tests for multi-ID backlinks and add links coverage


### 🚀 Features
- add offset pagination and fix default limits across surfaces
- add dot-prefix stats field access to query expressions
- unify comparison operators for meta.yaml attributes
- accept multiple node IDs in backlinks and links commands
- add max-lines to grep and improve MCP output hints


### 🚜 Refactor
- rename tag_expr to query_expr


### 🧪 Testing
- add query expression fixture with omega and comprehensive tests


## v0.17.0 - 2026-03-27



### ⚙️ Miscellaneous
- update cli-toolkit to v1.4.0 and goldmark to v1.8.2


### 🐛 Bug Fixes
- add shell completion for --explain flag field names


### 📚 Documentation
- document config cascade and env var overrides in schema and CLAUDE.md


### 🚀 Features
- surface config load errors and add --strict flag
- wire cfgcascade into ConfigService and add TAP_* env var overrides
- add transparent config layering with --explain and --show-sources


## v0.16.0 - 2026-03-26



### 🐛 Bug Fixes
- fan CLI log output to both file and stderr when --log-file is set
- address deferred items from invocation logging implementation


### 🚀 Features
- add invocation logging for CLI commands and MCP tools


## v0.15.0 - 2026-03-24



### 🐛 Bug Fixes
- use mtime-gated dex in serve handlers to prevent stale indexes
- resolve SSE redirect loop with broadcast debounce and client cooldown
- wire query resolver into KegService keg resolution
- SSE reload cascade and tolerant dex error handling in serve


### 🚀 Features
- add MCP tool annotations for agent auto-approval
- add QueryFilteredIndex with injected resolver callback
- add per-keg timezone configuration
- add filesystem watcher for proactive dex invalidation in serve handler
- add browser auto-refresh via SSE for live content updates


### 🚜 Refactor
- remove unused lsp/zekia stubs and genericize keg config templates


### 🧪 Testing
- add dex freshness and SSE event delivery tests


## v0.14.0 - 2026-03-21



### ⚙️ Miscellaneous
- remove unused Docker and release tasks from Taskfile


### ⚡ Performance
- reduce dex invalidation overhead with parallel I/O and caching


### 🐛 Bug Fixes
- resolve double node ID allocation in interactive tap create
- remediate critical and high-severity architecture findings


### 🚀 Features
- disable doctor entity check by default
- add doctor.tagCheck config to gate tag validation


### 🚜 Refactor
- remediate medium and low severity architecture findings


## v0.13.0 - 2026-03-17



### 🐛 Bug Fixes
- remove stale tag associations in TagIndex.Add
- include content in SetMeta dex write to preserve link indexes
- prevent index rebuild from silently dropping nodes on error
- add node lock to Remove and existence check in SetContent
- invalidate cached dex before writes to prevent stale overwrites
- add %c and %a format placeholders for created and accessed dates
- add minimal mode to info tool to reduce MCP response size
- replace direct os/stream usage with Runtime abstractions
- replace fmt.Fprintf(os.Stdout) with Runtime stream in Serve
- replace time.Now() with rt.Clock().Now() in temp file naming
- replace exec.Command with exec.CommandContext in runPagefind
- stabilize flaky TestEdit_LiveSave test under race detector
- prevent node resurrection on concurrent remove during edit
- revert anti-resurrection guards, upgrade cli-toolkit to v1.2.0
- remove zsh completion generation from install tasks
- prevent node resurrection in Remove link rewriting loop


### 📚 Documentation
- document Runtime abstraction rule in CLAUDE.md
- add shell completion verification to feature surface checklist


### 🚀 Features
- skip index and keg config updates when content unchanged
- add CLI-MCP parity test suite


### 🚜 Refactor
- extract resolveAndLookupLinks helper for Links/Backlinks
- replace renderUserError if-else chain with UserMessager interface


## v0.12.0 - 2026-03-15



### ⚙️ Miscellaneous
- run go mod tidy


### 🐛 Bug Fixes
- add missing shell completions for site build and serve


### 🚀 Features
- embed LICENSE in binary and expose via --license flag
- add MCP license tool for license text retrieval
- add repo_init, repo_rm, config, config_template MCP tools
- add import_from_keg MCP tool for cross-keg node import
- add export and import MCP tools for keg archives
- add upload/download file and image MCP tools
- add graph MCP tool for KEG visualization


### 🧪 Testing
- add tests for --license flag, MCP license tool, and completions


## v0.11.0 - 2026-03-15



### ⚙️ Miscellaneous
- update go-std to v0.1.0 and use toolkit package
- upgrade cli-toolkit to v0.2.0 and refactor tests to use new sandbox API
- update cli-toolkit to v0.2.1 and refactor project abstraction
- upgrade cli-toolkit to v0.4.0 and cobra to v1.10.2
- add Apache License and update dependencies
- add release automation with goreleaser and git-cliff
- add CI/CD automation with testing and release workflows
- migrate release process to GitHub Actions workflow
- improve code documentation and update dependencies
- update .gitignore and upgrade cli-toolkit dependency
- refactor release workflow to resolve version before changelog generation
- add TypeScript configuration for graph frontend
- add node_modules to .gitignore
- tidy go.mod after MCP SDK addition
- add docs to Taskfile source watches
- upgrade GitHub Actions to Node.js 24 compatible versions


### 🐛 Bug Fixes
- normalize and sort meta tags when serializing
- write updated timestamp before title in nodes index
- keg mapping for various commands
- remove context dependencies from service layer
- preserve unknown config fields when updating timestamp
- align keg defaults and resolver precedence coverage
- correct release workflow version detection
- preserve snapshot imports and local init paths
- support numeric shorthand after root flags
- make index resilient to missing/malformed node metadata
- make tap list --limit show most recent nodes instead of oldest
- resolve data races and add process-aware node locking
- open real config file in repo config edit instead of temp copy
- show clear error when --path points to nonexistent directory
- correct malformed property nesting in keg-config.json schema
- correct keg resolution precedence and kegMap merge
- use content comparison to prevent self-triggered editor warnings
- parallelize index rebuild for dirty nodes
- correct license to Apache-2.0 in Homebrew formulas
- add --version flag to root command for Homebrew test compatibility
- block writes to locked nodes when no lock token is provided


### 📚 Documentation
- add initial documentation and sample config for KEG project
- add meta, content, node, and links documentation
- add Tapper, KEG CLI, Zeke extension, and storage docs
- Improve MemoryRepo docs and simplify tests
- add CLI design patterns and update Tapper docs
- improve config error messages and template output
- expand README with quick start and configuration overview
- add installation instructions to README
- add comprehensive descriptions to keg and tap config JSON schemas
- clarify tapper's role in knowledge systems and problem statement
- update repo init examples to use --keg flag
- add long descriptions to backlinks, archive, create, mv, and stats commands
- overhaul entity structure examples
- add node snapshots documentation
- Add query expressions documentation
- add MCP server setup guide and update CLAUDE.md
- add feature surface checklist to CLAUDE.md
- add Homebrew installation instructions to README
- update tool counts, add lock commands, and trim redundant sections
- add backup docs, trim redundant sections, and document --cwd flag
- replace license summary with full Apache 2.0 text


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
- refactor tap package into focused modules and add file/image management
- add dex/changes.md index and tag-filtered custom indexes
- split index command into list and cat operations, add reindex command
- evolve config schema to support ordered keg search paths
- add project-local keg alias resolution in kegs/ directory
- return target path from InitKeg and update init command output
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
- show alias and summary in tap info output
- add RepositoryEvents interface for live node change notifications
- implement FsRepo file watcher with fsnotify
- implement in-place editing for FsRepo
- add event-aware conflict warnings for non-fs backend editing
- add MemoryRepo event support for testing
- add event system tests and fix path resolution
- add reverse sync to update temp file on external node changes
- add NodeEventAccessed and emit on content reads
- add shell completions for file and image subcommands
- add Homebrew formula generation via GoReleaser
- add version subcommand and inject version via ldflags
- interactive terminal editing for tap cat and access tracking
- add cross-process node locking with tap lock command
- add MCP lock tools for cross-process node locking
- add --lock-token flag to tap cat for edit paths
- add ApiRepo HTTP repository backend for remote keg access
- add tap site command for static HTML site generation
- add directory completion for tap site --output flag
- move links/backlinks to right sidebar with mobile flyout
- add tap serve command with dynamic KEG HTTP server


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
- rename command files to cmd_ prefix convention
- rename config edit methods and consolidate temp file utilities
- remove deprecated --tags flag from tap import
- remove deprecated --alias flag from index subcommands
- remove incremental indexing, always full rebuild
- rename kegv2 binary to keg
- remove work-specific references from codebase
- clean up site generation two-pass search integration
- move serve under site parent, rename site to site build


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
- expand mv and rm commands with comprehensive test coverage
- cover unsupported snapshot backends
- add unit and CLI integration tests for tap import
- add CLI concurrency tests
- add comprehensive lock integration and concurrency tests
- add comprehensive tests for tap site command
- add benchmark tests for tap site serve handlers

