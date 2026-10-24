---
spec_id: "18"
spec_name: "agent_fox_event_stream"
title: "Hub's event stream as the immediate trigger of the label protocol"
status: "active"
created_at: "2026-10-23T17:20:07.656012Z"
updated_at: "2026-10-24T02:23:08Z"
intent_hash: "d6e57c608582423ae88b71022b877290210b17b1ba7fd21b702c0b860b979ed7"
schema_version: 2
source: "docs/prd/04-drive-the-service-from-the-forge.md"
---
## Intent

Make hub's event stream the immediate trigger of the agent-fox service's label protocol. The service keeps one connection to hub's events for every subscribed workspace whose label poll is on. A request label that a person adds becomes a job within seconds, under the same rules the label poll applies. The poll stays as the catch-up for anything the stream missed, and `AF_POLL_INTERVAL=0` still turns every label trigger off. When an issue is closed, the service stops the work on that issue, puts no label back, and takes off the status labels of the jobs it stopped. The stream makes no second record of a request: the job record stays the truth, and the labels only mirror it.

## Goals

The tests for these goals run offline. They use:

- the fake hub (`internal/hub/fakehub`) with a scriptable event stream;
- the real store and the real jobs layer on a temporary state file, with `jobs/fakerunner` and `jobs/fakeclock`, and with `agent_fox_label_protocol`'s cancel cleanup wired to the fake hub;
- the label poll's supervisor, which owns the stream;
- workspace `widgets`, subscribed with prefix `af:`, with `AF_POLL_INTERVAL` unset (5 minutes) unless a goal says otherwise.

1. **A label becomes a job within a second.** Issue 42 is open and carries `af:fix`. The fake hub sends `hub.forge.issue.labeled` with id `evt-1`, issue 42 and label `af:fix`. Within one second:
   - exactly one fix job exists, with `source` `{"kind": "event", "by": "evt-1"}`, `request_label` `af:fix` and `input` `{"issue": 42}`;
   - one removal of `af:fix` from issue 42 has been sent through hub;
   - one poll record has been written for the job.

   If the same event (same id) is sent again, no job is made and no forge request is sent. If a poll cycle then runs while the fake forge still lists 42 under `af:fix` (removal lag), it makes no second job.
2. **The stream applies the poll's rules.** Each case below uses its own issue, and each gets a `labeled` event with a request label:
   - An issue that has an open fix job made through the API: the label is **absorbed**. It is removed and no job is made.
   - A stream-made job was cancelled while its attempt was in a runner, and the worker put `af:fix` back. The event for that restore is **held**: nothing is done and the label stays.
   - A stream-made job whose label removal failed with a 422 (the removal is **owed**): a second event removes the label and makes no job.
   - An issue that hub now reads as closed, as a pull request, or as no longer carrying the label: no job is made and no label is removed.

   A `labeled` event for a label that is not a request label (`af:ready`, `bug`) sends no request to hub. Neither does an event of any type other than `hub.forge.issue.labeled` and `hub.forge.issue.closed`, nor a `closed` event for an issue that has no open job.
3. **The stream and the poll never make two jobs for one request.** In 50 rounds, a new label is seen by a stream event and by a poll cycle at the same moment. Each round ends with exactly one job and the label removed.
4. **Reconnecting.** The waits below are measured on the fake clock.
   - When the fake hub ends the stream, the service reconnects after 1 s.
   - When connections keep failing, the waits between attempts are 1, 2, 4, 8, 16, 32, 60 and 60 s.
   - A connection that receives one frame, or one comment, resets the next wait to 1 s.
   - A connection that receives nothing for 60 s is closed and a new one is opened.
   - A label added while the stream was down (no event is ever sent for it) is found by the next poll cycle and becomes exactly one job.
5. **A closed issue stops its work and keeps none of its jobs' status labels.** In each case below, hub reads the issue as closed and the fake hub sends `hub.forge.issue.closed` for it.
   - Issue 42 has a running fix job made by the stream. Its runner has opened its hub session and set `af:in-progress`. The attempt ends `cancelled`, with a message saying that the issue was closed. Its lease is released, and its runner takes `af:in-progress` off.
   - Issue 43 has a blocked triage job whose record shows `af:needs-human` set. The issue carries that label, and `af:landed` from an earlier fix job. The job's `labels.cleanup` reads `"pending"`, then `"done"`. `af:needs-human` is removed, and `af:landed` stays.
   - Issue 44 has a fix job whose attempt was shut down, left `af:in-progress` on the issue, and waits to be handed over again. The cleanup removes `af:in-progress`.

   In every case:
   - each job reads `cancelled`, with `cancel_cause` `"issue_closed"` and `labels.restore` `null`;
   - nothing is added to the issue: no request label comes back, no status label is set, and no comment is posted;
   - nothing is held: after the issue is reopened and labelled `af:fix`, the next event makes a new job, and so does the next poll cycle when no event is sent.

   A `closed` event for an issue that hub reads as open cancels nothing. A `closed` event for an issue that has no open job sends no request to hub and logs nothing above `DEBUG`.
