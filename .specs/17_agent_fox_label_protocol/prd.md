---
spec_id: "17"
spec_name: "agent_fox_label_protocol"
title: "agent-fox label protocol: create the seven labels on subscribe, and make a cancelled job's issue match its record"
status: "active"
created_at: "2026-10-23T16:51:11.759013Z"
updated_at: "2026-10-23T19:10:26Z"
intent_hash: "bf1cf6ba4cda3884d0993f8c08bdf7a103551ebb24f78db7babe8ab87cb3ad86"
schema_version: 2
source: "docs/prd/04-drive-the-service-from-the-forge.md"
---
## Intent

Complete the forge side of the label protocol (PRD 01 §3) for the agent-fox service. A repository carries the seven agent-fox labels once its workspace is subscribed, or subscribed again. The service creates them through hub at that `PUT`. After a person or an unsubscribe cancels a job, the issue shows what the job record says: the request label is back, and no status label that the cancelled job set is left. This holds whether or not an attempt was running. The labels mirror the job record. They are never its state.

## Goals

The tests for these goals run offline. They use the fake hub (`internal/hub/fakehub`), the real jobs layer on a temporary database with `jobs/fakerunner` and `jobs/fakeclock`, and workspace `widgets` subscribed with prefix `af:`.

1. **Labels on subscribe.** `PUT /api/v1/workspaces/widgets` sends seven label-creation requests through hub, in table order (R1). Each request carries the name, the colour and the description. The fake hub ends up holding the seven labels. A second `PUT` with the same prefix answers `200`, and the fake hub's label set does not change. A `PUT` with prefix `bot-` creates the seven `bot-` labels and leaves the `af:` ones in place.
2. **A label that hub reports as already there does not fail a resubscribe.** The test runs three times. In each run the fake hub answers the third label creation one way: `409`, or `422` with `error_type` `already_exists`, or `400` with a message that contains "already exists". Each time the `PUT` goes on to the fourth label and answers as in goal 1.
3. **A refused subscribe stores nothing.** The fake hub answers the third label creation with one of the following:
   - `403`: the `PUT` answers `422` `forge_forbidden`.
   - `502` with `error_type` `forge_forbidden`: the `PUT` answers `422` `forge_forbidden`.
   - `401`: the `PUT` answers `422` `invalid_hub_token`.
   - `404`, or `405`: the `PUT` answers `502` `hub_unavailable`, with a message that says hub does not serve label creation.
   - `503`: the `PUT` answers `502` `hub_unavailable`.

   In every case no fourth creation is sent. A new subscription is not stored. A replaced subscription is left exactly as it was.
4. **A blocked job, cancelled.** A poll-created fix job for issue 42 is blocked. Its record shows `af:needs-human` set, and issue 42 carries that label. `POST /api/v1/jobs/{id}/cancel` answers at once, before any forge write, with `labels.cleanup` `"pending"`. Shortly afterwards the issue carries `af:fix` and no status label. The job then shows `labels` `{"set": ["af:fix"], "removed": ["af:needs-human"], "restore": null, "cleanup": "done"}`.
5. **Other cancels that reach no runner remove only what the job accounts for.**
   - A queued fix job that never ran, on an issue that carries `af:landed` and `af:ready` from earlier jobs: `af:fix` comes back, and `af:landed` and `af:ready` stay.
   - A queued re-triage of an issue that carries `af:ready`: `af:triage` comes back, and `af:ready` stays.
   - A job waiting out a transient backoff: its request label comes back.
   - A failed job that was retried and then cancelled while queued: `af:failed` is removed, and the request label comes back.
   - A blocked API fix job: it loses `af:needs-human`, and no label is added.
   - An attempt was shut down and left `af:in-progress` on the issue. Its job is cancelled while the attempt waits to be handed over again. The cleanup removes `af:in-progress`.
6. **Two open jobs of one issue.**
   - A blocked triage job of issue 42 is cancelled while a blocked fix job of issue 42 also shows `af:needs-human` set. `af:needs-human` stays, and `af:triage` comes back.
   - A triage job of issue 42 is cancelled while it is queued behind a running fix job of issue 42, and the issue carries `af:in-progress`. `af:in-progress` stays.
7. **A cancel that reaches a runner before its hub session.**
   - A blocked poll-created fix job of issue 42 is answered. The runner of its second attempt (a `fakerunner.Do` step) is cancelled before it reports a hub session, and then returns. Afterwards the issue carries `af:fix` and no `af:needs-human`.
   - A runner reports a hub session through its recorder before the cancel. Its job gets no cleanup: the fake hub records no label request from the jobs layer.
8. **A cleanup that fails is retried, then given up.**
   - The fake hub answers `503` to the label write. `labels.cleanup` stays `"pending"`, and `labels.restore` stays `"af:fix"`. One `WARN` is logged. Once hub answers again, a sweep one minute later (by the injected clock) completes the cleanup.
   - The fake hub answers `403` instead. The cleanup is abandoned with one `ERROR`, `labels.cleanup` becomes `"label_not_set"`, and nothing is tried again.
   - A cleanup still pending one hour after its cancel is abandoned with one `ERROR`, and `labels.cleanup` becomes `"label_not_set"`.
   - A service restarted while a cleanup is pending completes it at its first sweep. The start does not wait for the try: with hub's label routes delayed by 30 seconds, `Start` returns, and the sweep re-queues the queued attempts, before the try ends.
