# REST API contract compatibility

Tapper and Tapper Hub select a REST contract independently of their release
numbers. The initial and currently sole supported revision is `2026-09-11`.
Change the revision for incompatible API behavior. Compatible additions and
fixes retain the revision; release numbers remain diagnostic metadata.

`GET /api/version` is public, requires no database access or credentials, and
returns `Cache-Control: no-store` with the actual server build version:

```json
{"server_version":"0.24.0","api_versions":["2026-09-11"]}
```

Every request under `/api/v1`, including event-stream handshakes and telemetry,
must send exactly one `Tapper-API-Version: 2026-09-11` header. Accepted requests
echo it in the response. Clients send their release in `Tapper-Client-Version`.
The Hub validates the contract before authentication, flight authority, or
application handlers. Discovery, health, OAuth, browser routes, and hosted
`/mcp` are outside this REST gate. Browser code calling REST still sends it.

A missing header returns HTTP 400 with `API_VERSION_REQUIRED`. Empty, malformed,
duplicate, comma-joined, or unsupported values return HTTP 400 with
`API_VERSION_UNSUPPORTED`. The existing JSON error envelope includes
`requested_api_versions`, `supported_api_versions`, `server_version`, and
`operationPerformed: false`, for example:

```json
{"error":"unsupported REST contract; upgrade Tapper and Hub together","code":"API_VERSION_UNSUPPORTED","requested_api_versions":["2099-01-01"],"supported_api_versions":["2026-09-11"],"server_version":"0.24.0","operationPerformed":false}
```

CLI invocations and MCP connections discover each selected Hub once, before
REST operations or flight-authority loading. Discovery sends no credentials or
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
or connection; discovery results, including failures, are connection-scoped.
Clients never switch contracts or replay mutations automatically.

Compatibility failures emit structured `api.compatibility_failure` logs:
ERROR for blocked clients (including initialization), WARN for Hub rejections.
Fields include client/server releases, requested/supported revisions,
`operationPerformed`, and a request identifier when available. Credentials,
arguments, and request content are excluded.

This is a coordinated upgrade: old clients and scripts without the header stop
working against the new Hub, and new clients reject legacy Hubs. Update custom
scripts explicitly, for example:

```sh
curl -H 'Tapper-API-Version: 2026-09-11' \
  -H "Authorization: Bearer ${TAPPER_TOKEN}" \
  https://hub.example.com/api/v1/whoami
```

Publish the Tapper commit first; a hub that embeds Tapper then pins that
published SHA on its own side, by its own procedure. This development change
does not authorize tags, releases, merges, or deployment.
