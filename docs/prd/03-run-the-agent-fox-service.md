# Run the agent-fox service

Status: **proposed** (2026-10-20). Nothing here is implemented. This is
step 3 of [PRD 01](01-triage-and-fix-forge-issues-as-a-service.md)'s
order of work (§10): the process, its API, its queue and its worker,
over [PRD 02](02-rebuild-triage-and-fix-on-the-toolbelt.md)'s pipeline
and hub's sandbox and forge APIs. Written to PRD 01's decisions D1 to D4,
D9 and D10. Details left to settle at spec time are marked *spec time*.

## Intent

PRD 02 gives one machine the two agents and the toolbelt. This PRD gives
them a place to run unattended: `agent-fox`, one process with an API, a
durable queue and workers, that takes a job for a hub workspace, leases a
box from hub, runs PRD 02's loop in it, writes the result back through
hub's forge API, and reports cost and events to hub. It holds a hub
credential per workspace and nothing else; `af` is its client.

Its triggers in this PRD are the API and a poll of the forge through hub
for the request labels. The label protocol as a whole, hub's event stream
and the automatic follow-ups are PRD 04; carry patches are PRD 05.

## Goals

1. `agent-fox` starts from one `config.toml`, validates a hub credential
   on every request with a cached answer, and serves the jobs API of PRD
   01 §6.2 with SSE.
2. A subscribed workspace gets its jobs run: one attempt at a time per
   workspace, in a box hub leased, with PRD 02's loop unchanged, every
   forge byte through hub, every commit and push by the worker with the
   workspace PAT, every attempt as a hub session with its usage and
   events.
3. A job survives the service restarting: the record and the queue are
   SQLite, an attempt interrupted mid-way is retried as PRD 01 §11.10
   says, and an orphaned lease is released.
4. `af` drives it: subscribe, triage, fix, jobs, watch, answer, retry,
   cancel; `af --local` keeps working with no service.
5. Everything is offline-testable: an `httptest` hub serving its
   documented API plus the sandbox and forge endpoints, with leases
   pointing at an in-process `gatewayd.Server` over `local`.

## Non-goals

- **Labels created by the service, hub's event stream, `AF_AUTO_FIX`,
  answering by re-labelling.** PRD 04.
- **Carry patches.** PRD 05. A `carry_patch` workspace is accepted at
  subscribe and its fix jobs refuse at preflight with `category:
  unsupported` until PRD 05.
- **More than one attempt at a time per workspace**, position, priority,
  pause. The hub tasks draft's; `AF_CONCURRENCY` later.
- **A web client.** The API is the contract; `af` is the client.
- **Holding a vendor key or a forge token.** D4.

## Background: what exists

| Need | Exists | Where |
|---|---|---|
| The loop | PRD 02 | `af`'s packages, called in-process by the worker |
| A queue | `aflib/jobqueue` (aflib PRD 01) | SQLite, retry, per-key serialization, crash recovery, progress |
| A box | hub PRD 01 | `POST /workspaces/:slug/sandboxes` → a lease; `gateway.Dial` |
| The forge | hub PRD 02 | issues, labels, comments, pulls under the workspace |
| Identity | hub | `GET /api/v1/user`, `GET /workspaces/:slug` |
| Cost and events | hub | sessions, usage, audit ingestion |
| The clone | hub | `git clone <hub_url>` with the PAT; push the same way |
| A bound credential | hub PRD 03 | optional; a plain PAT works with wider reach |
| The CLI pattern | hub's `afc` | the credential file, the client, one JSON document, exit codes |

## 1. Configuration

PRD 01 §6.1 as decided: `[server]`, `[hub]` with `auth_cache_ttl`,
`[database]`, `[secrets].key_file`, `[workers].count`. Per-workspace
settings are hub variables read at the start of every job:
`AF_LABEL_PREFIX`, `AF_BRAIN` (and per agent), `AF_MODEL`,
`AF_MAX_BUDGET_USD`, `AF_MAX_TURNS`, `AF_TIMEOUT`, `AF_MAX_ATTEMPTS`,
`AF_POLL_INTERVAL`, `AF_SANDBOX_PROFILE`, `AF_FIX_LAND`.

## 2. Identity