9. **The poll holds a label that the cleanup put back.**
   - After the cleanup of goal 4 has put `af:fix` back on issue 42, the next three poll cycles create no job and remove nothing.
   - The hold is released by a complete listing without the issue, requested at least 5 minutes after the poll first saw that the cleanup was no longer pending. After that, re-adding the label makes exactly one new job.
   - A poll-created job whose cleanup is abandoned before the label is back holds nothing once the poll sees the abandonment.
10. **An unsubscribe cleans up too.**
    - `DELETE /api/v1/workspaces/widgets` is called while a poll-created fix job of issue 42 is blocked. Before the subscription is removed, issue 42 carries `af:fix` and no `af:needs-human`, and the job's `labels.cleanup` is `"done"`.
    - With the fake hub unavailable, the `DELETE` returns no later than 60 seconds (by the injected clock) after its cancels, and the subscription is removed. The next try of the cleanup finds the workspace no longer subscribed, and `labels.cleanup` becomes `"label_not_set"`.
11. `make check` passes offline.

## Non-goals

These belong to later specs or are out of scope:

- **Hub's event stream as a trigger** (`hub.forge.issue.labeled`, `source.kind: event`, `hub_events`, reconnect and heartbeats) and **cancelling a job when its issue is closed**. These are `agent_fox_event_stream`, the next spec of this split. The issue-closed cancel will set no label, so it brings its own cancel cause. This spec's cleanup runs only for a person's cancel and for an unsubscribe (R4).
- **Answering a blocked job or retrying a failed one from the forge** (a comment, then the request label again) and **`AF_AUTO_FIX`**. These are `agent_fox_forge_followups`, the last spec of this split. Until it lands, the poll keeps treating a request label on a blocked job's issue as absorbed.
- **Renaming or deleting labels.** A changed prefix creates the new set. Labels are never deleted, by an unsubscribe or by anything else.
- **Creating labels for a workspace subscribed before this change, without a `PUT`.** Such a workspace gets its labels at its next `PUT` (Design Decision 24).
- **Reconciling labels a person moved.** The service corrects only its own writes (PRD 01 §3, D5). The cleanup leaves every status label that the cancelled job does not account for.
- **Label state in the subscription view.** `GET /api/v1/workspaces/{slug}` is unchanged.
- **A `label_not_set` warning in the worker's envelopes.** The worker's existing warnings stay as they are. `label_not_set` names a cancel cleanup that did not finish (R5, Design Decision 9).
- **Changing the worker's mirror of attempts.** The worker's mark, its outcome mirror and its running-cancel cleanup behave exactly as `agent_fox_worker` specifies. They only take the label names from the shared table. A failed write of the worker after its hub session is open stays the worker's warning, and the cancel cleanup does not retry it.
- **Making `ForgeClient.CreateLabel` available** (Design Decision 22).
- **Changing how `af` presents a `422`** (Design Decision 23).
- **Changes to hub**, and carry patches (PRD 05).

## Background

PRD 01 §3 defines the label protocol, with seven labels under the subscription's prefix:
- the request labels `triage` and `fix`;
- the status labels `in-progress`, `ready`, `needs-human`, `landed` and `failed`.

It sets these rules:
- the labels are created on subscribe, through hub's forge API;
- an existing label is left as it is;
- a forge credential that may not create labels fails the subscribe "with the forge's answer, not later";
- a request label is consumed;
- one status label is shown at a time, and `ready` survives a fix's run;
- a label is never the only record.

PRD 04 §1 and §5 restate these rules and map job states to labels:
- running → in-progress;
- a done triage → ready or needs-human;
- a done fix → landed;
- blocked → needs-human;
- failed → failed;
- cancelled → the request label restored, with no status label.

The earlier specs left two gaps in this protocol.

**The labels are not created.** `subscribe` (`internal/service/api/subscribe.go:63`) checks the token with two hub calls (`Hub.Workspace`, `Hub.User`) and stores it with `Store.Upsert`. It never asks hub to create a label. `docs/api.md` says "Scopes of the token are not checked at subscribe". The hub forge client deliberately leaves `CreateLabel` unavailable (`internal/hub/forge.go:387`, `notAvailable("CreateLabel")`), and its tests assert this (`internal/hub/forge_test.go:271`, `internal/hub/listissues_test.go:300`). No code outside tests calls `CreateLabel`. The fake hub serves no label route at workspace level: `workspaceRoute` in `internal/hub/fakehub/workspace.go` answers `404` for a route under a workspace that a test did not add with `AddWorkspace`.

