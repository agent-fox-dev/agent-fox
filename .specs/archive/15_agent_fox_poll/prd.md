---
spec_id: "15"
spec_name: "agent_fox_poll"
title: "agent-fox poll: turn request labels found through hub's issues API into jobs, consuming each label once"
status: "active"
created_at: "2026-10-21T22:32:31.294143Z"
updated_at: "2026-10-21T22:32:31.294143Z"
intent_hash: "07f5eda7dd482e1c48c38b29aaeb6196e4509d507afa0f2dbb70a11d7beb0953"
schema_version: 2
source: "docs/prd/03-run-the-agent-fox-service.md"
---
## Intent

Give `agent-fox` a trigger that needs no client. For every subscribed workspace, the service periodically asks hub's forge API for open issues that carry the workspace's triage or fix request label. Each new request becomes exactly one job, and the service removes ("consumes") its label through hub, so a team can ask for triage or a fix just by labelling an issue. The poll never runs one request twice:

- a label on an issue the service is already working on is absorbed;
- a label it has already taken is removed again rather than taken a second time;
- a label the service itself put back when it cancelled a job is left alone until a person asks again.

`AF_POLL_INTERVAL` sets how often the poll runs, and `0` turns it off.

## Goals

The tests for these goals run offline. They use:
- the fake hub of the earlier specs, with the issues list this spec adds;
- the real jobs layer on a temporary database, with `agent_fox_jobs`'s fake runner unless a goal says otherwise;
- an injected clock and a first-cycle delay of zero;
- workspace `widgets`, subscribed with prefix `af:`.

1. **A labelled issue becomes one job.** Open issue 7 carries `af:triage` and open issue 42 carries `af:fix`.
   - One cycle creates exactly two jobs: a triage job for 7, created first, then a fix job for 42.
   - Their `source` is `{kind: "label", by: "label af:triage"}` and `{kind: "label", by: "label af:fix"}`. Their `request_label` is `af:triage` and `af:fix`.
   - The fake hub records one removal of `af:triage` from 7 and one of `af:fix` from 42, each after its job exists.
   - The next cycle creates no job.
   - These create no job and lose no label: a pull request carrying `af:fix`, a closed issue carrying `af:triage`, and an issue labelled only `bug`.
2. **A request already being served is absorbed.**
   - Set the fake hub to keep listing 42 under `af:fix` after the label was removed, while 42's fix job is still running. The next cycle creates no job and sends one more removal.
   - Create a fix job for 42 through the API, then add `af:fix` to 42. The next cycle creates no job and removes the label.
   - An API fix job for 42 is running when `af:fix` is added to 42, and the absorbing removal fails with 503. The API job then ends `done`. The next cycle removes the label and creates no job, and the cycle after that creates nothing.
   - A cycle that finds `af:fix` on 42 and `POST /api/v1/workspaces/widgets/fix {"issue": 42}` start together, 50 times, each on a fresh database, under `go test -race`. Each time exactly one fix job for 42 exists, and `af:fix` is removed from 42, either as the poll's new request or as absorbed.
3. **A label already taken is not taken twice.**
   - The removal of `af:fix` from 42 fails with 503. The job is still created, and it ends `done` before the next cycle.
   - The next cycle creates no job and removes the label.
   - The cycle after that creates nothing.
   - Adding `af:fix` to 42 again then makes exactly one new job.
4. **A label put back by a cancel is left alone.**
   - A poll-created fix job for 42 is cancelled while its fake runner holds the attempt. Before the runner returns, the test puts `af:fix` back on 42 in the fake hub, as the worker's cancel cleanup does. A cycle run in that window creates no job and removes nothing.
   - The runner then returns and reports `af:fix` as set. For the next three cycles, nothing creates a job for 42 or removes `af:fix`. The same holds after a restart on the same database file.
   - The same holds when the runner overruns the cancel grace and the jobs layer drops its report.
   - The same holds after retention purges the cancelled job (the clock moved past `[database].retain`): the next three cycles create no job for 42 and leave `af:fix` in place.
   - With the fake hub set to leave a newly added label out of its lists for the next two lists, and `AF_POLL_INTERVAL` at `1m`: the two cycles after the runner returned list `af:fix` without 42, and the third lists it with 42. None of them creates a job or removes the label.
   - Someone removes the label by hand, and a cycle runs, at least 5 minutes after the runner returned, that lists `af:fix` without 42. After that, adding `af:fix` again makes exactly one new job.
   - A poll-created job cancelled while queued puts nothing back. Adding its label again makes one new job at the next cycle.
5. **The interval.**
   - `AF_POLL_INTERVAL` unset: list requests are 5 minutes apart. Set to `2m`: 2 minutes apart.
   - Set to `0`: across 30 minutes of clock there is no list request, and there is one variables read every 5 minutes. Changing it to `1m` makes the next check list.
   - After `0` was read, a cycle whose variables read hub answers with 503 is followed by the next cycle 5 minutes later.
   - Set to `30s`, `abc`, `-1m` or `25h`: the poll uses 5 minutes and logs one `WARN` record naming the workspace, the variable and the value.
