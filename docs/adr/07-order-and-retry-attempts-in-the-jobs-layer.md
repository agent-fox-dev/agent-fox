# ADR 07: Order and retry attempts in the jobs layer

- **Status:** Accepted
- **Date:** 2026-10-25 (records decisions taken while specs 12, 15 and 17 were implemented)

## Context

The jobs layer keeps triage and fix requests durable on `aflib/jobqueue` and
runs them behind the runner seam
([architecture](../architecture.md#the-jobs-layer)). Its spec assumed a queue
that serializes by key, claims in the order of creation, lets a test inject the
waits of a retry, and hands a shut-down item over again. `jobqueue` does none
of those: it refuses an item whose type and key are already queued, claims by a
time to the second, computes its own backoff from the system clock, and
completes an item whose handler returned. The layer keeps those promises itself,
in the ways below. The spec also left open several cases of cancel, retry and
listing, settled here.

## Decision

**The queue.**

- An attempt is one item of type `attempt`, keyed by its nonce
  `<job id>:<n>`, with the workspace as its `Group`: the queue runs one item of
  a group at a time, which serializes the workspace, and the nonce key lets a
  workspace hold many attempts waiting their turn.
- Order within a workspace is `af_attempts.queue_seq`, which numbers every
  attempt in the order it was made. A handler whose attempt is open but is not
  the workspace's first returns an error the queue retries after a constant
  delay (base equal to cap, multiplier 1, a retry limit never reached), so the
  attempt at the head of the line is always in the queue.
- An automatic attempt gets the next `queue_seq`: it runs after the attempts of
  the workspace that are open already. One job that fails again and again
  cannot hold the workspace; its own retry waits for the jobs made before it.
- A transient failure waits in the handler, on the layer's clock (30 s, then
  2 minutes). The item stays claimed, so the workspace keeps its place, and the
  handler gives up its slot while it waits. The queue has `[workers].count` +
  16 workers and the slots are a semaphore of `[workers].count`, so a waiting
  handler holds a worker and no slot; with more than 16 workspaces waiting at
  once, the next one waits for a worker.
- On a shutdown nothing the runner returned is kept: the handler returns
  `errHandedBack`, the item goes back to queued after the retry delay, and the
  next process claims it as a further delivery of the same attempt. The queue's
  own grace is 200 ms, since the layer waits for its runners first.

**The runner's result.** `jobs.Result.Envelope` is the JSON the command wrote,
stored and served verbatim; the layer decodes it only to decide what the
attempt came to. A cancelled attempt's runner returns a result with the loop's
envelope (or a `cancelled` one when the loop had not begun); a shut-down
runner returns the cause alone. `Request.Round` is the attempt's position in
its round.

**Cancel.**

- An attempt that never ran has no envelope and no `attempt_end`; a job
  cancelled while queued has `result` null.
- A runner is not called for a cancelled job: the attempt is made known to the
  layer before its request is read, and the read checks the job. A cancel that
  lands between the claim and the call records the attempt cancelled, with an
  envelope of the category `cancelled`, and calls no runner.
- An attempt left `running` under a cancelled job (a crash between the cancel
  and the runner's return) is closed the same way when its item comes up.
- An attempt carries `in_runner`, which a claim clears and the call of the
  runner sets. A cancel reached a runner when its attempt has the mark and is
  still `running` or `cancelled` with an envelope; a claimed attempt whose
  runner was never called did not.
- Whether a running attempt's runner was called is known only after the
  cancel's transaction, so the "no runner" mark of the cancel cleanup is
  written in that transaction when no attempt runs, and otherwise right after
  it, before the route answers. Meanwhile `Standing` reads the cleanup as
  pending. A process that stops in between leaves `cleanup_wanted` set, and the
  reaped attempt marks the cleanup when it is recorded.

**Racing actions.** A cancel, an answer and a retry each change the job only
if it is still in the state they saw (`Store.CancelJob(ctx, id, from)`,
`Store.Requeue(ctx, id, from, …)`); the second of two actions that saw the same
state is a `*StateError`, `409 invalid_state`. "Exactly one of two" holds for
actions taken on the same state; two different actions that arrive one after
the other can both be valid. A lost change is not tried again.

**Duplicates.** The partial unique index of open jobs makes the duplicate
check atomic with the insert. A creator refused for an issue whose holder ended
before it could be named tries again, up to three times, so it is never told
that the job is missing. A retry that would make a second open job of a kind
for an issue is `409 duplicate_job` naming the holder, as a creation is: two
fix attempts on one issue would collide on its branch.

**Reading.**

- Jobs with one `created_at` are listed by `id` descending, the index read
  backwards; the cursor is the position `(created_at, id)` of the last job of
  the page.
- An attempt's envelope or report is served only when its row lists it
  (`has_envelope`, `has_report`); a file a running runner has begun to write is
  not served, and a listed file missing on disk is a `404` with a `WARN`.
- A stream is at rest when the latest `job_state` of the log says so, once
  every event up to it was sent; the `at_rest` of an event in the replay says
  nothing by itself, and a live `job_state` counts only if it was not in the
  replay.
- Shutdown closes the streams through the server (`RegisterOnShutdown`, so
  `Shutdown` is not held by them) and through `Service.Close`; the broker stays
  closed afterwards.

## Consequences

- The layer, not the queue, owns ordering, waits and handovers, so a change of
  `jobqueue`'s claim or retry rules does not change what a job does.
- A write to a stream client that does not read has no deadline; the broker
  lets go of it after 256 events.
