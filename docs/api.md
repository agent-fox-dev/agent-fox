# The agent-fox API

`agent-fox` is the long-running service of agent-fox (see [cli.md](cli.md#the-agent-fox-command-line)
for how to start it and [configuration.md](configuration.md#the-agent-fox-service) for its
configuration). It serves JSON over plain HTTP: a loopback address by default, and TLS, if you want it,
is a reverse proxy's job. It has no users of its own: [hub](https://github.com/agent-fox-dev/hub) says who
is calling and which workspaces they may see, and the service asks it.

This release has the probes and the subscription of workspaces. Jobs, the worker, the poll and the
`af` client come in later specs and add their routes to this document.

## Routes

| Route | Needs a token | What it does |
|---|---|---|
| `GET /healthz` | no | `200` `{"status":"ok"}` while the process serves. |
| `GET /readyz` | no | `200` `{"status":"ready"}` when the database answers a ping, else `503` `not_ready`. |
| `GET /version` | no | `200` with the version document: `name` (`agent-fox`), `version`, `commit`, `build_time`, `go_version`. It is the document of `agent-fox --version`. |
| `GET /api/v1/workspaces` | yes | The subscriptions whose workspace the caller sees on hub, sorted by slug. |
| `GET /api/v1/workspaces/{slug}` | yes | One subscription. |
| `PUT /api/v1/workspaces/{slug}` | yes | Subscribe a workspace, or replace its subscription. |
| `DELETE /api/v1/workspaces/{slug}` | yes | Remove a subscription. |

Every response is `application/json`, except the `204` of `DELETE`, which has no body. A path that is
not canonical (`//healthz`, `/x/../healthz`) is a `404`; the service redirects nothing.

## Authentication

Every route under `/api/v1` needs a header `Authorization: Bearer <token>`. The scheme is matched
without regard to case and the token must not be empty. The token is a credential of hub for the
person calling; the service forwards it to hub, and to nobody else, to learn whom it belongs to.

A request without a usable header is answered `401` `unauthenticated` with the header
`WWW-Authenticate: Bearer`, and without a call to hub.

Hub's answers are kept for `hub.auth_cache_ttl` (60 seconds unless the configuration says; at most 15
minutes), by the SHA-256 of the token and never by the token:

- **Whom the token belongs to** is asked with `GET /api/v1/user` on hub. A token that hub accepts is
  not asked about again within the TTL. A token that hub refuses (its `401` or `403`) is answered `401`
  `unauthenticated`, and is not asked about again within the TTL either.
- **Whether the caller sees a workspace** is asked with `GET /api/v1/workspaces/{slug}` on hub, with the
  caller's own token, and is kept for each token and slug. A workspace that hub does not show to the
  caller (hub's `403` or `404`) is answered `404` `not_found`.
- **Hub being unavailable** (it is down, slow, redirects, rate limits, or answers what the service does
  not understand) is answered `502` `hub_unavailable`, and is never kept: the next request asks hub again.
- When hub answers `401` to a call that the service makes with the caller's token on a route, the token
  is no longer good. Everything kept about it goes, and the request is answered `401`
  `unauthenticated`; the next request with the token asks hub whom it belongs to again.
- Each of the two caches holds at most 10,000 entries; when it is full the expired entries go first,
  then the oldest.

Hub's visibility is the whole rule. Anyone who sees a workspace on hub may subscribe it, read its
subscription, replace it and remove it; the service adds no tenancy of its own.

### Workspace slugs

A route with `{slug}` checks, in this order: the slug, the caller, and the caller's sight of the
workspace.

1. A slug must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`; otherwise `400` `invalid_request`, before hub
   is asked anything.
2. The caller is authenticated as above.
3. The caller must see the slug on hub; otherwise `404` `not_found`.

A slug that the caller cannot see is answered **exactly as a slug that is not subscribed**: `404`, with
the same body, which does not contain the slug. Nobody learns from an answer that a workspace they
cannot see is subscribed.

## The subscription

A subscription is what the service keeps of a workspace: the token it acts on the workspace with, which
the caller handed over when subscribing, sealed with the deployment's key (AES-256-GCM, see
[configuration.md](configuration.md#the-key-file)). Every route that shows a subscription shows this
view, and **the view never contains the token or its ciphertext**:

```json
{
  "slug": "widgets",
  "label_prefix": "af:",
  "token_user": "0b8bd6a4-1d6e-4f5e-9b3c-0f4a1c9a7e21",
  "subscribed_by": "7c0fd2d9-6a52-4c11-8d3e-5a0e9f4b3a10",
  "token_state": "ok",
  "created_at": "2026-10-22T08:00:00Z",
  "updated_at": "2026-10-22T08:00:00Z"
}
```

| Field | Meaning |
|---|---|
| `slug` | The workspace. |
| `label_prefix` | The label prefix **given at subscribe**, or `af:`. It is what was stored, not necessarily the prefix in effect: whether it or hub's variable `AF_LABEL_PREFIX` governs the labels is decided by the specs that apply labels. |
| `token_user` | The hub user the token belongs to, when hub said so; `null` when hub did not (a credential that is bound to a workspace can see it and still be refused by `/user`). |
| `subscribed_by` | The hub user who subscribed the workspace last. |
| `token_state` | `ok` when the stored token opens with this deployment's key, `unreadable` when it does not (the file was written under another key). An unreadable subscription is still listed, read, replaced and removed. |
| `created_at`, `updated_at` | RFC 3339, in UTC. A replacement keeps `created_at`. |

## Reading

`GET /api/v1/workspaces` answers `200`:

```json
{"workspaces":[{"slug":"alpha", …},{"slug":"widgets", …}]}
```

It holds every subscription whose slug the caller sees, sorted by slug, and `[]` (never `null`) when
there is none. If hub is unavailable for any of the slugs, the answer is `502` `hub_unavailable` and not
a list that leaves one out; if hub refuses the caller's token while the list is made, it is `401`.

`GET /api/v1/workspaces/{slug}` answers `200` with the view, or `404` `not_found` when the slug is not
subscribed or the caller cannot see it (the same body in both cases).

## Subscribing

`PUT /api/v1/workspaces/{slug}` takes

```json
{"hub_token": "<a hub token for the workspace>", "label_prefix": "af:"}
```

`hub_token` is required and must not contain a control character. `label_prefix` is optional: it
defaults to `af:`, and must be 1 to 32 characters with no whitespace, control character or comma
(otherwise `400` `invalid_request`, before hub is asked anything with the token). The body is read as
described under [request bodies](#request-bodies).

The service checks the token with two calls to hub, made with the token and not through the caches:

1. **The gate**: `GET /api/v1/workspaces/{slug}` must find the workspace. Hub's `401` is `422`
   `invalid_hub_token` with a message that says hub refused the token; hub's `403` and `404` are `422`
   `invalid_hub_token` with a message that names the workspace; hub being unavailable is `502`
   `hub_unavailable`.
2. **The user**: `GET /api/v1/user`, best effort. Its answer is `token_user`; if hub refuses the token
   there, `token_user` is `null` and the subscription is stored all the same; if hub is unavailable, `502`.

Nothing is written before every check has passed. A refused token is `422` and never `401`, even when it
is the very token that the caller authenticated with, and a call with the token never touches the
caches of the caller's answers.

Then the token is sealed and stored in one statement. `PUT` replaces the subscription in full:

- `201` **on create**, when the workspace was not subscribed;
- `200` **on replace**, when it was: `label_prefix` goes back to `af:` if the body leaves it out,
  `token_user` is this request's answer (and stays `null` if hub did not say), `subscribed_by` is the
  caller, `created_at` is kept and `updated_at` is now.

The body of both is the view. A client treats `200` and `201` alike as success. Scopes of the token are
not checked at subscribe: a token that lacks one fails at the first call that needs it.

## Removing

`DELETE /api/v1/workspaces/{slug}` removes the subscription, its sealed token with it, and answers `204`
with no body. A slug that is not subscribed, or that the caller cannot see, is the same `404` as for
`GET`. In this release it removes the row and nothing else: there are no jobs yet to cancel.

## Request bodies

A request body must be `application/json` (parameters such as `charset=utf-8` are allowed), at most 64
KiB, one JSON value with nothing after it, and with no field that the route does not know. Otherwise the
request is refused before anything is written: `415` `unsupported_media_type`, `413`
`payload_too_large`, or `400` `invalid_request`.

## Errors

Every error is `application/json` with the body

```json
{"error":{"code":"unauthenticated","message":"a Bearer token is required"}}
```

This is the error envelope of apikit. Its optional `error_type` is **not sent** in this release, and a
client must tolerate its absence (and, in the future, its presence). `code` is for programs and does not
change; `message` is for people and may. A message never contains a token, a ciphertext, the key or the
text of an internal error.

| `code` | Status | When |
|---|---|---|
| `invalid_request` | 400 | The slug is not valid; the body is malformed JSON, has an unknown field, a field of the wrong type or data after the value; `hub_token` or `label_prefix` is not valid. |
| `unauthenticated` | 401 | There is no usable `Authorization: Bearer` header, or hub does not accept the token. |
| `not_found` | 404 | The workspace is not subscribed or the caller cannot see it (one answer for both), or the route does not exist. |
| `method_not_allowed` | 405 | The route does not take the method; the `Allow` header lists those it does. |
| `payload_too_large` | 413 | The body is larger than 64 KiB. |
| `unsupported_media_type` | 415 | The body is not `application/json`. |
| `invalid_hub_token` | 422 | At subscribe, hub refused the token, or does not show the workspace to it. |
| `internal` | 500 | A bug or a failure of the database. The panic or the error is in the log, not in the answer. |
| `hub_unavailable` | 502 | Hub could not say. Nothing was kept or written. |
| `not_ready` | 503 | `GET /readyz`: the database does not answer. |

## What the service logs

Everything on stderr is a JSON record (`log/slog`). Each request is one record with `msg` `request` and
`method`, `path` (without the query), `status`, `duration` (in nanoseconds) and, once the request has
shown who is calling, `user`. The `Authorization` header, request and response bodies, tokens,
ciphertext and the key are never logged. See [cli.md](cli.md#the-agent-fox-command-line) for the other
records.