6. **Failures stay contained.**
   - Hub answers 503 to the list: no job is created and no label is removed. The next cycle, one interval later, creates the job.
   - Hub answers 403 to the list for `widgets`: across three cycles there is exactly one `ERROR` record. It names the operation and points to `PUT /api/v1/workspaces/widgets`. A second workspace, `gadgets`, is polled normally in the same cycles.
   - Hub answers 503 to the list in one cycle and 403 to the variables read in the next: there is exactly one `WARN` record and one `ERROR` record.
   - Hub answers 409 to the removal of `af:fix` from 42 after its job was created: the job stands, one `ERROR` record names the label removal and hub's message, and the cycle goes on to the next issue. The next cycle removes the label and creates no job.
   - Hub answers 404 to a label removal: there is no failure record, and the removal counts as done.
   - When the listing of `af:fix` comes back incomplete in three cycles in a row, there is exactly one `WARN` record, naming the workspace and the label.
   - A subscription whose token is unreadable gets no hub request, and one `ERROR` record.
   - No log record contains a PAT.
7. **Unsubscribing stops the poll.** A cycle that races `DELETE /api/v1/workspaces/widgets` creates no job. Once the next subscription scan has run, the fake hub records no further request made with the `widgets` PAT.
8. **End to end (PRD 03 §9.3).** This goal uses the real worker of `agent_fox_worker`: the fake hub, its git server, the in-process gateway over `local`, and the scripted brain.
   - An issue labelled `af:triage` becomes one job with `source.kind` `label`, and its label is removed through hub.
   - The job ends `done`. The issue body is rewritten with a `## Original report` section byte-identical to the original body, and `af:ready` is set.
   - The next cycle creates no second job.
9. `make check` passes offline.

## Non-goals

- **The rest of the label protocol (PRD 04).** That covers:
  - creating the labels when a workspace subscribes;
  - hub's event stream and `source.kind: event`;
  - `AF_AUTO_FIX`;
  - answering a blocked job or retrying a failed one by re-labelling;
  - applying `labels.restore` for jobs cancelled while queued or blocked;
  - cancelling a job when its issue is closed.

  Until PRD 04, the poll handles a request label on an issue whose job is blocked by absorbing it, and treats a request label on an issue whose job failed as a new request.
- **Status labels.** Labels such as `in-progress`, `ready`, `needs-human`, `landed` and `failed` are the worker's mirror (`agent_fox_worker`). The poll writes only the removal of request labels.
- **`af` as a client.** That is `af_service_client`. No `af` command starts or inspects the poll.
- **Poll status in the API.** There is no "poll now" route, and no last-poll time or last-error field in the subscription view. The job records show what the poll created, and the log shows how it failed.
- **New `config.toml` keys.** The poll's only setting is the hub variable `AF_POLL_INTERVAL`.
- **Who added a label.** The poll does not read issue timelines, and it ignores labels other than the two request labels. Closed issues are never polled.
- **Recovery of attempts and orphaned leases.** That is `agent_fox_worker_recovery`. It changes nothing in how a cancelled attempt puts its request label back (Design Decision 35).
- **Changing retention.** The jobs layer's purge is unchanged. The poll's records do not depend on the job's row (R5).
- **Carry patches (PRD 05).** A fix label on a `carry_patch` workspace becomes a job that the worker refuses, just as the same request through the API would.
- **More than one attempt per workspace at a time.** That is the jobs layer's ordering.

## Background

PRD 01 sets the rules this poll follows:
- §3, the label protocol: "A request label is consumed. When the service takes a job for a request label it removes the label, so adding it again is a new request."
- §6.2, the job shape: a job's `source` is `{kind, by}`, and for a label-triggered job `by` reads `label af:fix`.
- §6.3, the triggers: every `AF_POLL_INTERVAL`, hub's issues API is filtered by each request label, and "each issue not already the input of an open job becomes a job with `source.kind: label`… a forge that lags is handled by the open-job check".
- §6.1's variables table: `AF_POLL_INTERVAL` defaults to `5m`, and `0` disables it.

PRD 03 §5 restates this and assigns it to this scope. PRD 03 §10 accepts that the poll spends the workspace's forge rate limit, with the interval as the way to control it. PRD 04 §2 adds the rule for an issue that already has an open job: "nothing; the label is removed (a second request while one runs is absorbed)".

No service code exists in the tree yet. The earlier specs of this split are written, and this spec builds on what they deliver.

- **`agent_fox_service_core`** delivers:
  - the store's subscriptions, with their `label_prefix`;
  - an in-process read of a subscription's PAT, which fails as "not subscribed" or "unreadable";
  - the append-only `af_` migration list;
  - `log/slog` JSON logging;
  - `internal/hub` and its fake.

  Its Design Decision 24 already keeps commas out of the prefix "because of the `labels=` filter of hub's issues API, which the poll uses". Its Design Decision 25 leaves the choice of which prefix governs the labels to the worker and the poll.
