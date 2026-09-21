# Gotchas

Behavior that has surprised people before.

- A keg must be initialized (`keg.Init(ctx)`) before Create/SetContent/etc. Init
  writes the config file and zero node.
- The Dex is lazily loaded and cached; direct `k.dex` assignment is guarded by
  `k.dexMu`.
- Node content (README.md), meta (meta.yaml), and stats (stats.json) are
  separate reads **at the Repository layer**. At the business layer,
  `Keg.ReadNode` returns the full node state (content, raw meta, stats, asset
  lists) in one operation — a single round trip on RemoteKeg.
- The keg settings file is named `keg` (no extension), though `keg.yaml` and
  `keg.yml` are also accepted.
- Node IDs are allocated by the Hub, which owns a per-keg counter that only
  ever rises: a removed id is never handed out again. There is no way to read
  the next id ahead of time — creation uses `POST /nodes` with complete
  content, and the response carries every allocated id.
- **Cobra skips PersistentPostRunE when RunE returns an error.** Any cleanup
  or logging that must run on both success and failure paths cannot rely on
  PersistentPostRunE. In tapper, invocation logging and log file cleanup are
  performed in `RunWithProfile` after `ExecuteContext` returns, bypassing this
  Cobra limitation.
- **`duration_ms` in invocation logs includes editor wait time.** For `tap edit`
  and `tap cat` (which open an editor on TTY), the logged duration includes the
  time the user spends in the editor. This is a known limitation of the
  invocation logging system, not a bug. The `interactive` field in the log entry
  can help distinguish interactive from non-interactive invocations.

## Current Surface Notes

KEG metadata uses `description`; legacy settings/archive `summary` is accepted
only when description is absent. Flight descriptions flow through manifests,
REST/MCP, editing, and hashes independently of instructions and authority.
`orient` renders full active instructions, the complete active effective KEG cover; child flights require explicit inspection. No-flight orientation is search-first. `flight_search` returns
at most 50 ordered metadata rows with a truncation notice; `guide` serves detailed
canonical guidance by topic. Display descriptions never affect authority revisions.

The coordinated REST/MCP replacement is in progress, not complete. Cover entries
now carry depth (default 2, range 1–8). The Hub composes authority through declared
same-Hub settings relationships. `full_access` is rejected, not converted.
`keg:~alias/node` explicitly resolves the source KEG's settings alias; canonical
references keep their meaning. Orientation request transport requires a root and
active flight, with no revision acknowledgment requirement. The hub publishes
its own verification status for the replacement's scope and gaps.

KEG hard deletion is available as `tap keg delete <keg>` and MCP `keg_delete`
with an explicit canonical `keg`. It removes all data, including snapshots,
without an expected hash (settings hashes do not cover a whole KEG).
Flight-scoped deletion requires the independent `delete_kegs` capability and
admin cover plus identity admin permission. No-flight calls require identity
admin permission. `manage_kegs` alone cannot delete and is not additionally
required for deletion. Attachment deletion uses `filename`, with `name` retained
as an equal-only compatibility alias; conflicting or empty inputs fail before mutation.