6. **Each failure is logged once.**
   - A `401` when connecting is one `ERROR` that names the `PUT` route that replaces the token.
   - A `403` is one `ERROR` that names the scope `audit:read`.
   - A `503` is one `WARN`.
   - A `404` on the events route, after the workspace record was read, is one `WARN` saying that hub does not serve the event stream and that labels are found by the poll alone. Each later attempt waits 60 s.
   - The stream keeps retrying in every case.
   - A stream that has been down for longer than the workspace's poll interval logs one `WARN`. It is not logged after the `404` `WARN`.
   - The first frame or comment after logged failures logs one `INFO`, and a failure after it is logged again.
   - Two request events in a row whose issue read hub answers with `503` log one `WARN`. After an event is handled without failure, the next such failure is logged again. These failures neither silence nor end the connection's failures.
   - A `labeled` event with a request label and no issue number logs one `WARN` per connection. An event of another type without an issue number logs nothing above `DEBUG`.
   - No log record contains the PAT.
7. **Event ids expire.** The ids of events that were taken are stored with the time they were taken. At the next scan of the supervisor, an id taken 7 days and 1 minute ago is deleted and a newer one is kept.
8. **Lifecycle.**
   - A workspace subscribed while the service runs has a connected stream within one scan period (60 s on the fake clock).
   - An unsubscribed workspace loses its stream at the next scan.
   - A `PUT` that replaces the token makes the next scan reconnect with the new token.
   - When the store answers "not subscribed" to the stream, the workspace's poll loop is taken away too. When the workspace is subscribed again, the next scan starts the loop and the stream.
   - When `serve` shuts down, it closes the streams before the jobs layer stops.
9. **`AF_POLL_INTERVAL=0` turns the stream off with the poll.**
   - With `0`, no connection to the events route is opened, and a label added to an issue makes no job.
   - When the variable is set to `5m`, a connection is open within 5 minutes on the fake clock.
   - A connected stream whose variable is set to `0` acts on no event once its reading is 5 minutes old. The next request event makes no job and removes no label, and the connection is closed.

## Non-goals

- **Answering and retrying from the forge, and `AF_AUTO_FIX`.** A request label added to an issue whose job is `blocked` is still absorbed here, and one added to an issue whose job is `failed` is still a new request, exactly as the poll does today. Turning those into an answer or a retry, the one-line reply, and the fix that follows a ready triage (`source.kind: triage`) belong to **`agent_fox_forge_followups`**.
- **Label creation at subscribe, and the cancel cleanup for cancels that reach no runner.** Both belong to **`agent_fox_label_protocol`**. This spec builds on them. It changes the cleanup in one way only: the issue-closed cause gets it without its restore step (R9).
- **Webhooks to the service.** Hub receives them. The service keeps no public endpoint and no webhook secret.
- **Replay.** Hub's stream has no `since` and no `Last-Event-ID`. A reconnect does not try to recover the gap, and does not bring the poll's next cycle forward. The poll at `AF_POLL_INTERVAL` is the catch-up.
- **Rebuild events** (`hub.rebuild.*`) and every other event type, including `unlabeled`, `reopened`, `edited` and pull-request events. They are ignored. PRD 05 consumes the rebuild events on the same stream.
- **Releasing a hold from the stream.** A `labeled` event never releases a hold. Only the poll's complete listing does, as today.
- **Labels on a closed issue that its cancelled jobs do not account for.** A label a person set, and a status label of an earlier job that is still true (`ready`, `landed`), stay as they are.
- **Closed issues while the poll is off.** With `AF_POLL_INTERVAL=0` no stream runs, so closing an issue cancels nothing, as today.
- **Settings.** The stream has no setting of its own. `AF_POLL_INTERVAL=0` turns it off with the poll (R2). There is no switch for the stream alone, and none for its backoff or its 60 s idle limit.
- **The `af` client** (spec 16, sealed). It is not changed. It passes the new `cancel_cause` field through like any other field of the job document.

## Background

PRD 03's service makes jobs from two places: its API, and the label poll of spec 15 (`internal/service/poll`). The poll lists each workspace's request labels every `AF_POLL_INTERVAL`, which is 5 minutes by default. PRD 01 (D11) and PRD 04 §2 make hub's SSE stream the immediate trigger:

- the route is `GET /api/v1/events?workspace=<slug>`, opened with the subscription's PAT, which carries `audit:read`;
- the poll stays as the catch-up, because hub's stream cannot replay.

What exists today:

- **The poll's rules.** For each issue a cycle applies four rules in order: absorbed, held, already taken (owed), new request (`internal/service/poll/handler.go`). The rules work over the records in `af_poll_requests` (migration 5, `poll/store.go`) and the jobs-layer reads `OpenJob`, `Create` and `Standing`. `note` (`poll/reconcile.go`) brings a record up to date with its job. Spec 17 extends `note` for the labels its cleanup puts back. The stream must run the same rules, or it would take back labels the worker restored, or make a second job for a label whose removal is owed.
- **What only the poll does.** Only a complete listing releases a hold, and only a cycle settles an owed removal for an issue that left the listing (`settle`, which runs for complete listings only).
- **The poll's switch.** `AF_POLL_INTERVAL` is a hub variable of the workspace, read at the start of each cycle (`poll/interval.go`). A zero duration turns the poll off: the cycle ends after reading the variables, "lists nothing and writes no label", and reads the variable again 5 minutes later (`docs/configuration.md`). The listings and the removals reach the forge, so the poll spends the workspace's forge rate limit, and the interval is how a team controls that (`docs/architecture.md`).
- **The supervisor.** The poll's `Supervisor` (`poll/supervisor.go`, `poll/loop.go`) scans the subscriptions every 60 s and keeps one loop per workspace. A cycle that finds the workspace not subscribed takes its loop away (`forget`), and the next scan starts a new one if the workspace is subscribed again. Per-workspace state lives in `wsState` (the interval last read, and logged failures). Failure logging (`poll/failure.go`) logs each failure once until recovery, as a `WARN` when hub is unavailable and an `ERROR` when a person must act.
- **Event sources already allowed.** The jobs layer already accepts the source kind `"event"`, so no schema change is needed for it.
- **No SSE client for hub.** None exists. `internal/afclient/sse.go` is the `af` client's reader for the service's own stream. It is unexported and belongs to sealed spec 16.
- **Hub field names stay in `internal/hub`.** `internal/hub/calls_test.go` fails if any non-test Go file outside `internal/hub` contains hub field names (`"event_type"`, `"workspace_slug"`, `"is_pr"`, and others). Event decoding must therefore live in `internal/hub`.
- **Cancel causes.** The causes are a person's cancel and an unsubscribe (`jobs/runner.go`). They are not stored. A cancel always marks the request label to be restored (`Store.CancelJob`).
  - The worker's `mirror` (`internal/worker/mirror.go`) responds to any cancel by taking off the `af:in-progress` it set and putting the request label back.
  - Spec 17 adds a cleanup for cancels that no runner with a hub session cleans up. It removes the status labels that the cancelled job accounts for, and puts the request label back. It runs only for a person's cancel and an unsubscribe.
  - A cancel because the issue was closed must not put the request label back. Of the cleanup it wants only the removals.
- **The fake hub has no event stream.** It records requests, injects faults by path (`Inject`, `Fail`, `Hangup`), and holds issue state with list lags (`SetRemovalLag`, `SetAdditionLag`).

## Requirements

**R1. Hub's event stream in the hub client, and in the fake hub.**

*Connecting.* The hub client (`internal/hub`) gains a call that opens `GET /api/v1/events?workspace=<slug>`:
- the slug is query-escaped;
- the request carries `Authorization: Bearer <PAT>`, `Accept: text/event-stream` and the client's `User-Agent`;
- redirects are not followed;
- the client waits for the answer's headers for at most the client's ordinary bound (10 s). After that the body is read with no overall timeout, until the caller's context ends.

*Answers that are not a stream.* They are classified as the other calls classify theirs:
- `401` → refused;
- `403` → forbidden, with a message that names `audit:read` and points to `PUT /api/v1/workspaces/{slug}`;
- `404` → not found;
- any other 4xx except 429 → rejected, with hub's code and message;
- 429, 5xx, a transport failure, a timeout, a redirect, or a `200` whose content type is not `text/event-stream` → unavailable.

*Reading frames.* The stream is read frame by frame, following SSE rules: the fields `id:`, `event:` and `data:`; data lines are joined with a line break; a line that starts with a colon is a comment; a frame with no data is no event. A line longer than 1 MiB ends the read with an error.

*Decoding.* Every frame and every comment reaches the caller as activity. A frame that carries an event is decoded into a typed value:
- the event id: the JSON `id`, or else the frame's `id:`;
- the type: the JSON `event_type`, or else the frame's `event:`;
- the workspace slug, when hub names one;
- for forge events, the issue number and the label name from the payload.

The decoding accepts the encodings listed under Verified External API. A frame whose data is not a JSON object is activity and nothing more. Only `internal/hub` knows hub's field names, and nothing the client returns or logs contains the PAT.

*The fake hub.* It serves the same route:
- it checks the token's access to the workspace as its other routes do;
- tests can inject faults on the route through the existing `Inject`, `Fail` and `Hangup` seams;
- a test can send an event to the open connections of a workspace, send a heartbeat, end every connection of a workspace, and read how many connections are open;
- a test can make the fake hub emit `hub.forge.issue.labeled` whenever a label is added through its forge API, the way a webhook would report the worker's own restore.