Every request carries `Authorization: Bearer <hub credential>`. The
service calls `GET /api/v1/user` with it, caches the answer by the
token's SHA-256 for `auth_cache_ttl` (default 60 s, at most 15 m),
caches a refusal for the same TTL, evicts on a 401 from hub, and uses
the user id as `source.by`. Workspace visibility is `GET
/workspaces/:slug` with the caller's token, cached the same way; 404 is
404. The service has no user table.

## 3. Subscriptions

```
PUT    /api/v1/workspaces/:slug      {hub_token, label_prefix?}  → 201
GET    /api/v1/workspaces            the subscriptions the caller may see (hub's answer, cached)
GET    /api/v1/workspaces/:slug
DELETE /api/v1/workspaces/:slug
```

`PUT` validates the PAT against hub (`GET /workspaces/:slug` with it must
answer 200), checks it carries the scopes §6 needs (`GET /user/tokens`
with the caller's token, when the PAT is the caller's; else trusted and
found out at the first failure, *spec time*), stores it encrypted with
`[secrets].key_file` (AES-GCM, one key per deployment), and records the
settings. A second `PUT` replaces the token. `DELETE` cancels the
workspace's open jobs and forgets the token. Labels on the forge are
PRD 04's; in this PRD the request labels are expected to exist.

## 4. Jobs

The record and the API of PRD 01 §6.2: `POST /triage`, `POST /fix`,
`GET /jobs`, `GET /jobs/:id`, `cancel`, `answer`, `retry`, `events`
(replay then tail, SSE), `attempts/:n/envelope`, `attempts/:n/report`.
States `queued | running | blocked | done | failed | cancelled`.

- A job is one `jobqueue` job of type `attempt`, key `<slug>` (one
  running attempt per workspace), nonce the job id plus the attempt
  number, payload the job id. The queue's retry policy covers retryable
  failures (`sandbox_unavailable`, a hub 5xx); `AF_MAX_ATTEMPTS` bounds
  the brain-level attempts, which are separate rows.
- `answer` from `blocked` and `retry` from `failed` create the next
  attempt with the answer as `--context`; `cancel` stops the running
  attempt's context, releases the lease and sets the request label back
  (PRD 04 makes that visible; here it is the record).
- Events: every `toolio` event of the attempt (`run_start`, `step`,
  `tool_call`, `run_end`, heartbeat) is stored with a per-job sequence
  and pushed to the job's SSE subscribers; a client reconnecting with
  `since` gets the gap from the store.

## 5. Triggers in this PRD

- **The API**: `POST /triage` and `/fix` with the caller's identity.
- **The poll**: every `AF_POLL_INTERVAL`, `GET /workspaces/:slug/issues?
  state=open&labels=<prefix>triage` and the same for `fix` through hub's
  forge API with the workspace PAT; each issue not the input of an open
  job becomes a job with `source.kind: label`, and the request label is
  removed through hub (D5: consumed). `0` disables the poll.

## 6. The worker

PRD 01 §6.4, with PRD 02's commands as the implementation:

```
claim      the queue hands over an attempt; af:in-progress through hub's forge API; POST /sessions {workspace_slug, archetype, model, run_id}
resolve    GET /workspaces/:slug (PAT); GET .../vars/resolved; GET .../issues/:n (the thread)
clone      git clone <hub_url> --branch <base> into the attempt's directory, the PAT as the password; gitx.New over it
lease      POST /workspaces/:slug/sandboxes {profile: AF_SANDBOX_PROFILE, purpose: triage|fix, labels: {job, attempt}}
box        gateway.Dial(lease.gateway_url, lease.token); Create(sandbox.Spec{Labels}); the lease's spec applies; Sync(LocalDir{clone}) with .git
run        PRD 02's loop over that box: brief, brain (AF_BRAIN), measure, export, the host-side commit  — the same functions af --local calls
land       git push <hub_url> <branch>; standard: POST /workspaces/:slug/branches/<branch>/push (hub PRD 05), then POST .../pulls; the body or the comment; the labels
report     the envelope and the events file as attempt artifacts; POST /sessions/:id/usage per model turn; POST .../runs/<run_id>/events/batch; POST /sessions/:id/complete
release    DELETE /workspaces/:slug/sandboxes/:id; the job's state
```

The worker holds the workspace PAT and the lease token; it never holds a
vendor key or a forge token, and a test asserts it over every request it
sends and every `Spec.Env` it builds (PRD 01 §11.9).