- **`agent_fox_jobs`** delivers two Go entry points for this spec:
  - one creates a job with a given source and records the request label the trigger consumed as `request_label`, with the API routes' refusals (duplicate, not subscribed, token unreadable);
  - the other looks up the open (queued, running or blocked) job for a workspace, kind and issue.

  It also covers cancel. A cancelled job becomes `cancelled` at once. A running attempt's runner is stopped with the cancel cause and returns later. Its report then sets the job's `labels`, and anything it returns after the 60 s grace is dropped. A job with a request label gets `labels.restore`. Retention purges a finished job after `[database].retain`, once no runner of it is still in progress, deleting its rows in `af_events`, `af_attempts` and `af_jobs`.
- **`agent_fox_worker`** delivers:
  - the `internal/hub` forge client (an `issuex.Client` bound to one workspace and its PAT), with `RemoveLabel` and the issuex error mapping. It leaves `ListIssues` unavailable, and its Design Decision 13 says "the poll … add[s] `ListIssues` … when [it needs it]";
  - the hub calls for the workspace record and the resolved variables, and the classes of a hub answer: refused (401), forbidden (403), not found (404), rejected (another 4xx, carrying hub's message) and unavailable;
  - the decision (its Design Decision 7) that the subscription's `label_prefix` governs and `AF_LABEL_PREFIX` is ignored, "so the worker and `agent_fox_poll` cannot disagree";
  - the cancel cleanup (its R8). For an attempt stopped by a cancel, the worker puts the job's request label back on the issue, releases the lease and reports to hub, and only then returns its result with the labels. A cancel from queued or blocked involves no runner, so nothing puts the label back.
- **`agent_fox_worker_recovery`** changes nothing the poll relies on. Its journal, outcome file and resume paths cover deliveries stopped by a shutdown, a crash or a transient error. A cancel ends its attempt, which is never handed over again, and the cancel cleanup stays the worker's. Its reconciliation pass releases leases and completes sessions, and writes no label.

Other code in this repository that the poll relies on:
- `issuex.Client.ListIssues(ctx, Repo, IssueFilter) (IssueList, error)`, whose call site is `internal/toolio/dryrun.go:63`.
- issuex's own forge clients, as `.specs/archive/02_issuex_github` describes them. They drop pull requests from `ListIssues`, report `Incomplete` when a list is capped, and treat a 404 on `RemoveLabel` as success.
- The fake-server pattern of `internal/triage/fakeforge`.

Without this spec, the service runs only what someone posts to its API. A label put on an issue does nothing.

## Requirements

### R1. One poll loop per subscribed workspace

**Starting and stopping.** The poll runs inside `agent-fox`. It starts after the jobs layer and stops before it.

**Following the subscriptions.** The poll reads the store's subscriptions at start and then every 60 seconds.
- Each subscribed workspace has exactly one loop.
- A new subscription gets a loop.
- When a subscription is gone, its loop stops and any cycle it has in flight is cancelled.

**Timing.**
- A loop's first cycle starts after a random delay of 0 to 30 seconds.
- Each later cycle starts one interval (R2) after the previous cycle ended.
- Cycles of one workspace never overlap.

**Limits.**
- At most four cycles run at once across all workspaces. A cycle that is due waits for a slot.
- A cycle that has run for 5 minutes is cancelled. What it has already done stands.

**Shutdown.** No new cycle starts. Cycles in flight are cancelled, and the poll returns before the jobs layer begins its own shutdown.

**Time in tests.** The clock, the first-cycle delay, the subscription scan period and the hold's release delay (R6) can be injected, so that tests do not sleep.

### R2. The interval and the label prefix

**The start of a cycle.** A cycle first brings the poll's records for the workspace up to date with their jobs (R5), which needs no hub request. It then reads the subscription's label prefix and its PAT from the store, and reads the workspace's resolved hub variables with the PAT.

**How `AF_POLL_INTERVAL` is read.** It is a Go duration.

| Value | Interval |
|---|---|
| unset or empty | 5 minutes |
| a zero duration (`0`, `0s`, `0m`) | the poll is disabled |
| from `1m` to `24h` inclusive | that value |
| anything else: unparsable, negative, below `1m`, above `24h` | 5 minutes, and a `WARN` record naming the workspace, the variable and the value, logged once per distinct value |

**A disabled poll.** The cycle ends right after the variables are read. It reads no workspace record, lists nothing and writes no label. The next cycle starts 5 minutes later and reads the variable again.

**A cycle that ends early.** When a cycle ends before it could read the variable, the next cycle starts after the interval the workspace last read. If the last value read disabled the poll, or none has been read, the next cycle starts after 5 minutes.

**The request labels.** They are `<prefix>triage` and `<prefix>fix`, where the prefix is the subscription's `label_prefix`. `AF_LABEL_PREFIX` is not read. When it is set and differs from the subscription's prefix, a `WARN` record is logged once per distinct value.

### R3. Listing labelled issues through hub

**`ListIssues` in the hub forge client.** `internal/hub`'s forge client gains `ListIssues`, which `agent_fox_worker` left unavailable.
- **The call.** For a filter with `State: "open"`, one label and a `Limit`, it calls `GET /api/v1/workspaces/{slug}/issues?state=open&labels=<label>` with the PAT. The label is query-escaped.
- **Pages.** It asks hub for pages of 100. It follows hub's further pages until it holds `Limit` issues or hub reports no further page. A page whose answer says nothing about further pages is the last.
- **`Incomplete`** is true only when the client stopped at `Limit` while hub reported more.
- **Order.** The client sends no sort. The order of the issues is hub's, and the poll relies on none.
- **Pull requests.** Items that hub marks as pull or merge requests are left out.
- **Other filters.** Any other filter field makes the call send nothing and return the client's "not available through hub" error.
- **Errors** map as for the client's other forge methods: refused or forbidden, not found, rejected (409 as issuex's conflict error), rate-limited, and retryable.
- **Hub's field names** stay inside `internal/hub`.

