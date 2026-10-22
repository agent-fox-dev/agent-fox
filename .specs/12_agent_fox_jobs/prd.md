---
spec_id: "12"
spec_name: "agent_fox_jobs"
title: "agent-fox jobs: the job and attempt records, the per-workspace queue, the jobs API and its event stream"
status: "active"
created_at: "2026-10-21T20:45:10.458238Z"
updated_at: "2026-10-21T20:45:10.458238Z"
intent_hash: "ad748469564016ee6e893d4005c40809c2a5fe9c4d20ea8b8437fe68cb210f6c"
schema_version: 2
source: "docs/prd/03-run-the-agent-fox-service.md"
---
## Intent

Give `agent-fox` its jobs. A job is the durable record of one triage or fix request against a subscribed hub workspace, together with the attempts that carry it out and an event log a client can follow. This spec owns four things:

- the job and attempt state machine;
- the queue it runs on (`aflib/jobqueue`), with one attempt at a time per workspace and at most `[workers].count` in flight overall;
- the jobs HTTP API, with server-sent events (SSE) that replay the stored log and then tail it;
- retention.

Each attempt is carried out behind a `Runner` interface. What an attempt does is the business of later specs: the hub session, the lease, the clone, the loops and the forge writes.

## Goals

1. Against the fake hub of `agent_fox_service_core`, with a fake runner that returns a `done` envelope:
   - `POST /api/v1/workspaces/widgets/fix {"issue": 42}` answers 202 with a `queued` job;
   - the runner is called once, for attempt 1;
   - `GET /api/v1/jobs/:id` then reads `done`, with the envelope's artifacts, summed usage, `result.exit_code` 0 and one attempt;
   - `GET /api/v1/jobs/:id/attempts/1/envelope` returns the envelope the runner gave, byte for byte as stored.
2. A runner that ends `needs_human` leaves the job `blocked` with the question and its lettered options. `POST /jobs/:id/answer {"option":"B"}` then starts attempt 2, and the runner is given the question, its options and B's label as the answer.
3. `retry` on a `failed` job starts a new attempt. A fix whose envelope is `unverified` with `result.outcome` `parked` leaves the job `failed`, so `retry` applies to it. An `unverified` envelope with any other outcome leaves the job `done`.
4. `cancel` on a running job:
   - cancels the runner's context, and the runner can tell from it that the job was cancelled, not that the service is stopping;
   - makes the job read `cancelled` at once, with `finished_at` set;
   - ends the job's event stream with a `job_state` event whose `at_rest` is true, once the runner returns;
   - lets the next job on the same workspace start only after the runner has returned, even when the runner overruns the 60 s grace.
5. With `[workers].count = 2`:
   - two jobs on one workspace are never in the runner at the same time;
   - jobs on two workspaces are in the runner at the same time;
   - with three workspaces, never more than two attempts run at once.
6. A failed envelope whose `error.category` is `brain` or `no_result` and whose `error.retryable` is true is followed by another attempt without anyone asking. This repeats up to the maximum the runner reports (default 2), and then the job is `failed`. A retryable failed envelope of any other category (for example `sandbox` or `forge`) fails the job at once, and so does any envelope with `retryable: false`.
7. A runner that reports a transient failure carrying `toolio.Failure{Category: "sandbox", Retryable: true}` is handed the same attempt again after each injected backoff. The third transient failure ends the attempt with category `sandbox` and the job `failed`, and no attempt 2 exists. A handover after a restart does not count as a transient failure.
8. `GET /jobs/:id/events?since=k` delivers events `k+1…` in order, with no gap and no duplicate, then the live events, and closes once the job is at rest. A client that reconnects with the last `id` it saw gets exactly the events it missed.
9. Restart on the same database file:
   - if the service is stopped while the fake runner holds attempt 1, the restarted service hands the runner the same attempt 1 with a handover count of 2, and no attempt 2 row exists;
   - jobs that were still queued at the stop run after the restart.
10. Unsubscribe and retention:
    - `DELETE /api/v1/workspaces/widgets` cancels the workspace's queued, running and blocked jobs, and while it runs a new job for `widgets` is refused;
    - with an injected clock, the purge removes done, failed and cancelled jobs whose `finished_at` is older than `[database].retain`, along with their attempt directories. A job cancelled while queued is one of them;
    - the purge keeps blocked jobs, and keeps a job cancelled while blocked until `retain` has passed since the cancel.
11. Visibility:
    - `GET /jobs/:id` for a job on a workspace the caller cannot see on hub answers 404, with the same body as for an id that does not exist;
    - `GET /api/v1/jobs` answers 502 `hub_unavailable`, with no partial page, when hub is unavailable for one of the workspaces it needs.
12. Request bodies: the routes accept a fix request with the largest valid `context`, and a triage request with the largest valid text and context, even when every byte of their strings is JSON-escaped as `\u00XX`.
13. `make check` passes offline: loopback `httptest` only, no hub, no key, no container engine.

## Non-goals

- **The attempt pipeline** belongs to `agent_fox_worker`: the hub session, workspace variables, the thread, the clone, the lease, the gateway box, the triage and fix loops, the push and pull request, usage and events posted to hub, the carry_patch refusal, and the no-vendor-key test. This spec ships only the runner contract (R4) and a placeholder runner. The contract places obligations on the runner that replaces the placeholder: one brain-level attempt per attempt, infrastructure failures reported as transient, the PAT read at the start. `agent_fox_worker` implements and tests them.
- **Recovery inside an attempt** belongs to `agent_fox_worker_recovery`: reusing the clone's branch, skipping the brain when a valid `submission.json` exists, the one retry of `sandbox_unavailable` with a new lease, and releasing orphaned leases. This spec does two things for it: it hands an interrupted attempt over again, and it tells the runner how many times that attempt has been handed over.
- **The label poll** belongs to `agent_fox_poll`. This spec provides two Go entry points for it. The first creates a job with `source.kind: label` and records the request label the poll consumed. The second looks up the open job for an issue.
- **`af` as a client** belongs to `af_service_client`: `af jobs …`, `--workspace`, `--wait`, `AF_URL`.
- **Writing labels on the forge.** This spec writes no label. When a job whose trigger consumed a request label is cancelled, the spec records in the job that the label belongs back (R6). For a running attempt, the runner's cleanup restores the label. For a queued or blocked job, applying the record on the forge is PRD 04's label protocol, which is outside this split.
- **Hub's event stream and the `hub_events` table** (PRD 04), and **carry patches** (PRD 05).
- **More than one attempt per workspace at a time**, and also priority, position, pause and reordering. First come, first served per workspace.
- **Per-user ownership of jobs.** Anyone hub lets see a workspace may read, cancel, answer and retry its jobs.
- **A queue of our own.** Durability, claiming, delayed retry and crash recovery come from `aflib/jobqueue`. On top of it the adapter adds only what R4 lists: per-workspace order, the nonce no-op and the handover count. If the module lacks one of the queue's own capabilities, this spec does not build it (Design Decision 23).
- **Changes to `af`'s envelope or loops**, and a web client. A runner that runs the fix loop with one in-run attempt passes a parameter that already exists. It changes nothing in the loop.