**Some cancels leave the issue stale.**
- `Store.CancelJob` (`internal/service/jobs/cancel.go:24`) sets `labels_restore = COALESCE(request_label, labels_restore)`.
- `docs/api.md` describes `labels.restore` as the request label that belongs back on the issue, "and the service writes no label itself".
- Only the worker writes labels. Its mirror (`internal/worker/mirror.go`) sets one status label at a time (`setStatus`, which keeps `ready` during a fix). Its cancel path takes `in-progress` off and puts the request label back. That path runs only for an attempt that is in a runner and has opened its hub session: `mirror` returns before any write when the attempt has no session (`mirror.go:132`). A shutdown writes nothing either, and leaves the labels for the attempt that is handed over again.
- So these cancels leave `needs-human` (or `failed`, after a retry) on the issue, and their request label never comes back:
  - a job cancelled while queued or blocked;
  - a job whose runner was cancelled before its hub session opened.
- `agent_fox_poll` lists this gap among its non-goals: "applying `labels.restore` for jobs cancelled while queued or blocked" is PRD 04's. Its R6 states the consequence: such a job "holds nothing".
- The poll's hold logic is in `internal/service/poll/reconcile.go`. In `note`, a cancelled job whose cancel did not reach a runner has ended. A cancel that reached a runner holds, whatever the runner reports.
- A triage job and a fix job of one issue can be open together. The unique index of migration 2 (`af_jobs_one_open_per_issue`) is per kind, and `agent_fox_poll` Design Decision 8 relies on this.

What already exists and is reused:
- **The worker's status mirror.** It reads the issue's labels at the start of each attempt (`internal/worker/resolve.go:232`), so a status label left stale by a failed write is removed at the next transition. PRD 04's "retry on the next transition" already holds for attempts.
- **The job's own record of its labels.** A runner's report replaces the job's `labels_set` and `labels_removed` (`internal/service/jobs/finish.go:266-293`). An attempt's hub session is recorded in `af_attempts.hub_session` while it runs, and each delivery clears it (`Store.Claim`).
- **The hub client's answer classes** (`internal/hub/calls.go:150`): refused, forbidden, not found, rejected, and unavailable. A `502` with `error_type` `forge_forbidden` or `forge_unauthenticated` is a rejection, not unavailable (erratum 13 §3). A rejection carries hub's `error_type` as its code.
- **The jobs layer's minute sweep** (`internal/service/jobs/sweep.go`), its injectable clock, and its `loops` wait group for shutdown.
- **The append-only migration list** (`internal/service/store/migrations.go`). The last entry is 6.

This is the first of three specs written from `docs/prd/04-drive-the-service-from-the-forge.md`.

## Requirements

### R1. The seven labels, in one table

The protocol has exactly these seven labels. Each label's name is the subscription's `label_prefix` followed by its suffix. `AF_LABEL_PREFIX` is never used, as `agent_fox_worker` and `agent_fox_poll` decided.

| Suffix | Kind | Colour | Description |
|---|---|---|---|
| `triage` | request | `0e8a16` | Ask agent-fox to triage this issue |
| `fix` | request | `0e8a16` | Ask agent-fox to fix this issue |
| `in-progress` | status | `fbca04` | agent-fox is working on this issue |
| `ready` | status | `1d76db` | Triaged: the diagnosis is in the body |
| `needs-human` | status | `d93f0b` | agent-fox stopped with a question; see the last comment |
| `landed` | status | `0e8a16` | A branch and a pull request (or a carry patch) exist |
| `failed` | status | `b60205` | agent-fox could not finish; see the last comment |

Four parts of the service name these labels, and all four use the names of this table, from one source (Design Decision 8):
- the subscribe step (R2), which creates them;
- the worker's mirror, which writes the status labels and puts back a request label;
- the cancel cleanup (R4), which removes status labels and puts back a request label;
- the poll, which reads the request labels.

The worker's and the poll's behaviour does not change.

### R2. Creating the labels when a workspace is subscribed

`PUT /api/v1/workspaces/{slug}` gains one step.

**Order.** The step runs after the body has been validated and after the token has passed both of today's checks (the gate and the user). It runs before the subscription is stored. The labels are created through hub (R3), with the `hub_token` of the request and the prefix the request stores (`af:` when the request omits it). They are created one at a time, in the order of R1's table.

**What counts as success.**
- A label is in place when hub creates it, or reports it as already there (R3). An existing label's colour and description are left as they are.
- When all seven are in place, the subscription is stored as today, and the answer (`201` or `200` with the view) is unchanged.
- One `INFO` record names the workspace, how many labels were created and how many were already there. It never contains the token.

**Workspaces subscribed before this change.** They have no labels until their next `PUT`. A token replacement, or a resubscribe with the same body, is such a `PUT`. Nothing else creates the labels.

**When a creation fails.** The first failure stops the step: no further label is asked for and nothing is written to the store. On a replace, the existing subscription stays exactly as it was. The answer depends on hub's answer:

| Hub's answer to a label creation | `PUT` answers |
|---|---|
| 401 | `422` `invalid_hub_token`, "hub refused the token" |
| 403 | `422` `forge_forbidden`. The message says that hub forbids the token to create labels, because it lacks the scope for hub's forge API |
| 502 with `error_type` `forge_forbidden` or `forge_unauthenticated` | `422` `forge_forbidden`. The message says that the forge refused hub's credential for the repository, and carries hub's message |
| 404 or 405 | `502` `hub_unavailable`. The message says that hub does not serve label creation for the workspace |
| another 4xx that does not report the label as already there (R3) | `422` `forge_forbidden`, with hub's message |
| unavailable: 429, another 5xx, a redirect, a transport failure, a timeout, or a body that is not hub's | `502` `hub_unavailable` |