**One enabled cycle** does the following, in order:
1. Reads the workspace record, for the forge repository.
2. Lists `<prefix>triage`, then `<prefix>fix`, each with a limit of 100.
3. Takes each list's issues in ascending issue number, and applies R4 to each.
4. When a listing comes back incomplete, logs a `WARN` record naming the workspace and the label. It is logged once, and not again for that label until a listing of it has come back complete. An incomplete listing releases no hold and settles no owed removal (R5, R6), and issues past its last page wait for a later cycle.

**The fake hub** serves this route from its issue state. It supports:
- the `state` and `labels` filters;
- paging, including a listing that reports a further page;
- issues that are pull requests;
- a removal lag, under which an issue stays listed under a label for a set number of lists after the label was removed;
- an addition lag, under which an issue is left out of the lists of a label for a set number of lists after the label was added.

### R4. What a listed issue becomes

For each issue N listed under label L of kind K, the first rule that applies decides:

1. **Absorbed.** The jobs layer has an open job of kind K for N on the workspace, whether queued, running or blocked. The poll removes L (R5) and creates no job. If that removal fails, the poll records it as owed, with no job (R5).
2. **Held.** A hold on L for N is in place (R6). The poll leaves L in place and does nothing.
3. **Already taken.** The removal of L from N is still owed (R5), whether for a job the poll created or for a label it absorbed. The poll removes L and creates no job.
4. **A new request.** The poll creates a job through the jobs layer's entry point, with:
   - kind K;
   - input `{issue: N}`;
   - source `{kind: "label", by: "label <L>"}`, for example `label af:fix`;
   - request label L.

   It then consumes L (R5).

**When the entry point refuses:**
- **Duplicate**, because a job for the same workspace, kind and issue was created in the meantime, for example through the API: handled as rule 1.
- **Not subscribed**, for example during an unsubscribe: the cycle ends at once and writes no label.
- **Token unreadable, or any other error:** a `WARN` record is logged, L stays on the issue, and the cycle ends.

**Two creators racing.** When two creators race on the same workspace, kind and issue, exactly one job is created. The jobs layer's duplicate check and its insert are atomic. If `agent_fox_jobs` does not already make them so, this spec does.

**Logging.** Each job created, and each label absorbed, is logged at `INFO` with the workspace, the kind, the issue and the job id.

### R5. Consuming the label, and the poll's records

**The poll's records.** The poll keeps durable records in a table of its own, `af_poll_requests`, which a migration appended to the core's list creates. It writes a record:
- for each job it creates, right after the job is created and before the removal;
- for each absorbing removal that fails (rule 1 of R4, a duplicate refusal included), with no job.

A record carries what rules 2 and 3 of R4 need without reading its job's row: the workspace, the issue and the label, whether the removal is still owed, and, for a job, what the poll has seen of it (R6). Records survive a restart. They are not tied to the job's row, so retention's purge of a job leaves them in place. A record that cannot be written is logged at `WARN`, and the removal still goes ahead.

**Keeping the records current.** At the start of every cycle, before any hub request and whether or not the poll turns out to be enabled, the poll reads the job of each of the workspace's records whose job it has not yet seen end. It notes:
- a job that is `done`, or `cancelled` without a hold (R6): it has ended, holding nothing;
- a job that holds (R6), and the first time the poll sees that no runner of it is in progress;
- a job that is gone because retention purged it before the poll saw it end: it now holds, from the moment the poll found it gone.

A job that is `queued`, `running`, `blocked` or `failed` has not ended. A failed job can still be retried, and then cancelled.

**When a record is dropped.** Once nothing is owed and nothing holds: its removal has been confirmed or settled, and either its job has ended holding nothing or its hold has been released. A record with no job is dropped once its removal is confirmed or settled.

