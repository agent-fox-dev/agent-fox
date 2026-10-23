# The agent-fox API

`agent-fox` is the long-running service of agent-fox (see [cli.md](cli.md#the-agent-fox-command-line)
for how to start it and [configuration.md](configuration.md#the-agent-fox-service) for its
configuration). It serves JSON over plain HTTP: a loopback address by default, and TLS, if you want it,
is a reverse proxy's job. It has no users of its own: [hub](https://github.com/agent-fox-dev/hub) says who
is calling and which workspaces they may see, and the service asks it.

This release has the probes, the subscription of workspaces and jobs: triage and fix requests against a
subscribed workspace, queued, ordered, cancelled, answered and retried, with their attempts, their stored
envelopes and a replayable stream of events. What runs an attempt is a runner behind an interface; the
binary runs the worker, which carries out each attempt against the hub workspace of the job (see
[What the worker adds to an attempt](#what-the-worker-adds-to-an-attempt)). The poll and the `af` client
come in later specs and add their routes to this document.

## Routes

| Route | Needs a token | What it does |
|---|---|---|
| `GET /healthz` | no | `200` `{"status":"ok"}` while the process serves. |
| `GET /readyz` | no | `200` `{"status":"ready"}` when the database answers a ping, else `503` `not_ready`. |
| `GET /version` | no | `200` with the version document: `name` (`agent-fox`), `version`, `commit`, `build_time`, `go_version`. It is the document of `agent-fox --version`. |
| `GET /api/v1/workspaces` | yes | The subscriptions whose workspace the caller sees on hub, sorted by slug. |
| `GET /api/v1/workspaces/{slug}` | yes | One subscription. |
| `PUT /api/v1/workspaces/{slug}` | yes | Subscribe a workspace, or replace its subscription. |
| `DELETE /api/v1/workspaces/{slug}` | yes | Remove a subscription, after cancelling the open jobs of the workspace. |
| `POST /api/v1/workspaces/{slug}/triage` | yes | Make a triage job. Body up to 1 MiB. |
| `POST /api/v1/workspaces/{slug}/fix` | yes | Make a fix job. Body up to 256 KiB. |
| `GET /api/v1/workspaces/{slug}/jobs` | yes | List the jobs of one workspace, subscribed or not. |
| `GET /api/v1/jobs` | yes | List the jobs of every workspace the caller sees. |
| `GET /api/v1/jobs/{id}` | yes | One job, with its attempts. |
| `POST /api/v1/jobs/{id}/cancel` | yes | Cancel a job that is queued, running or blocked. |
| `POST /api/v1/jobs/{id}/answer` | yes | Answer the question of a blocked job. |
| `POST /api/v1/jobs/{id}/retry` | yes | Ask again for a failed job. |
| `GET /api/v1/jobs/{id}/attempts/{n}/envelope` | yes | The `envelope.json` an attempt stored, byte for byte. |
| `GET /api/v1/jobs/{id}/attempts/{n}/report` | yes | The `report.md` an attempt stored. |
| `GET /api/v1/jobs/{id}/events` | yes | The events of a job as `text/event-stream`. |

Every response is `application/json`, except the `204` of `DELETE`, which has no body, the report of an
attempt (`text/markdown; charset=utf-8`) and the stream of events (`text/event-stream`). A path that is
not canonical (`//healthz`, `/x/../healthz`) is a `404`; the service redirects nothing.

## Authentication

Every route under `/api/v1` needs a header `Authorization: Bearer <token>`. The scheme is matched
without regard to case and the token must not be empty. The token is a credential of hub for the
person calling; the service forwards it to hub, and to nobody else (through the proxy that `HTTP_PROXY` or
`HTTPS_PROXY` name, if the environment has one), to learn whom it belongs to.

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
`GET`.

Before the row goes, the workspace is marked as being unsubscribed, which refuses new work for it (a request
to make a job is `404`, as for any workspace that is not subscribed; one to answer or retry a job is `409`
`not_subscribed`), and every job of the workspace that is
queued, running or blocked is cancelled with the cause *unsubscribed*, which a runner reads as a cancel. The
request waits for the runners of those jobs to return, for at most 60 seconds each (see [Cancel](#cancel)). A
removal that then fails is `500`; the subscription stays and the workspace takes jobs again. The jobs of the
workspace are not removed: they are listed and read until the retention purge removes them.

## Jobs

A **job** is one request for `triage` or `fix` against one subscribed workspace. It is made in the state
`queued` with its first **attempt**, and each attempt is one try of a runner, which ends with a stored
**envelope** (the JSON that `af` writes, see [cli.md](cli.md#the-envelope)). A job is *open* while it is
`queued`, `running` or `blocked`, and *finished* once it is `done`, `failed` or `cancelled`. It is *at rest*
when it is blocked or finished and no attempt of it is running or queued.

| From | To | Because |
|---|---|---|
| (new) | `queued` | The job was made. |
| `queued` | `running` | An attempt of it was handed to a runner. |
| `running` | `done` | The attempt came to `done`, or `unverified` other than a parked fix. |
| `running` | `blocked` | The attempt asked a question (`needs_human`). |
| `running` | `failed` | The attempt failed and no automatic attempt follows (see below). |
| `running` | `queued` | An automatic attempt was queued after a failure of the brain; a transient failure of the runner, which asks for the same attempt to be handed over again after 30 seconds and then 2 minutes, does not change the state. |
| `blocked` | `queued` | A person answered. |
| `failed` | `queued` | A person asked again (`retry`). |
| `queued`, `running`, `blocked` | `cancelled` | A person cancelled the job, or its workspace was unsubscribed. |

A job in any open state can be cancelled. The ids are `job_` and 20 lowercase hex characters, and every time is
RFC 3339 in UTC with milliseconds (`2026-10-22T08:00:00.000Z`).

### Making a job

`POST /api/v1/workspaces/{slug}/triage` and `POST /api/v1/workspaces/{slug}/fix` answer `202` with the job
(`state` `queued`, `source` `{"kind":"api","by":<the caller's hub user>}`, `request_label` `null`). The body is
read as described under [request bodies](#request-bodies), with these limits: **1 MiB for a triage, 256 KiB
for a fix and 64 KiB for everything else**. They are six times what the fields may hold, which is the most
that a JSON encoder makes of a byte (`\u00XX`), so that a body that is valid is never `413`.

| Field | Triage | Fix | Rule |
|---|---|---|---|
| `issue` | one of `issue`, `text`, `file` | required | A positive integer, an issue URL that is not a pull request or a merge request (at most 2048 bytes), or `owner/repo#N`. Only the number is kept for the duplicate check; which repository it belongs to is for the runner to check against the workspace. |
| `text` | with `title` optional | not accepted | Valid UTF-8, no NUL byte, not empty after trimming, at most 128 KiB (131072 bytes). A text over it is refused, not truncated. |
| `title` | with `text` only | not accepted | One line, at most 256 bytes. |
| `file` | `{"name", "content"}` | not accepted | `name` is a base name of 1 to 255 bytes with no `/` and no NUL byte; `content` follows the rule of `text`. |
| `context` | optional | optional | At most 10 strings, each not empty after trimming and at most 8 KiB, and at most 32 KiB together. Notes for the runner; they are not part of the document of the job. |

A field that breaks its rule is `400` `invalid_request` and the message names it (`context: string 2 must not be
empty`). Nothing is stored for a request that is refused. The checks come in this order: the slug, the caller
and the caller's sight of the workspace (as for every route with a `{slug}`), the body (`415`, `413`, `400`),
then the workspace: `404` `not_found` when it is not subscribed or is being unsubscribed, `409`
`token_unreadable` when its token does not open, and `409` `duplicate_job` when a job of the same kind is open
for the same issue number on the workspace (two fixes of one issue would collide on its branch).

The job is stored with its first attempt, and the event `job_state` `queued`, in one transaction, and only then
is the attempt put in the queue. If that fails the answer is still `202`, a `WARN` is logged, and the sweep
that runs every minute puts it in the queue.

### Jobs that a request label makes

The routes are not the only way to make a job. For every subscribed workspace the service asks hub, every
`AF_POLL_INTERVAL` (see [the label poll](configuration.md#the-label-poll)), for the open issues that carry the
workspace's **request labels**, `<label_prefix>triage` and `<label_prefix>fix` (`af:triage` and `af:fix` for the
prefix `af:`), so that a team asks for a triage or a fix by labelling an issue. Each new request becomes exactly one job,
and the service then removes the label through hub: it **consumes** it, so that adding it again is a new request.

Such a job is made as the routes make one, with these fields: `source` `{"kind": "label", "by": "label af:fix"}` (`by`
names the label, `label af:triage` for a triage; the list of issues does not say who added it, so no person is named),
`request_label` the full label, prefix included (`af:fix`), `issue` and `input` `{"issue": 42}`. It is queued behind the
other jobs of its workspace in the order they were made, and an issue that carries both labels gets its triage job queued
before its fix job. A fix label on a `carry_patch` workspace becomes a job that the worker refuses, as the same request
through the route would. Pull requests, closed issues and issues that carry neither label are never touched.

What becomes of a label on a listed issue is decided by the first of these that applies:

1. **Absorbed.** The workspace has an open job (`queued`, `running` or `blocked`) of the same kind for the issue, whoever
   made it, an API job included: the label is removed, no job is made, and the service logs the absorbed label at `INFO`
   with the workspace, the kind, the issue and the job. An open job of the other kind does not absorb it. A label on an
   issue whose job `failed` is a new request.
2. **Held.** A job the poll made for the label was cancelled while one of its attempts was in a runner (an unsubscribe
   included), and the worker's cancel cleanup put the label back on the issue ([cancel](#cancel)). The label is left in
   place until a person asks again: the service does nothing about it until a complete listing of the label that does not
   contain the issue was requested at least 5 minutes after the runner of the job returned, which releases the hold, so
   removing the label and adding it back is a new request. The API is the immediate way to ask again. A job cancelled
   while it was queued, while it waited out a backoff or while it was blocked holds nothing.
3. **Already taken.** The removal of the label from the issue failed after the job was made (or after a label that the poll absorbed
   was not removed), so the removal is still owed: the label is removed again, and no second job is made. A removal that hub
   answers with not found is done.
4. **A new request.** The job is made, the poll's record of it is written, and the label is removed.

A request is therefore never run twice by the poll: a label on an issue the service is already working on is absorbed, a
label it has already taken is removed and not taken again, and a label the service put back is held. The poll's records
are kept in the state file until nothing is owed and nothing holds, and outlive the retention purge of their jobs.

### The job document

Every route that shows a job shows this document, a list entry included; `GET /api/v1/jobs/{id}` adds
`attempts`.

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
  "labels": {"set": ["af:fixed"], "removed": ["af:fix"], "restore": null},
  "usage": {"cost_usd": 1.25, "input_tokens": 1000, "output_tokens": 200, "turns": 7},
  "hub": null,
  "created_at": "2026-10-22T08:00:00.000Z",
  "started_at": "2026-10-22T08:00:01.000Z",
  "finished_at": "2026-10-22T08:04:12.000Z"
}
```

| Field | Meaning |
|---|---|
| `id`, `workspace`, `kind` | The job, its workspace and `triage` or `fix`. |
| `input` | What the job was asked about, as it was posted and without the `context`: `{"issue": …}` (as given: the number, the URL or `owner/repo#N`), `{"text", "title"}` or `{"file": {"name", "content"}}`. |
| `issue` | The issue number, or `null` for a text or a file. |
| `source` | `{"kind", "by"}`: `api` for a request of this API, and who made it; `label` for a request label that the poll found, and `by` is `label <the request label>` (see [jobs that a request label makes](#jobs-that-a-request-label-makes)). The triggers of later specs add their own kinds. |
| `request_label` | The request label that the trigger consumed when it made the job, or `null`. Always `null` for a job of this API; the label of the poll's, prefix included (`af:fix`). |
| `state` | `queued`, `running`, `blocked`, `done`, `failed` or `cancelled`. |
| `blocked` | While the job is blocked: `{"reason": "needs_human", "question", "options": [{"id", "label"}], "answer_with": "POST /api/v1/jobs/<id>/answer"}`, with the ids `A`, `B`, … in order; otherwise `null`. |
| `attempt_count`, `current_attempt` | How many attempts the job has, and the number of the latest. |
| `result` | `{"status", "exit_code", "summary", "error"}` of the latest attempt that came to something, from its envelope; `null` before any. `exit_code` is that of the status (`done` 0, `failed` 1, `usage` 2, `needs_human` 3, `unverified` 4). |
| `artifacts`, `side_effects` | Those of the same envelope; `[]` when there are none. |
| `labels` | `{"set", "removed", "restore"}`, or `null` when none has a value. `set` and `removed` are the labels as the runner last reported them. `restore` is the request label that a cancel says belongs back on the issue and that no runner has reported writing; the service writes no label itself. |
| `usage` | The sum of `cost_usd`, `input_tokens`, `output_tokens` and `turns` over the attempts that have finished. |
| `hub` | `{"session_id", "run_id", "sandbox_id"}` of the attempt that is running, as its runner reported them; `null` otherwise. |
| `created_at` | When the job was made. |
| `started_at` | When an attempt of the job was first handed to a runner, or `null`. A later attempt does not move it. |
| `finished_at` | When the job last entered `done`, `failed` or `cancelled`: every such transition sets it, a cancel of a queued or blocked job included. `null` while the job is `queued`, `running` or `blocked`; an answer, a retry or an automatic attempt that queues the job again clears it. The retention is counted from it. |

`attempts` lists the attempts in the order of their numbers:

| Field | Meaning |
|---|---|
| `n`, `state` | The number from 1, and `queued`, `running`, `done`, `needs_human`, `failed` or `cancelled`. |
| `trigger` | `{"kind", "by"}`: `create`, `answer` (a person answered), `retry` (a person asked again) or `auto` (the service tried again after a failure of the brain; `by` is `agent-fox`). |
| `answer` | For an attempt that an answer started: `{"question", "option", "text"}`, where `option` is the id chosen or `null`, and `text` is what the person typed or the label of the option. Otherwise `null`. |
| `deliveries` | How many times the attempt was handed to a runner. A handover after a restart or a crash counts. A `deliveries` above 1 means a resume: the worker carries on what the delivery before it left, if there is anything to carry on (see [Warnings](#what-an-attempt-comes-to)). |
| `transient_failures` | How many times the runner reported an infrastructure failure for the attempt (at most 3). |
| `status`, `exit_code`, `summary`, `error` | Those of the attempt's envelope; `null` until it has one. |
| `usage` | `{"brain", "cost_usd", "cost_source", "input_tokens", "output_tokens", "turns"}` of the envelope, once the attempt has ended; otherwise `null`. |
| `started_at`, `finished_at` | When the attempt was first handed to a runner and when it ended, or `null`. |

### What an attempt comes to

The first of these rules that matches the envelope of an attempt decides:

1. `done` makes the attempt and the job `done`. `unverified` does so too, except for a fix whose `result.outcome`
   is `parked`, which did not do what was asked and is `failed`, so that `retry` can ask again.
2. `needs_human` with a question makes the attempt `needs_human` and the job `blocked`, with the options lettered
   `A`, `B`, … whatever ids the envelope gave them.
3. A `failed` envelope whose `error.category` is `brain` or `no_result` and whose `error.retryable` is true
   queues an **automatic attempt** (`trigger.kind` `auto`) with the same context and answers, while the round
   has made fewer attempts than the runner's maximum. A *round* is the latest attempt whose trigger is not `auto`
   and the automatic attempts after it; the maximum is what the runner reports (`AF_MAX_ATTEMPTS`), 2 when it
   reports none, and never less than 1 or more than 10. Each human action (create, answer, retry) opens a round
   of its own.
4. Everything else is `failed`: another category, `retryable` false, a round that has used its attempts, and an
   attempt whose transient failures are spent.

An attempt that ends without a usable envelope (the runner returned an error, panicked, returned an envelope that
is not one, or was cancelled) gets an envelope written by the service: `failed`, with the category of the error
(`internal` for a plain error or a panic, `cancelled` for a cancel), so that every finished attempt has one
and `result.exit_code` is always defined.

### What the worker adds to an attempt

The worker runs the loop of `af triage` or `af fix` in the service's process and amends the envelope it wrote only by
adding to three lists; every other field of the stored envelope is the loop's, byte for byte.

- **Artifacts.** `{"kind": "hub_session", "name": <session id>}` once hub opened a session for the attempt, and
  `{"kind": "sandbox", "name": <sandbox id>}` once it leased one. The other artifacts that name a path (`run_dir`,
  `brief`, `report`, `transcript`, …) are paths on the worker, in a clone that is removed when the attempt returns:
  they stay in the envelope as facts of the run and cannot be retrieved. The envelope and the report text are what
  the service keeps.
- **Error categories.** `hub` is a failure of hub itself, and is a transient error, with no envelope, when hub is
  unavailable; a hub that refuses the token, or forbids a call, is `auth`, with a message that points to
  `PUT /api/v1/workspaces/{slug}`. `unsupported` is, besides the placeholder's, a `fix` on a `carry_patch` workspace or
  with `AF_FIX_LAND=patch` (carry-patch landing comes in a later release; a triage on such a workspace runs). `config` is
  a hub variable outside its range, with `fix_hint.var` naming it. `cancelled` is an attempt that was cancelled before
  the loop made an envelope. A loop that fails with a retryable `sandbox`, `forge` or `hub` error is not an envelope:
  the attempt is handed over again (see [What an attempt comes to](#what-an-attempt-comes-to)).
- **Warnings.** `lease_shortened_timeout` (the timeout was cut to end two minutes before the sandbox lease
  expires), `lease_release_failed` (the lease could not be released; hub destroys it when it expires),
  `label_prefix_ignored` (`AF_LABEL_PREFIX` is set and differs from the `label_prefix` of the subscription, which is
  the prefix used), `status_label_failed`, `comment_failed` and `request_label_failed` (a write of the mirror to the
  issue failed, with a side effect of the state `failed`), and `hub_usage_failed`, `hub_events_failed` and
  `hub_session_failed` (a report to hub failed). A warning never changes the status.
  A delivery that carries an attempt on, or begins again after it could not (and one that had nothing to carry on, or only went on in a replaced box, adds none), adds `attempt_resumed`, which names the handover count, the stage it
  picked up from (`outcome`, `published`, `applied`, `submitted` or `brain`) and whether the brain was skipped; at the
  stage `outcome` it is the only change to the envelope that was stored. `resume_discarded` says a delivery could not
  carry on what the one before it left, with the reason, and began again from a fresh clone; `box_replaced` says
  the sandbox was lost during the delivery and names it and the one that replaced it; `journal_unwritable` says the
  journal of the attempt could not be written, and the delivery went on from what it held; `brain_spend_unrecorded`
  says a brain run was cut short by the death of the process, so that what it cost is not known and counts as nothing.
- **Side effects.** Every label and comment the worker writes to the issue: `add_labels`, `remove_label` and
  `add_comment`, `done` or `failed`.
- **Labels.** The result of the attempt reports the labels it set and removed, as the net of the attempt (the
  status label of the outcome is set and `in-progress` is removed), which is what `labels` of the job shows. The
  status labels are `in-progress`, `ready`, `needs-human`, `landed` and `failed`, with the prefix of the
  subscription.

### Cancel

`POST /api/v1/jobs/{id}/cancel` (no body, or `{}`) answers `200` with the job. A job that is `queued`, `running`
or `blocked` becomes `cancelled` at once, with `finished_at` set; a job in another state is `409`
`invalid_state`. A queued attempt is cancelled and never runs. The context of a running attempt is cancelled
with the cause *cancelled*, and its runner has **60 seconds** to release what it holds, restore the request label
and return. The attempt is recorded `cancelled` when the runner returns, or after the 60 seconds if it has not,
with an `ERROR` in the log; either way the runner keeps its workspace's place and its worker slot until it
actually returns. When the job is cancelled, `labels.restore` names the request label that belongs back on the
issue, if the job consumed one and no runner has reported writing it.

### Answer and retry

`POST /api/v1/jobs/{id}/answer` takes `{"answer": "<text>"}` or `{"option": "<id>"}`, exactly one: the text is
trimmed, not empty and at most 8192 bytes, and the id is one of the options of the question. It answers `202` with
the job, `queued` again with `finished_at` cleared, and attempt *n*+1 (trigger `answer`) queued; the runner is
given every question answered so far, with its options and the answer, in order. The text of an answer by option is
the label of the option.

`POST /api/v1/jobs/{id}/retry` (no body, or `{}`) answers `202` with the job for a `failed` job, queued again
with attempt *n*+1 (trigger `retry`). If another job has taken the issue meanwhile, it is `409` `duplicate_job`.

Both check, in this order: the job exists and its workspace is one the caller sees (`404`, one answer for both);
a body that is too large or not `application/json` (`413`, `415`), as soon as it is read; the state (`409`
`invalid_state`, naming it: `blocked` for an answer, `failed` for a retry); the workspace is subscribed and not
being unsubscribed (`409` `not_subscribed`) and its token opens (`409` `token_unreadable`); and last that the body
is exactly one valid answer or option (`400`). A refusal changes nothing. A cancel, an answer and a retry change the job only
if it is still in the state they looked at, so of two requests that arrive together one is applied and the other
is `409` `invalid_state`.

### Reading jobs

`GET /api/v1/jobs` lists the jobs of every workspace that has jobs, subscribed or not, that hub shows the caller
(through the same cache as every route), and `GET /api/v1/workspaces/{slug}/jobs` lists one workspace, `404` when
the caller cannot see it. A workspace that is no longer subscribed still lists the jobs it has left. If hub is
unavailable for a workspace that the list needs, the answer of `GET /api/v1/jobs` is `502` `hub_unavailable` and
no page; the workspaces it needs are those that have a job the filter matches.

Both take these query parameters, each at most once, and a parameter that is there must be valid, an empty one
included (`400` `invalid_request`, naming it, the first that is wrong in this order):

| Parameter | Value |
|---|---|
| `kind` | `triage` or `fix`. |
| `state` | `queued`, `running`, `blocked`, `done`, `failed` or `cancelled`. |
| `issue` | A positive integer, written without a sign or a leading zero. |
| `limit` | An integer from 1 to 200; 50 when it is left out. |
| `cursor` | The `next_cursor` of a page. |

The answer is `{"jobs": [...], "next_cursor": "..."}`: the jobs of the page, newest first by `created_at` and then
by `id` (descending, both), as list entries without `attempts`, and the cursor of the page after it, or `null`
on the last page. Following the cursors yields every job that matches exactly once. A cursor is opaque: it is the
position of the last job of a page, and one that the service did not give is `400`.

`GET /api/v1/jobs/{id}` answers `200` with the job and its `attempts`. `GET /api/v1/jobs/{id}/attempts/{n}/envelope`
answers `200` with the `envelope.json` that attempt *n* stored, byte for byte, as `application/json`, and
`GET /api/v1/jobs/{id}/attempts/{n}/report` the `report.md`, as `text/markdown; charset=utf-8`. All of them answer
one `404` `not_found` (`job not found`) for a job that does not exist, a job on a workspace the caller cannot
see, a number that is not an attempt's (`0`, `9` of 2, `01`, `x`), an attempt that has no envelope yet, and an
attempt that has no report: nobody learns from an answer what exists.

### The event stream

`GET /api/v1/jobs/{id}/events` answers `200` `text/event-stream` with `Cache-Control: no-cache`, and the
caller sees the same `404` as for any file of a job. Each event of the log of the job is a frame:

```
id: 7
event: attempt_end
data: {"seq":7,"ts":"2026-10-22T08:04:12.000Z","job_id":"job_5f0c1a9e2b7d4c3a8e10","attempt":1,"type":"attempt_end","data":{"attempt":1,"state":"done","status":"done","exit_code":0}}

```

The `data` is the event on **one line**, with the keys in this order. `seq` is the number of the event in the log
of the job: 1, 2, 3, … across all its attempts, without a gap. An event is stored before it is published, so
the log is whole whatever happens to a stream.

- **Where it starts.** After the `since` query parameter (an integer of at least 0); else after the
  `Last-Event-ID` header, which is what a reconnecting `EventSource` sends; else at 0, the beginning. A value that
  is not an integer of at least 0, written without a sign, a leading zero or white space, is `400`.
- **Order.** The stored events after the starting point, in order, and then the live ones, with none missing and
  none sent twice. A client that reconnects with the last id it saw receives exactly the events it missed.
- **Keepalive.** A comment, `: keepalive`, every 15 seconds.
- **The end.** If the job is at rest when the replay ends (the latest `job_state` of its log says `at_rest`), the
  stream ends there. Otherwise it ends after the first live `job_state` whose `at_rest` is true, and the last
  frame of a finished job is always such a `job_state`. A `job_state` with `at_rest` false (a cancel whose runner
  has not returned) does not end it. A client that wants more after an answer or a retry opens a new stream
  with `since`.
- **Slow clients.** Each stream has a buffer of 256 events. A client that falls further behind is disconnected,
  without the runner or the other clients waiting for it, and recovers by reconnecting with its last id. The
  shutdown of the service closes every stream in the same way.

| `type` | `data` |
|---|---|
| `job_state` | Emitted for every transition of the job: `{"state", "attempt", "at_rest", "blocked"?, "result"?}`. `blocked` is the question when the job blocks, `result` that of the finished attempt when it finishes. |
| `attempt_start` | `{"attempt", "delivery"}`: the attempt was handed to a runner, for the *delivery*th time. |
| `attempt_end` | `{"attempt", "state", "status", "exit_code"}`: how the attempt ended. |
| anything else | What a runner reports about its attempt, under a type of its own that matches `^[a-z][a-z0-9_]*$`. |
| `invalid_event_type` | An event of a runner whose type is malformed, or is `job_state`, `attempt_start` or `attempt_end` (the service's own): `{"type": <the type given>, "data": <the data given>}`. |

Data of a runner that is larger than 64 KiB as JSON is replaced by `{"truncated": true, "bytes": N}`, and data
that cannot be encoded by `{"error": "…"}`. An event that cannot be stored is logged at `WARN` and dropped; it never
fails an attempt.

### How long a job is kept

A purge runs when the service starts and then every hour. It removes the jobs that are `done`, `failed` or
`cancelled`, have no running attempt and no runner still in progress, and whose `finished_at` is older than
`database.retain` (see [configuration.md](configuration.md#the-keys)): first the directory
`attempts/<job id>/` beside the database, which holds the `envelope.json` and `report.md` of each attempt, then
the rows of the job. A job whose directory cannot be removed keeps its rows, a `WARN` names it, and the next purge
tries again. A blocked job is never removed, and neither is a job cancelled less than `retain` ago, though
its question is older. After the purge, the routes answer `404` for the job.

## Request bodies

A request body must be `application/json` (parameters such as `charset=utf-8` are allowed), at most 64
KiB (1 MiB for a triage and 256 KiB for a fix, see [Making a job](#making-a-job)), one JSON value with
nothing after it, and with no field that the route does not know. Otherwise the request is refused before
anything is written: `415` `unsupported_media_type`, `413` `payload_too_large`, or `400` `invalid_request`.

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
| `invalid_request` | 400 | The slug is not valid; the body is malformed JSON, has an unknown field, a field of the wrong type or data after the value; `hub_token` or `label_prefix` is not valid; a field of a job request breaks its rule; an answer is not exactly one valid answer or option; a parameter of a list or the `since` of a stream is not valid. The message names the field or the parameter. |
| `unauthenticated` | 401 | There is no usable `Authorization: Bearer` header, or hub does not accept the token. |
| `not_found` | 404 | The workspace is not subscribed or the caller cannot see it (one answer for both), or the route does not exist. For a job: it does not exist, or its workspace is one the caller cannot see (one answer for both), or the attempt, its envelope or its report is not there. |
| `method_not_allowed` | 405 | The route does not take the method; the `Allow` header lists those it does. |
| `payload_too_large` | 413 | The body is larger than the limit of its route: 64 KiB, 1 MiB for a triage, 256 KiB for a fix. |
| `unsupported_media_type` | 415 | The body is not `application/json`. |
| `invalid_state` | 409 | The action does not apply to the job in the state it is in: the message names the state. |
| `duplicate_job` | 409 | A job of the same kind is open (queued, running or blocked) for the same issue on the workspace; the message names it. |
| `not_subscribed` | 409 | The workspace of an existing job is not subscribed, or is being unsubscribed, and the action needs it: answer and retry. Making a job on such a workspace is `404`. |
| `token_unreadable` | 409 | The subscription's token does not open with this deployment's key (`token_state` `unreadable`), and the request would make an attempt run with it: making a job, answer and retry. Subscribe the workspace again. |
| `invalid_hub_token` | 422 | At subscribe, hub refused the token, or does not show the workspace to it. |
| `internal` | 500 | A bug or a failure of the database. The panic or the error is in the log, not in the answer. |
| `hub_unavailable` | 502 | Hub could not say. Nothing was kept or written. |
| `not_ready` | 503 | `GET /readyz`: the database does not answer. |

## What the service logs

Everything on stderr is a JSON record (`log/slog`), except the plain-text usage that a command line the
service cannot parse gets. Each request is one record with `msg` `request` and
`method`, `path` (without the query), `status`, `duration` (in nanoseconds) and, once the request has
shown who is calling, `user`. The `Authorization` header, request and response bodies, tokens,
ciphertext and the key are never logged. See [cli.md](cli.md#the-agent-fox-command-line) for the other
records.