**R2. One stream per subscription, while the poll is on.**

*Starting and stopping.* The label poll's supervisor owns the streams:
- each workspace that has a poll loop also has a stream;
- the stream starts with the loop, at the first scan or at the scan that finds a new subscription;
- it stops when the loop is taken away: the workspace is unsubscribed, a scan finds it gone, or a cycle or the stream finds it not subscribed (below). It also stops when the poll stops.

*Each connection* first reads the subscription's token from the store, then the workspace's resolved variables from hub, then the workspace record from hub (for the forge repository). It then connects at once, with no random delay. The request labels are read from the subscription's `label_prefix` each time an event is handled.

*The poll's switch.* The stream reads `AF_POLL_INTERVAL` as a cycle reads it.
- A zero duration turns the stream off together with the poll. The stream opens no connection, and reads the variables again 5 minutes later.
- While a connection is open, the stream relies on its reading for at most 5 minutes. Before it acts on a request event (R5) or a closed event (R8), it reads the variables again if its reading is older than that. If the new reading is `0`, the event is not acted on and not recorded, the connection is closed, and the variables are read again 5 minutes later.
- Any other value (unset, invalid, or a duration from 1m to 24h) leaves the stream on. The stream logs nothing about the value, because a cycle already warns about an invalid one.
- A failure to read the variables is a connection failure (R4) when the stream connects, and an event-handling failure (R4) before an event.

*A replaced subscription.* If a scan finds that the subscription's `UpdatedAt` has changed, the open connection is closed and the stream reconnects at once, with the new token.

*Store answers.* A store that answers "not subscribed" stops the stream and takes the workspace's poll loop away with it, as a cycle that finds the workspace not subscribed does. A later scan that finds the workspace subscribed again therefore starts both. A token that does not open is an `ERROR`, logged once, and the stream keeps retrying at the backoff.

*Concurrency.* Events of one workspace are handled one at a time, in the order they arrive. Streams of different workspaces never wait on each other.

*Shutdown.* When the poll stops, it cancels the streams and any event being handled, and returns only once they have returned. This keeps the existing order: the poll and its streams stop before the jobs layer and before the state file is closed. `serve` needs no wiring beyond the poll's.

**R3. Reconnecting.**

*Backoff.* After a connection fails or ends, the stream waits before trying again, using the poll's clock:
- the waits are 1 s, then double after each further failure, up to 60 s;
- after hub has been found not to serve the stream (R4), every wait is 60 s;
- the wait goes back to 1 s once a connection has received its first frame or comment;
- there is no jitter.

*Idle limit.* A connection that receives no bytes for 60 s (no frame, no comment, no heartbeat) is closed and treated as ended. Time spent acting on an event does not count toward the 60 s.

*No replay.* A reconnect does not try to recover what it missed.

**R4. What the stream logs.**

*Activity.* The first frame or comment that a connection receives ends a failure episode. It resets the backoff (R3) and the connection-failure records. If failures or an outage were logged, it logs one `INFO` that says the stream has recovered.

*Connection failures* are the failures of the token read, the variable read, the record read and the connect. Each is logged once per kind and operation, until the next activity. These records are kept apart from the poll's failure records and from the event-handling records below, so none of them silences another.
- A refused or forbidden token, a workspace that hub does not find when its record is read, a rejected request, or an unreadable token: `ERROR`. The message names the operation, and either the `PUT` route that replaces the token or the scope `audit:read`, whichever cures it.
- A `404` or `405` on the events route, after the workspace record was read: one `WARN` saying that hub does not serve the event stream and that labels are found by the poll alone. The waits become 60 s (R3).
- Hub unavailable: `WARN`.

*Outage.* When the stream has had no connection with activity for longer than the poll interval it last read (5 minutes when none was read), one `WARN` says that labels are being found by the poll alone. It is not logged while the `WARN` that hub does not serve the stream stands.