A create and a replace are refused alike.

**The new error code.** `forge_forbidden` is added to the error envelope's code table with status `422`. Like every other message, its message never contains a token.

**How `af` shows it.** `af workspace subscribe` shows `forge_forbidden` as it shows every `422` today: an auth failure that prints the message and the code, with the hint `--hub-token-file`. The client does not change (Design Decision 23).

**Labels already created stay.** A label created before a failure is left on the forge, and the next `PUT` finds it there. The rule "nothing is written before every check has passed" now covers the store only (Design Decision 3).

**A changed prefix.** A replace with a different prefix creates the new seven labels. The labels of the old prefix are neither renamed nor deleted. An unsubscribe deletes no label.

### R3. Hub's label route, in the hub client and in the fake hub

**The hub client** (`internal/hub`) gains the call that creates a label for a workspace:
- `POST /api/v1/workspaces/{slug}/labels` with the body `{"name", "color", "description"}`, using the PAT as the bearer token.
- Its bound, its refusal of redirects, its reading limits and its answer classes are those of the client's other calls. No error carries the PAT.
- It reports whether the label was created or was already there:
  - `201` is created;
  - `200` is already there;
  - a refusal that says the label exists is also already there. That is a `409`, or any other 4xx whose `error_type` is `already_exists` or whose message contains "already exists" in any case.
- Every other answer is classed as the client's other calls class it.

**The forge client does not change.** `ForgeClient.CreateLabel` stays among the methods that are not available through hub, and its tests stay as they are (Design Decision 22).

**The fake hub** serves the route:
- for any slug that the token sees (granted with `AddUser`, `AddWorkspaceToken` or `AddWorkspace`), whether or not the workspace was added, so that the existing API tests keep subscribing;
- it keeps each slug's labels (name, colour, description);
- it answers `201` for a new name and `200` for a name it holds, and records every request;
- it fails on demand through the existing fault seams (`Fail`, `FailOnce`, `FailFor`, `Inject`), including an answer with any status and body;
- a test can read a slug's labels.

### R4. The cancel cleanup

**Which cancels get it.** A job with an issue can be cancelled by a person (`POST /api/v1/jobs/{id}/cancel`) or by an unsubscribe (R6). The jobs layer runs the **cancel cleanup** for such a cancel when no runner writes the job's labels. That happens in two cases:
- **No runner.** The cancel reaches no attempt in a runner of this process. The job is queued (an attempt waiting out a transient backoff included), or blocked, or its attempt was claimed but its runner was not yet called.
- **A runner without a session.** The cancel reaches a runner that has not reported its hub session. The worker writes nothing to the issue in that case (`agent_fox_worker`, `mirror.go`). This is known only when the attempt is recorded cancelled, which happens when the runner returns or when its grace runs out.

**Which cancels do not.**
- A cancel that reaches a runner that has reported its hub session. The worker's own cleanup handles it (`agent_fox_worker` R8), unchanged, warnings included.
- A cancel of a job with no issue (a triage of a text or a file).
- Any other cancel cause, such as the issue-closed cancel of `agent_fox_event_stream`.

**Timing.**
- **No runner:** the cleanup is marked pending, durably, in the transaction that cancels the job. Its first try starts at once after that transaction.
- **A runner without a session:** the cleanup is marked pending in the transaction that records the cancelled attempt. Its first try starts once no runner of the job is in progress.
- The cleanup never delays the cancel route's answer. That answer still shows `labels.restore` as today, with `labels.cleanup` `"pending"` in the first case.
- At most one try of a job's cleanup runs at a time.

**One try:**
1. Read the subscription's token, and read the workspace record through hub to learn the forge repository.
2. Read the issue's current labels.
3. Of the status labels the issue carries, remove:
   - `in-progress`, unless another job of the same issue is running;
   - each other status label that the job's record shows its own attempts set (`labels.set`, as its runners last reported it), unless another open job of the same issue shows that label in its `labels.set` too.

   Every other label stays. That includes a status label that an earlier job set and that is still true, such as `ready` or `landed`, and a label a person set.
4. If the job consumed a request label and the issue does not carry it, add it.

A try writes only what is missing. A try repeated after a partial one, or after a runner that wrote after all, is therefore safe.

**What a finished cleanup records:**
- `labels.restore` becomes `null` when the issue carries the request label, whether the try put it there or it was there already.
- If the try wrote anything, `labels.set` and `labels.removed` are replaced by what it set and removed, as a runner's report replaces them.
- `labels.cleanup` becomes `"done"`, or `"label_not_set"` where R5 says so.
- One `INFO` record names the workspace, the job, the issue, and the labels set and removed.

The job's event log is not touched: the job is already at rest.

**`labels.cleanup` on the job document.** The `labels` object gains the member `cleanup`. Its value is one of:
- `"pending"`: the cleanup has not finished, and tries go on;
- `"done"`: the request label is on the issue, and the status labels of step 3 are gone;
- `"label_not_set"`: the cleanup ended without that;
- `null`: the job has had no cleanup.