## Background

PRD 03 (`docs/prd/03-run-the-agent-fox-service.md` §4, §8) and PRD 01 (§6.2 API and job shape, §6.5 state) define the job record and its API. Spec `agent_fox_service_core` is written (`.specs/11_agent_fox_service_core/`), but its code is not in the tree yet. It delivers what this spec builds on:

- `cmd/agent-fox`;
- `config.toml` loading, which refuses unknown keys and exits 2 when the configuration is bad;
- one SQLite file with `af_`-prefixed tables and append-only migrations;
- the hub client;
- caller identity and workspace visibility, cached;
- the `/api/v1` mux with the error envelope `{error: {code, message, error_type?}}`, its code table, and a 64 KiB request body limit;
- subscriptions, with the PAT sealed and a `token_state` of `ok` or `unreadable`;
- an in-process read of a subscription's PAT by slug, which fails once the slug is not subscribed or the token does not open.

The core answers `GET /api/v1/workspaces` with 502 `hub_unavailable` and no partial list when hub fails for any subscription. It leaves jobs, `[workers].count`, `[database].retain` and "unsubscribe cancels open jobs" to this spec.

What exists in this repository that this spec reuses:

- **`internal/toolio/envelope.go`.** `Envelope` holds `schema_version`, `tool`, `run_id`, `status`, `exit_code`, `summary`, `input`, `result`, `needs_human`, `error`, `warnings`, `artifacts`, `side_effects`, `next`, `usage`, `started_at`, `finished_at` and `duration_ms`. The statuses are `done`, `failed`, `usage`, `needs_human` and `unverified`, and `ExitCode` maps them to 0, 1, 2, 3 and 4. `ErrorInfo.Retryable` exists.
- **Which failures are retryable today.**
  - Brain failures are retryable (`internal/brain/decide.go:240`), and so is `no_result` (`decide.go:254`; `cmd/af/triage.go:917` for a submission that no longer holds).
  - So are sandbox failures to prepare, sync or export a box (`internal/box/session.go:171,184,299,328`; `internal/box/podman.go:62`).
  - So are forge rate limits and lost connections at triage land (`internal/triage/land.go` `refuse`, lines 350 and 354).
  - The in-box `submit` command's refusal is retryable too (`internal/submission/command.go:125`). It goes back to the brain inside the attempt, so it is not what a loop ends with.
  - Usage, preflight, budget and timeout failures are not retryable.
- **How `af fix` ends.**
  - A change parked because no attempt was left ends `unverified`, with outcome `parked` (`internal/fix/park.go:107`).
  - A change landed under `--no-verify` is also `unverified`, but with outcome `landed` (`docs/cli.md`, "How a fix run ends").
  - `fix.Result` carries the outcome as the JSON field `outcome`.
- **`internal/toolio/events.go`.** An event line is `{seq, ts, run_id, type, data}`. The shell emits `run_start`, `warning` and `run_end`. Commands emit `step`, `tool_call` and `heartbeat` through `Run.Emit(typ, data)`. `internal/brain/report.go` (`Report`: `Emit`, `Warn`, `Artifact`, `SetUsage`) is the pattern for a recorder. `Run.Warn` keeps a malformed warning code as `invalid_warning_code`, with the original code quoted in the message (`internal/toolio/app.go:169-174`).
- **Questions and options.**
  - `newNeedsHuman` (`internal/toolio/app.go:735`) letters options `A`, `B`, … in order.
  - A report is `report.md` (`RunDir.WriteReport`).
  - Time stamps are RFC 3339 UTC with milliseconds (`stamp`).
  - `toolio.MaxInputBytes` is 131072.
- **Issue references.** `issuex.ParseIssueURL` accepts GitHub and GitLab issue, pull request and merge request URLs, and it sets `IssueRef.IsPullRequest` for the last two. `af triage` and `af fix` both refuse a pull or merge request (`internal/triage/preflight.go:114`, `cmd/af/fix_brief.go:101`). No parser for `owner/repo#N` exists in the repository.
- **`af fix` already makes attempts inside one run.**
  - `--attempts` defaults to 2, with a maximum of 5 (`cmd/af/fix.go:33-34`). `internal/fix/loop.go` `RunLoop` re-attempts after a retryable failure.
  - When its attempts run out with nothing applied, `Finish` returns the last failure, which is still retryable (`internal/fix/finish.go` `exhausted`).
  - Triage runs its brain once (`cmd/af/triage.go:884`, `Attempt: 1`).
  - Earlier questions are found in run directories (`fix.FindEarlier`). A service attempt starts from a fresh clone, so questions and answers have to live in the job record.
- **Grace precedents.** `fixGrace` is 30 s (`cmd/af/fix.go:38`): the time a fix run has to put the tree back and park. `toolio`'s default grace is 10 s (`internal/toolio/run.go:13`), and the revert check's is 10 min (`cmd/af/revert_check.go:31`).
- **`aflib/jobqueue`.** The README lists it in the pinned aflib (`go.mod`: `aflib v0.0.0-20261019184100-07adcb691832`). Its source cannot be read from this repository. `go.sum` lists the aflib module and its `go.mod`, and no SQLite driver anywhere in the graph. That suggests jobqueue works on a caller-supplied `*sql.DB` (inferred).