*Event-handling failures* are the failures of hub calls and of the jobs layer while an event is handled (R5, R8). Each is logged once per kind and operation, until an event of the workspace is next handled without failure. They are classified as connection failures are (`ERROR` for a refusal that a person must cure, with hub's message for a rejection; `WARN` for hub unavailable). They do not reset, and are not reset by, the connection-failure records.

*Malformed events.* A `hub.forge.issue.labeled` event that carries no label, a `labeled` event with a request label, or a `hub.forge.issue.closed` event is malformed when it has no id, has no issue number, or has an id longer than 256 bytes or containing control characters. A malformed event is logged at `WARN`, once per connection.

*Every other ignored event* (R5) is logged at `DEBUG` at most.

**R5. A `labeled` event goes through the poll's rules.**

*Which events.* An event of type `hub.forge.issue.labeled` is a request when its label equals `<prefix>triage` or `<prefix>fix`, ignoring case, and it names no other workspace. A `hub.forge.issue.closed` event is handled by R8. Every other event is ignored with no request to hub, including:
- a `labeled` event with another label;
- an event of another type;
- an event that names another workspace;
- a malformed event (R4).

*Handling a request.* Under the per-workspace lock (R7), the stream:
1. ignores the event if its id is already recorded (R6);
2. makes sure the poll is on (R2), and stops here if it is not;
3. reads the issue through hub's forge API, and does nothing more if the issue is closed, is a pull request, or no longer carries the label (ignoring case);
4. otherwise applies the poll's rules to the issue and the canonical label `<prefix><kind>`, exactly as a cycle does:
   - **absorbed** by an open job of that kind: the label is removed, or the removal is owed if it fails;
   - **held**: nothing is done;
   - **already taken** (owed): the label is removed again;
   - **new request**: a job is made with `source` `{"kind": "event", "by": "<event id>"}`, `request_label` `<prefix><kind>` and the issue number as input. The poll record is written before the removal, and the removal follows.

*Refusals from the jobs layer* are handled as in a cycle:
- a duplicate is an absorbed label;
- "not subscribed" ends the handling with no label written;
- anything else is an event-handling failure (R4) at `WARN`, and the label stays.

*Failures on hub.* If hub cannot be asked, or refuses the variable read, the issue read or the removal, the event is left to the poll. The label stays, or its removal stays owed, and the failure is logged as an event-handling failure (R4).

**R6. Hub event ids.**

*Which ids are recorded.* The id of a request event (R5) or a closed event (R8) is recorded once the event has been decided: a job made, a label absorbed or removed again, a hold left alone, a cancel made, or nothing to do because of what hub's read of the issue showed. These are not recorded:
- ignored events;
- a closed event for an issue with no open job;
- an event not acted on because the poll is off;
- an event whose handling failed.

*Storage.* The ids live in a new table `af_hub_events`, keyed by workspace and event id, with the time the event was taken. It is created in one migration appended after `agent_fox_label_protocol`'s migration. An event whose id is recorded for its workspace is not acted on again.

*Expiry.* The supervisor's scan deletes ids taken more than 7 days ago (by its clock). The table is the only place ids are kept. It survives a restart and does not depend on the jobs.

**R7. The poll's side of sharing the rules.**

*Locking.* A cycle's rules for one issue and the stream's handling of one event take the same per-workspace lock, so that the rules of two triggers never run at once for one workspace. The lock is held for one issue or one event at a time, never for a whole cycle.

*A listing is older than any record made after it was requested.*
- When a cycle reaches an issue whose label no open job absorbs, no hold holds and no removal is owed, but a record of that label on that issue was made at or after the listing was requested, the cycle makes no job and leaves the label to its next cycle.
- A complete listing settles no owed removal and releases no hold for a record made at or after the listing was requested.

*Everything else about the poll is unchanged:* its interval, its rules, its records and their dropping.

**R8. A `closed` event cancels the issue's open jobs.**

*The stream's side.* For a `hub.forge.issue.closed` event that is not malformed and names no other workspace, the stream, under the per-workspace lock:
1. ignores the event if its id is already recorded;
2. looks in the job records for the open jobs (queued, running or blocked, triage or fix, whoever made them) of the workspace whose issue is that number. With none, it stops here: nothing is recorded, nothing is logged above `DEBUG`, and no request is sent to hub;
3. makes sure the poll is on (R2), and stops here if it is not;
4. reads the issue through hub. An issue that hub reads as open gets nothing more;
5. cancels every open job of the issue, as found at that moment, with the issue-closed cause;
6. logs one `INFO` that names the workspace, the issue and the cancelled jobs.

If hub cannot read the issue, nothing is cancelled, the event is not recorded, and the failure is logged as an event-handling failure (R4). The jobs run on.

*The cancel.* An issue-closed cancel is the ordinary cancel in every way except its recorded cause and its labels (R9):
- queued attempts are cancelled, a running attempt's runner is told with the issue-closed cause and given the 60 s grace, and `job_state` is emitted;
- a job that changes state between the look and the cancel is looked at again, as the unsubscribe's cancel does today;
- every place that stops for a cancel (lease release, grace, recovery) treats this cause as a cancel. The runner can tell it apart from a person's cancel and from an unsubscribe.

*Recording the cause.* The cause of every cancel is stored with the job (same migration as R6) and shown in the job document as `cancel_cause`:
- `"cancelled"` for a person's cancel, `"unsubscribed"` for an unsubscribe, `"issue_closed"` for this one;
- `null` for a job that is not cancelled, or that was cancelled before this release.

*The envelope.* The envelope that the jobs layer writes for a runner that returns without one, or that overruns the grace, keeps category `cancelled`. Its message says that the issue was closed.

**R9. A job cancelled because its issue closed leaves no status label of its own, and adds none.**

*What the issue shows afterwards.* Once an issue-closed cancel has been cleaned up, the issue carries no status label that the cancelled jobs account for, and nothing was added to it: no request label came back, no status label was set, and no comment was posted. Labels the cancelled jobs do not account for stay (Non-goals).

*Who removes the labels.*
- **A cancel that reaches a runner that has opened its hub session.** The worker stops as it does for any cancel: it releases the lease, reports to hub, and returns within the grace. On the issue it does exactly one thing: it takes off the `af:in-progress` it set. The envelope it writes for an attempt stopped before the loop ended keeps category `cancelled`, with a message saying that the issue was closed.
- **Every other cancel**, in the two cases of `agent_fox_label_protocol` R4 (no runner, or a runner without a hub session): that spec's cancel cleanup runs, marked, tried, retried and given up exactly as its R4–R5 say, with one difference. A try makes the removals of its step 3 (`in-progress` unless another job of the issue is running, and the status labels in the job's `labels.set` unless another open job of the issue shows them) and never makes step 4: it adds no request label. `labels.restore` stays `null`. `labels.cleanup` becomes `"done"` once the removals are made, or `"label_not_set"` where that spec's R5 says so.
- **A jobs layer built without a forge** writes nothing for such a cancel, as `agent_fox_label_protocol` R5 says for every cancel.