A value other than `null` makes `labels` non-null.

### R5. Failures, retries and durability of the cleanup

Every outcome of a try:

| What happens in a try | The cleanup |
|---|---|
| The workspace is no longer subscribed | Ends `label_not_set`. Nothing is written. An `INFO` record |
| The token is unreadable or the store fails | Stays pending. A `WARN` record, on the first failure of the job's cleanup only |
| Hub is unavailable on any call: 429, a 5xx other than the two `forge_*` refusals, a transport failure, a timeout, a body that is not hub's, or a workspace record without a forge URL | Stays pending. A `WARN` record, on the first failure of the job's cleanup only |
| On any call: hub refuses the token (401), hub forbids the call (403), or the forge refuses hub's credential (`502` `forge_forbidden` or `forge_unauthenticated`) | Ends `label_not_set`. An `ERROR` record names the operation and carries hub's message. For 401 and 403 it points to `PUT /api/v1/workspaces/{slug}` |
| Hub answers 404 when the workspace record is read | Ends `label_not_set`. An `ERROR` record: hub does not find the workspace |
| The record's forge URL names no repository | Ends `label_not_set`. An `ERROR` record |
| Hub answers 404 when the issue is read | Ends `label_not_set`: the issue is gone. An `INFO` record |
| Hub rejects the record read or the issue read with another 4xx | Ends `label_not_set`. An `ERROR` record with hub's message |
| Hub answers 404 when a label is removed | That removal is done |
| Hub rejects a label removal with another 4xx | That label stays. The try goes on, and the cleanup ends `label_not_set`. A `WARN` record |
| Hub answers 404 or another 4xx when the request label is added | Ends `label_not_set`. An `ERROR` record names the operation and carries hub's message |

A cleanup that ends records what its last try wrote, as R4 says.

**Retries.**
- On each tick of the jobs layer's sweep (every minute), a pending cleanup is tried again, until one hour after its cancel by the layer's clock.
- A cleanup still pending after that hour ends `label_not_set`, with one `ERROR` record.
- A cleanup that ends `label_not_set` leaves `labels.restore` as it is, unless the request label is on the issue.

**Tries run in the background.**
- The sweep, and the first sweep at start, only start the tries of pending cleanups, after they have re-queued the attempts. Neither the start of the service nor the sweep waits for a try.
- A whole try is bounded by one minute. Each hub call in it is bounded by the hub client's own bound.
- The number of tries in flight at once is bounded, and only one try of a job runs at a time. A sweep starts no try for a job whose try is still running.

**Restart and shutdown.**
- A cleanup that is pending when the service stops stays pending. The first sweep after the next start tries it again, or ends it if its hour has passed.
- A try in flight at shutdown is cancelled along with the layer's other loops.
- The retention purge never removes a job whose cleanup is pending.

**Isolation.** No log record holds a PAT. A cleanup's failure never fails the cancel, another job, or another workspace.

**Without a forge.** The jobs layer can be built with no forge to clean up through, as `agent_fox_jobs`'s own tests build it. It then behaves exactly as today: no cleanup is marked, and `labels.cleanup` is `null`.

### R6. An unsubscribe cleans up its jobs before the subscription goes

When `DELETE /api/v1/workspaces/{slug}` cancels a workspace's jobs:
- Each cancel in one of R4's two cases gets the cleanup, marked and tried as R4 says, even though its cause is the unsubscribe.
- Before it removes the subscription and its token, the unsubscribe waits for the first try of each cleanup it marked. This runs alongside its wait for the running attempts, which is unchanged. The unsubscribe stops waiting for the tries 60 seconds (the cancel grace) after its cancels and its wait for the running attempts have both ended.
- A cleanup still pending when the subscription is removed ends `label_not_set` at its next try, because the workspace is no longer subscribed (R5).
- A running attempt that has reported its hub session keeps the worker's own cleanup, unchanged.

### R7. The poll holds a label that the cleanup puts back

These rules apply to a cancelled job that the poll made. They replace `agent_fox_poll` R6's "a job cancelled while queued … or while blocked … holds nothing".

- **The hold.** The job holds its request label from the cancel in two cases:
  - the cancel reached a runner, as today, whatever the runner reports;
  - the job's cleanup is pending, or has finished with `labels.restore` `null`. This means the request label is on the issue, whoever put it there.
- **No hold.** A job whose cancel reached no runner holds nothing in two cases: it got no cleanup, or its cleanup ended with `labels.restore` still set. A record that held while the cleanup was pending stops holding the next time the poll sees that the cleanup ended this way.
- **The release clock** starts the first time the poll sees that no runner of the job is in progress and no cleanup of the job is pending.
- **Release** happens as `agent_fox_poll` specifies: through a complete listing without the issue, requested at least the release delay (5 minutes) after the release clock started.

These rules hold whatever cancelled the job, a person or an unsubscribe.

### R8. Documentation and tests

**Documentation.** After `make doc`, the following are updated in the same change:

| File | What it gains |
|---|---|
| `docs/api.md` | In *Subscribing*: the label step, the seven labels and the answers of R2, including that hub's report of a label as already there in any of its forms counts as present. Also: a workspace subscribed before this change gets its labels at its next `PUT`, and `af` shows `forge_forbidden` with its usual `422` hint. In *Errors*: `forge_forbidden`. In *The job document*: `labels.cleanup` and its values, and the service now writes labels itself for a cancel that no runner cleans up. In *Cancel*: which cancels get the cleanup, what it removes and keeps, its retries, and that `labels.restore` stays set while the request label is not back. In *Removing*: an unsubscribe cleans up its jobs before it removes the subscription, and waits at most 60 seconds for that. In *Jobs that a request label makes*: the hold of a label that a cleanup put back |
| `docs/architecture.md` | The label protocol in *The service, as built*: the one table, the subscribe step, the cancel cleanup and its retries. In *The label poll, as built*: the holds |
| `docs/configuration.md` | The label poll section's note on what a cancel puts back |
| `docs/development.md` | Where the label table and the cleanup live; the fake hub's label route |
| `README.md` | One sentence: subscribing a workspace, or subscribing it again, creates the labels a team uses |

`docs/cli.md` is unchanged, because the `af` client does not change. Existing documentation tests (`docs/*_docs_test.go`) must keep passing. No new test checks the documents.

**Tests.**
- Every test runs offline. It uses the fake hub extended by R3, the jobs layer with `fakerunner` and `fakeclock`, and the poll's `Supervisor.Cycle`.
- Some tests of `agent_fox_poll` assert that nothing is put back for a job cancelled while queued, or cancelled by an unsubscribe while queued or blocked. Those tests change to assert the hold of R7.
- The tests of `agent_fox_jobs` run without a forge, unchanged.

## Design Decisions

1. **PRD 04 is split into three specs, and this one is first.** This spec covers the labels and the mirror of a cancel. `agent_fox_event_stream` follows, then `agent_fox_forge_followups`. The labels have to exist before a person can pick them on the forge. The cancel cleanup changes the poll's holds, and the stream's rules will be built on those holds. Answering and retrying change the rules that the poll and the stream share, so they come after both. In review the cleanup grew to cover a runner cancelled before its hub session and the cancels of an unsubscribe. It stays in this spec because the holds depend on it.
2. **The labels are created on every `PUT`, create and replace alike, one at a time and in table order, stopping at the first failure.** Hub reports an existing label as already there, so repeating the step is harmless. Repeating it is how a changed prefix gets its labels.
3. **The labels are created with the token being subscribed, after the two token checks and before the store write. A failure stores nothing.** PRD 01 §3 makes a forge that cannot take labels a subscribe failure "not later", and this checks the forge scope of the token at subscribe. The core's rule "nothing is written before every check has passed" now covers the store only. Labels created before a later failure stay on the forge. That is harmless, because an existing label is left as it is and the next `PUT` finds it.
4. **A label that hub reports as already there counts as present, whichever way hub reports it.** That is `200`, `409`, or a 4xx whose `error_type` is `already_exists` or whose message says "already exists". This is one rule, applied in the hub client (R3). The input names `200` for an existing label, but hub's real answer is unverified. The forges themselves refuse an existing label: GitHub with `422` `already_exists`, GitLab with `409` or `400` "already exists" (`.specs/archive/02_issuex_github/prd.md:127`, `.specs/archive/03_issuex_gitlab/prd.md:132`). A hub that relays those answers must not fail every resubscribe.
5. **A refused label creation answers as follows:**
   - **`401`** is `invalid_hub_token`.
   - **Unavailable** is `502` `hub_unavailable`.
   - **`404` or `405`** is `502` `hub_unavailable`, with a message saying that hub does not serve label creation. The token's view of the workspace was checked just before, so these answers say that hub lacks the route. They do not mean the forge refused, and no other token cures them. A create and a replace are refused alike: the service needs hub's label route (PRD 01 §8) as it needs its other routes, and storing a subscription without labels would break the subscribe's promise. A replace refused this way leaves the existing subscription working as it was.
   - **Every other refusal** is `422` `forge_forbidden`. The subscriber has to fix something on hub or on the forge, and a retry cannot fix it. One code keeps clients simple, and the message says which party refused. `422` matches the core's `invalid_hub_token` for a token that cannot serve the workspace.
6. **Colours are sent as six lowercase hex digits without `#`.** That is issuex's `Label.Color` convention (archived issuex spec, TS-01-6: `Color: "ff0000"`), and its GitHub backend strips a `#`. The colours and descriptions are PRD 04's table.
7. **A changed prefix creates a new set. Labels are never renamed or deleted, by an unsubscribe or by anything else.** Labels on issues carry history that a person may still read. Renaming or deleting is a person's choice on the forge.
8. **One table in code defines the seven labels.** The subscribe step, the worker's mirror, the cancel cleanup and the poll take their names from it. This prevents a subscribe that creates names the worker or the cleanup does not write. The worker's behaviour is untouched.
9. **The input's `label_not_set` is the state of a cancel cleanup that did not finish (`labels.cleanup`). It is not a new warning of the worker's transitions.**
   - For attempts, the worker already reports a failed write as a warning (`status_label_failed`, `comment_failed`, `request_label_failed`) with a failed side effect, which the job document shows in `side_effects`. It corrects its status labels at the next transition, because each attempt reads the issue's labels afresh. That is the input's "a warning on the job and a retry on the next transition", under the names that `agent_fox_worker` fixed. This spec does not rename them.
   - The cleanup has no envelope, so its states are a member of `labels`. `pending` means it may still succeed, `done` means it finished, and `label_not_set` means it gave up or a removal was refused. The job API therefore tells a pending cleanup from an abandoned one, and `labels.restore` still shows which label is not back.
   - This departs from the input's wording on purpose. The input's acceptance criterion 6 maps to goal 8 for the cleanup, and to the worker's existing tests for transitions.
