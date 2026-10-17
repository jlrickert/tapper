package testapi

// The Hub resolves GET /api/v1/context?flight= the way Tapper's orient does.
// A qualified @namespace/+slug names one flight. A bare +slug is matched
// against every flight the user can read (the list GET /api/v1/flights
// returns), not only the personal namespace: exactly one match resolves to
// it, none answers HubContextFlightNotFound, and several answer
// HubContextFlightAmbiguous.

// HubContextWork is a GET /api/v1/context body in the shape tapper-hub serves
// (its handler.ContextResponse, documented in tapper-hub's
// docs/tapper-hub-api-v1.md), for user jared and flight @foldwise/+work. The
// flight's cover is the Hub-composed cover: @foldwise/dev is listed, and
// @foldwise/linked is reached through dev's links at distance 1.
// @foldwise/private is outside the cover. Unknown fields stand in for ones a
// newer Hub adds.
const HubContextWork = `{
  "user": {
    "user_id": 1,
    "username": "jared",
    "display_name": "Jared",
    "email": "jared@example.com",
    "created_at": "2026-01-01T00:00:00Z",
    "default_namespace": "foldwise",
    "namespaces": ["foldwise", "jared"],
    "personal_namespace": "jared"
  },
  "flight": {
    "name": "@foldwise/+work",
    "title": "Work",
    "cover": [
      {"ref": "@foldwise/dev", "role": "editor", "source": "@foldwise/dev", "distance": 0},
      {"ref": "@foldwise/linked", "role": "viewer", "source": "@foldwise/dev", "distance": 1}
    ]
  },
  "flight_error": null,
  "kegs": [
    {"ref": "@foldwise/dev", "namespace": "foldwise", "alias": "dev", "title": "Development",
     "description": "", "visibility": "private", "keg_version": "", "role": "admin",
     "flight_cap": "editor", "effective_role": "editor", "added_later": true},
    {"ref": "@foldwise/linked", "namespace": "foldwise", "alias": "linked", "title": "Linked",
     "description": "", "visibility": "private", "keg_version": "", "role": "admin",
     "flight_cap": "viewer", "effective_role": "viewer"},
    {"ref": "@foldwise/private", "namespace": "foldwise", "alias": "private", "title": "Outside the flight",
     "description": "", "visibility": "private", "keg_version": "", "role": "admin",
     "flight_cap": "", "effective_role": "admin"}
  ],
  "unknown_top_level": "ignored"
}`

// HubContextFlightNotFound is HubContextWork's answer when the requested
// flight is missing or unreadable: still 200, with flight null and the
// reason in flight_error.
const HubContextFlightNotFound = `{
  "user": {"user_id": 1, "username": "jared", "default_namespace": "foldwise"},
  "flight": null,
  "flight_error": "flight not found",
  "kegs": [
    {"ref": "@foldwise/dev", "namespace": "foldwise", "alias": "dev", "title": "Development",
     "description": "", "visibility": "private", "keg_version": "", "role": "admin",
     "flight_cap": "", "effective_role": "admin"}
  ]
}`

// HubContextFlightAmbiguous is the answer to a bare +work when the user can
// read more than one flight with that slug: still 200, with flight null and
// the same reason orient gives when it resolves the slug itself.
const HubContextFlightAmbiguous = `{
  "user": {"user_id": 1, "username": "jared", "default_namespace": "foldwise"},
  "flight": null,
  "flight_error": "flight \"+work\" is ambiguous; use @namespace/+work",
  "kegs": [
    {"ref": "@foldwise/dev", "namespace": "foldwise", "alias": "dev", "title": "Development",
     "description": "", "visibility": "private", "keg_version": "", "role": "admin",
     "flight_cap": "", "effective_role": "admin"}
  ]
}`