*The poll and the stream.* The jobs layer reports the cancel cause to the poll. `note` treats a job cancelled with `issue_closed` as ended and holding nothing, whether or not its cancel reached a runner and whatever its cleanup shows. A later request label on a reopened issue is therefore a new request.

**R10. Documentation and errata.**

Run `make doc` first. Then update:
- `docs/api.md`:
  - "Jobs that a request label makes": the stream as the first path, `source` `{"kind": "event", "by": "<hub event id>"}`, and the poll as the catch-up;
  - the job document's `cancel_cause` row;
  - "Cancel" and "Removing": the causes, and that an issue-closed cancel removes its jobs' status labels and adds none, with `labels.cleanup` as for any cleanup.
- `docs/architecture.md`: a new section "The event stream, as built", plus the changes to "The label poll, as built", including its cost: the stream reads the issue once for each request event it acts on and for each closed event of an issue with an open job, and removes a label for each label it takes, absorbs or takes again.
- `docs/configuration.md` ("The label poll"): `AF_POLL_INTERVAL`'s role as the catch-up, that `0` turns the stream off too (as before, `0` makes no job from a label), and the stream's fixed timings.
- `docs/development.md`: the fake hub's new event-stream seams.
- `README.md`: one paragraph on what a team does, and that `AF_POLL_INTERVAL=0` turns label-driven jobs off.

Every phrase that `docs/*_docs_test.go` asserts must keep passing; `docs/poll_docs_test.go` in particular asserts phrases of these sections. `docs/cli.md` must still never contain the word "poll". Where hub's real event shape differs from what this PRD assumes, the code follows hub, and the difference is recorded in `docs/errata/` for this spec.

## Design Decisions

1. **The stream lives in `internal/service/poll`, owned by the poll's `Supervisor`.** This is a note for the implementer, not a requirement. It shares the scan, the per-workspace state, the records and the rules. A separate package would have to export the poll's rules, or copy them.
2. **SSE reading and event decoding live in `internal/hub`, with a frame reader of their own.** `calls_test.go` confines hub's field names to that package. `afclient`'s reader is unexported, belongs to a sealed spec, and would be the wrong direction of dependency. The frame rules are copied from it, so both readers behave alike.
3. **Hub's event shape is read in each plausible encoding** (see Verified External API). This follows the precedent of erratum 13 for resolved variables. The implementer checks hub's OpenAPI document and records any divergence.
4. **A `labeled` event is a hint; the issue as hub reads it now decides.** The stream reads the issue before acting. One read per request event guards against several things the open-job check alone cannot:
   - a delayed webhook for a label that the poll already took, whose job then ended;
   - a duplicate delivery under a new id;
   - a label on a pull request or on a closed issue.