10. **The cancel cleanup belongs to the jobs layer, behind an optional seam that `cmd/agent-fox` wires to a hub-backed implementation.** Only the layer knows that a cancel reached no runner, or reached one that recorded no hub session. The layer must not import hub. A missing seam keeps today's behaviour, so `agent_fox_jobs`'s tests stand.
11. **The cleanup runs for every cancel, by a person or by an unsubscribe, that leaves no runner to write the labels. API jobs with an issue are included.** Without it, a blocked API job's issue would keep `needs-human` after the cancel. The issue must mirror the record whatever made the job.
12. **The cleanup removes only the status labels that the cancelled job accounts for, and none that another open job of the issue accounts for.**
    - The job's own `labels.set` is the record of what its attempts left on the issue. A blocked job's `needs-human` and a retried job's `failed` are in it. An earlier job's `ready` or `landed` is not, so those stay: they are still true, and the issue keeps mirroring those jobs' records.
    - `in-progress` is true only while a job of the issue runs, so it is removed unless another one does. This also clears an `in-progress` that a shut-down attempt left behind.
    - A triage job and a fix job of one issue can be open together (migration 2's index is per kind). A label that the other job's record or state accounts for therefore stays.
    - `ready` is never removed, because no open job's record sets it. This makes PRD 04's "cancelled → the request label restored, no status label" true of the cancelled job, with PRD 01's `ready` exception.
    - One difference remains and is accepted. A running re-triage that is cancelled still loses `ready`, through the worker's mark, which this spec does not change.
13. **The cleanup runs from a pending mark that is written durably with the cancel, or with the cancelled attempt.** A cancel stays immediate and never depends on hub. The mark carries the cleanup across a restart.
14. **Each try reads the issue and writes only what is missing.** Tries can repeat after a partial failure or a restart without double effects. A label that a person, or a late runner, already put back is not written again.
15. **A pending cleanup is retried at each minute's sweep for one hour after its cancel. Failures that may pass are retried, and refusals end it.** The sweep already runs every minute on the layer's clock. An hour covers a hub outage of ordinary length. It is shorter than the minimum retention (`database.retain` ≥ 1 h), so a purge never waits long. Refusals would only repeat. R5's table gives every operation and answer class an outcome, so two implementations end the same way.
16. **An unsubscribe cleans up its jobs before it removes the subscription. This replaces the earlier decision that it writes nothing.** For a running attempt, the worker already writes to the issue during the same `DELETE`, while the unsubscribe waits for the attempt and before the token is removed. The token is therefore available, and the service already writes to issues it is about to stop watching. Leaving `needs-human` on a blocked job's issue would contradict PRD 04's mapping for no gain. The wait is bounded by the cancel grace, so a hub outage delays `DELETE` by at most 60 seconds beyond today. A cleanup the wait does not finish ends at its next try, because the workspace is gone.
17. **The poll holds a label that the cleanup puts back, and the release clock waits for the cleanup.** Otherwise the next cycle would take the restored label as a new request, and a cancel would become a restart that spends again. This is the reason behind `agent_fox_poll` Design Decision 12, now applied to every cancel. "Put back" means that the cleanup finished with the request label on the issue, whoever put it there. Whether a job holds therefore does not depend on whether the poll happened to look while the cleanup was pending.
18. **The purge never removes a job whose cleanup is pending.** The cleanup reads the job's request label and records its result on the job.
19. **The requirements state behaviour. Design decides the rest:**
    - where the table and the cleanup live;
    - the names of types and functions;
    - the columns of migration 7;
    - how the seam is typed;
    - how the jobs layer tells the poll about a cleanup (for example, new facts in `jobs.Service.Standing`);
    - how a cancel records that its cleanup waits for the runner's report.

    The earlier specs of this split did the same. The table must be importable by `internal/service/api`, `internal/service/poll`, `internal/worker` and the cleanup's implementation without a cycle.
20. **Hub's label route follows hub's OpenAPI document where it differs from what R3 assumes, and the difference is recorded in an erratum.** That document is outside this repository (`../hub/docs/openapi.yaml`). Errata 13 and 15 handled the same situation the same way.
21. **A cancel that reaches a runner before its hub session opens gets the cleanup, as a cancel that reaches no runner does.** In that case the worker's mirror returns before any write. Otherwise the request label would never come back, and an answered or retried job would leave its `needs-human` or `failed` behind. The jobs layer can tell this case from `af_attempts.hub_session` once the attempt is recorded cancelled. Its tries wait until no runner of the job is in progress, so they never race the worker. A runner that wrote after all is harmless, because a try writes only what is missing (Design Decision 14). The poll holds such a job as it does today, since the cancel reached a runner.
22. **`ForgeClient.CreateLabel` stays not available through hub.** No loop and no part of the service calls it. The subscribe step creates labels with the hub client's own call, using the token being subscribed and no repository. Making it available would change `agent_fox_worker`'s surface and tests for nothing this input asks for.
23. **`af` is not changed for `forge_forbidden`.** `af` (`internal/afclient/errors.go`) shows every `422` as an auth failure with the hint `--hub-token-file`, and prints the code with the message. For a `403` from hub, the hint is right, because a token with the forge scope cures it. When the forge refuses hub's credential, the message says so, and `docs/api.md` says that another token does not cure it. Changing the sealed client is not worth one code. `docs/cli.md` stays unchanged.
24. **A workspace subscribed before this change gets its labels at its next `PUT`.** Creating them at start, or in the background, would need a token-using hub write outside any request, with its own failure reporting. A `PUT` with the same body is cheap and is already the remedy that the service points to for a token problem. `docs/api.md` and `README.md` say so.
25. **Cleanup tries run in the background, bounded, and never inside the sweep's re-queue or the start.** The start runs its first sweep before the workers' tickers. Several hub calls per pending cleanup inside it would delay the service and the re-queue of attempts during a hub outage.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `11_agent_fox_service_core` | depends on, modifies | Adds the label step to `PUT /api/v1/workspaces/{slug}` (`internal/service/api/subscribe.go`) and the error code `forge_forbidden` (`internal/service/api/errors.go`). Uses the store's `Token` and subscriptions, and appends migration 7 to the append-only list. |
| `12_agent_fox_jobs` | depends on, modifies | Adds to the jobs layer: the cancel cleanup seam, the durable pending mark (written with a cancel, or with a cancelled attempt that recorded no hub session), the background retries started by the sweep, the unsubscribe's bounded wait for the cleanups, and the purge's wait (`cancel.go`, `finish.go`, `grace.go`, `unsubscribe.go`, `sweep.go`, `retain.go`, `service.go`). Adds `labels.cleanup` to the job document (`model.go`, `document.go`). Extends what the layer tells the poll about a cancelled job (`read.go`). |
| `13_agent_fox_worker` | depends on, modifies | Adds the hub client's label route (`internal/hub`), and extends `internal/hub/fakehub` with it. The worker's mirror takes its status names from the shared table, with no change in behaviour. The cleanup reuses the hub forge client's `ReadIssue`, `AddLabels`, `RemoveLabel` and error mapping, and `WorkspaceRecord`. `ForgeClient.CreateLabel` is left as it is. |
| `15_agent_fox_poll` | depends on, modifies | Replaces R6's "a job cancelled while queued or blocked holds nothing" with R7's hold. Changes `note` in `internal/service/poll/reconcile.go`, and the tests that asserted the old rule. |

## Verified External API

**`github.com/agent-fox-dev/aflib/issuex`** (pinned `v0.0.0-20261019184100-07adcb691832`). Its source is in the module cache, outside the repository, and was not read. The symbols below come from call sites and archived specs:
- `ReadIssue(ctx, issuex.IssueRef) (issuex.IssueThread, error)`, whose `IssueThread.Issue.Labels` is a `[]string`: read from `internal/hub/forge.go:125` and `internal/worker/resolve.go:232`.
- `AddLabels(ctx, issuex.IssueRef, []string) error`, `RemoveLabel(ctx, issuex.IssueRef, string) error`, `issuex.IsNotFound(error) bool`, `issuex.ErrConflict` and `*issuex.HTTPError{Status, Message}`: read from `internal/hub/forge.go`, `internal/worker/mirror.go:54-87` and `internal/service/poll/handler.go:187`.
- `issuex.ParseRemote(string) (issuex.Repo, bool)`: read from `internal/hub/forge.go:19`.
- The colour convention of `issuex.Label.Color` (six hex digits, no `#`) comes from `.specs/archive/01_issuex_core/test_spec.json`, TS-01-6. It is **unverified at the source**. This spec sends colours in that form on hub's route and does not call `issuex.CreateLabel`.

**Hub's HTTP API.** Its source and schema are not in this repository. Its OpenAPI document is at `../hub/docs/openapi.yaml`, outside the repository.
- **Unverified (assumed):** `POST /api/v1/workspaces/{slug}/labels` with `{"name", "color", "description"}` and the PAT as the bearer token. It answers `201` for a created label and `200` for an existing one. PRD 01 §8 names the route, and PRD 04 §1 names the answers. R3 also counts a relayed "already exists" refusal as present (Design Decision 4).
- **Read from code:**
  - the answer classes, including a `502` with `error_type` `forge_forbidden` or `forge_unauthenticated` read as a `*RejectedError`, whose `Code` is hub's `error_type` (`internal/hub/calls.go:150-200`, `errorObject`);
  - the issue read and the label routes that the forge client already uses (`internal/hub/forge.go`);
  - `WorkspaceRecord`, which classes a record without a forge URL as unavailable (`internal/hub/calls.go:240`).

**Standard library:** `database/sql`, `log/slog`, `context`, `time`, `net/http`, `encoding/json`.
