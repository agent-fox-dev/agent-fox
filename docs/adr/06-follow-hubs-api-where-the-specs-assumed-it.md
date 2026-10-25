# ADR 06: Follow hub's API where the specs assumed it

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 11 to 17 were implemented)

## Context

The service reaches hub for identity, for the calls of an attempt and for the
forge ([architecture](../architecture.md#the-service)). Hub could not be run
where the specs were written, and they marked its API as unverified: the
shapes of several calls, its error object, its paging and the sandbox
gateway's protocol were assumptions. The client was written against hub's
`docs/openapi.yaml` and `docs/api.md` instead, and `internal/hub/fakehub`
serves what hub serves, so that the client is tested against hub's shapes. Where
hub and the specs disagreed, hub won; this ADR lists the cases.

## Decision

**Shapes of hub's calls.** `internal/hub` is the one package that knows them.
It reads the older assumed shape as well where that costs nothing.

| Call | Hub's shape, which the client follows |
|---|---|
| the user | `id` and `username`; the login is read from `login`, else `username` |
| resolved variables | a list of `{key, value, origin}` (a `{vars: {…}}` object and a list of `{name, value}` are read too) |
| the mode of a workspace | `workspace_mode` (`mode` is read too) |
| post an events batch | a bare array of at most 1000 `{id, run_id, event_type, timestamp, payload}`, `payload` an object, the id `<run id>-<number>`, so a batch posted twice adds nothing |
| post usage | `{model, input_tokens, output_tokens, cache_read_tokens}`; the client also sends the cost, cost source, brain and turns, which hub ignores |
| complete a session | `completed`, `failed` or `timeout` |
| an error | `{error: {code: <status>, message, error_type}}`; `error_type`, else the status, is the code of a `*RejectedError` |
| a forge that refused hub's credential | `502` with `error_type` `forge_unauthenticated` or `forge_forbidden`: a rejection no retry cures; other `502`s and `504` are unavailable |
| an issue whose comments failed | `comments_err`, an object; the loops warn `comments_unreadable` |
| push a branch | the branch as one path segment, each `/` escaped |
| create a label | `{name, color, description}`, `201` created and `200` there already; `color` is six hex digits without `#`; a relayed "already exists" refusal (a 409, or a 4xx whose `error_type` or message says so) counts as present |

**The status decides.** An answer is classed by its status alone
(refused, forbidden, not found, rejected, unavailable), so an error page of any
size cannot turn a refusal into "unavailable". The body of a 401, 403 or 404 is
read only for hub's message, which is added to the error as ` (hub said: …)`
with the token scrubbed. A workspace that a token may not see is hub's `404`,
which the fake answers by default (`Hub.HiddenStatus` changes it).

**Bounds.** An ordinary call is bounded by `Client.HTTP.Timeout` (10 s) and
the lease and the branch push by `Client.LongTimeout` (5 minutes when zero),
because hub does slow work before answering. An answer is read up to 1 MiB,
except a list of issues, which is read up to 32 MiB: a list of 100 issues with
bodies can be longer, and a list refused for its size would never shorten.

**Lists without pages.** Hub's lease and issue lists are bare arrays without
pages.

- `ListSandboxes` reads one array (at most 200 live leases, newest first) and
  fails when a lease has no id or an end that does not parse.
- `ListIssues` asks for `state=open&labels=<label>&limit=100`, follows a
  further page only when an answer names one in a `Link` header with
  `rel="next"` (hub never does; the fake can, so the rule is tested), leaves out
  an item marked `is_pr`, and reports the list incomplete when it stopped at its
  limit with issues left **or when the last page is as long as the page asked
  for**, since a bare array cannot say it was cut.

**The sandbox gateway.** `gateway.Dial(ctx, url, gateway.Options{Token})`
returns the provider, and a plain `http://` URL is allowed only for a loopback
host. `gatewayd` is a gRPC server in a module of its own, which `go.mod`
requires and replaces with `../agentkit-go/sandbox/gatewayd`, as hub's does. It
relays the agent protocol without looking into it, so the fake hub records the
commands of a box by wrapping the provider on the client's side
(`Gateway.Instrument`).

## Consequences

- A workspace with 100 or more issues under one request label has an
  incomplete listing every cycle until the backlog drains, which costs a `WARN`
  and holds and owed removals left for a cycle
  ([ADR 10](10-turn-request-labels-into-jobs-once.md)).
- `aflib`'s `issuex.ParseIssueURL` takes a host for a forge only when its name
  holds `github` or `gitlab`, or when it is the host of `GITHUB_API_URL` or
  `GITLAB_API_URL` in the process's environment. A workspace on another forge
  host runs only when the service has one of those variables. The fix is in
  `aflib`, which has no way to be told the host of a repository.
- When hub's API changes, `internal/hub` and `fakehub` change together.