5. **An id is recorded only once its event is decided, and only for events that are acted on.** An event whose handling failed is left to the poll (and to a duplicate delivery, should hub send one). Ignored audit events, closed events of issues without an open job, and events not acted on because the poll is off do not fill the table. The table is `af_hub_events`, following the `af_` prefix convention; the input called it `hub_events`.
6. **`source.by` is the hub event id, as is.** This is what the input specifies, and it lets a job be traced to the hub audit event.
7. **One per-workspace lock, held for one issue or one event, plus the "a listing is older than any record made after it" rule.** The unique index already keeps two jobs from being open at once. The rule also covers a stream job that ended before a cycle reached a listing that was requested before the job was made.
8. **Backoff of 1 s doubling to 60 s, without jitter, reset by the first frame or comment.** These are the input's numbers. A service holds a few dozen streams at most, so jitter buys little and would complicate the fake-clock tests.
9. **Any byte received counts as a heartbeat.** Hub's heartbeat form is not known, and any traffic proves the connection is alive. Acting on an event pauses the idle clock, so a slow hub call cannot cause a false reconnect.
10. **`AF_POLL_INTERVAL=0` turns off every label trigger, the stream included (revised in review).** An earlier draft kept the stream on with `0`, on the claim that the stream spends no forge rate limit. That claim was false: the stream reads the issue for each event it acts on and removes the label it takes. Three reasons decide it:
    - `0` keeps its documented meaning. Today it "lists nothing and writes no label". A team that set it to stop label-driven jobs does not start getting paid jobs from labels after this release.
    - The stream needs its catch-up. Only a complete listing releases a hold, only a cycle settles an owed removal for an issue that left the listing, and failed events are left to the poll. Without the poll, a held label would stay held for good, an owed removal would stay on the issue, and a failed event would be lost without a word.
    - No separate switch is needed. A stream without its catch-up loses requests. A team that wants the poll to cost less sets a longer interval (up to 24h), as PRD 04 §2 says.

    The stream rereads the variable when its reading is older than 5 minutes and it is about to act. That bounds how long a change to `0` takes, and it matches the poll's own 5-minute reread when it is off.
11. **A replaced subscription reconnects at the next scan.** Continuing to use a token that was replaced is wrong. The scan already reads the subscriptions every 60 s, so this costs nothing new.
12. **A `closed` event first looks for open jobs locally, then confirms by reading the issue, then cancels every open job of the issue, of both kinds, however each was made (revised in review).** Most closed issues of a repository never had a job. The local look means they cost no forge read and log nothing, even while hub is down. A cancel throws work away, so it acts on hub's current view, not on a possibly stale event. An issue that is closed has no use for any job about it.
13. **The issue-closed cause is a kind of cancel.** In code it wraps the cancel cause (suggested name `jobs.ErrIssueClosed`, wrapping `ErrCancelled`). Every place that stops for a cancel (lease release, grace, recovery) works unchanged. Only the label writes, the cleanup and the poll's `note` check the specific cause.
14. **The input's "category: issue_closed" is the job document's `cancel_cause`, and the envelope category stays `cancelled`.** A queued or blocked job has no envelope for its cancel, so only a job-level field can show the cause in every case. Keeping the envelope category means no consumer of envelopes has to learn a new value.
15. **An issue-closed cancel removes the status labels its jobs account for and adds none (revised in review).** The input's "no label set" for a closed issue is read as "nothing is added". Its §5 ("`cancelled` → … no status label") and its goal that labels mirror the record require the removals. An earlier draft left a blocked job's `af:needs-human`, and an `af:in-progress` left by a runner that the cancel did not reach, on the closed issue. That contradicted the mapping that `agent_fox_label_protocol` (its DD12, DD16) already applies to every other cancel. A runner with a hub session takes off its own `af:in-progress`. Every other case reuses that spec's cleanup without its restore step: the same read of the issue, the same rule for which labels a job accounts for, and the same retries. So the service has no second path that writes labels. The split's phrase "a cause that writes no label" is read as "puts no label on the issue".
16. **An issue-closed cancel holds nothing in the poll's records, whatever its cleanup shows.** Nothing put a label back. If it held, a request label on a reopened issue would be ignored until a listing released the hold. This overrides `agent_fox_label_protocol` R7's hold for a cleanup that finished with `labels.restore` `null`, which assumes that the cleanup put the label back.
17. **Connect at once, with no random first delay.** The point of the stream is latency, and one connection per workspace is not a burst that hub needs spread out.
18. **A `404` or `405` on the events route, after the workspace record was read, means hub does not serve the event stream.** The record read proves that the token sees the workspace. So these answers mean that the route is missing, and replacing the token would not cure them. Hub's route is unverified, so this is a plausible state of a deployed hub, not a fault of the subscriber. One `WARN` says so, the poll carries on alone, and the stream retries every 60 s so that it picks up a hub that gains the route. `agent_fox_label_protocol` DD5 treats its unverified label route the same way.
19. **Only malformed forge issue events warn. Every other ignored event is `DEBUG`.** Hub's stream carries every audit event of the workspace, and most of them have no issue number or label. A `WARN` is useful only where a missing field means a request or a close was lost, or that hub's shape differs from what this spec assumes. Goal 2 counts the closed event among the events that may reach hub, and its local look means that a closed event for an issue with no open job does not.
20. **One condition ends a connection's failure episode: its first frame or comment. Event-handling failures keep their own records.** On a quiet workspace whose heartbeats are comments, a frame-only rule would never log recovery. Every event is a frame, so handling failures sharing the connection's records would be reset by the very event that failed. Separate records keep each failure logged once, and keep each recovery visible.
21. **A stream that finds the workspace not subscribed takes the poll loop away with it.** This is what a cycle does. It keeps the rule that a loop always has a stream, and the next scan restarts both after a quick resubscribe.
22. **The requirements state behaviour. Design decides the rest.** `agent_fox_label_protocol` DD19 did the same. These are suggestions for the implementer, not requirements:
    - the jobs layer gains `Service.CancelIssue(ctx, workspace, issue)`, which returns the ids it cancelled and shares the retry loop of `cancelWhatIsOpen`, given the cause;
    - `jobs.ErrIssueClosed` wraps `ErrCancelled`, and the runner contract comment in `jobs/runner.go` says what the worker does for it;
    - the worker gains a stop value, or receives the cause, so that `mirror` only unmarks;
    - a cycle's per-issue rules (`cycleRun.issue`) take the source and the label as parameters, so the stream can call them;
    - `jobs.Standing` gains the cancel cause;
    - the column is `af_jobs.cancel_cause`, and the poll's `Jobs` interface gains the look and the cancel.