## Requirements

### R1. Configuration

The core's configuration gains two keys. They follow the core's rules: an out-of-range value or a malformed duration exits 2 before listening, with an `ERROR` log record that names the key.

- **`[workers].count`** is an integer, default 2, from 1 to 64. It is the number of attempts in flight across all workspaces.
- **`[database].retain`** is a Go duration, default `720h`, from `1h` to `8760h`. It is how long a finished job is kept.

Attempt artifacts live in an `attempts/` directory beside the database file. The service creates that directory with mode 0700 when it is missing.

### R2. Records

New migrations, appended to the core's list, create three tables:

- `af_jobs`: one row per job.
- `af_attempts`: one row per attempt, keyed by job id and attempt number.
- `af_events`: the per-job log, keyed by job id and `seq`.

`aflib/jobqueue` creates its own tables in the same file. The `af_` prefix keeps the two sets apart.

**Ids and times.** A job id is `job_` followed by 20 lowercase hex characters drawn from `crypto/rand`. Attempt numbers start at 1. Times are RFC 3339 UTC with milliseconds.

**The job document.** Every jobs route serves the PRD 01 §6.2 shape:

- `id`, `workspace`, `kind` (`triage` or `fix`);
- `input`: as posted, without the context;
- `issue`: the issue number, or null;
- `source`: `{kind, by}`;
- `request_label`: the request label the trigger consumed when it created the job, or null. It is always null for a job created through the API;
- `state`: one of `queued`, `running`, `blocked`, `done`, `failed`, `cancelled`;
- `blocked`: `{reason: "needs_human", question, options: [{id, label}], answer_with: "POST /api/v1/jobs/<id>/answer"}`, or null;
- `attempt_count` and `current_attempt`;
- `result`: `{status, exit_code, summary, error}` from the latest finished attempt's envelope, or null;
- `artifacts` and `side_effects`: from that envelope, `[]` when there are none;
- `labels`: `{set, removed, restore}`, or null when none of the three has a value:
  - `set` and `removed` are as the runner last reported them;
  - `restore` is the request label that a cancel says belongs back on the issue and that no runner has reported writing (R6), or null;
- `usage`: the sum of `cost_usd`, `input_tokens`, `output_tokens` and `turns` over every finished attempt;
- `hub`: `{session_id, run_id, sandbox_id}` of the current attempt, or null;
- `created_at`;
- `started_at`: when an attempt of the job was first claimed, or null;
- `finished_at`: when the job last entered `done`, `failed` or `cancelled`. It is set by every such transition, a cancel from `queued` or `blocked` included. It is null while the job is `queued`, `running` or `blocked`, and it is cleared when an answer, a retry or an automatic attempt queues the job again.

`GET /jobs/:id` adds `attempts`. For each attempt it lists:

- `n` and `state` (`queued`, `running`, `done`, `needs_human`, `failed`, `cancelled`);
- `trigger`: `{kind, by}`, where `kind` is `create`, `answer`, `retry` or `auto`;
- `answer`, or null;
- `deliveries`: how many times the attempt has been handed to a runner;
- `transient_failures`: how many transient failures it has had (R5);
- `status`, `exit_code`, `summary` and `error`;
- `usage`;
- `started_at` and `finished_at`.

### R3. Creating jobs

**Routes.**

- `POST /api/v1/workspaces/:slug/triage` takes exactly one of `{issue}`, `{text, title?}` or `{file: {name, content}}`, plus an optional `context`.
- `POST /api/v1/workspaces/:slug/fix` takes `{issue, context?}`.

Both answer 202 with the job document.

**Validation.** An invalid body answers 400 with a message that names the field. The core's decoder refuses unknown fields.

| Field | Rule |
|---|---|
| `issue` | One of three forms: a JSON number that is a positive integer; a string that is a GitHub or GitLab issue URL that `issuex.ParseIssueURL` accepts and does not mark as a pull or merge request; or a string `<path>#<N>`, where `<path>` is two or more `/`-separated segments of `[A-Za-z0-9_.-]` and `N` is a positive integer. A string is at most 2048 bytes. Anything else is refused: a pull or merge request URL, any other URL, any other string. |
| `text` | Non-empty after trimming, valid UTF-8, no NUL byte, at most `toolio.MaxInputBytes` bytes. |
| `title` | Optional. One line, at most 256 bytes. |
| `file.name` | A base name of 1 to 255 bytes, with no `/` and no NUL. |
| `file.content` | The same rules as `text`. |
| `context` | An array of at most 10 strings. Each is non-empty after trimming and at most 8 KiB, and together they are at most 32 KiB. |

The rules above are about decoded values. The jobs layer takes the number from a URL or from `owner/repo#N`. Checking the repository part against the workspace's repository belongs to the worker's preflight, and so does refusing a number the forge says is a pull request.

**Body limits.** Each route's limit is at least six times its largest valid decoded body, plus room for the JSON syntax. A body whose strings are escaped byte by byte as `\u00XX` therefore still fits.

- The triage route accepts up to 1 MiB: 128 KiB of text or file content, 32 KiB of context, and a 256-byte title or a 255-byte name.
- The fix route accepts up to 256 KiB: a 2048-byte issue and 32 KiB of context.
- Every other route keeps the core's 64 KiB. Its largest valid body is an 8 KiB answer.

**Refusals.**

| Condition | Answer |
|---|---|
| The caller cannot see the workspace on hub, or the workspace is not subscribed or is being unsubscribed (R6) | 404 |
| The subscription's token is unreadable | 409 `token_unreadable` |
| An issue that is already the input of an open job of the same kind on the workspace (open means queued, running or blocked) | 409 `duplicate_job`, with a message that names that job's id |

**Creation.**

- The job row, the attempt 1 row (trigger `create`) and the `job_state` event for `queued` are stored together or not at all.
- The queue item is enqueued after they are stored. If the enqueue fails, the answer is still 202: the job is stored and `queued`. A `WARN` is logged, and the sweep (R9) enqueues the item within a minute.
- `source` is `{kind: "api", by: <hub user id>}`, and `request_label` is null.

**Go entry points.** The jobs layer exports two entry points for the later triggers:

- **Create a job** with a given source (kind `label`, `event`, `rebuild` or `triage`, and its `by`). When the trigger consumed a request label, the call also passes that label, and the job records it as `request_label`. The validation and refusals are the routes'.
- **Look up the open job** for a (workspace, kind, issue number).

### R4. The queue and the runner

**The queue item.** Each attempt is one `aflib/jobqueue` item:

- type `attempt`;
- key: the workspace slug;
- nonce: `<job id>:<n>`. Enqueuing an attempt the queue already holds is a no-op, whether the item is pending, delayed or claimed;
- payload: `{"job_id", "attempt"}`.

**Ordering.**

- At most `[workers].count` attempts are in a runner at once.
- Attempts of one workspace run one at a time, in the order they were enqueued.
- An attempt waiting out a transient backoff (R5) keeps its workspace's place: no later attempt of that workspace starts before it. The wait does not take one of the `[workers].count` slots.
- A runner that has overrun its cancel grace (R6) keeps its workspace's place and its slot until it returns.

**The adapter.** `aflib/jobqueue` is used at an aflib version that has the package, and aflib is bumped if the pinned commit lacks it. The adapter may add three things on top of the module, when the module lacks them: the per-workspace order above, the nonce no-op, and the handover count. Durability, claiming, delayed retry and crash recovery are the module's. If the module lacks one of them, implementation stops and an erratum (`docs/errata/12_*.md`) records the gap.

**Claiming.** When an attempt is handed over, three things happen together or not at all: the attempt moves to `running`, its `deliveries` goes up by one, and the job moves to `running`. The runner is not called, and the item is acknowledged, in three cases:

- the job is gone;
- the job is no longer queued or running;
- the attempt is no longer queued or running.

**What the runner is given.** The attempt's context, and:

- the job id, the attempt number, and how many times this attempt has been handed over (1 on the first delivery);
- the workspace, the kind and the input;
- the request's context strings;
- every question answered so far, in order, each with its options and the answer text;
- the source and the request label;
- the absolute directory `attempts/<job id>/<n>/`, created before the call.

**Why it was stopped.** The context is cancelled when the attempt has to stop, and its cause tells the runner why:

- a cancel;
- an unsubscribe, which also reads as a cancel, so a runner that checks for a cancel handles both;
- a shutdown, which does not read as a cancel.

**What the runner reports while it runs.** It reports through a recorder that is safe for concurrent use:

- events for the job's log (R8);
- the attempt's hub references (session id, run id, sandbox id), which the job shows in `hub` while the attempt runs.

**What the runner returns.** One of two things:

- a result: the envelope, the report text (empty when there is none), the labels it set and removed, and the most attempts a round may make (0 means 2);
- an error. The runner can mark the error transient, which asks for the same attempt to be handed over again (R5).

**What every runner owes the contract.** `agent_fox_worker`'s runner implements these obligations, and its tests check them:

1. **One attempt is one brain-level attempt.**
   - The runner runs the brain's loop once per attempt. For `fix`, that is the loop with one in-run attempt, its rounds included.
   - It does not start another brain-level attempt after a failure. Further attempts belong to the jobs layer (R5 rule 5), bounded by the maximum the runner returns.
2. **Infrastructure failures are transient errors, not failed envelopes.** Those failures are:
   - a box or lease that cannot be had, synced or exported;
   - hub being unavailable or answering 5xx;
   - a forge rate limit or a lost forge connection.

   A failed envelope says what a brain-level attempt came to.
3. **The runner reads the workspace PAT when the attempt starts** and keeps it for the attempt's life, cleanup included. An unsubscribe removes the subscription while a cancelled attempt may still be cleaning up (R6).
4. **Cleanup depends on why the runner was stopped.** Stopped for a cancel, the runner releases what it holds and restores the request label within the grace. Stopped for a shutdown, it leaves the attempt resumable.

**Placeholder runner.** Until `agent_fox_worker` replaces it, `cmd/agent-fox` wires a runner that ends every attempt with a failed envelope of category `unsupported`, not retryable.

The names of the packages, types and functions for all of this are left to design.

### R5. Ending an attempt

**A shutdown first.** Suppose the attempt's context was cancelled for a shutdown before the runner returned. Then whatever the runner returns is discarded, whether a result, an error or a panic:

- nothing is stored;
- none of the rules below applies;
- the attempt stays `running`, and it is handed over again after the restart (R9).

**Otherwise**, the jobs layer stores the envelope as `envelope.json`, and a non-empty report as `report.md`, both mode 0600, in the attempt's directory. It then applies the first of these rules that matches:

1. **The job was cancelled meanwhile.** The attempt becomes `cancelled`, and the job stays `cancelled`.
   - The job's `labels` follow the runner's report.
   - `restore` is cleared when the runner reports having set the request label.
2. **Status `done`, or status `unverified` with a `result.outcome` that is not `parked`.** The attempt is `done`, and so is the job.
3. **Status `needs_human`.** The attempt is `needs_human` and the job is `blocked`, with the question and its lettered options.
4. *(reserved for the shutdown case above, which never reaches this list)*
5. **An automatic next attempt.** This rule applies when all of the following hold:
   - the status is `failed`;
   - `error.category` is `brain` or `no_result`, and `error.retryable` is true;
   - the attempt did not end because its transient retries were spent;
   - the current round has fewer attempts than the maximum the runner returned, clamped to 1–10. A round is the latest attempt whose trigger is not `auto`, plus the `auto` attempts after it.

   Then the attempt is `failed`, a new attempt `n+1` with trigger `auto` and the same context and answers is queued, and the job is `queued`.
6. **Any other ending.** The attempt is `failed` and the job is `failed`. This covers a parked fix, a retryable failure of any other category, and spent transient retries.

The job's `result`, `artifacts`, `side_effects`, `labels`, `usage` and `finished_at` follow the attempt that just finished (R2).

**When the runner gives no usable envelope**, the jobs layer makes one with:

- the toolio schema version;
- `tool` set to the job's kind;
- status `failed`;
- empty arrays and usage source `none`.

Its error depends on what the runner did:

| What the runner did | Error in the synthesized envelope |
|---|---|
| Returned a `*toolio.Failure` (found with `errors.As`), transient or not | That failure's category, message and retryable flag. A transient error gets this envelope only once its transient retries are spent, and rule 5 never applies to it. |
| Returned a plain error | Category `internal`, not retryable |
| Panicked | Category `internal`, not retryable, with the panic logged at `ERROR` |
| Returned an envelope with an unknown status, or `needs_human` without a question | Category `internal`, not retryable |
| Returned after a cancel without an envelope, or overran the cancel grace (R6) | Category `cancelled` |

**Transient errors.**

- The attempt's row counts its transient failures (`transient_failures`).
- After the first and the second, the attempt goes back to `queued` and is handed over again, after 30 s and after 2 min respectively. Tests inject shorter backoffs.
- The third ends the attempt under rule 6, with the synthesized envelope above.
- A handover after a shutdown or a crash raises `deliveries` but not this count.
- An attempt waiting out a backoff is `queued` and keeps its workspace's place (R4). A cancel treats it as a queued attempt.

**Events about the attempt.** The jobs layer emits:

- `attempt_start {attempt, delivery}` at each claim;
- `attempt_end {attempt, state, status, exit_code}` when the attempt finishes;
- `job_state {state, attempt, at_rest, blocked?, result?}` on every job transition, and once more when the last running attempt of a cancelled job ends. `at_rest` is true when the job is `blocked`, `done`, `failed` or `cancelled` and none of its attempts is running.

Once an attempt has ended, anything its runner still sends is dropped: events, hub references and the result.

### R6. Cancel, answer, retry, unsubscribe

**`POST /api/v1/jobs/:id/cancel`** works from `queued`, `running` or `blocked`.

- The job becomes `cancelled` at once and `finished_at` is set. The answer is 200 with the job.
- When the job has a `request_label`, `labels.restore` is set to that label. This spec writes nothing on the forge. For a running attempt, the runner restores the label in its cleanup (R4). For a queued or blocked job, applying the record belongs to PRD 04's label protocol.
- A queued attempt, including one waiting out a backoff, becomes `cancelled` and never reaches the runner.
- A running attempt has its context cancelled with the cancel cause, and its row becomes `cancelled` when the runner returns.
- If the runner has not returned within 60 s:
  - the attempt is recorded `cancelled`, with the synthesized `cancelled` envelope;
  - `attempt_end` and the job's at-rest `job_state` are emitted;
  - an `ERROR` naming the job and the attempt is logged;
  - the runner keeps its workspace's place and its worker slot until it does return, and anything it reports or returns after that is dropped.

**`POST /jobs/:id/answer`** works from `blocked` and takes exactly one of `{answer}` or `{option}`.

- `answer` is non-empty after trimming and at most 8 KiB.
- `option` must be one of the question's option ids.
- The answer text is the free text, or the option's label.
- Attempt `n+1` is queued with trigger `{answer, by}` and the stored `{question, option?, text}`. The answer is 202 with the job.

**`POST /jobs/:id/retry`** works from `failed` and takes no body or `{}`. It queues attempt `n+1` with trigger `retry` and answers 202 with the job.

**Checks, in order, for cancel, answer and retry:**

1. The job does not exist, or its workspace is one the caller cannot see: 404 (R7).
2. The job is in a state the action does not start from: 409 `invalid_state`, with a message that names the current state.
3. For answer and retry only:
   - the workspace is not subscribed, or is being unsubscribed: 409 `not_subscribed`;
   - its token is unreadable: 409 `token_unreadable`.

When two actions on one job race, exactly one wins, and the other answers 409 `invalid_state`.

**Unsubscribe.** After its own checks, the core's `DELETE /api/v1/workspaces/:slug` does the following:

1. It marks the workspace as being unsubscribed. From then on, creating a job for it, through the route or the entry point, is refused as not subscribed, and answer and retry answer 409 `not_subscribed`.
2. It cancels every open job of the workspace as above, with the unsubscribe cause.
3. It waits for running attempts to return, up to the same 60 s grace. An attempt still running at the end of the wait is handled as a cancel whose grace ran out.
4. It forgets the token as the core specifies, and answers. If the removal fails, the mark is cleared.

Jobs and their history stay until retention removes them.

### R7. Reading jobs

**Routes.**

- `GET /api/v1/jobs` lists the jobs of every workspace, subscribed or not, that has jobs and that hub lets the caller see. Visibility is the core's cached check. If hub is unavailable for any of those workspaces, the answer is 502 `hub_unavailable` with no partial page, as the core does for `GET /api/v1/workspaces`.
- `GET /api/v1/workspaces/:slug/jobs` lists one workspace. It answers 404 when the caller cannot see the workspace on hub. A workspace that is no longer subscribed still lists the jobs it has left.

**Parameters.** Both routes take `kind`, `state`, `issue` (a positive integer), `limit` (1 to 200, default 50) and an opaque `cursor`. A bad value answers 400. Results come newest first, ordered by `created_at` and then `id`, as `{jobs: [...], next_cursor}`. `next_cursor` is null on the last page.

**One job.**

- `GET /api/v1/jobs/:id` serves the job with its attempts.
- `GET /jobs/:id/attempts/:n/envelope` serves the stored `envelope.json` verbatim, as `application/json`.
- `GET /jobs/:id/attempts/:n/report` serves `report.md` as `text/markdown; charset=utf-8`.

A job that does not exist, and a job on a workspace the caller cannot see, both answer 404 with the same code and message. The same 404 covers:

- an attempt number out of range;
- an attempt that has no envelope yet;
- an attempt with no report.

### R8. The event log and SSE

**Recording.** Each event is stored in `af_events` before it is published:

- `seq` is strictly increasing and gap-free per job, across attempts;
- each event carries `ts`, `job_id`, `attempt`, `type` and `data`;
- a type that does not match `^[a-z][a-z0-9_]*$`, or that is one of the jobs layer's three types, is stored as type `invalid_event_type` with data `{"type": <the type given>, "data": <the data given>}`;
- data larger than 64 KiB as JSON is replaced by `{"truncated": true, "bytes": N}`;
- data that cannot be encoded is replaced by `{"error": "…"}`.

