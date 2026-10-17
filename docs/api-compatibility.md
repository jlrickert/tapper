# REST API contract compatibility

Tapper and Tapper Hub select a REST contract independently of their release
numbers. The current revision is `2026-10-17`, which replaced `2026-10-12`
when `POST /api/v1/@{ns}/kegs/{alias}/archive/restore` started answering
`202 Accepted` with a restore job and a `Location` to poll instead of
`200 {imported, removed}`. `2026-10-12` had replaced `2026-09-28` when `POST /api/v1/namespaces/search` was removed in favor of `POST /api/v1/explore`, the public directory of KEGs, flights, and people. `2026-09-28` had replaced `2026-09-11` when grep, listing, and relationship responses gained server-side projection.
Change the revision for incompatible API behavior. Compatible additions and
fixes retain the revision; release numbers remain diagnostic metadata.

A Hub also keeps accepting the previous revision, `2026-10-12`
(`apicontract.SupportedRevisions`), so released clients keep working through
the upgrade. A request made under it gets that revision's behavior: the Hub
reads the negotiated revision with `apicontract.RequestRevision` and does not
answer such a request with the asynchronous restore. A client always sends the
newest revision it knows (`apicontract.Revision`) and never falls back, so a
new client still needs a Hub that serves `2026-10-17`.

`GET /api/version` is public, requires no database access or credentials, and
returns `Cache-Control: no-store` with the actual server build version:

```json
{"server_version":"0.24.0","api_versions":["2026-10-17","2026-10-12"]}
```

Every request under `/api/v1`, including event-stream handshakes and telemetry,
must send exactly one `Tapper-API-Version` header naming a supported revision,
for example `Tapper-API-Version: 2026-10-17`. Accepted requests echo the
revision they sent in the response. Clients send their release in `Tapper-Client-Version`.
The Hub validates the contract before authentication, flight authority, or
application handlers. Discovery, health, OAuth, browser routes, and hosted
`/mcp` are outside this REST gate. Browser code calling REST still sends it.

A missing header returns HTTP 400 with `API_VERSION_REQUIRED`. Empty, malformed,
duplicate, comma-joined, or unsupported values return HTTP 400 with
`API_VERSION_UNSUPPORTED`. The existing JSON error envelope includes
`requested_api_versions`, `supported_api_versions`, `server_version`, and
`operationPerformed: false`, for example:

```json
{"error":"unsupported REST contract; upgrade Tapper and Hub together","code":"API_VERSION_UNSUPPORTED","requested_api_versions":["2099-01-01"],"supported_api_versions":["2026-10-17","2026-10-12"],"server_version":"0.24.0","operationPerformed":false}
```

CLI invocations and MCP connections discover each selected Hub once, before
REST operations or flight-authority loading. `tap` and `tap mcp` also keep a
successful discovery in `api-discovery.json` under the cache directory, keyed
by Hub URL and contract revision, and trust it for an hour, so most
invocations skip `GET /api/version` entirely. A stale entry is safe: the Hub
still validates every request, and its typed rejection evicts the entry. Discovery sends no credentials or
cookies, refuses redirects, and is bounded by the caller's deadline and five
seconds. Library callers can share `apicontract.Session` through their context.
Standalone clients check discovery too. Legacy Hubs with no discovery endpoint
produce `API_DISCOVERY_UNAVAILABLE`; invalid discovery produces
`API_DISCOVERY_INVALID`; no common revision produces `API_VERSION_UNSUPPORTED`.
Network errors, cancellation, timeouts, and authentication failures stay distinct.
There is no fallback to unversioned requests.

The Hub validates every request even after deployment during an existing MCP
connection. Rejections preserve typed compatibility errors and supported
revisions in MCP results. Initialization failures leave recovery diagnostics
available. Upgrade both sides to a common contract and start a new invocation
or connection; discovery failures are connection-scoped and never cached, and
the rejection that ends a connection also evicts any cached success.
Clients never switch contracts or replay mutations automatically.

Compatibility failures emit structured `api.compatibility_failure` logs:
ERROR for blocked clients (including initialization), WARN for Hub rejections.
Fields include client/server releases, requested/supported revisions,
`operationPerformed`, and a request identifier when available. Credentials,
arguments, and request content are excluded.

Clients and scripts without the header do not work against a Hub, and a
client rejects a Hub whose discovery does not list the client's revision. Update custom
scripts explicitly, for example:

```sh
curl -H 'Tapper-API-Version: 2026-10-17' \
  -H "Authorization: Bearer ${TAPPER_TOKEN}" \
  https://hub.example.com/api/v1/whoami
```

## Ranked search route

Ranked search is an optional Hub capability, so a client must tell a Hub
without it from the route's own failures. Every response from
`POST /api/v1/tap/search`, success and error alike, carries
`Tapper-Search: ranked` (`tapapi.SearchRouteHeader` and
`tapapi.SearchRouteHeaderValue`). Tapper reads a 404 or 405 without that
header as a Hub that does not offer ranked search, reported as
`NOT_SUPPORTED` with a pointer to `node_grep`; with the header, the error envelope
maps as usual, so a `NOT_FOUND` names a KEG the caller cannot read.

A search request names at most 50 KEGs (`tapapi.SearchMaxKegs`). A governed
request, one carrying the `Tapper-Orientation` proof, may leave `kegs` empty:
the Hub then searches every readable KEG in the proof's flight cover. Tapper
relies on that for an unscoped `node_search` inside a flight, so a cover wider
than 50 KEGs still searches in one request. Without the proof, Tapper names
the flight's cover itself and refuses a cover wider than 50 KEGs with
`INVALID`, asking for `kegs` to narrow it.

## Caller context route

`GET /api/v1/context` is an additive route, so it did not change the
revision. It returns, in one request, what `tap info` otherwise asks the
selected Hub in three: the `whoami` user, the flight named by the optional
`flight` query parameter (`@namespace/+slug`, or a bare `+slug` the Hub
resolves in the caller's personal namespace), and the identity KEG catalog.
A flight the Hub cannot read comes back as `flight: null` with the reason in
`flight_error` and HTTP 200, so the rest of the context still arrives. The
flight's `cover` is the Hub-composed cover, the rows a flight read reports as
`effective_cover`:

```json
{"user":{"user_id":1,"username":"jared","default_namespace":"jared"},
 "flight":{"name":"@jared/+work","title":"Work","cover":[{"ref":"@jared/dev","role":"editor","source":"@jared/dev","distance":0}]},
 "flight_error":null,
 "kegs":[{"ref":"@jared/dev","namespace":"jared","alias":"dev","title":"Dev","role":"admin","flight_cap":"editor","effective_role":"editor"}]}
```

Tapper ignores fields it does not know and projects the catalog through the
composed cover itself; `tap info` lists the composed cover on either path,
the authority MCP enforces. A Hub that predates the route answers 404;
Tapper then sends `whoami`, the flight read, and `GET /api/v1/kegs`
concurrently, and skips the route on that Hub for the rest of the invocation
or connection.

Publish the Tapper commit first; a hub that embeds Tapper then pins that
published SHA on its own side, by its own procedure. This development change
does not authorize tags, releases, merges, or deployment.