## Dependencies

| Spec | Why |
|---|---|
| `11_agent_fox_service_core` | The store's subscriptions and tokens (`Subscription.UpdatedAt`, `Token`), the migrations list, the hub client core (`hub.New`, its bound, no redirects), and `serve`'s wiring. |
| `12_agent_fox_jobs` | `Create` with source kind `event`, the cancel path (cancel, the unsubscribe's retrying cancel, causes, grace, synthesized envelope), the job document, and `Standing`. Gains the issue-closed cancel and `cancel_cause`. |
| `13_agent_fox_worker` | The worker's cancel handling in `mirror.go` and `cancel.go`, changed for the new cause. The hub forge client (`ReadIssue`, `RemoveLabel`) and the fake hub are extended. |
| `15_agent_fox_poll` | The `Supervisor`, its scan and loops, the rules, the records, `note`, `AF_POLL_INTERVAL` and the failure logging, which the stream reuses. The cycle gains the lock and the newer-record rule. |
| `17_agent_fox_label_protocol` | Its cancel cleanup, which the issue-closed cause gets without its restore step (R9), and its changes to `note`/`Standing` for labels that a cleanup puts back. The stream relies on them to hold such labels. Its migration comes before this spec's. |

## Verified External API

**Hub's event stream: unverified.** Hub's OpenAPI document (`../hub/docs/openapi.yaml`) is outside the repository. What this spec assumes:

- **Route.** `GET /api/v1/events?workspace=<slug>`, opened with `Authorization: Bearer <PAT>`. The PAT needs the scope `audit:read`. The answer is `200` `text/event-stream`, or hub's error object `{error: {code, message, error_type}}` with 401, 403, 404, 429 or 5xx, like hub's other routes. A hub that does not serve the route is assumed to answer `404` or `405` (R4).
- **No replay.** There is no `since` parameter and no `Last-Event-ID` (stated by PRD 01 and PRD 04).
- **Frames.** Each frame's `data:` is one JSON object, assumed to be hub's audit event: `{"id": string, "event_type": string, "workspace_slug": string?, "timestamp": string, "payload": object}`. This mirrors the ingestion shape of `PostEvents` in `internal/hub/calls.go` (`id`, `run_id`, `event_type`, `timestamp`, `payload`). The frame's `id:` and `event:` fields are read when the JSON lacks them.
- **Event types.** `hub.forge.issue.labeled` and `hub.forge.issue.closed`, as named in PRD 04 §2. PRD 01 wrote `forge.issue.labeled`. The names are constants in `internal/hub`, so a divergence is a one-line change.
- **Payload of a forge event.** The issue number is `payload.issue` (an integer), or `payload.issue.number`, or `payload.number`. The label is `payload.label` (a string) or `payload.label.name`. All of these encodings are read.
- **Heartbeats.** Their form is unknown: an SSE comment line, or an event whose type is `heartbeat` or `ping`. Any received byte is treated as activity.

**`github.com/agent-fox-dev/aflib/issuex`** is outside the repository. The following are verified from call sites in this repository:

- `issuex.Client.ReadIssue(ctx context.Context, ref issuex.IssueRef) (issuex.IssueThread, error)`, used in `internal/hub/forge.go:125`.
- `issuex.IssueThread{Issue issuex.Issue; Comments []issuex.Comment; Truncated bool; CommentsErr error}`.
- `issuex.Issue` fields `Number int`, `State string`, `Labels []string` and `IsPR bool` (`poll/cycle.go` `asked`, `hub/forge_test.go`).
- `issuex.IssueRef{Repo issuex.Repo; Number int}`.
- `(*hub.ForgeClient).RemoveLabel(ctx, ref, label string) error`.
- `issuex.IsNotFound(err) bool`.
- `issuex.ParseRemote(url) (issuex.Repo, bool)`, through `hub.Record.Repo()`.
