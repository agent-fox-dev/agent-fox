# ADR 08: Answer every request of the service in the error envelope

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 11 and 12 were implemented)

## Context

The service's HTTP API answers errors as one JSON envelope with a code and a
message ([api.md](../api.md)). Its spec named the codes of the routes it
defined and was silent on what `net/http` does by itself (plain-text answers,
redirects), on requests no rule mentioned, and on the order in which a
request's checks run. The process's start and stop had similar gaps. The
choices below close them, each so that a client never has to parse a second
kind of answer.

## Decision

**No answer the mux would write.** A request no route takes is answered by the
service, not by `net/http`: `404 not_found` for an unknown route (one under
`/api/v1` included, without authentication), `405 method_not_allowed` with the
`Allow` header for a method the route does not take, and `404 not_found`, never
a redirect, for a path that is not in canonical form. So the slugs `.` and
`..` are `404`, not the `400 invalid_request` of the slug rule.

**Requests the rules did not mention.**

- A bearer token with white space inside (`Bearer a b`) is no token: `401`,
  without a call to hub.
- A `hub_token` with a control character cannot be sent to hub as a header, so
  it is `400 invalid_request` without a call to hub, rather than the `502` it
  would surface as.
- An answer with no body at all is `400 invalid_request`, not `415`: it is the
  body that is missing. A body that is not `application/json` is `415`.

**The order of a request's checks.** The caller's token first (`401`), then
the parameters (`400`), then hub (`502`). On a route of one workspace the guard
comes before the parameters, so a workspace the caller cannot see is `404`
whatever the query says.

**List parameters.** A parameter that is present is checked even when it is
empty (`kind=`, `limit=`), a repeated one is refused, and a number is written
as a person writes one (`007`, `+5` and `1.0` are not). A parameter the list
does not have is left alone. The first wrong one, in the order kind, state,
issue, limit, cursor, is named. A cursor is the one the service gave (base64
without padding of `{created_at, id}`); another spelling of the same bytes is
refused. `GET /api/v1/jobs` asks hub about the workspaces that have a job the
filters match and no others, the same ones for every page, so a hub that is
down for a workspace with no matching job does not fail the list.

**The event stream's starting point.** `since` is used whenever it is in the
query, `since=0` included; otherwise `Last-Event-ID`, which must be an integer
of at least 0 written without a sign, a leading zero or white space, or it is
`400` naming the header. An empty header is none.

**The process.**

- `$XDG_CONFIG_HOME` and `$XDG_STATE_HOME` are read as unset when they are
  empty or not absolute, as the XDG Base Directory specification says of a
  relative path.
- A second SIGINT or SIGTERM while requests drain changes nothing: the service
  waits for them, up to the grace, closes the database and exits 0. SIGKILL
  ends it sooner.
- `af_subscriptions` has a `revision`, 1 for a new row and one more for each
  replacement, so that the one `INSERT … ON CONFLICT DO UPDATE … RETURNING`
  that stores a subscription says whether the row is new (`201`) or replaced
  (`200`). The subscription view does not show it.

## Consequences

- Every answer of the service, whatever produced it, is the envelope a client
  already decodes.
- The checks run in a fixed order, so the same bad request gets the same
  answer whether hub is up or not.