An event that cannot be stored is logged at `WARN` and dropped, and the attempt goes on.

**`GET /api/v1/jobs/:id/events`** answers `text/event-stream` with `Cache-Control: no-cache`.

- **Starting point.** The `since` query parameter (an integer of at least 0) sets where to start. Without it the `Last-Event-ID` header is used, and without either the stream starts at 0.
- **Order.**
  - The stream sends the stored events with `seq` greater than the starting point, in order, and then the live events.
  - No event is missing between the replay and the live part, and no event is sent twice.
- **Frame.** Each event is `id: <seq>`, `event: <type>`, then `data: <one-line JSON {seq, ts, job_id, attempt, type, data}>`.
- **Keepalive.** A `: keepalive` comment is sent every 15 s.
- **End.** If the job is at rest when the replay ends, the stream ends there. Otherwise it ends after the first live `job_state` whose `at_rest` is true.
- **Slow subscribers.** Each subscriber has a buffer of 256 events. A subscriber that falls further behind is disconnected, and it recovers by reconnecting with its last id.
- **Server shutdown** closes every stream.
- **Visibility.** It follows R7.

### R9. Shutdown, restart and retention

**Shutdown.** On shutdown the service:

- stops claiming;
- cancels running attempts with the shutdown cause;
- waits up to 30 s for runners to return;
- leaves those attempts `running`, and discards whatever their runners return (R5).

After a restart, the queue hands those attempts over again under the same attempt number, with the handover count incremented. This counts no new attempt, waits no backoff, and adds no transient failure. After a real crash, jobqueue's crash recovery does the same.

**Start and sweep.** At start, and then every minute, every attempt row in `queued` is enqueued again. The nonce makes this a no-op when the queue already holds the item. The sweep therefore repairs two cases: a crash between storing an attempt and enqueuing it, and a failed enqueue.

**Retention.** A purge runs at start and then every hour. It removes jobs that meet all of these conditions:

- the job is `done`, `failed` or `cancelled`;
- no attempt of it is running, and no runner of it is still in progress;
- its `finished_at` is older than `retain`.

For each job it removes, in this order:

1. the `attempts/<job id>/` directory;
2. the job's rows in `af_events`, `af_attempts` and `af_jobs`.

If the directory cannot be removed, the rows are kept for the next purge and a `WARN` is logged. Blocked jobs are never purged.

### R10. Documentation and offline tests

**Documentation.** After `make doc`, update in the same change:

- `docs/api.md`: the routes and their body limits; the job, attempt and event documents; the SSE framing and when a stream ends; and the codes `invalid_state`, `duplicate_job`, `not_subscribed` and `token_unreadable`;
- `docs/configuration.md`: `[workers].count` and `[database].retain`;
- `docs/model-usage.md`: automatic attempts spend more, they follow only `brain` and `no_result` failures, and a job's usage is the sum of its attempts;
- `docs/architecture.md`: the jobs part of the service section. It covers the states, the queue key and ordering, the runner contract and its four obligations, transient retries versus automatic attempts, and the event log;
- `docs/development.md`: the new package in the layout table.

**Tests.** Every test runs offline, against the core's fake hub and a fake runner whose behaviour each test scripts. The fake runner is exported for the later specs' tests.

## Design Decisions

1. **Table names: `af_jobs`, `af_attempts`, `af_events`.** The core already chose the `af_` prefix. This settles PRD 03's question about two `jobs` tables without touching jobqueue's tables.
2. **One attempt row per attempt number. A queue redelivery reuses the row.** PRD 03 has the nonce be "job id plus attempt number" and keeps brain-level attempts as separate rows. Crash recovery and transient retries are deliveries of the same attempt. They are counted in `deliveries` and passed to the runner as the handover count, which the recovery spec needs.
3. **An automatic next attempt follows a failed envelope whose `error.category` is `brain` or `no_result` and whose `error.retryable` is true. The bound is the maximum the runner reports from `AF_MAX_ATTEMPTS`, default 2.**
   - These are the brain-level failures that PRD 03 counts with `AF_MAX_ATTEMPTS` ("the brain-level attempts, which are separate rows").
   - The loops also mark other failures retryable: sandbox failures (`internal/box/session.go`), and forge rate limits and lost connections at triage land (`internal/triage/land.go:350,354`). Those are infrastructure, which PRD 03 gives to the queue's retry policy. A retryable failure at triage land must not run the brain again.
   - The in-box `submit` command's refusal (`internal/submission/command.go:125`) goes back to the brain inside the attempt, and it is never what a loop ends with.
   - Only the worker reads hub variables, so the bound travels back with the result.
4. **The attempt budget resets at each human action (create, answer, retry).** A person asking again should get a full set of attempts. The count stays bounded because each human action opens only one round.
5. **Transient errors are retried on the same attempt: three transient failures, with 30 s and then 2 min between them, and then the job fails.**
   - Spent transient retries never become an automatic attempt, whatever the failure's retryable flag says.
   - Only transient failures count toward the three.
   - PRD 03 gives infrastructure failures to the queue's retry policy. Chaining spent transient retries into automatic attempts would multiply the tries, to 3 × the maximum.
   - A handover after a shutdown or a crash is no failure of the attempt. It raises `deliveries` but not the count. Otherwise restarting the service twice would fail an attempt that never failed.
6. **The jobs layer synthesizes an envelope for every attempt that ends without one:** an error, a panic, a cancel, an overrun grace, an invalid envelope. Every finished attempt then has an envelope at `attempts/:n/envelope`, and `result.exit_code` is always defined for the client's `--wait`.
7. **`unverified` maps to job state `done`, except a parked fix (`result.outcome` `parked`), which maps to `failed`.**
   - `af fix` returns `unverified` both for a change landed under `--no-verify` and for a change parked when no attempt was left (`internal/fix/park.go:107`; `docs/cli.md`, "How a fix run ends").
   - A parked fix did not do what was asked. Because `retry` works from `failed`, a person can ask again, and `state=failed` finds the job.
   - The forge label is still the worker's to set, and PRD 01 §4.2 makes it `af:needs-human`.
   - `result.status` and `result.exit_code` (4) keep the detail.