**Failure handling**: a lease hub refuses is retryable with hub's
reason; a box gone unavailable is retryable once with a new lease; a
brain ending without a submission is `no_result` and counts an attempt;
a forge write failing after the push is a warning on a landed run; a
worker killed mid-attempt leaves the queue's crash recovery to re-run
it, and the next run reuses the clone's branch and skips the brain when
`submission.json` is present and valid (*spec time*: re-measure versus
trust, PRD 01 §11.10). On start, the service lists its leases (`GET
/workspaces/:slug/sandboxes` per subscription) and releases any whose
attempt is not running.

## 7. The CLI

```
af login                                 afc's credential file
af workspace subscribe <slug> --hub-token-file f [--prefix af:] | list | get | unsubscribe
af triage <input> --workspace <slug> [--context …] [--wait]
af fix    <input> --workspace <slug> [--context …] [--wait]
af jobs list [--workspace] [--state] | get <id> | watch <id> | cancel <id> | answer <id> "<text>"|--option B | retry <id>
af triage|fix … --local                  PRD 02's loop, now explicit
```

`--wait` follows the job's SSE stream to a terminal state and exits with
the envelope's exit code. `AF_URL` or `--server` names the service; with
neither and no `--local`, `af` says which to use.

## 8. State

`$XDG_STATE_HOME/agent-fox/agent-fox.db`: `subscriptions`, `jobs`,
`attempts`, `events`, beside `jobqueue`'s `jobs` table (*spec time*: a
table prefix, since both are named `jobs`); attempt artifacts under
`attempts/<id>/`. Retention: terminal jobs and their artifacts are kept
30 days by default (`[database].retain = "720h"`).

## 9. Acceptance criteria

Offline, against an `httptest` hub and an in-process gateway over `local`:

1. `agent-fox` starts, `PUT /workspaces/widgets` with a PAT the fake hub
   accepts stores it encrypted (the row is not plaintext), and `GET
   /workspaces` lists it for the subscriber and not for another user.
2. `POST /workspaces/widgets/fix {issue}` creates a `queued` job; the
   worker leases a box, syncs the clone, runs the scripted brain, pushes
   `fix/…` to the fake hub's git server, calls the push route and
   `POST /pulls`, posts the comment, sets `af:landed`, opens and completes
   a session with usage, posts an events batch, releases the lease, and
   the job reads `done` with the pull request in `artifacts`.
3. The poll finds an issue labelled `af:triage`, creates one job (not two
   on the next poll), removes the label through hub, and the job runs
   triage to `af:ready` with the body rewritten.
4. A blocked job answered with `POST /jobs/:id/answer` runs a second
   attempt whose brief carries the answer; `retry` from `failed` runs
   another; `cancel` on a running job ends it, releases the lease and
   restores the request label.
5. Two jobs on one workspace run one at a time; jobs on two workspaces
   run concurrently with `[workers].count = 2`.
6. The service killed between `export` and `land` and restarted: the
   attempt is re-run by the queue, reuses the branch, and lands without
   running the brain again; the orphaned lease is released at start.
7. A token validated once is not revalidated within `auth_cache_ttl`; a
   401 from hub evicts it; `GET /jobs/:id` for a job on a workspace the
   caller cannot see on hub is 404.
8. No request the worker sends and no `Spec.Env` it builds carries a
   vendor key or a forge token; the subscription PAT and the lease token
   are the only credentials observed.
9. `af fix <issue> --workspace widgets --wait` exits with the job's
   envelope code; `af jobs watch` streams the events; `af fix --local`
   runs with the service down.

## 10. What is given up, stated plainly

- **The service needs hub PRDs 01 and 02 and aflib PRD 01 to run at
  all.** PRD 02's `--local` is the path that does not wait.
- **One attempt per workspace at a time**, and no ordering beyond first
  come first served.
- **Two `jobs` tables** in one database unless prefixed; a detail the
  spec settles.
- **The poll spends the workspace's forge rate limit** through hub; the
  interval is the lever, and PRD 04's events reduce the need.

## Documentation

| Change | Update |
|---|---|
| the API, the job schema, the errors | `docs/api.md` |
| `agent-fox`'s config, the `AF_*` variables | `docs/configuration.md` |
| `af`'s service commands, `--workspace`, `--local`, `--wait` | `docs/cli.md` |
| the process, the worker pipeline, the state | `docs/architecture.md` (the service section) |
| setup | `README.md` |