**Removing the label.** After creating a job, and when absorbing or taking an owed label, the poll removes L from N through hub.
- A removal that succeeds, or that hub answers with not found, confirms the removal. It confirms every owed removal the poll has recorded for N and L.
- A removal that fails in any other way leaves the removal owed. A job already created stands. R7 says whether the cycle goes on.

**What happens to an owed removal.**
- It is retried whenever N is listed under L (rules 1 and 3 of R4).
- When a complete listing of L (one whose `Incomplete` is false) does not contain N, the label is gone, and every owed removal for N and L is settled.

### R6. A label the service put back

**When a job holds its label.** A job the poll created holds its request label once it is cancelled while one of its attempts is in a runner. A cancel caused by an unsubscribe counts. The hold starts at the cancel: the worker's cancel cleanup writes the label back to the issue before the runner returns and reports it (agent_fox_worker R8). The hold does not depend on what the runner reports. It is in place before the runner returns, after it returns, and when the runner overruns the cancel grace and the jobs layer drops its report.

A job the poll created that retention purges before the poll saw it end also holds (R5), so a cancel the poll never saw is not taken as a new request.

**While a hold on L for N is in place**, a listing of L that contains N creates no job and removes nothing.

**Releasing a hold.** A hold's release clock starts when the poll first sees that no runner of the job is in progress. For a purged job, it starts when the poll found the job gone. A complete listing of L that does not contain N releases the hold, provided it was requested at least 5 minutes after the release clock started. Until then the runner may still be writing the label, and the forge's list may not yet show a label that was just put back. Once a hold is released, L on N is a request again (rule 4 of R4).

**Jobs that hold nothing.** In this split, a job cancelled while queued, while waiting out a transient backoff, or while blocked has no runner to put its label back, so it holds nothing.

**How long a hold lasts.** Until it is released. It survives a restart and the purge of its job.

### R7. Failures

| What happens | What the poll does |
|---|---|
| The store says the workspace is not subscribed | The loop stops |
| The store says the token is unreadable | No hub request this cycle; an `ERROR` record |
| Hub is unavailable on any call: 429, 5xx, a transport error, a timeout, or a body that is not the expected JSON | The cycle ends; a `WARN` record; the next cycle runs one interval later |
| Hub refuses the PAT (401) or forbids the operation (403) on any call, or does not find the workspace (404) when the workspace record, the variables or a list is read | The cycle ends; an `ERROR` record that names the hub operation and, for 401 and 403, points to `PUT /api/v1/workspaces/{slug}` to replace the token |
| Hub answers 404 to a label removal | Not a failure: the removal is confirmed (R5) |
| Hub rejects the workspace record read, the variables read or a list (another 4xx, 409 included) | The cycle ends; an `ERROR` record that names the hub operation and carries hub's message |
| Hub rejects a label removal (another 4xx, 409 included) | The removal stays owed; an `ERROR` record that names the label removal, the issue, the label and hub's message; the cycle goes on with the next issue |
| The jobs entry point refuses | As R4 says |

A label removal that fails, in any row, stays owed (R5).

**Logging a failure once.** A failure is logged when it first occurs. Two failures are the same when they fall in the same row of the table above and concern the same hub operation (for a label removal, the same issue and label). The same failure on that workspace is not logged again until a cycle of that workspace has completed without failure. A different failure is logged when it first occurs. The cycle that completes without failure logs one `INFO` record saying the workspace's poll has recovered.

**Isolation.** A workspace's failure never stops or delays another workspace's loop, beyond the four shared slots.

**Secrecy.** No log record and no job field contains a PAT.

### R8. Documentation and offline tests

**Documentation.** Per the steering, after `make doc`, update in the same change:

| File | What it gains |
|---|---|
| `docs/api.md` | jobs created by the poll: `source` `{kind: "label", by: "label <request label>"}` and `request_label`; how a labelled issue becomes a job; absorbed, already-taken and held labels |
| `docs/configuration.md` | `AF_POLL_INTERVAL`: its default, `0`, its range and what an invalid value does; that the subscription's `label_prefix` names the request labels |
| `docs/model-usage.md` | the poll runs no model; each labelled issue is a job that spends like an API job; a label put back by a cancel does not spend again |
| `docs/architecture.md` | the poll in the service section: its loops and cycles, consuming and absorbing labels, owed removals, holds and their release delay, records that outlive their jobs, incomplete listings, and the cost of each cycle on hub and on the forge |
| `docs/development.md` | where the poll's code lives, in the layout table |
| `README.md` | that a subscribed workspace picks up labelled issues |

`docs/cli.md` is unchanged.

**Tests.**
- Every test runs offline. It uses the fake hub extended by R3, the jobs layer with the fake runner of `agent_fox_jobs`, and an injected clock.
- Goal 8 uses `agent_fox_worker`'s test harness.
- `agent_fox_worker`'s test of the hub forge client's unavailable methods no longer lists `ListIssues`.

## Design Decisions