8. **Cancel sets the job to `cancelled` at once. The attempt is recorded `cancelled` when the runner returns, or after 60 s if it has not. The runner keeps its workspace's place and its worker slot until it actually returns.**
   - The client sees its action take effect, and the event stream ends.
   - The bounds of Goal 5 hold even for a stuck runner. The `ERROR` record points the operator to it, and a restart frees it.
   - The 60 s is `fixGrace` plus 30 s. `fixGrace` (30 s, `cmd/af/fix.go:38`) is the time a fix run has to put the tree back and park. The extra 30 s let the worker release the lease and restore the label through hub. No `af` command uses 60 s itself.
9. **The cancel cause tells the runner why it stopped: a cancel, an unsubscribe (which also reads as a cancel), or a shutdown (which does not).** On a cancel the worker must release the lease and restore the label. On a shutdown it must leave the attempt resumable. How the cause is carried is left to design (Design Decision 36).
10. **A cancel writes no label on the forge, but records in `labels.restore` the label to put back, for jobs whose trigger consumed a request label.**
    - PRD 03 §4 says cancel "sets the request label back (PRD 04 makes that visible; here it is the record)".
    - A job created through the API consumed no label, so it has nothing to restore.
    - For a running attempt, the runner restores the label in its cleanup, and the record clears when the runner reports it.
    - For a queued or blocked job, no runner is involved, and so no hub forge client. PRD 04's label protocol applies the record.
11. **Answers live in the job record and go to the runner as `{question, options, answer}`, with the option's label as the answer text.** A service attempt starts from a fresh clone, so `fix.FindEarlier`'s search of run directories cannot find earlier questions. The label is what a person would have typed.
12. **`fix` takes an issue only. `triage` takes an issue, text or a file. `context` is an array.** This follows PRD 01 §6.2's bodies. The array mirrors the repeatable `--context`.
13. **`issue` accepts a number, an issue URL or `owner/repo#N`. The jobs layer takes the number and refuses pull and merge request URLs. Matching the repository, and refusing a number that the forge says is a pull request, are the worker's preflight.**
    - PRD 01 §4.1 names `owner/repo#N` and URLs as the issue forms.
    - `af triage` and `af fix` both refuse pull and merge requests (`internal/triage/preflight.go:114`, `cmd/af/fix_brief.go:101`), and `issuex.ParseIssueURL` already marks such URLs (`IssueRef.IsPullRequest`). Refusing them up front costs nothing, and it keeps the duplicate check on issue numbers, so a GitLab merge request !N cannot collide with issue #N.
    - The workspace has one repository, and only hub knows which.
14. **A second open job of the same kind for the same issue on a workspace is refused with 409 `duplicate_job`.** Two fix attempts on one issue would collide on its branch, and the poll needs the same open-job lookup anyway.
15. **Text and file content over `toolio.MaxInputBytes` is refused, not truncated.** An API should not change input silently. The client spec can truncate with `toolio.Truncate` before sending, as `af --local` does.
16. **Responses carry the job object itself: 202 for create, answer and retry, 200 for cancel. Lists carry `{jobs, next_cursor}`.** One decoder serves every single-job response.
17. **Both `GET /jobs` (across visible workspaces) and `GET /workspaces/:slug/jobs` exist.** PRD 03 names the first and PRD 01 the second, and `af jobs list` with and without `--workspace` maps onto them.
18. **An unknown job, and a job on a workspace the caller cannot see, get the same 404.** PRD 01 D2 says "404 is 404", and the answer must not reveal that the job exists.
19. **The jobs layer emits its own `job_state`, `attempt_start` and `attempt_end` events, and an SSE stream ends once the job is at rest.**
    - `--wait` needs a clean end and the final state in-band.
    - `job_state` carries `at_rest`. When a cancelled job's running attempt ends, one more `job_state` is emitted, so the last event of every finished stream is a `job_state` at rest even when the state did not change.
    - A client that wants more after an answer or a retry reconnects with `since`.
20. **Events are stored before they are published. Each subscriber has a 256-event buffer, and a slow subscriber is disconnected rather than allowed to block the runner.** Replay from the store makes a disconnect lossless.
21. **Attempt artifacts go under `attempts/<job id>/<n>/` beside the database.** One directory per job makes purging a single removal. PRD 01 §6.5 puts them next to the state.
22. **Retention purges only done, failed and cancelled jobs with no running attempt. Blocked jobs are kept.** A blocked job is waiting for a person, not finished.
23. **`aflib/jobqueue` is used through a small adapter, at an aflib version that has the package. aflib is bumped if the pinned commit lacks it. The adapter may add the per-workspace order (a backoff keeping its place included), the nonce no-op and the handover count, and nothing more.**
    - The module's source is unreadable here, so its API cannot be relied on.
    - Durability, claiming, delayed retry and crash recovery are the queue's. If the module lacks one of them, implementation stops and an erratum (`docs/errata/12_*.md`) records the gap, rather than this spec growing a queue of its own.
24. **`cmd/agent-fox` wires a placeholder runner that fails each attempt as `unsupported`.** The binary should run end to end before `agent_fox_worker` lands, and `unsupported` is the category PRD 03 already uses for work the service cannot do yet.
25. **One attempt row is one brain-level attempt, and a runner does not re-attempt the brain itself.**
    - PRD 03 §4 says "`AF_MAX_ATTEMPTS` bounds the brain-level attempts, which are separate rows".
    - `af fix` already re-attempts inside a run (`internal/fix/loop.go`). When its attempts run out, it returns the last failure, still retryable (`internal/fix/finish.go` `exhausted`). A runner that also re-attempted would make up to 2 × 2 brain attempts.
    - The obligation sits in the runner contract (R4), so `agent_fox_worker` inherits it and tests it.
26. **A runner reports infrastructure failures as transient errors, and only brain-level outcomes as envelopes.** That is the line PRD 03 draws between the queue's retry policy and attempt rows. It keeps a lost box or a hub outage from spending a brain attempt.
27. **Body limits are per route and cover the worst JSON escaping: 1 MiB for triage, 256 KiB for fix, 64 KiB elsewhere. `context` is capped at 32 KiB in total.**
    - A valid body must never get 413.
    - Six times is the most a JSON encoder can grow a byte (`\u00XX`).
    - The 32 KiB total keeps the fix route small and still leaves each of the ten strings its 8 KiB.
