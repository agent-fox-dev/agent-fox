# HTTP API reference

`agent-fox`, the long-running service, serves a JSON API over plain HTTP. It listens on a loopback
address by default; put a reverse proxy in front of it if you need TLS. The service has no users of
its own: [hub](https://github.com/agent-fox-dev/hub) is the identity provider, and the service asks hub
who is calling and which workspaces they may see.

To start the service, see [cli.md](cli.md#the-agent-fox-command). To configure it, see
[configuration.md](configuration.md#the-agent-fox-service). The `af` client wraps every route in this
document; see [cli.md](cli.md#service-client-commands).

## Contents

- [Overview](#overview)
- [Routes](#routes)
- [Authentication](#authentication)
  - [Workspace visibility](#workspace-visibility)
- [Workspaces](#workspaces)
  - [The subscription view](#the-subscription-view)
  - [List and read](#list-and-read)
  - [Subscribe](#subscribe)
  - [Unsubscribe](#unsubscribe)
- [Labels](#labels)
  - [The label set](#the-label-set)
  - [Request labels](#request-labels)
  - [Answering and retrying from the issue](#answering-and-retrying-from-the-issue)
- [Jobs](#jobs)
  - [Job states](#job-states)
  - [Create a job](#create-a-job)
  - [The job document](#the-job-document)
  - [How an attempt ends](#how-an-attempt-ends)
  - [What the worker adds to an attempt](#what-the-worker-adds-to-an-attempt)
  - [Cancel](#cancel)
  - [Answer and retry](#answer-and-retry)
  - [List and read jobs](#list-and-read-jobs)
  - [The event stream](#the-event-stream)
  - [Retention](#retention)
- [Request bodies](#request-bodies)
- [Errors](#errors)
- [Logging](#logging)

## Overview

- A **subscription** connects a hub workspace to the service. You subscribe a workspace by handing
  over a hub token for it; the service keeps that token encrypted and acts on the workspace with it.
- A **job** is one request to `triage` or `fix` an issue (or, for triage, a text or a file) on a
  subscribed workspace. Jobs of one workspace run one at a time, in the order they were made.
- An **attempt** is one run of a job. Every attempt ends with an **envelope**: the JSON document that
  `af` prints (see [cli.md](cli.md#the-envelope)). A job gets a new attempt when someone answers its
  question, retries it, or when the service retries a failed brain by itself.
- Each job has an **event log** that you can read, and follow live, as a server-sent event stream.
- There are two ways to make a job: call the API, or put a **request label** (`af:triage` or `af:fix`)
  on an issue and let the service find it.

## Routes

| Route | Token | What it does |
|---|---|---|
| `GET /healthz` | no | `200` `{"status":"ok"}` while the process serves. |
| `GET /readyz` | no | `200` `{"status":"ready"}` when the database answers; otherwise `503` `not_ready`. |
| `GET /version` | no | `200` with the version document: `name` (`agent-fox`), `version`, `commit`, `build_time`, `go_version`. The same document as `agent-fox --version`. |
| `GET /api/v1/workspaces` | yes | [List](#list-and-read) the subscriptions you can see. |
| `GET /api/v1/workspaces/{slug}` | yes | [Read](#list-and-read) one subscription. |
| `PUT /api/v1/workspaces/{slug}` | yes | [Subscribe](#subscribe) a workspace, or replace its subscription. |
| `DELETE /api/v1/workspaces/{slug}` | yes | [Unsubscribe](#unsubscribe): cancel the workspace's open jobs and remove the subscription. |
| `POST /api/v1/workspaces/{slug}/triage` | yes | [Create](#create-a-job) a triage job. |
| `POST /api/v1/workspaces/{slug}/fix` | yes | [Create](#create-a-job) a fix job. |
| `GET /api/v1/workspaces/{slug}/jobs` | yes | [List](#list-and-read-jobs) the jobs of one workspace, subscribed or not. |
| `GET /api/v1/jobs` | yes | [List](#list-and-read-jobs) the jobs of every workspace you can see. |
| `GET /api/v1/jobs/{id}` | yes | [Read](#list-and-read-jobs) one job, with its attempts. |
| `POST /api/v1/jobs/{id}/cancel` | yes | [Cancel](#cancel) a queued, running or blocked job. |
| `POST /api/v1/jobs/{id}/answer` | yes | [Answer](#answer-and-retry) the question of a blocked job. |
| `POST /api/v1/jobs/{id}/retry` | yes | [Retry](#answer-and-retry) a failed job. |
| `GET /api/v1/jobs/{id}/attempts/{n}/envelope` | yes | The `envelope.json` that attempt `n` stored, byte for byte. |
| `GET /api/v1/jobs/{id}/attempts/{n}/report` | yes | The `report.md` that attempt `n` stored. |
| `GET /api/v1/jobs/{id}/events` | yes | The job's [event stream](#the-event-stream) (`text/event-stream`). |

Responses are `application/json`, with three exceptions: `DELETE` answers `204` with no body, an
attempt's report is `text/markdown; charset=utf-8`, and the event stream is `text/event-stream`.

The service never redirects. A path that is not canonical (`//healthz`, `/x/../healthz`) is `404`. A
method the route does not take is `405` with an `Allow` header.

## Authentication

Every route under `/api/v1` needs the header `Authorization: Bearer <token>`, where the token is a
hub credential of the person calling (an API key or a personal access token). The scheme is
case-insensitive and the token must not be empty. A request without a usable header gets `401`
`unauthenticated` with `WWW-Authenticate: Bearer`, and hub is not asked.

The service sends the token to hub, and to nobody else, to learn who it belongs to
(`GET /api/v1/user` on hub). It then asks hub, with the same token, whether you can see the workspace
a route names (`GET /api/v1/workspaces/{slug}` on hub). If `HTTP_PROXY` or `HTTPS_PROXY` is set, these
calls go through that proxy.

Hub's answers are cached for `hub.auth_cache_ttl` (60 seconds by default, at most 15 minutes), keyed
by a hash of the token, never the token itself:

| Hub says | The service answers | Cached |
|---|---|---|
| The token is valid | (the request goes on) | yes |
| The token is refused (`401` or `403`) | `401` `unauthenticated` | yes |
| You cannot see the workspace (`403` or `404`) | `404` `not_found` | yes |
| Hub is down, slow, redirects, rate-limits or answers something unexpected | `502` `hub_unavailable` | no: the next request asks again |

If hub refuses the token during a request that has already authenticated, everything cached about the
token is dropped and the request gets `401`.

Hub's visibility is the whole access rule. Anyone who can see a workspace on hub can subscribe it,
read its subscription, replace it and remove it, and make, read and act on its jobs. The service adds
no tenancy of its own.

### Workspace visibility

A route with `{slug}` checks three things, in this order:

1. The slug matches `^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$`. Otherwise `400` `invalid_request`, before hub
   is asked anything.
2. You are authenticated.
3. You can see the workspace on hub. Otherwise `404` `not_found`.

A workspace you cannot see gets **exactly the same answer** as a workspace that is not subscribed: the
same `404` with the same body, which does not contain the slug. Nobody can learn from an answer that a
workspace they cannot see is subscribed.

## Workspaces

### The subscription view

Every route that returns a subscription returns this document. It **never contains the workspace
token or its ciphertext.**

```json
{
  "slug": "widgets",
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
| `token_user` | The hub user the workspace token belongs to, or `null` when hub did not say (a token bound to a workspace may see the workspace and still be refused by hub's `/user`). |
| `subscribed_by` | The hub user who last subscribed the workspace. |
| `token_state` | `ok` when the stored token decrypts with this deployment's key; `unreadable` when it does not (it was stored under another key). An unreadable subscription can still be listed, read, replaced and removed, but it cannot run jobs. |
| `created_at`, `updated_at` | RFC 3339, UTC. Replacing a subscription keeps `created_at`. |

### List and read

`GET /api/v1/workspaces` answers `200` with every subscription you can see, sorted by slug:

```json
{"workspaces": [{"slug": "alpha", …}, {"slug": "widgets", …}]}
```

The list is `[]`, never `null`, when there are none. If hub is unavailable for any of the slugs, the
answer is `502` `hub_unavailable` rather than a list with a gap. If hub refuses your token while the
list is built, the answer is `401`.

`GET /api/v1/workspaces/{slug}` answers `200` with the view, or `404` `not_found` when the workspace is
not subscribed or you cannot see it.

### Subscribe

`PUT /api/v1/workspaces/{slug}` subscribes a workspace, or replaces its subscription:

```json
{"hub_token": "<a hub token for the workspace>"}
```

`hub_token` is required and must not contain a control character (`400` `invalid_request`
otherwise). The service acts on the workspace with this token from then on: it reads issues, writes
labels and comments, clones the repository, leases sandboxes and opens pull requests through hub. The
token needs hub's forge scope (`forge:write`) for that.

The service checks the token in three steps. Each call uses the handed-over token itself, and none
goes through the caches of [authentication](#authentication).

1. **Can the token see the workspace?** Hub's `GET /api/v1/workspaces/{slug}` must succeed. Hub's
   `401`, `403` or `404` is `422` `invalid_hub_token`; hub unavailable is `502` `hub_unavailable`.
2. **Who owns the token?** Hub's `GET /api/v1/user`, best effort. The answer becomes `token_user`. If
   hub refuses the token here, `token_user` is `null` and the subscription is stored anyway. Hub
   unavailable is `502`.
3. **Create the labels.** The service creates the seven [labels](#the-label-set) on the workspace's
   repository through hub, one at a time, in table order. A label that already exists is left as it
   is (colour and description included). The first creation hub does not accept stops the step and
   refuses the `PUT`:

   | Hub answers a label creation with | `PUT` answers |
   |---|---|
   | `401` | `422` `invalid_hub_token`: hub refused the token. |
   | `403` | `422` `forge_forbidden`: the token lacks the scope for hub's forge API (`forge:write`). Use a token with that scope. |
   | `502` with `error_type` `forge_forbidden` or `forge_unauthenticated` | `422` `forge_forbidden`: the forge refused the credential hub holds for the repository. Another token does not help; the credential on hub must be fixed. |
   | `404` or `405` | `502` `hub_unavailable`: hub does not serve label creation for the workspace. |
   | another `4xx` (not `429`) | `422` `forge_forbidden`, with hub's message. |
   | `429`, another `5xx`, a redirect, a transport failure, a timeout, or a body that is not hub's | `502` `hub_unavailable`. |

   A label counts as present when hub creates it (`201`) or says it already exists (`200`, `409`, or a
   `4xx` whose `error_type` is `already_exists` or whose message says "already exists").

A refused token is always `422`, never `401`, even if it is the same token you authenticated with.

Nothing is stored until every check passes. A refused `PUT` stores nothing, and a refused replacement
leaves the existing subscription as it was. Labels created before a refusal stay on the forge; the next
`PUT` finds them.

On success the token is encrypted and stored, and the service answers with the view:

- `201` when the workspace was not subscribed;
- `200` when it was. The subscription is replaced in full: `token_user` is this request's answer,
  `subscribed_by` is you, `created_at` is kept and `updated_at` is now.

Treat `200` and `201` alike as success. Subscribing an already-subscribed workspace again with the same
token is how you create any labels that are missing.

### Unsubscribe

`DELETE /api/v1/workspaces/{slug}` removes the subscription and its stored token, and answers `204`
with no body. A workspace that is not subscribed, or that you cannot see, is the same `404` as for
`GET`.

Before the subscription goes, the service:

1. Stops taking new work for the workspace. Creating a job answers `404`; answering or retrying a job
   answers `409` `not_subscribed`.
2. Cancels every queued, running or blocked job of the workspace, with `cancel_cause`
   `unsubscribed`. See [Cancel](#cancel).
3. Waits for the running attempts to stop, at most 60 seconds each.
4. Waits, while the token is still available, for the first try of each job's
   [cancel cleanup](#cancel), so that a blocked issue gets its request label back before the token is
   gone. This wait ends at most 60 seconds after step 3.

The request can therefore take a minute or two. If the removal itself fails, the answer is `500`, the
subscription stays, and the workspace takes jobs again.

The workspace's jobs are not deleted. You can still list and read them until [retention](#retention)
removes them. Labels on the forge are never renamed or deleted.

## Labels

A team can drive agent-fox from the issue tracker alone: put a label on an issue to ask for work, and
read the status labels and comments that agent-fox writes back.

### The label set

[Subscribing](#subscribe) a workspace creates these seven labels on its repository. The `af:` prefix
is fixed.

| Label | Kind | Colour | Description |
|---|---|---|---|
| `af:triage` | request | `0e8a16` | Ask agent-fox to triage this issue |
| `af:fix` | request | `0e8a16` | Ask agent-fox to fix this issue |
| `af:in-progress` | status | `fbca04` | agent-fox is working on this issue |
| `af:ready` | status | `1d76db` | Triaged: the diagnosis is in the body |
| `af:needs-human` | status | `d93f0b` | agent-fox stopped with a question; see the last comment |
| `af:landed` | status | `0e8a16` | A branch and a pull request (or a carry patch) exist |
| `af:failed` | status | `b60205` | agent-fox could not finish; see the last comment |

You add the request labels. agent-fox sets the status labels: an issue carries at most one of them at
a time (a fix in progress keeps `af:ready` until its outcome replaces it). See
[What the worker adds to an attempt](#what-the-worker-adds-to-an-attempt).

### Request labels

The service polls each subscribed workspace for open issues that carry `af:triage` or `af:fix`, every
`AF_POLL_INTERVAL` (5 minutes by default; `0` turns polling off for the workspace). See
[the label poll](configuration.md#the-label-poll).

Each new request becomes exactly one job, and the service then removes the label. It **consumes** the
label, so adding it again later is a new request. Pull requests, closed issues and issues without a
request label are never touched.

A job made from a label differs from an API job in three fields:

| Field | Value |
|---|---|
| `source` | `{"kind": "label", "by": "label af:fix"}` (or `label af:triage`). The forge's list does not say who added a label, so no person is named. |
| `request_label` | The label, with its prefix: `af:fix`. |
| `input` | `{"issue": <number>}`. |

Label jobs queue behind the workspace's other jobs in the order they are made. An issue that carries
both labels gets its triage job before its fix job. A fix label on a `carry_patch` workspace becomes a
job that fails as `unsupported`, as the same request through the API would.

When the poll sees label L (kind K) on issue N, the first rule that applies decides:

1. **A blocked job is waiting.** The workspace has a blocked job of kind K for N: the label is taken as
   an [answer from the issue](#answering-and-retrying-from-the-issue) (unless rule 4 or 5 applies to
   it first).
2. **A job is already open.** The workspace has another queued, running or blocked job of kind K for N,
   whoever made it: the label is removed and no job is made. An open job of the other kind does not
   count.
3. **The label was put back by a cancel.** A job made from this label was cancelled, and the cancel put
   the label back on the issue. The label is left alone until a person asks again: remove the label and
   add it back (no sooner than about 5 minutes after the cancel finished). Or use the API, which is the
   immediate way to ask again.
4. **The label was already taken.** An earlier removal of the label failed: the service removes it
   again and makes no second job.
5. **Someone else just took it.** Another poll cycle took the label after this listing was requested:
   it is left to the next cycle.
6. **The last job failed.** The latest job of kind K for N failed: it is
   [retried from the issue](#answering-and-retrying-from-the-issue).
7. **Otherwise it is a new request.** A job is made and the label is removed. Labelling a triaged issue
   `af:triage` again starts a new triage, which replaces the diagnosis and keeps the original report.

Every rule that consumes the label (a new job, an answer, a retry, a reply) records it first, so the
same label is not acted on twice while its removal is pending. If the forge keeps listing a label for
a moment after it was removed, a second job can still result; the window is small.

### Answering and retrying from the issue

You can move a job forward without the API: comment on the issue, then put the request label back.

**Which comments count.** Every comment agent-fox writes ends with a marker
(`<!-- agent-fox job=<id> attempt=<n> kind=… -->`). The comments that count are the ones after the
marker of the job's latest attempt: after its question for a blocked job, or after its failure
comment (or parked-fix summary) for a failed one. If no comment carries that marker, the comments made
since the attempt finished count. agent-fox's own comments and blank comments never count.

The counted comments are joined, in order, as `--- comment by <login> ---` blocks. The text is cut at
8192 bytes, with a closing line saying so.

| Situation | What happens |
|---|---|
| The job is **blocked** and comments count | The text answers the job, as `POST /api/v1/jobs/{id}/answer` with `{"answer": <text>}` would. The attempt's `trigger` is `{"kind": "answer", "by": "label af:fix"}`. |
| The job is **blocked** and no comment counts | No attempt is made. agent-fox replies once: "No answer found since the question; comment, then add the label `<L>` again." The label is removed. |
| The job **failed** | The job is retried, as `POST /api/v1/jobs/{id}/retry` would. The counted comments, if any, become the new attempt's `context`. |
| Hub cut the comment thread | No attempt is made. agent-fox replies that it could not read the whole thread and points to `af jobs answer` / `af jobs retry` and the API routes. The label is removed. |
| Hub is unavailable, or refuses the token or the forge credential | The label stays and the poll tries again next cycle. |

A reply that cannot be posted is logged, and the label is removed anyway.

## Jobs

A job is one request for `triage` or `fix` against one subscribed workspace. Ids look like
`job_` followed by 20 lowercase hex characters. All times are RFC 3339 in UTC with milliseconds
(`2026-10-22T08:00:00.000Z`).

### Job states

A job is **open** while it is `queued`, `running` or `blocked`, and **finished** once it is `done`,
`failed` or `cancelled`. It is **at rest** when it is blocked or finished and none of its attempts is
queued or running.

| From | To | When |
|---|---|---|
| (new) | `queued` | The job is created. |
| `queued` | `running` | An attempt is handed to a worker. |
| `running` | `done` | The attempt ends `done`, or `unverified` (except a parked fix). |
| `running` | `blocked` | The attempt asks a question (`needs_human`). |
| `running` | `failed` | The attempt fails and no automatic attempt follows. |
| `running` | `queued` | The service queues an automatic attempt after a brain failure. |
| `blocked` | `queued` | Someone answers. |
| `failed` | `queued` | Someone retries. |
| `queued`, `running`, `blocked` | `cancelled` | Someone cancels the job, or its workspace is unsubscribed. |

An infrastructure failure (hub down, no sandbox available) does not change the state: the service
hands the same attempt to a worker again after 30 seconds, then after 2 minutes. A third such failure
fails the attempt and the job.

### Create a job

`POST /api/v1/workspaces/{slug}/triage` and `POST /api/v1/workspaces/{slug}/fix` answer `202` with the
new [job document](#the-job-document): `state` `queued`, `source` `{"kind": "api", "by": <your hub
user>}`, `request_label` `null`.

```json
{"issue": "https://github.com/acme/widgets/issues/42", "context": ["The pager is in web/"]}
```

| Field | Triage | Fix | Rule |
|---|---|---|---|
| `issue` | one of `issue`, `text`, `file` | required | A positive integer, an issue URL (not a pull or merge request; at most 2048 bytes), or `owner/repo#N`. |
| `text` | one of `issue`, `text`, `file` | not accepted | Valid UTF-8, no NUL byte, not empty after trimming, at most 128 KiB (131072 bytes). Longer text is refused, not truncated. |
| `title` | optional, with `text` only | not accepted | One line, at most 256 bytes. |
| `file` | one of `issue`, `text`, `file` | not accepted | `{"name", "content"}`. `name` is a base name of 1 to 255 bytes with no `/` and no NUL byte; `content` follows the rule for `text`. |
| `context` | optional | optional | Notes for the agent: at most 10 strings, each non-empty after trimming and at most 8 KiB, at most 32 KiB in total. Not shown in the job document. |

The request body may be up to 1 MiB for a triage and 256 KiB for a fix (see
[Request bodies](#request-bodies)). A field that breaks its rule is `400` `invalid_request`, and the
message names it (`context: string 2 must not be empty`).

The checks run in this order, and a refused request stores nothing:

1. The slug, the caller and the caller's view of the workspace ([as for every `{slug}`
   route](#workspace-visibility)).
2. The body: `415`, `413`, `400`.
3. The workspace: `404` `not_found` when it is not subscribed or is being unsubscribed; `409`
   `token_unreadable` when its token cannot be decrypted; `409` `duplicate_job` when an open job of the
   same kind exists for the same issue number on the workspace (two fixes of one issue would collide on
   its branch).

The service checks only the issue *number* for duplicates. Whether the issue belongs to the
workspace's repository is checked when the attempt runs.

### The job document

Every route that returns a job returns this document. `GET /api/v1/jobs/{id}` adds `attempts`.

```json
{
  "id": "job_5f0c1a9e2b7d4c3a8e10",
  "workspace": "widgets",
  "kind": "fix",
  "input": {"issue": 42},
  "issue": 42,
  "source": {"kind": "api", "by": "7c0fd2d9-6a52-4c11-8d3e-5a0e9f4b3a10"},
  "request_label": null,
  "state": "done",
  "blocked": null,
  "attempt_count": 1,
  "current_attempt": 1,
  "result": {"status": "done", "exit_code": 0, "summary": "Fixed the off-by-one in the pager.", "error": null},
  "artifacts": [{"kind": "pull_request", "url": "https://github.example.com/acme/widgets/pull/9", "number": 9}],
  "side_effects": [],
  "labels": {"set": ["af:landed"], "removed": ["af:fix", "af:in-progress"], "restore": null, "cleanup": null},
  "cancel_cause": null,
  "usage": {"cost_usd": 1.25, "input_tokens": 1000, "output_tokens": 200, "turns": 7},
  "hub": null,
  "created_at": "2026-10-22T08:00:00.000Z",
  "started_at": "2026-10-22T08:00:01.000Z",
  "finished_at": "2026-10-22T08:04:12.000Z"
}
```

| Field | Meaning |
|---|---|
| `id`, `workspace`, `kind` | The job, its workspace, and `triage` or `fix`. |
| `input` | What the job was asked to do, as posted, without `context`: `{"issue": …}` (number, URL or `owner/repo#N` as given), `{"text", "title"}` or `{"file": {"name", "content"}}`. |
| `issue` | The issue number, or `null` for a text or a file. |
| `source` | `{"kind", "by"}`. `api` with the hub user who made it, or `label` with `by` = `label <request label>` (see [request labels](#request-labels)). |
| `request_label` | The request label the job consumed (`af:fix`), or `null` for an API job. |
| `state` | See [job states](#job-states). |
| `blocked` | While the job is blocked: `{"reason": "needs_human", "question", "options": [{"id", "label"}], "answer_with": "POST /api/v1/jobs/<id>/answer"}`, with option ids `A`, `B`, … in order. Otherwise `null`. |
| `attempt_count`, `current_attempt` | How many attempts the job has, and the number of the latest. |
| `result` | `{"status", "exit_code", "summary", "error"}` from the envelope of the latest attempt that finished, or `null` before any. `exit_code` matches the status: `done` 0, `failed` 1, `usage` 2, `needs_human` 3, `unverified` 4. |
| `artifacts`, `side_effects` | From the same envelope; `[]` when there are none. |
| `labels` | `{"set", "removed", "restore", "cleanup"}`, or `null` when all are empty. `set` and `removed` are the labels last written to the issue for this job. `restore` is a request label that belongs back on the issue after a cancel and is not back yet. `cleanup` is the state of the [cancel cleanup](#cancel): `"pending"`, `"done"`, `"label_not_set"`, or `null` when there was none. |
| `cancel_cause` | `cancelled` (someone cancelled it) or `unsubscribed` (its workspace was unsubscribed); `null` when the job is not cancelled. |
| `usage` | `cost_usd`, `input_tokens`, `output_tokens` and `turns`, summed over the finished attempts. |
| `hub` | `{"session_id", "run_id", "sandbox_id"}` of the running attempt; `null` otherwise. |
| `created_at` | When the job was made. |
| `started_at` | When the first attempt was first handed to a worker, or `null`. Later attempts do not move it. |
| `finished_at` | When the job last became `done`, `failed` or `cancelled`; `null` while it is open. Requeuing it (an answer, a retry, an automatic attempt) clears it. [Retention](#retention) counts from it. |

`attempts` lists the attempts in order:

| Field | Meaning |
|---|---|
| `n` | The attempt number, from 1. |
| `state` | `queued`, `running`, `done`, `needs_human`, `failed` or `cancelled`. |
| `trigger` | `{"kind", "by"}`. `kind` is `create`, `answer`, `retry`, or `auto` (the service retried after a brain failure; `by` is `agent-fox`). |
| `answer` | For an attempt started by an answer: `{"question", "option", "text"}`. `option` is the chosen id or `null`; `text` is the typed answer or the option's label. Otherwise `null`. |
| `context` | For an attempt started by a retry from the issue: the counted comments, as a list of strings. Otherwise `null`. |
| `deliveries` | How many times the attempt was handed to a worker. A handover after a restart or crash counts. Each delivery starts afresh. |
| `transient_failures` | How many infrastructure failures the attempt hit (at most 3). |
| `status`, `exit_code`, `summary`, `error` | From the attempt's envelope; `null` until it has one. |
| `usage` | `{"brain", "cost_usd", "cost_source", "input_tokens", "output_tokens", "turns"}` from the envelope once the attempt has ended; otherwise `null`. |
| `started_at`, `finished_at` | When the attempt was first handed to a worker and when it ended, or `null`. |

### How an attempt ends

The first rule that matches the attempt's envelope decides:

1. **`done`** makes the attempt and the job `done`. So does **`unverified`**, except a fix whose
   `result.outcome` is `parked`: it did not do what was asked, so it is `failed` and can be retried.
2. **`needs_human`** with a question makes the attempt `needs_human` and the job `blocked`. Options are
   relettered `A`, `B`, … in order.
3. **A retryable brain failure** (`failed` with `error.category` `brain` or `no_result` and
   `error.retryable` true) queues an **automatic attempt** (`trigger.kind` `auto`) with the same context
   and answers, unless the round has used up its attempts. A *round* is the latest attempt started by a
   person (create, answer, retry) plus the automatic attempts after it. The limit per round is the
   workspace's `AF_MAX_ATTEMPTS`: 2 by default, between 1 and 10.
4. **Anything else** is `failed`: another error category, a non-retryable error, a round that has used
   its attempts, or an attempt whose infrastructure retries are spent.

An attempt that ends without a usable envelope (the worker errored, crashed, or was cancelled) gets a
`failed` envelope written by the service, with category `internal` or `cancelled`. So every finished
attempt has an envelope and `result.exit_code` is always set.

### What the worker adds to an attempt

The worker runs `af triage` or `af fix` for the attempt and stores the envelope the loop produced.
It changes that envelope only by adding to its lists:

- **Artifacts.** `{"kind": "hub_session", "name": <session id>}` and `{"kind": "sandbox", "name":
  <sandbox id>}`. Artifacts with a `path` (`run_dir`, `brief`, `report`, …) point into a temporary clone
  on the worker that is deleted when the attempt ends: they record facts of the run but cannot be
  fetched. Use the [envelope and report routes](#list-and-read-jobs) instead.
- **Side effects.** Every label and comment the worker writes on the issue (`add_labels`,
  `remove_label`, `add_comment`), `done` or `failed`.
- **Warnings.** These never change the status:

  | Code | Meaning |
  |---|---|
  | `lease_shortened_timeout` | The run's timeout was cut to end two minutes before the sandbox lease expires. |
  | `lease_release_failed` | The sandbox lease could not be released; hub destroys it when it expires. |
  | `status_label_failed`, `comment_failed`, `request_label_failed` | Writing a label or comment to the issue failed. |
  | `hub_usage_failed`, `hub_events_failed`, `hub_session_failed` | Reporting the attempt to hub failed. |

The worker can also end an attempt with these error categories:

| Category | Meaning |
|---|---|
| `auth` | Hub refused or forbade the workspace token. Re-subscribe with `PUT /api/v1/workspaces/{slug}`. |
| `unsupported` | A fix on a `carry_patch` workspace, or with `AF_FIX_LAND=patch`. Carry-patch landing is not implemented. A triage on such a workspace runs normally. |
| `config` | A [workspace variable](configuration.md#workspace-variables-on-hub) is out of range; `fix_hint.var` names it. |
| `cancelled` | The attempt was cancelled before the loop produced an envelope. |

Infrastructure failures (hub unavailable, a sandbox or clone that cannot be had, a retryable `sandbox`,
`forge` or `hub` failure of the loop) produce no envelope: the attempt is handed over again (see
[job states](#job-states)).

**Labels and comments on the issue.** When the attempt starts the worker sets `af:in-progress`. When
it ends, it sets one status label and, where useful, writes one comment:

| Outcome | Label | Comment |
|---|---|---|
| Triage `done`, confidence `confirmed` or `probable` | `af:ready` | none (the diagnosis is in the body) |
| Triage `done`, confidence `suspected` or `not_reproducible` | `af:needs-human` | the reason |
| Any `needs_human` | `af:needs-human` | the question |
| Fix that landed | `af:landed` | a summary |
| Fix that landed with `AF_FIX_LAND=none` | none | a summary saying nothing was kept |
| Parked fix | `af:needs-human` | what failed and how to ask again |
| Any other failure | `af:failed` | the failure |

A failure that the service retries automatically, and an infrastructure failure, only remove
`af:in-progress`. Each comment carries a marker and is not posted twice. When label polling is on, the
question, failure and parked-fix comments explain how to answer or retry from the issue.

### Cancel

`POST /api/v1/jobs/{id}/cancel` (no body, or `{}`) answers `200` with the job. A queued, running or
blocked job becomes `cancelled` at once and `finished_at` is set. A job in any other state is `409`
`invalid_state`.

- A **queued** attempt is cancelled and never runs.
- A **running** attempt is told to stop. Its worker has **60 seconds** to release the sandbox, restore
  the issue's labels and return. The attempt is recorded `cancelled` when the worker returns, or after
  the 60 seconds (with an `ERROR` in the log).

A job is cancelled for one of two causes, kept in `cancel_cause`: `cancelled` (this route) or
`unsubscribed` (the workspace was [unsubscribed](#unsubscribe)). Closing an issue cancels nothing.

**Labels after a cancel.** A cancel puts the issue back the way it was before the job: `af:in-progress`
and the status labels this job set are removed, and the request label the job consumed is put back.
Labels from other jobs and labels set by people stay.

If the worker was running when the cancel arrived, it does this itself. Otherwise (the job was queued
or blocked, or the worker had not started properly) the service does it in the background, as the
**cancel cleanup**:

- `labels.cleanup` is `"pending"` while it runs. This route never waits for it.
- It is retried every minute while hub is unavailable, for up to an hour.
- On success `labels.cleanup` becomes `"done"` and `labels.restore` becomes `null`.
- If it gives up, or hub or the forge refuses, it ends `"label_not_set"` with an `ERROR` in the log, and
  `labels.restore` stays set while the request label is not on the issue.

A cleanup still pending when the service stops continues after the next start. Once a request label is
back on the issue, the poll holds it rather than treating it as a new request (see
[request labels](#request-labels), rule 3).

### Answer and retry

`POST /api/v1/jobs/{id}/answer` answers the question of a blocked job. Send exactly one of:

```json
{"answer": "The web front end; the API is fine."}
{"option": "B"}
```

The text is trimmed, must not be empty, and is at most 8192 bytes. The option must be one of the
question's ids. The route answers `202` with the job, queued again with `finished_at` cleared, and a new
attempt (trigger `answer`). The agent is given every question answered so far, with its options and
answer, in order. An answer by option is recorded with the option's label as its text.

`POST /api/v1/jobs/{id}/retry` (no body, or `{}`) re-runs a failed job. It answers `202` with the job,
queued again with a new attempt (trigger `retry`, `context` `null`). If another job has taken the issue
in the meantime, it is `409` `duplicate_job`.

Both routes are also available [from the issue](#answering-and-retrying-from-the-issue). Each attempt
is given the job's `context` strings, then the `context` of each attempt up to its own, so an automatic
attempt after a retry keeps the retry's comments.

Checks, in order. A refusal changes nothing.

1. The job exists and you can see its workspace (`404`, the same answer for both).
2. The body size and type (`413`, `415`).
3. The state: `409` `invalid_state` unless the job is `blocked` (answer) or `failed` (retry).
4. The workspace is subscribed and not being unsubscribed (`409` `not_subscribed`), and its token can
   be decrypted (`409` `token_unreadable`).
5. The body is exactly one valid answer or option (`400`).

Cancel, answer and retry only apply if the job is still in the state they checked, so of two requests
that arrive together, one wins and the other gets `409` `invalid_state`.

### List and read jobs

`GET /api/v1/jobs` lists the jobs of every workspace you can see that has jobs, subscribed or not.
`GET /api/v1/workspaces/{slug}/jobs` lists one workspace, and answers `404` if you cannot see it. If hub
is unavailable for a workspace the list needs, `GET /api/v1/jobs` answers `502` `hub_unavailable` with
no page.

Query parameters, each at most once. A parameter that is present must be valid, even if empty
(`400` `invalid_request` names the first bad one).

| Parameter | Value |
|---|---|
| `kind` | `triage` or `fix`. |
| `state` | `queued`, `running`, `blocked`, `done`, `failed` or `cancelled`. |
| `issue` | A positive integer, with no sign or leading zero. |
| `limit` | 1 to 200; default 50. |
| `cursor` | The `next_cursor` of the previous page. |

The answer is `{"jobs": [...], "next_cursor": "..."}`: jobs newest first (by `created_at`, then `id`),
without `attempts`, and the cursor for the next page, or `null` on the last page. Following the cursors
returns every matching job exactly once. Cursors are opaque; one the service did not issue is `400`.

`GET /api/v1/jobs/{id}` returns the job with its `attempts`.

`GET /api/v1/jobs/{id}/attempts/{n}/envelope` returns the envelope attempt `n` stored, byte for byte,
as `application/json`. `GET /api/v1/jobs/{id}/attempts/{n}/report` returns its `report.md` as
`text/markdown; charset=utf-8`.

All of these answer the same `404` `not_found` (`job not found`) for a job that does not exist, a job
in a workspace you cannot see, an attempt number that does not exist (`0`, `9` of 2, `01`, `x`), and an
attempt that has no envelope or report yet.

### The event stream

`GET /api/v1/jobs/{id}/events` answers `200` with `text/event-stream` and `Cache-Control: no-cache`.
Each event in the job's log is one frame:

```
id: 7
event: attempt_end
data: {"seq":7,"ts":"2026-10-22T08:04:12.000Z","job_id":"job_5f0c1a9e2b7d4c3a8e10","attempt":1,"type":"attempt_end","data":{"attempt":1,"state":"done","status":"done","exit_code":0}}

```

`data` is the event on one line, with keys in this order. `seq` numbers the events of the job 1, 2, 3,
… across all attempts, with no gaps. Events are stored before they are sent, so the log is always
complete.

- **Where it starts.** After the `since` query parameter; otherwise after the `Last-Event-ID` header
  (what a reconnecting `EventSource` sends); otherwise from the beginning. A value that is not a
  non-negative integer without sign, leading zero or whitespace is `400`.
- **Order.** The stored events after the starting point, then live events, with none missing and none
  repeated. Reconnect with the last `id` you saw to get exactly what you missed.
- **Keepalive.** A comment line, `: keepalive`, every 15 seconds.
- **The end.** The stream ends when the job is at rest: right after the replay if it already is,
  otherwise after the first live `job_state` with `at_rest` true. The last frame of a finished job is
  always such a `job_state`. To follow the job after an answer or a retry, open a new stream with
  `since`.
- **Slow clients.** Each stream buffers 256 events. A client that falls further behind is
  disconnected, and recovers by reconnecting with its last id. Shutting the service down closes every
  stream the same way.

| `type` | `data` |
|---|---|
| `job_state` | Every state change: `{"state", "attempt", "at_rest", "blocked"?, "result"?}`. `blocked` is the question when the job blocks; `result` is the finished attempt's result when it finishes. |
| `attempt_start` | `{"attempt", "delivery"}`: the attempt was handed to a worker for the `delivery`th time. |
| `attempt_end` | `{"attempt", "state", "status", "exit_code"}`: how the attempt ended. |
| other types | Progress the worker reports for the attempt (the loop's events and the worker's `step` events). Types match `^[a-z][a-z0-9_]*$`. |
| `invalid_event_type` | A worker event with a malformed type, or one that reuses `job_state`, `attempt_start` or `attempt_end`: `{"type", "data"}` as given. |

Worker data larger than 64 KiB as JSON is replaced by `{"truncated": true, "bytes": N}`, and data that
cannot be encoded by `{"error": "…"}`.

### Retention

A purge runs when the service starts and then every hour. It removes jobs that are `done`, `failed` or
`cancelled`, have nothing running and no pending cancel cleanup, and finished more than
`database.retain` ago (30 days by default; see [configuration.md](configuration.md#keys)). The job's
stored envelopes and reports go with it. Blocked jobs are never removed. After the purge the job's
routes answer `404`.

## Request bodies

A request body must be:

- `application/json` (parameters such as `charset=utf-8` are allowed), otherwise `415`
  `unsupported_media_type`;
- at most 64 KiB, or 1 MiB for a triage job and 256 KiB for a fix job, otherwise `413`
  `payload_too_large` (the job limits are six times what the fields may hold, so a valid body is never
  too large);
- one JSON value with nothing after it and no unknown fields, otherwise `400` `invalid_request`.

A refused request writes nothing.

## Errors

Every error is `application/json`:

```json
{"error": {"code": "unauthenticated", "message": "a Bearer token is required"}}
```

`code` is for programs and is stable; `message` is for people and may change. A message never contains
a token, a ciphertext, the key or an internal error. The body may gain an optional `error_type` field
in future; clients must accept it being present or absent.

| `code` | Status | When |
|---|---|---|
| `invalid_request` | 400 | A bad slug, malformed JSON, an unknown or mistyped field, trailing data, a bad `hub_token`, a job field that breaks its rule, an answer that is not exactly one valid answer or option, or a bad list parameter or `since`. The message names the field. |
| `unauthenticated` | 401 | No usable `Authorization: Bearer` header, or hub does not accept the token. |
| `not_found` | 404 | The workspace is not subscribed or you cannot see it; the job does not exist or is in a workspace you cannot see; the attempt, envelope or report does not exist; or the route does not exist. Each case gets the same answer, so nothing leaks. |
| `method_not_allowed` | 405 | The route does not take the method. The `Allow` header lists those it does. |
| `invalid_state` | 409 | The action does not apply to the job's current state; the message names the state. |
| `duplicate_job` | 409 | An open job of the same kind exists for the same issue on the workspace; the message names it. |
| `not_subscribed` | 409 | Answer or retry on a job whose workspace is not subscribed or is being unsubscribed. (Creating a job there is `404`.) |
| `token_unreadable` | 409 | The workspace token cannot be decrypted with this deployment's key, and the request would run an attempt with it. Subscribe the workspace again. |
| `payload_too_large` | 413 | The body is over the route's limit. |
| `unsupported_media_type` | 415 | The body is not `application/json`. |
| `invalid_hub_token` | 422 | At subscribe: hub refused the token, or it cannot see the workspace. |
| `forge_forbidden` | 422 | At subscribe: a label could not be created. The token lacks hub's forge scope, the forge refused hub's credential, or hub refused the label. The message says which, with hub's message. |
| `internal` | 500 | A bug or a database failure. Details are in the log, not the answer. |
| `hub_unavailable` | 502 | Hub could not answer. Nothing was cached or written. |
| `not_ready` | 503 | `GET /readyz`: the database does not answer. |

## Logging

The service writes one JSON log record per line to stderr. Each request produces one record with
`msg` `request` and `method`, `path` (without the query), `status`, `duration` (nanoseconds) and, once
the caller is known, `user` (the hub user id). The `Authorization` header, request and response
bodies, tokens, ciphertext and the key are never logged. For the other records, see
[cli.md](cli.md#logs).