1. **The poll runs beside the jobs layer, started after it and stopped before it. A supervisor finds workspaces by rescanning the store every 60 seconds, instead of hooks in `PUT` and `DELETE`.** Where its code lives is left to design.
   - The core's handlers stay unchanged.
   - A new subscription is first polled within 90 seconds, well inside the 5-minute default.
   - The jobs layer's unsubscribing mark already stops job creation the moment `DELETE` starts.
2. **The subscription's `label_prefix` names the request labels, and `AF_LABEL_PREFIX` is ignored, with a warning when it differs.** `agent_fox_worker` decided the same for the status labels (its Design Decision 7). One source of truth keeps the poll and the worker in agreement.
3. **`AF_POLL_INTERVAL` defaults to `5m`, `0` disables the poll, and the range is `1m` to `24h`.**
   - PRD 01's variables table gives `5m` and `0`.
   - The 1-minute floor bounds the forge rate limit the poll spends (PRD 03 §10).
   - A day is the longest interval that is still useful.
4. **An invalid interval falls back to 5 minutes with a warning; it does not disable the poll.**
   - Whoever set a non-zero value wanted polling.
   - A poll has no envelope that could carry a refusal.
   - Silently not polling would drop requests with no sign on the forge.
5. **A disabled workspace reads the variable again every 5 minutes. The next cycle is timed from the end of the previous one.** A workspace that turns polling on is picked up within the default interval. Timing from the end keeps a slow cycle from overlapping the next one.
6. **The first cycle waits a random 0 to 30 seconds, at most four cycles run at once, and a cycle is cut off at 5 minutes. None of these is configurable.**
   - At start, every subscription would otherwise hit hub at the same instant.
   - A cycle is a handful of short requests, so four slots bound hub's load without starving anyone.
   - The 5-minute cut-off frees a slot that a slow hub holds.
7. **The triage label is listed before the fix label, and issues are taken in ascending number.**
   - An issue carrying both labels gets its triage job queued first, and the jobs layer's per-workspace order then runs it first.
   - Ascending numbers make each cycle deterministic in tests, whatever order hub returns.
8. **An open job of the same kind absorbs the label: the label is removed and no job is created. This includes a blocked job. An open job of the other kind does not absorb it.**
   - PRD 04 §2 says "a second request while one runs is absorbed".
   - Absorbing only for the same kind departs on purpose from PRD 03 §5 ("not the input of an open job") and PRD 04 §2 ("an issue that has an open job"), which read as any kind. Under their reading, an issue carrying both labels (Design Decision 7) would lose its fix request to the triage job created a moment earlier, and so would a fix asked for while a triage runs.
   - The jobs layer's duplicate rule is per kind (its Design Decision 14), so the API already allows a triage and a fix of one issue to be open together. The per-workspace order runs them one after the other.
   - Answering a blocked job by re-labelling it is PRD 04 §4, which replaces this rule for blocked jobs when it lands.
9. **`source` is `{kind: "label", by: "label <request label>"}`, and `request_label` is the full label, prefix included.**
   - This is PRD 01 §6.2's job shape, whose `by` for a label-triggered job reads `label af:fix`.
   - The issues list does not say who added the label, so no person can be named.
   - `source.kind` already tells this trigger apart from PRD 04's events (`source.kind: event`).
10. **The job is created first and the label removed second. A removal that fails is recorded as owed, and an owed label is removed, not taken again.**
    - PRD 01 §6.3 removes the label "when the job is created".
    - Removing first would lose the request whenever the create fails.
    - Without the owed record, a failed removal would turn the same label into a second job once the first job had ended between two cycles.
11. **A removal answered with not found counts as done.** The label is gone either way, and issuex's own forge clients treat it the same (`.specs/archive/02_issuex_github`).
12. **A label the service put back when it cancelled a job is held, from the cancel until a complete listing shows the issue without the label. That listing must be requested at least 5 minutes after the job's runner returned.**
    - `agent_fox_jobs` and `agent_fox_worker` put the request label back on cancel (PRD 01 §6.2, PRD 03 §9.4). Without a hold, the next cycle would take the label again, and a cancel would become a restart that spends again.
    - Releasing on absence lets a person ask again by removing the label and adding it back. A poll can only observe that across two cycles.
    - The API stays the immediate way to ask again.
13. **A hold needs a poll-created job whose cancel reached an attempt in a runner, and nothing more.** In this split only the worker's cancel cleanup puts a request label back, and it runs only for a running attempt. A cancel from queued, from a backoff or from blocked only records `labels.restore`, which PRD 04 applies. Any label on the issue after such a cancel was put there by a person.
14. **Holds are released, and owed removals settled, only by a complete listing.** An incomplete listing proves nothing about issues past its last page. A hold also counts only listings requested at least 5 minutes after its release clock started (Design Decision 28).
15. **At most 100 issues are listed per label per cycle.**
    - 100 is issuex's own default limit.
    - Consumed labels leave the filter, so a larger backlog drains over successive cycles.
    - Only held labels and owed removals stay in the filter. Design Decision 33 covers a filter that stays full.