28. **A job's `finished_at` is when it last entered `done`, `failed` or `cancelled`. Every such transition sets it, a cancel included, and it is null otherwise.** Retention keys on it. A job cancelled while queued must therefore have one, and a job cancelled while blocked must not inherit the time its question was asked.
29. **Unsubscribe refuses new work for the workspace while it runs and waits the same 60 s grace as cancel. Runners read the PAT when the attempt starts.** The cleanup a cancel asks of a runner needs the PAT, and the core's PAT read fails once the subscription row is gone.
30. **`GET /api/v1/jobs` answers 502 `hub_unavailable`, with no partial page, when hub is unavailable for a workspace the list needs. It lists jobs of unsubscribed workspaces to whoever hub lets see them.**
    - The core does the same for `GET /api/v1/workspaces`, and a partial list would hide jobs silently.
    - Visibility is hub's answer, not the subscription's, and jobs outlive their subscription until retention.
31. **Whatever a runner returns after a shutdown cause is discarded, and the attempt is handed over again.** The attempt was stopped so that it could resume. Recording a `cancelled` or partial result would end a job the operator only restarted.
32. **An event with a malformed or reserved type is stored as `invalid_event_type`, with the type and data it was given.** An event never fails a run. `toolio` keeps a malformed warning code the same way, as `invalid_warning_code` (`internal/toolio/app.go:169-174`, `internal/toolio/warnings.go`).
33. **`answer` and `retry` check the subscription and its token as creation does, and answer 409 `not_subscribed` or `token_unreadable`.** Without the checks, both would queue an attempt that could only fail in the runner. The job itself is visible, so a 409 says what is wrong, where a 404 would claim the job does not exist.
34. **A failed enqueue after a job is stored still answers 202. It logs a `WARN`, and a sweep that runs every minute repairs it.** The job is durable once stored, so failing the request would tell the client the job was not created. The sweep is the start-time re-enqueue on a timer, and the nonce makes it idempotent.
35. **An attempt waiting out a transient backoff keeps its workspace's place and frees its worker slot.** First come, first served per workspace still holds, and a transient fault in one workspace does not hold up the others.
36. **The requirements state the runner seam, the stores and the SSE handler as behaviour. Packages, types, signatures and the order of operations inside a handler are left to design.** The core made the same choice (its Design Decision 36). The fake runner stays an exported test helper because later specs' tests use it.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `agent_fox_service_core` | depends on, modifies | It supplies the config struct, the SQLite store and migration list, caller identity and visibility, the `/api/v1` mux, error envelope and body limit, subscriptions with `token_state`, and the in-process PAT read. This spec adds two config keys, three migrations, the jobs routes and their error codes, and per-route body limits (1 MiB triage, 256 KiB fix). It also makes the core's `DELETE /workspaces/:slug` refuse new work and cancel open jobs before it removes the row. |
| `05_af_cli_envelope` | depends on | `toolio.Envelope`, the statuses and `ExitCode`, `toolio.Failure` and its categories, `MaxInputBytes`, and the event vocabulary that attempts store and stream. |

## Verified External API

**`github.com/agent-fox-dev/aflib/jobqueue`** is **unverified**. Its source is outside the repository, and the module cache cannot be read. `go.sum` lists the aflib module (`h1:` and `/go.mod` lines) and no SQLite driver. The capabilities assumed come from PRD 01 D9, PRD 03 §4 and `docs/drafts/add_tasks_to_the_hub.md`:

- a SQLite-backed queue on a caller-supplied `*sql.DB`. This is inferred from the missing driver;
- items with a type, a key, a nonce and a payload;
- handlers registered per type and run by a bounded worker pool;
- per-key serialization;
- a retry policy with delay;
- crash recovery of claimed items on start;
- cancellation through the handler's context.

No concrete function names are assumed. The adapter reads the real API when it is implemented. The fallback order and what the adapter may add are in R4 and Design Decision 23.

**`github.com/agent-fox-dev/aflib/issuex`** is **unverified at the source**, but code in this repository compiles against it:

- `func ParseIssueURL(s string) (IssueRef, bool)`, from the call site `ref, isIssue := issuex.ParseIssueURL(arg)` (`internal/toolio/input.go:87`);
- `IssueRef{Repo, Number, IsPullRequest}` (`internal/toolio/dryrun_test.go:157`, `cmd/af/fix_brief.go:102`) and `ref.URL()` (`cmd/af/triage.go:64`).

**The core spec (planned, not yet code)**:

- an in-process read of the opened PAT by slug, which fails with "not subscribed" or "unreadable" errors (`.specs/11_agent_fox_service_core/tasks.json`);
- the `decodeBody` helper with a 64 KiB `http.MaxBytesReader`, which this spec makes per-route;
- the error code table, which this spec extends.

**In this repository** (read):

- in `internal/toolio/envelope.go`:
  - `toolio.Envelope` and `toolio.Status`;
  - the constants `StatusDone`, `StatusFailed`, `StatusUsage`, `StatusNeedsHuman` and `StatusUnverified`;
  - `func ExitCode(s Status) int` and `const SchemaVersion = "5.0.0"`;
  - `ErrorInfo{Category, Message, Retryable, FixHint}`;
  - `Usage{Brain, CostUSD, CostSource, InputTokens, OutputTokens, Turns}`;
  - `Artifact` and `SideEffect`;
- in `internal/toolio/failure.go`: `toolio.Failure{Category, Message string; Retryable bool; FixHint *FixHint}`, `CategoryCancelled` and `CategoryInternal`;
- in `internal/toolio/input.go`: `const MaxInputBytes = 131072` and `func Truncate(body string) (string, bool)`;
- `fix.Result.Outcome` (JSON `outcome`) and the outcome `parked` (`internal/fix/result.go`, `internal/fix/park.go`).

**Standard library:**

- `context.WithCancelCause` and `context.Cause`;
- `http.NewResponseController`, with `Flush` and `SetWriteDeadline`;
- `crypto/rand`, `database/sql`, `encoding/base64` (for the cursor) and `log/slog`.