16. **Pull and merge requests are left out of the listing, and their labels are left alone.**
    - Triage and fix refuse pull requests (`internal/triage/preflight.go:114`, `cmd/af/fix_brief.go:101`).
    - issuex's own `ListIssues` drops them the same way.
17. **A fix label on a `carry_patch` workspace becomes a job, which the worker refuses as `unsupported`.**
    - Every request takes one path, and the job record says why it failed.
    - The poll needs no knowledge of workspace modes.
    - PRD 05 lifts the refusal without touching the poll.
18. **How each failure is handled:**
    - **hub unavailable:** the cycle ends and the next one runs at the normal interval. The 1-minute floor already acts as a backoff;
    - **refused, forbidden, or workspace not found:** an `ERROR` record, and the loop keeps running. A `PUT` that replaces the PAT is read at the next cycle. This meets the core's duty to name the operation and point to `PUT` on a 403 (core Design Decision 22);
    - **unreadable token:** no hub request;
    - **not subscribed:** the loop stops.
19. **A repeated failure is logged once, plus once on recovery.** A failing workspace polled every minute would otherwise write 1,440 records a day.
20. **The API shows no poll status.**
    - The core keeps the subscription view as it is in this split (core Design Decision 34).
    - The job records show what the poll created, and the log shows how it failed.
21. **The poll's records live in `af_poll_requests`, are not tied to the job's row, and outlive the purge of their job. A record is dropped once nothing is owed and nothing holds.**
    - A hold or an owed removal concerns a label on the forge, and that label can outlive the job. If the record went with the job, the first cycle after the purge would take a held label as a new request and spend again.
    - With no foreign key to `af_jobs`, the jobs layer's purge succeeds unchanged and needs no step for the poll.
    - Dropping records once nothing is owed or held bounds the table to the labels still in question.
22. **The jobs layer's duplicate check is atomic with its insert.**
    - PRD 04 §2 makes "an open job for this issue" the single check that every trigger relies on.
    - `agent_fox_jobs` stores the job in one transaction (its 12-REQ-3.6) but does not say the check is inside it.
23. **What this spec assumes about hub's issues list stays inside `internal/hub`.** That covers the route, the paging and the pull-request marker. Hub's API has no source in this repository, and the core (its Design Decision 35) and the worker (its R2) keep hub's names in one package.
24. **The requirements state behaviour.** Where the code lives, type and function names, the records' columns and how time is injected are left to design, as in the earlier specs of this split (their Design Decision 36). The table `af_poll_requests` and `internal/hub/fakehub` are named because other specs' code meets them: the core's migration list and the worker's fake hub.
25. **The hold starts at the cancel of a running attempt, not at the runner's report.** The worker writes the label back before it returns, and the jobs layer drops a report that comes after the 60 s grace. A hold keyed on `labels.set` would leave a window, or no hold at all, in which the restored label becomes a new job. Holding from the cancel, whatever the runner reports, closes both gaps. If the runner did not in fact put the label back, the first complete listing after the release delay releases the hold.
26. **A record whose job was purged before the poll saw it end holds.** The poll brings its records up to date at the start of every cycle, enabled or not, before any hub call, so it normally sees a job end long before the purge (`retain` is at least an hour). A purge can still come first, for example after the service was down longer than `retain` or while the workspace was unsubscribed. Then the poll cannot know whether the job was cancelled with a label put back, and it errs toward not spending again. The cost is a label left in place until a person removes it, or until the next complete listing that shows the issue without it.
27. **An absorbing removal that fails is recorded as owed, with no job.** Otherwise the label would become a new job once the open job ended between two cycles: the double run that Design Decision 10 guards against. It is settled like any owed removal.
28. **A hold's release counts only listings requested at least 5 minutes after the runner returned (or after the purged job was found gone).** PRD 01 §6.3 accepts that a forge lags. A list that does not yet show a label just put back would otherwise release the hold, and the next list would take the label as a new request. Five minutes is the default interval. It also covers a runner whose label write lands just before it returns. The delay costs a person who removes and re-adds the label at most one extra cycle, and only right after a cancel.
29. **Hub rejecting a call (another 4xx, 409 included) is an `ERROR`.** On a read it ends the cycle. On a label removal the removal stays owed and the cycle goes on, so one issue's refused removal cannot starve the issues after it. Hub's message goes into the record, since only it says why.
30. **A 404 on a label removal always confirms the removal. R7's not-found row applies only to the workspace record, the variables and the lists.** The hub forge client maps every 404 to issuex's not-found error. The poll tells the cases apart by the operation, and a label already gone is the outcome the removal wanted.
31. **A cycle that ends before reading the variable waits the last interval read, or 5 minutes when the last value disabled the poll or none was read.** A zero is not an interval. Waiting zero would hammer hub during an outage on a workspace that has turned polling off.
32. **Two failures are the same when they fall in the same row of R7's table and concern the same hub operation.** A failure of another row or operation is logged when it first occurs. The operator then sees each distinct cause once, and the counts in Goal 6 follow from the rule.
33. **The order of a listing is hub's. A listing that stays incomplete is logged once per workspace and label.** The client sends no sort, because hub's sort parameters are unknown. Held labels and owed removals stay in the filter, so enough of them can keep every listing incomplete. Holds are then not released, owed removals not settled, and requests past the first 100 may wait. The `WARN` makes that state visible without changing the cap.
34. **The create race is tested, not only required.** This spec takes on making the jobs layer's duplicate check atomic. A poll-versus-API race under `-race` (Goal 2) is the evidence that exactly one job results.
35. **`agent_fox_worker_recovery` is not a dependency.** Its resume paths cover shutdowns, crashes and transient errors, never a cancel. Its outcome file and reconciliation pass write no request label. The hold depends only on the cancel reaching a running attempt (Design Decision 25), not on any labels a delivery reports.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `11_agent_fox_service_core` | depends on, extends | Uses the store's subscription list, `label_prefix` and the in-process PAT read (not subscribed or unreadable). Also uses the append-only `af_` migration list, to which this spec adds one migration, plus slog logging, `internal/hub` and `internal/hub/fakehub`. The poll is wired into `cmd/agent-fox`'s start and shutdown. |
| `12_agent_fox_jobs` | depends on, modifies | Creates jobs through the label entry point, with its duplicate, not-subscribed and token-unreadable refusals, and uses the open-job lookup. Reads a job by id: its state, its source and request label, whether its cancel reached an attempt in a runner, and whether a runner of it is still in progress. Adds an exported read giving these if none exists. Makes the duplicate check atomic with the insert. Tests with the fake runner. Retention's purge is unchanged: the poll's records carry no foreign key to the job's row and outlive it (R5). |
| `13_agent_fox_worker` | depends on, modifies | Uses the hub calls for the workspace record and the resolved variables, the hub client's answer classes (refused, forbidden, not found, rejected, unavailable), and the hub forge client's `RemoveLabel` and error mapping. Adds `ListIssues` to that client, which removes it from the unavailable methods and their test, and extends the fake hub with the issues list. Relies on the worker's cancel cleanup putting the request label back only for an attempt stopped while running. Goal 8 uses its test harness. |

## Verified External API

**`github.com/agent-fox-dev/aflib/issuex`** (the pinned aflib `v0.0.0-20261019184100-07adcb691832`). Its source is in the module cache, outside the repository, so it was not read.
- `ListIssues(ctx context.Context, repo issuex.Repo, filter issuex.IssueFilter) (issuex.IssueList, error)`. Read from the call site `internal/toolio/dryrun.go:63`.
- `IssueFilter{State string; Labels []string; Assignee string; Sort string; Direction string; Limit int}`, with a default `Limit` of 100, and `IssueList{Issues []Issue; Incomplete bool}`. These come from the archived issuex spec (`.specs/archive/01_issuex_core/prd.md:89-90`); `internal/toolio/dryrun_test.go:171` constructs `issuex.IssueFilter{}`. **Unverified at the source.**
- `RemoveLabel(ctx context.Context, ref issuex.IssueRef, label string) error`, `IssueRef{Repo, Number, IsPullRequest}`, `Repo{Owner, Name, Host}`, `Issue{Number, IsPR, Labels, …}`, `IsNotFound`, `IsRateLimited` and `*HTTPError{Status, Message}`. These are read from call sites, as `agent_fox_worker` lists them.

**Planned by earlier specs (not yet code).** Names come from their `tasks.json`, and the real names are read when they exist.
- `internal/service/jobs`: `Create(ctx, req)`, with source kind `label`, `by`, and the request label; `OpenJob(workspace, kind, issue)`; and the errors `ErrDuplicate` and `ErrNotSubscribed`. No exported read of a job by id is planned.
- `internal/service/store`: `List`, `Get` and `Token(ctx, slug)`, with the errors `ErrNotSubscribed` and `ErrUnreadable`.
- `internal/hub`: `WorkspaceRecord` and `ResolvedVars`, the forge client (`internal/hub/forge.go`) and its factory, and `internal/hub/fakehub`.

**Hub's issues list.** **Unverified**: hub has no source or schema in this repository.
- Assumed: `GET /api/v1/workspaces/{slug}/issues?state=open&labels=<label>` with the PAT as a bearer token.
- The answer holds issue objects of the same shape as hub's single-issue read, including the pull or merge request marker that `agent_fox_worker` assumes.
- Assumed: hub honours a page size of 100 and says when a further page exists, either with a cursor field or a `Link` header. The hub client confirms the real paging before implementation. Release and settlement (R5, R6) depend on telling a complete list from an incomplete one.
- Hub's default order and its sort parameters are unknown. The poll sends none and relies on no order (Design Decision 33).

**Standard library.**
- `time.ParseDuration`, which accepts `"0"` as zero;
- `math/rand/v2`, for the start delay;
- `context.WithCancel`, `database/sql` and `log/slog`.
