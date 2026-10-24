---
spec_id: "14"
spec_name: "agent_fox_worker_recovery"
title: "agent-fox worker recovery: resuming interrupted attempts, replacing lost boxes and releasing orphaned leases"
status: "active"
created_at: "2026-10-21T22:00:00.664947Z"
updated_at: "2026-10-21T22:00:00.664947Z"
intent_hash: "3e2a22e12b104d96a7abaedc7f10b236fcf57d80344c1c3439466a41743217f4"
schema_version: 2
source: "docs/prd/03-run-the-agent-fox-service.md"
---
## Intent

Make an attempt of the agent-fox worker survive the service stopping, the process dying and the box going away, without spending a second brain run on work a brain already finished and without writing anything twice to the forge. A later delivery of the same attempt picks up the attempt where it stood. The attempt's clone, run directory and accepted submission are used again. The change is measured again before anything is published. Once something is published it is never redone. A box lost in the middle of a delivery is replaced once with a new lease. Leases that no runner holds any more are released.

## Goals

1. **Crash between export and land (fix).** The setup is offline: the worker's fake hub, an in-process gateway over `local`, and the scripted brain. Delivery 1 of a fix attempt on issue 42 is stopped as if the process died, right after the brain's tree was applied to the clone. Delivery 2 of the same attempt (handover count 2) then:
   - sends no clone request and does not call the brain;
   - takes one new lease, measures the change again inside it, and pushes `fix/42-…` under the same name an uninterrupted run would have used (no `-2` suffix);
   - opens one pull request, posts one comment and sets `af:landed`;
   - releases delivery 1's lease;
   - returns a `done` envelope whose usage includes delivery 1's brain spend.
2. **Graceful shutdown during the fix land.** The real jobs layer is stopped while the attempt lands, and is restarted on the same database file. The stopped delivery posts no usage and no events batch, and leaves the hub session open. The local branch that the interrupted run parked is brought back, and the job ends `done` with exactly one pull request, one completed session and one usage record holding the attempt's total.
3. **Crash after the pull request was opened.** Delivery 2 opens no second pull request, takes no lease and does not call the brain. It posts exactly one comment, sets the labels and reports the pull request delivery 1 opened.
4. **Crash after the outcome was stored.**
   - **After the mirror, before the report.** Delivery 2 makes no forge write, takes no lease and does not call the brain. It completes the session delivery 1 opened, with no second `POST /sessions`, posts the usage once, and returns the stored envelope with only the warning `attempt_resumed` added.
   - **Before the mirror**, for example a `failed` attempt whose brain left no submission. Delivery 2 takes no lease and does not call the brain. It sets the status label and posts the mirror's comment once, then reports as above.
5. **Crash before the change was applied.** In this case the submission was accepted but never applied, or not accepted at all. Delivery 2 discards any change in the clone, runs the fix loop from the base head with what is left of the attempt's budget, and lands.
   - A journal that is unreadable, or that belongs to another job or attempt, leads to a fresh clone and a `resume_discarded` warning.
   - A run ledger that does not read leads to a fresh clone and `resume_discarded` with the reason `ledger_unreadable`. Delivery 1's brain spend, as the journal records it, still counts against the budget and appears in the usage. Delivery 1's session is the one that is completed, and no second session is opened.
   - A delivery handed over again after a transient hub error at resolve, which left neither a journal nor a clone, starts fresh without `resume_discarded`.
6. **Triage.** A triage attempt interrupted after its submission was accepted is landed by delivery 2 without a brain run and without a lease. The `## Original report` section of the new body is byte-identical to the original body. When delivery 1 had already posted the question comment of a question submission, the issue still has exactly one such comment. A triage of text interrupted after hub created its issue, and before the run's landing record was written, is landed by delivery 2 into that same issue: the repository gains one issue, not two.
7. **Lost box.** A box that becomes unavailable during the brain is replaced, within the same delivery:
   - one new lease is taken, the clone is synced into it, and the work goes on with what is left of the budget;
   - the delivery's deadline is cut to end 2 minutes before the replacement lease's `expires_at` when that is earlier, and a replacement with less than 2 minutes left counts as refused;
   - the envelope carries `box_replaced`;
   - the job's `hub.sandbox_id` shows the new box;
   - the lost lease is released.

   A second loss in the same delivery returns a transient `sandbox` error to the jobs layer.
8. **Orphaned leases and open sessions.** At start, the fake hub holds four leases:
   - one labelled for an attempt that this database knows and that no runner is carrying out. It is released;
   - one labelled for a job unknown to this database. It is kept;
   - one with no labels. It is kept;
   - one labelled for an attempt that a runner is carrying out. It is kept.

   Hub answering 5xx for one workspace logs a `WARN` and does not stop the other workspaces. The next pass releases what was missed. An attempt that the jobs layer ended after its transient retries were spent, and whose journal records an open session, has that session completed `failed` by the next pass, with its unposted usage, exactly once.
9. **No credential on disk.** No file under the attempt's `recovery/` directory contains the subscription's PAT or a lease token.
10. **Offline.** `make check` passes offline: loopback `httptest` only, no hub, no key, no container engine.

## Non-goals

- **The attempt pipeline itself** belongs to `agent_fox_worker`. That covers the session, workspace variables, the thread, the clone, the lease and its labels `job` and `attempt`, releasing the lease on every path, the gateway box, the loops, the hub-backed landing, the mirror and its comment marker, usage, events and release. It also owns the first lease that hub refuses (a transient error carrying hub's reason) and the `carry_patch` refusal. This spec adds the journal, the outcome file, the resume paths, the in-delivery box replacement and the reconciliation pass on top of it. It also changes what the worker reports to hub on a shutdown and on a transient error (R1).
- **The jobs layer's retry policy** belongs to `agent_fox_jobs`: transient backoff (30 s, 2 min, three failures), automatic attempts, handing an attempt over again after a restart, and discarding what a runner returns after a shutdown. This spec relies on these and changes none of them.
- **A limit on how often one attempt is resumed.** This is this spec's own choice (Design Decision 38). Each handover raises the attempt's `deliveries`, which the job document shows. The jobs layer counts a handover after a shutdown or a crash as no failure of the attempt (jobs Design Decision 5), so a process that crashes on one attempt again and again is resumed again and again. Noticing that is the operator's job.
- **Carrying the time bound across deliveries.** Each delivery gets `AF_TIMEOUT` afresh. The budget and turn bounds do carry over (R4).
- **Accounting exactly for a brain run that a process death cut short.** Its spend is not recorded anywhere this spec can read. It counts as nothing, with a warning (R4, Design Decision 25).
- **Replaying an interrupted delivery's events to hub audit.** Those events are already in the job's event log (`af_events`). The events batch posted to hub covers the delivery that produces the attempt's outcome.
- **Recovering the work of an attempt whose clone is gone.** If the attempt directory's clone was removed, or the journal or the ledger cannot be read, the attempt starts again from a fresh clone, and the change of the interrupted delivery is lost. When the journal reads, its session, its run id and the attempt's brain spend carry over (R2, R4). When the journal does not read, nothing of the interrupted delivery is known: its session stays open on hub, and its spend is not counted.
- **Reattaching to a leased box.** Lease tokens are never written down, so a resumed delivery always takes a new lease.
- **The label poll** (`agent_fox_poll`), **`af` as the service's client** (`af_service_client`), hub's event stream (PRD 04) and carry patches (PRD 05).

## Background

PRD 03 §6 ("Failure handling") and PRD 01 §6.4 and §11.10 ask three things:

- a worker killed mid-attempt is re-run by the queue's crash recovery;
- the next run "reuses the clone's branch and skips the brain when `submission.json` is present and valid";
- a box gone unavailable "is retryable once with a new lease", and orphaned leases are released when the service starts.

PRD 01 left one question open at spec time: should the second run re-measure, or trust the stored verdict?

The service code is not in the tree yet. `11_agent_fox_service_core`, `12_agent_fox_jobs` and `13_agent_fox_worker` are written specs. What this spec builds on, from those specs:

- **The jobs runner contract** (jobs R4, R5 and R9).
  - Each call carries the attempt's directory `attempts/<job id>/<n>/` and the handover count, which is 1 on the first delivery.
  - A shutdown cancels the context with a cause that does not read as a cancel. The jobs layer discards what the runner returns, and hands the same attempt over again after the restart with the count incremented. Crash recovery does the same.
  - A transient error is also handed over again on the same attempt, with the count incremented. The third transient failure ends the attempt without any runner seeing that end.
  - Runner obligation 4 says a runner stopped for a shutdown "leaves the attempt resumable".
  - The recorder takes the attempt's hub references (session id, run id, sandbox id).
- **The worker** (`agent_fox_worker`).
  - It works only in named subdirectories of the attempt directory, each with mode `0700`: `clone/` and `input/`. It keeps `clone/` when the service is stopping or the attempt returns a transient error (its R5).
  - It leases a box with `POST /workspaces/:slug/sandboxes {profile, purpose, labels: {job, attempt}}`. It cuts the attempt's timeout to end 2 minutes before the lease's `expires_at`, with the warning `lease_shortened_timeout`. A lease with less than 2 minutes left is a transient error. It releases the lease after the box closes on every path, a shutdown included (its R6).
  - It runs the loops of `af` in-process, with every gate inside the box. Each in-box result is read from the command's envelope (its R7).
  - Its order is resolve, session, mark, clone, lease and box, run, mirror, release, report (its R8). The run lands through a hub-backed publish hook: push, hub's branch push route, the pull request. The mirror then writes the status labels and the comment. Every comment carries a marker naming the job, the attempt and the kind, and a comment whose marker is already on the issue is not posted again.
  - It posts usage and an events batch and completes the session after the release (its R9). It also posts events and completes the session `failed` when it returns a transient error, and on a shutdown it completes the session as far as the shutdown wait allows (its R8 stop table and shutdown paragraph). This spec changes those two paths (R1).

What exists in this repository that decides how a resume can work:

- **Run directories.**
  - The loops keep their state in a run directory `<clone>/.agent-fox/run/<id>/` (`internal/toolio/rundir.go`), which git ignores.
  - `toolio.OpenRunDir(dir, id)` reopens one, and `RunDir.Record` then carries the ledger on from where it stopped.
  - `RunDir.Ledger` fails on any line that does not parse, and `Record` fails with it.
  - `fix.json` (`internal/fix/state.go`) and `triage.json` (`internal/triage/runfiles.go`) pin what the preflight and the brief produced: the base, the base head, the gate table, the baseline gate id, the brief prefix and the rounds so far.
- **The order inside a fix round** (`internal/fix/round.go` `RunRound`).
  1. `brain.Execute` writes the host's `submission.json` and records `submission_accepted {attempt, sha256}` (`internal/brain/decide.go`).
  2. Only then does the round export the box and `Apply` the tree to the clone. `Apply` records `apply` only after it finishes (`internal/fix/apply.go`).

  So a valid `submission.json` alone does not prove the change is in the clone.
- **Measuring a change.**
  - `fix.Measure` refuses a checkout that is not the base branch at the base head (`checkBase`, `internal/fix/measure.go`).
  - It compares with the baseline result by id through `checks.Compare(dir, baselineID, afterID)`, which reads both stored results from the host directory under `.agent-fox/gate/<id>/` (`internal/checks/compare.go`). The worker spec reads in-box results from the commands' envelopes, and does not say that the baseline result is kept in the clone.
  - `fix.LandRun` (`internal/fix/landcmd.go`, used by `af fix land --run-id`, `cmd/af/fix_land.go`) is exactly "measure the change in the checkout once, then land it, or park it if refused, with no brain round". `af fix run --run-id` (`cmd/af/fix_run.go`) runs one round of a brain on an existing run and applies its tree without measuring.
- **The fix loop** (`internal/fix/loop.go` `RunLoop`). A refused change goes back to the brain as the task of the next round, while the attempt's cost, turns and time allow. The last refusal is held in memory, and the ledger records only its kind. `fix.json`'s round count is written after every round.
- **What a stopped fix run leaves.** `fix.Finish` parks what was applied on a local branch: an unpushed commit with verdict `interrupted` and the trailer `Agent-Fox-Run: <run id>`. It then puts the checkout back on the base (`internal/fix/finish.go` `stopped`, `internal/fix/park.go`). This is "the clone's branch" PRD 03 refers to.
- **A loop left with no change.** `fix.Finish` (`exhausted`) fails with category `no_result`, not retryable.
- **Landing a fix.**
  - `fix.Land` names the branch with `gitx.UniqueBranchName(BranchName(number, title))`, which checks local and remote branches. It writes the PR body to `report.md`, commits, pushes and opens the pull request (`internal/fix/land.go`).
  - Repeating it after a push would create `fix/42-…-2` and a second pull request.
  - No `issuex` call lists pull requests by head branch (the method set of `internal/toolio/dryrun.go`).
- **Landing a triage.** `triage.Land` says "a landing can be repeated: it writes the same bytes again" (`internal/triage/land.go`). It validates the submission again against the host tree, and reads the issue's current body and comments. That holds for an issue input. For text or a file it creates an issue, and it finds one an earlier landing created only through its `Created` parameter. `af` reads that from the run's last landing ledger record, which is written only after the landing ends (`cmd/af/triage.go` `createdIssue`).
- **Usage.** `brain.Execute` records `brain_start` before the brain runs and `brain_end` with `cost_usd`, `cost_source` and `turns`, but no token counts, after it returns (`internal/brain/execute.go`). A process death in between leaves a `brain_start` with no `brain_end`. `brain.Tally.Add` can seed a tally (`internal/brain/report.go`).
- **Box failures.**
  - A box failure is a `toolio.Failure{Category: sandbox}`, retryable for prepare, sync, write and export (`internal/box/session.go`), and not retryable for exec (`internal/box/exec.go`).
  - The hands report a box that has gone away with the tool error code `sandbox_unavailable` (`internal/box/fs.go:28`; the stub in `internal/box/exec_test.go:123`). Today that code survives only inside the failure's message.
  - Inside `fix.RunLoop`, a round's box failure becomes `LastFailure`. `Finish.exhausted` then parks the applied change. A box loss must therefore be caught before `Finish`.

## Requirements

### R1. The attempt journal

The worker keeps a journal, `recovery/journal.json`, in the attempt's directory. `recovery/` is a named subdirectory of mode `0700`, beside the worker's `clone/` and `input/`. The worker never removes it, and retention removes it with the attempt directory.

**Writing it.** The journal is mode 0600 and is replaced atomically and synced to disk, so that a reader finds the old journal or the new one and never part of one. It is updated each time a step below completes, before the next step starts.

**What it records:**

- its format version, the job id, the attempt number and the kind;
- the clone's directory relative to the attempt directory, the workspace's `hub_url` without credentials, and the workspace's forge repository;
- the settings the run was started with: the brain, the model, the sandbox profile, the landing mode, the budget and the turn bound (R2);
- the run id, the base branch and the base head;
- the hub session: its id, its run id, and whether it was completed;
- every lease taken, by sandbox id, with its `expires_at` and whether it was released;
- each delivery: its handover count, when it started, and how it ended (`outcome`, `transient`, `shutdown`, or nothing when the process died);
- the attempt's brain spend so far (R4);
- the landing plan (R5) and the landing steps done: `pushed`, `hub_push`, `pull_request {number, url}`, `created_issue {number, url}`;
- the mirror steps done: `status_labels`, `comment` (R6);
- the report steps done: `usage_posted`, `events_posted`, `session_completed`.

**No credential.** The journal never holds a credential: no PAT, no lease token, no `Authorization` value, and no URL with user information.

**When it cannot be written.**

- Before the landing's first write to hub's git server or the forge, the journal must hold the landing plan. When the plan cannot be recorded, the delivery makes none of those writes. It returns a transient error of category `io` and keeps the clone, and the next delivery resumes from before the landing (R5).
- Any other journal write that fails raises the warning `journal_unwritable` once per delivery, and the delivery goes on. It finishes its landing from what it knows in memory. If the process dies later in that delivery, a resume can repeat a step that the journal does not record: a pull request or a created issue. Comments are still guarded by the worker's marker.

**A delivery that stops without an outcome.** This applies to a delivery stopped for a shutdown and to one returning a transient error. The delivery:

- releases its lease, as the worker does on every path (agent_fox_worker R6), and records the release in the journal;
- records how it ended;
- posts no usage and no events batch, and does not complete the session. The delivery that produces the attempt's outcome does these (R6), or the reconciliation pass when no delivery does (R8);
- writes no outcome file (R6);
- leaves the clone, the run directory and any branch the interrupted loop parked as they are.

The worker's other behaviour on these paths stays: a shutdown writes nothing to the forge, and a transient error removes the in-progress label.

### R2. How a delivery starts

**The first delivery.** A delivery whose handover count is 1 starts fresh. If the attempt directory holds a clone or a journal, they are removed first.

**A fresh start with nothing to discard.** Some later deliveries find neither a journal nor a clone, or a journal that records no completed clone. That happens, for example, after a transient error at resolve. Such a delivery starts as a first delivery would and raises no warning. It removes a partial clone if there is one, and it reuses the journal's session when the journal records one.

**Later deliveries resume** when all of the following hold:

- the journal reads, has a known format version, and names this job, attempt and kind;
- the clone is a git work tree whose `origin` URL is the workspace's current `hub_url`;
- the workspace's current forge repository is the one the journal records;
- the run directory the journal names opens;
- that run directory holds the run's pinned file (`fix.json` or `triage.json`);
- the run's ledger reads: every line of it parses.

**When a resume is not possible**, for a journal or a clone that exists and is rejected, the delivery does the following, and it is still the same attempt:

1. raises `resume_discarded` with the reason, for example `journal_unreadable`, `clone_invalid`, `origin_changed`, `forge_repository_changed`, `run_missing` or `ledger_unreadable`;
2. if the journal reads, it releases every lease the journal records as unreleased, and carries over the attempt's brain spend (R4);
3. removes the clone;
4. starts as a first delivery would. There is one exception: when the journal records a session that was not completed, the delivery opens no new session, and its new run uses that session's run id.

**What a resumed delivery repeats and what it keeps.**

- It reads the PAT, the workspace, its variables and the thread again, as every delivery does. The worker's checks at resolve run on the current values: the `carry_patch` refusal, `AF_FIX_LAND=patch`, and the variables' range checks. Unless it resumes from a stored outcome (R6), it sets the in-progress label again.
- The run goes on with the settings it was started with:
  - the base branch, the base head, the gate table, the baseline and the brief come from the pinned file;
  - the brain, the model, the sandbox profile, the landing mode, the budget and the turn bound come from the journal.

  A value changed on hub since then takes effect from the next attempt. `AF_TIMEOUT` is read afresh for each delivery. `AF_MAX_ATTEMPTS` is the current value, returned with the result. Of the workspace's fields, only a changed `hub_url` or forge repository discards the resume. A changed base branch or landing mode does neither: the run finishes against what it pinned.
- It does not run the loop's preflight or brief again: the pinned file holds what they produced.
- **The session.**
  - If the journal records a session that was not completed, the resumed delivery reports to that session and opens none.
  - If the journal records the session as completed and the delivery still has something to report, it opens a new session as the worker does, with the run's run id. It posts to that session only the usage that the journal does not record as posted.
- It reports to the recorder the session id and run id in effect.
- It releases every lease the journal records as unreleased before it takes one of its own.

**The catch-all.** Suppose the journal, the clone and hub's git server disagree in a way that R3 to R6 do not cover. For example, the base branch has commits that do not carry the run's trailer, or the run's branch is on hub's git server at another commit. The delivery does not guess. It ends the attempt with a `failed` envelope of category `internal`, not retryable, with a message that names what it found, and it cleans up the way the worker cleans up any failed attempt.

### R3. Bringing the clone back (fix)

This requirement applies when the run's change is not on hub's git server (R5).

**When the brain is skipped (R4)**, the resumed delivery puts the clone into the state that measuring requires before the change is measured:

- the base branch is checked out at the base head;
- the run's change is in the working tree.

The run's change comes from one of two places:

- the working tree;
- an unpushed local branch whose commits after the base head all carry `Agent-Fox-Run: <run id>`. This is a parked `interrupted` commit or an unfinished landing commit. Its change is brought back into the working tree, and the local branch is deleted. The landing then names the branch again, and it gets the same name.

**When the brain runs (R4)**, the clone starts again from the base:

1. any change is discarded: a hard reset to the base head, and a clean that keeps `.agent-fox/`;
2. any local branch of the run is deleted;
3. the fix loop runs (R4).

A triage clone is reused as it is.

### R4. When the brain is skipped, and what it gets when it runs

**When the brain is skipped.** A resumed delivery skips the brain when the run directory holds a submission that the host accepted and that still holds. All of the following must be true:

- `submission.json` parses as a submission file whose kind is the run's kind (`fix_report` or triage's), whose `run_id` is the journal's run id, and whose `payload_sha256` is the hash of its payload;
- the ledger's last `submission_accepted` record carries that hash;
- the payload passes the kind's check again against the clone's tree (`submission.Check`; for triage, `triage.Validate` as `triage.Land` does);
- for a fix report only, an `apply` record follows that `submission_accepted` record. A fix question needs none.

**Otherwise the brain runs**, inside the same attempt and the same run directory, after R3.

- For a fix, it is the fix loop as the worker runs it, with one in-run attempt. It is not one round of `af fix run --run-id` followed by `af fix land`.
  - `fix.json`'s round count carries on, so the first resumed round is the one after the last round recorded.
  - The first round starts from the base head. Its task carries the earlier questions as the worker gives them, and no earlier refusal.
  - A refusal later in the delivery goes back to the brain as the task of its next round, within the bounds below. The loop ends through `fix.Finish`, as in an uninterrupted delivery.
- For a triage, the triage loop runs again.

**One budget per attempt.** A brain-level attempt has one budget and one turn bound, however many deliveries it takes. A brain run on a resumed delivery, or after a box was replaced (R7), gets what is left:

- the pinned budget (`AF_MAX_BUDGET_USD` at the start of the run) minus the attempt's brain spend;
- the pinned turn bound (`AF_MAX_TURNS`) minus the attempt's turns.

The attempt's brain spend is the spend carried over in the journal from run directories a discarded resume removed (below), plus the brain runs that the current run's ledger records in `brain_end`. When nothing is left, the attempt ends `failed` with category `budget`, not retryable, and the brain does not run.

**A brain run cut short by a process death.** A `brain_start` record with no `brain_end` after it, before the next `brain_start` or the end of the ledger, is a brain run that the process died during. Its spend cannot be known. It counts as nothing against the budget and the usage, and the envelope carries the warning `brain_spend_unrecorded`, naming the ledger sequence number of that `brain_start` and its round.

**Spend that outlives the clone.** The journal records the attempt's brain spend so far: the cost, the cost source, the input and output tokens, and the turns. It is refreshed whenever the journal is written after a brain run ended. A discarded resume (R2) that removes a clone carries the spend over, counting it as already spent. It uses the larger of two figures: what the journal records, or what was carried before plus what the removed run's ledger records in `brain_end`, when that ledger reads.

**Usage.** The `brain_end` ledger record gains `input_tokens` and `output_tokens`. The envelope's usage covers every brain run of the attempt, from every delivery: the spend carried over in the journal and the `brain_end` records of the current run's ledger.

### R5. Landing a resumed attempt

**The landing plan.** Before it pushes, the worker records the landing plan in the journal. The plan holds everything the remaining landing steps need, and everything the result reports:

- the branch and the commit;
- the outcome (`landed` or `parked`) and the status;
- the measured verdict and whether the change closes the issue;
- the pull request's title, body and draft flag;
- the warnings raised so far.

The status labels and the comment are the mirror's, and the mirror derives them from the outcome (R6).

**The run's change is on hub's git server** when the branch named in the journal's landing plan is on hub's git server at the plan's commit. A read of the remote with the PAT decides this. When it is:

- nothing is measured or committed again;
- the delivery finishes the landing steps the journal does not record as done, from the plan, and then the mirror (R6);
- the result reports the same branch, commit, verdict and pull request that an uninterrupted landing would have.

**The baseline.** Measuring compares the change with the baseline result that `fix.json` names, which `checks.Compare` reads from the clone's `.agent-fox/gate/<id>/`. A resumed delivery, or a delivery with a replacement box (R7), checks that this result is there before it measures. When it is not, the delivery runs the baseline again in its box, at the pinned base head and with the pinned table, with the run's change set aside and brought back afterwards. `fix.json` then names the new baseline, and the change is measured against it. A baseline that cannot be run because the box failed is handled as that box failure (R7, or the worker's transient `sandbox` error).

**Repeating steps.**

- A pull request, and an issue created for a triage of text or a file, are recorded in the journal as soon as hub answers. A resumed delivery never repeats a recorded one.
- A resumed triage landing is given the recorded issue as the one an earlier landing of the run created (`triage.Land`'s `Created`). It replaces that issue's body, and creates no second issue.
- Comments are not journalled for idempotency: the worker's marker keeps a comment from being posted twice.
- The push, hub's branch push route and the label writes are repeated when they are not recorded, because repeating them changes nothing.
- A pull request refused on a resumed delivery is the existing warning `pull_request_failed` on a change that landed.

**A fix report, with the brain skipped and the change not published:**

1. Take one lease as the worker does, with the pinned profile, and sync the clone.
2. Check the baseline (above), then measure the change once, with the pinned table and baseline.
3. A change that lands is landed. A refused change is parked, with no further brain round, as `af fix land` (`fix.LandRun`) does.
4. A change that is nothing once the protected paths are put back ends the attempt `failed` with category `no_result`, not retryable. That is the failure `fix.Finish` gives an uninterrupted run that is left with no change.

**A fix question, or a triage question**, returns `needs_human`. It takes no lease. The mirror posts the question comment and sets the label once, with the worker's marker guarding the comment.

**A triage diagnosis** is landed again through `triage.Land` against the clone's tree, with the created issue the journal records, if any. It takes no lease.

### R6. Finishing the attempt's bookkeeping

**The outcome file.** As soon as an attempt's outcome is known, the worker writes `recovery/outcome.json` in the attempt directory, mode 0600, replaced atomically and synced. The outcome is the loop's envelope, or an envelope the worker built.

- The first write comes before the mirror. It holds the envelope, the report text and the most attempts, marked as not yet mirrored.
- The journal records each mirror step as it is done: `status_labels` and `comment`.
- When the mirror has finished, the worker writes `outcome.json` again, with the envelope as the mirror amended it and the labels set and removed, marked as mirrored.
- Both writes come before the release, the usage, the events batch and the completion of the session.

A delivery stopped for a shutdown, or returning a transient error, never writes it.

**A resumed delivery that finds a valid `outcome.json`** for this job and attempt:

- runs no loop and takes no lease;
- when it is not yet mirrored, finishes the mirror:
  - status label writes the journal does not record are made again, since repeating them changes nothing;
  - the comment is posted only when the worker's marker is not already on the issue;
- releases the leases the journal records as unreleased;
- finishes the report steps the journal does not record as done;
- returns that outcome, with only the warning `attempt_resumed` added to the envelope (R9).

**Reporting to hub.** Only the delivery that produces the attempt's outcome, or a resumed delivery that finds it stored, posts usage, posts the events batch and completes the session. When no delivery does, the reconciliation pass completes the session (R8).

**Usage** is posted to the session once per attempt, with the attempt's total from R4, and never again once the journal records it.

### R7. A box lost during a delivery

**What counts as lost.** A box is lost when, after it was opened, a box operation fails because the sandbox is gone or cannot be reached. A box operation is a brain's tool call, a host-side exec, a sync, an export, a file write or a gate run. The signs are:

- the hands' tool error code `sandbox_unavailable`;
- the gateway answering that the sandbox does not exist or that its lease has expired;
- the gateway not being reachable.

**What does not count.** A lease that hub refuses, a box that cannot be opened, and any other sandbox failure go the worker spec's way. The box layer makes a loss recognisable without changing a failure's category or message.

**The first loss in a delivery.** The worker:

1. stops the work in progress without turning it into a landing or a parking;
2. records the loss;
3. releases the lost lease (hub answering 404 counts as released, and a failure is logged at `WARN` and left to R8);
4. takes a new lease with the pinned profile and the same purpose. The delivery's deadline is then cut to end 2 minutes before the replacement's `expires_at` when that is earlier, with the worker's warning `lease_shortened_timeout`. A replacement with less than 2 minutes left counts as refused. The worker opens the box and syncs the clone;
5. reports the new sandbox id to the recorder;
6. raises `box_replaced` with both sandbox ids;
7. goes on from where the attempt stands, by R3 to R5:
   - an accepted and applied submission skips the brain: the baseline is checked, then R5's measure, then land or park;
   - otherwise the clone starts again from the base (R3), and the loop runs with what is left of the budget.

**A second loss**, or a replacement lease that is refused or cannot be opened, ends the delivery with a transient error of category `sandbox`, retryable. The jobs layer's backoff then applies, and its next delivery resumes by R2.

### R8. Orphaned leases and open sessions

**The labels.** Every lease the worker takes carries the labels `job` (the job id) and `attempt` (the attempt number), as agent_fox_worker R6 sets them. The reconciliation pass reads them.

**When a pass runs.** A reconciliation pass runs when the service starts, without delaying listening or claiming, and then every 15 minutes. Only one pass runs at a time, and a pass stops on shutdown. The interval can be injected in tests.

**Leases.** For each subscription whose token opens, the pass lists the workspace's leases through hub with the PAT, following hub's pagination if it has any. A lease is an orphan when:

- its `job` label names a job in this database;
- no runner of this process is carrying out the attempt its `attempt` label names.

Each orphan is released, and hub answering 404 counts as released. A lease with no `job` label, or one that names a job this database does not know, is left alone.

**Open sessions.** The pass also looks at attempts of this database that have ended (`done`, `failed`, `cancelled` or `needs_human`) and whose journal records a session that was not completed. Such an attempt ended without a delivery finishing its report: its transient retries were spent, it was cancelled while waiting out a backoff, or hub refused its completion. For each one, with the workspace's PAT, the pass:

- posts the usage the journal records as spent and not yet posted;
- completes the session with the status of the stored outcome when there is one, and `failed` otherwise;
- records both steps in the journal.

It posts no events batch. An attempt whose journal does not read is left alone.

**When a workspace fails.** A workspace whose token is unreadable, or for which hub answers 401, 403, 5xx or not at all, is logged at `WARN`. The log names the workspace and never the token. The pass goes on to the next workspace, and the next pass tries again.

**Logging.** Each release and each completed session is logged at `INFO` with the workspace, the sandbox id or session id, the job and the attempt.

### R9. Visibility

A resumed delivery emits a `step` event with name `resume`. Its data is:

- the handover count;
- the stage it resumed from: `outcome`, `published`, `applied`, `submitted` or `brain`;
- whether the brain was skipped;
- the reason, when the resume was discarded.

A box replacement emits a `step` event with name `box_replaced` and both sandbox ids.

Every resumed delivery's envelope carries the warning `attempt_resumed`, which names the handover count, the stage, and whether the brain was skipped. At the `outcome` stage, that warning is the only change to the stored envelope. The envelope also carries `resume_discarded`, `box_replaced`, `journal_unwritable` and `brain_spend_unrecorded` when they apply.

### R10. Documentation and offline tests

**Documentation.** After `make doc`, the following are updated in the same change:

- `docs/architecture.md`, in the service section: the journal and the outcome file under `recovery/`, the resume stages and their rules, the settings a resume keeps, re-measuring before publishing and running a missing baseline again, the box replacement, and the reconciliation of leases and open sessions;
- `docs/api.md`:
  - beside the worker's warnings, the warning codes `attempt_resumed`, `resume_discarded`, `box_replaced`, `journal_unwritable` and `brain_spend_unrecorded`;
  - an attempt's `deliveries` above 1 means it was resumed, and the resumed delivery's envelope says from where;
- `docs/cli.md`: the `brain_end` ledger fields `input_tokens` and `output_tokens`, and nothing else;
- `docs/model-usage.md`:
  - a resumed attempt with a valid submission does not run the brain again;
  - a replaced box or a resume without one runs it within what is left of the attempt's budget;
  - an attempt's usage includes its interrupted deliveries, except a brain run that a process death cut short, which is warned about;
- `docs/development.md`: the fake hub's lease listing and the stage-abort seam;
- `docs/errata/14_*.md`: any divergence from this PRD.

**Tests.** Every test runs offline, against the worker's fake hub and an in-process gateway over `local`, with the scripted brain.

- A process dying is simulated by a test-only seam that stops a delivery right after a named stage, without any cleanup. The runner is then called again with the next handover count on the same attempt directory.
- A graceful shutdown is tested through the real jobs layer and a restart on the same database file.

## Design Decisions

1. **Every delivery after the first resumes, and the handover count together with the journal decides how.** The jobs layer passes the handover count for this purpose. One rule then covers a restart, a crash and a transient re-handover.
2. **The journal lives in the attempt directory beside the clone, in `recovery/`, not in SQLite.** It has to agree with the clone it describes. Both survive or vanish together, retention removes both, and the runner contract gives the runner no store.
3. **The change is re-measured, not trusted from the stored verdict (PRD 01 §11.10).**
   - `fix.Measure` needs the host checkout, which survives in the clone, and the baseline result, which is run again when the clone does not hold it (Design Decision 26).
   - `fix.LandRun` already measures and lands without a brain.
   - Trusting a verdict would mean storing the whole measurement and proving the tree has not changed since.
   - The cost is one lease and the gate's time, not a brain run.
4. **Once the change is on hub's git server, nothing is measured or committed again; the recorded landing plan is finished.** The published commit is the change, and a second measurement could disagree with what is already public.
5. **An unpushed commit of the run is undone into the working tree, and the branch name comes back the same.** `checkBase` refuses any other checkout. This keeps `fix.Land` unchanged, and `UniqueBranchName` returns the same name once the local branch is gone and the remote lacks it.
6. **Skipping the brain needs the ledger to show the apply, not only a valid `submission.json`.** `RunRound` writes the host submission before it exports and applies the tree, so a crash in between leaves a valid submission with a missing or partial change.
7. **A refused re-measure parks, with no new brain round.** This is `af fix land`'s rule. A path that skipped the brain never spends brain money, and the parked job is `failed`, so a person can retry it.
8. **One budget per brain-level attempt across deliveries. It is counted from the ledger's `brain_end` records, which gain token counts, plus the spend the journal carries over.**
   - `AF_MAX_BUDGET_USD` bounds an attempt (jobs Design Decision 25).
   - A fresh budget per delivery could spend up to six times the bound: three transient handovers, each with one box replacement. Both of those paths record `brain_end`, because the brain returns when its box is lost or its context is cancelled.
9. **The time bound is per delivery.** A restart should not eat the next delivery's clock, and time is not spend.
10. **A pull request and a created issue are recorded when hub answers. Comments rely on the worker's marker. The push, hub's branch push route and labels are simply repeated.**
    - No `issuex` or hub call lists pull requests by head. An issue created for text input is found again only through the run's landing ledger record, which is written after the landing ends. So for these two the journal is the only record.
    - The worker already puts a marker naming the job, the attempt and the kind on every comment, and checks the issue for it before posting (agent_fox_worker R8). A resume of the same attempt therefore never posts a comment twice, and the journal need not guard comments.
    - Pushing the same commit again, and re-applying labels, changes nothing.
    - The window between hub's answer and the journal write is accepted for the pull request and the created issue only.
11. **The outcome file is written once the outcome is known, before the mirror, and again once the mirror is done. A delivery stopped for a shutdown or returning a transient error never writes it.**
    - Without it, a crash after the outcome would run the brain again for an attempt that had already ended.
    - A shutdown's outcome is exactly what the jobs layer discards, and a transient error is no outcome.
12. **The hub session is reused, and usage is posted once with the attempt's total.** PRD 01's glossary makes the session, the cost and the lease belong to the attempt, not the delivery. Only the delivery that produces the outcome, or the reconciliation pass, reports to it (Design Decision 24).
13. **The interrupted delivery's events are not replayed to hub audit.** They are already stored in `af_events`. An exact replay would need a cursor into hub's batch that the journal cannot make reliably.
14. **A lost box is replaced once inside the delivery, and the delivery goes on from where the attempt stands. A second loss is a transient error.**
    - PRD 01 §6.4 says "retryable once with a new lease".
    - Replacing it in place keeps the accepted and applied change, so a loss during the measure is no reason to run the brain again.
    - The jobs layer's backoff is for repeated failure.
15. **"Lost" is narrow: a gone or unreachable sandbox.** A sync that fails for a reason in the tree is not mended by a new box, and the worker spec already handles other sandbox failures.
16. **An orphan is a lease labelled for a job in this database whose attempt has no runner in this process. Leases of unknown jobs are left alone.**
    - Two deployments may watch one workspace, and their leases are not ours to destroy.
    - Hub's TTL bounds whatever is left.
    - Checking for a live runner, rather than the attempt's state, keeps a pass from killing a box that a runner has just leased.
17. **Reconciliation runs at start without blocking, then every 15 minutes, with no configuration key.**
    - The periodic pass catches a release or a session completion that failed at the end of an attempt.
    - Not waiting for hub keeps a slow hub from delaying start.
    - A fixed interval avoids a key that nobody needs to tune.
18. **The lease labels `job` and `attempt` are the worker's (agent_fox_worker R6), as PRD 03 §6 names them. This spec relies on them and adds nothing to the lease request.** Without them a pass cannot tell whose a lease is.
19. **Releasing the lease on a shutdown is the worker's: it releases on every path (agent_fox_worker R6). This spec only records the release in the journal.** A resume and the reconciliation pass then know whether a lease still needs releasing. The lease token is never written down, so a box cannot be reattached anyway.
20. **The journal holds no credential and is written mode 0600, atomically and synced. A failed write before the landing stops the landing; after that, a failed write is a warning.**
    - "Tracking never fails a run" (PRD 01 §6.4), so a journal that cannot be written does not end the attempt. Before the landing, it costs only a transient retry, because nothing has been written to the forge yet.
    - A journal that missed the landing plan would let a resume re-land under a `-2` branch with a second pull request. The landing therefore does not start without the plan.
    - Once the landing has started, the delivery finishes it from memory. A process death after a failed write can then repeat an unrecorded pull request or created issue. This is the same window as Design Decision 10, widened to the rest of that delivery.
21. **A state the rules do not cover ends the attempt `failed` with category `internal`, not retryable.** Guessing could publish the wrong change. A person's retry starts a new attempt from a fresh clone.
22. **When the brain runs again, it reuses the run directory and runs the whole loop (Design Decision 32).** One run id per attempt keeps the hub run id, the ledger's spend and `fix.json`'s round count continuous.
23. **The requirements state behaviour. Packages, types and signatures are left to design.** The core and jobs specs made the same choice. The fake hub's lease listing and the stage-abort seam are test helpers.
24. **A delivery that stops without an outcome (a shutdown or a transient error) posts no usage and no events batch, and leaves the session open. The reconciliation pass completes the sessions of attempts that ended without a delivery finishing them.**
    - The worker spec completes the session on both paths. Then a resume would find it closed, and "one session per attempt, usage posted once with the total" could not hold.
    - The jobs layer always hands the attempt over again after a shutdown or a transient error, so the finishing delivery reports once.
    - Sometimes the attempt ends without a runner: spent transient retries, or a cancel during a backoff. The pass then completes the session from the journal, so no session stays open longer than one pass interval.
    - A journal that shows a completed session while work remains is handled by opening a new session for the rest. Writing to a completed session could be refused by hub.
25. **A brain run cut short by a process death counts as no spend, with the warning `brain_spend_unrecorded`.**
    - Its cost is not in the ledger or anywhere else this spec can read.
    - Charging its whole allowance would fail every attempt whose first brain run a crash cut, because the first round's allowance is the whole budget.
    - A graceful shutdown and a box loss both record `brain_end`, so Design Decision 8's bound holds for them. Only a hard crash escapes it, and repeated hard crashes are the operator's (Design Decision 38).
26. **A baseline result missing from the clone is run again in the box, at the pinned base head with the pinned table. It is not a failure.**
    - The worker spec reads in-box results from envelopes and does not promise to keep the baseline on the host. After a crash or a lost box, the box that ran it is gone.
    - `checks.Compare` reads the host directory, so without the result every re-measure would be `unverified` and park.
    - Running it again costs one gate run and keeps re-measuring meaningful.
27. **An issue created for a triage of text or a file is recorded in the journal as soon as hub answers, and handed to `triage.Land` as the issue an earlier landing created.** `triage.Land` is repeatable for issue inputs only. For text, the ledger record it relies on is written after the landing ends, so a crash in between would create a second issue.
28. **The outcome file comes before the mirror, and the mirror's steps are journalled.**
    - If the file were written after the mirror, a crash during the mirror of a `failed` attempt with no submission would leave no outcome and no landing plan, and the brain would run again for an attempt that had ended.
    - If it were written before the mirror and the mirror were not journalled, the mirror would never run.
    - Status labels are safe to repeat, and the worker's marker guards the comment.
29. **A resume from a stored outcome returns the stored envelope with only the warning `attempt_resumed` added.** The worker amends envelopes only by adding to them (agent_fox_worker Design Decision 33). The warning keeps the stored record honest that the attempt was resumed, and every resumed delivery says so the same way.
30. **A resumed change that is nothing once the protected paths are put back is `no_result`, not retryable, as in an uninterrupted run.** `fix.Finish` gives that failure, and with the jobs layer's rule 5 a retryable one would start an automatic attempt that an uninterrupted run would not.
31. **A resume finishes the run with the settings it was started with. Only a changed `hub_url` or forge repository discards it.**
    - The base, the table and the baseline are pinned in `fix.json`, and the session was opened with the first delivery's model. Mixing in changed values would measure, bill or land a run under settings it was not started with.
    - The journal pins the brain, the model, the sandbox profile, the landing mode, the budget and the turn bound for the same reason.
    - The time bound is per delivery anyway (Design Decision 9), and `AF_MAX_ATTEMPTS` bounds the job, not the run.
    - A changed clone URL or forge repository makes the clone or the hub forge client unusable, so the attempt starts afresh.
    - The worker's refusals at resolve still apply to the current values, so a workspace moved to `carry_patch` stops its fixes.
32. **A brain run on a resumed delivery, or after a box replacement, is the fix loop from the base head, not one round followed by land-or-park.**
    - The clone is reset to the base whenever the brain runs again. A partial change, or a change an earlier round left and a later round was building on, is no ground to build on without the refusal that went with it, and the ledger keeps only the refusal's kind.
    - Running the loop gives the brain its refusal-fed rounds within what is left of the budget, as an uninterrupted delivery would.
    - The run-directory commands `af fix run --run-id` and `af fix land --run-id` exist. Only `fix.LandRun`, which `af fix land` uses, is reused, for the path that skips the brain.
33. **A ledger that does not read discards the resume. The attempt's spend and session outlive a discarded clone through the journal.**
    - The brain-skip test and the budget both read the ledger, and `RunDir.Record` fails on a torn ledger, so the run cannot go on.
    - Without the journal's spend, a fresh start would get a full budget again and report none of the interrupted delivery's spend.
    - Reusing the journal's uncompleted session, and its run id, keeps one session per attempt and leaves none open on hub.
34. **A replacement lease cuts the delivery's deadline to 2 minutes before its `expires_at`, and one with less than 2 minutes left counts as refused.** This is the worker's rule for every lease it takes (agent_fox_worker R6). A loop that outlives its box loses its work.
35. **A delivery with nothing to discard starts fresh without `resume_discarded`.** A re-handover after a transient error at resolve or at the clone never got as far as a clone. A warning about discarding work would be noise there.
36. **The journal and the outcome file live in a named subdirectory, `recovery/`.** The worker spec works only in named subdirectories of the attempt directory, of mode `0700` (agent_fox_worker R5). Adding one keeps that rule, and the jobs layer's `envelope.json` and `report.md` stay untouched.
37. **The service warnings this spec adds are documented in `docs/api.md`, beside the worker's.** The worker keeps service warnings there and leaves `docs/cli.md` unchanged. Only the `brain_end` ledger fields, which `af` also writes, go in `docs/cli.md`.
38. **This spec sets no limit on how often one attempt is resumed.** Each handover is visible in `deliveries`, and the jobs layer counts a crash or shutdown handover as no failure (jobs Design Decision 5). A cap would fail attempts on a service that is only restarted often. A process that crashes on one attempt again and again needs an operator in any case.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `11_agent_fox_service_core` | depends on | The subscription list and the in-process PAT read by slug (failing as not subscribed or unreadable), the hub client, and the fake hub. This spec extends the last two with lease listing. |
| `12_agent_fox_jobs` | depends on | The runner contract: the handover count, the attempt directory, transient errors, the shutdown cause, and the recorder's hub references. Also the job lookup by id for orphans, the attempt states and directories for the session sweep, and redelivery after a restart. It assumes the jobs layer keeps the attempt directory's content across deliveries of one attempt (it creates the directory when missing and never empties it). |
| `agent_fox_worker` (`13_agent_fox_worker`) | depends on, modifies | It relies on the pipeline stages, the clone, the gateway box, the lease request with the labels `job` and `attempt`, release on every path including a shutdown, the timeout cut to the lease's `expires_at`, the session, the hub-backed publish hook, the mirror and its comment marker, usage, events and release. It adds `recovery/` to the worker's named subdirectories, with the journal and the outcome file; the outcome file is written before the mirror, and the mirror steps are recorded. It records a pull request and a created issue as soon as hub answers. It replaces the worker's "a delivery that finds a kept `clone/` removes it" with the resume paths. It adds the in-delivery box replacement. It changes the worker's shutdown and transient-error paths (its R8 stop table and shutdown paragraph, its R9) to post no usage and no events batch and to leave the session open. |
| `09_af_fix` | depends on | It relies on `Measure`, `LandRun`, `Land`, `Park`, `Finish`'s interrupted park, the `Agent-Fox-Run` trailer, and `UniqueBranchName` naming. This spec changes no behaviour of `af fix`. |
| `08_af_triage` | depends on | `triage.Land` is repeatable for issue inputs and takes the created issue through `Created`. `triage.Validate` checks a diagnosis against the host tree. |
| `07_af_brains` | modifies | The `brain_end` ledger record gains `input_tokens` and `output_tokens`. A tally is seeded from earlier `brain_end` records and the spend the journal carries over. |
| `10_af_boxes_claude` | modifies | Box failures caused by a gone sandbox (`sandbox_unavailable`) become recognisable with `errors.Is`, keeping their category, retryable flag and message. |
| `05_af_cli_envelope` | depends on | `RunDir`, `OpenRunDir` and `Ledger`, the warning code format, and the envelope. |

## Verified External API

**`github.com/agent-fox-dev/agentkit-go/sandbox`** (replaced to `../agentkit-go/sandbox`, outside the repository). These are verified by the call sites in `internal/box` that compile against them:

- `sandbox.Sandbox` with `Sync(ctx, src)`, `Export(ctx, name string, dst)`, `Close(ctx) error` and `Spec() sandbox.Spec` (`internal/box/session.go`);
- `sandbox.LocalDir{Path}` and `sandbox.Tar{R, Path}`;
- `sandbox.ErrNoAgent` (`internal/box/podman.go:42`).

No sentinel for a gone sandbox is referenced anywhere in the repository. Whether `sandbox` exports one is **unverified**.

**`github.com/agent-fox-dev/agentkit-go/core`**: `core.ToolResult{OK bool; Error, Detail string; Data map[string]any}` and `core.ErrResult(code, detail string) core.ToolResult` are read from call sites (`internal/box/exec.go`, `internal/box/exec_test.go:123`). The tool error code `sandbox_unavailable` for a gone box is implied by `internal/box/fs.go:28` and that test stub, and is **unverified** at the source.

**`gateway.Dial` and `gatewayd.Server`**: **NOT FOUND** in the repository. The worker spec introduces them. This spec assumes only that the gateway box surfaces a gone sandbox through the tool error code above, a not-found or expired answer, or a connection error.

**Hub HTTP API**: **unverified**, since no schema exists in the repository. The following are assumed, from PRD 01 §6.4 and §9 and PRD 03 §6:

- `GET /workspaces/:slug/sandboxes` lists leases with `{id, labels, purpose, expires_at}`, with pagination if hub has it;
- `POST /workspaces/:slug/sandboxes {profile, purpose, labels}` returns `{id, gateway_url, token, expires_at}`;
- `DELETE /workspaces/:slug/sandboxes/:id`, where 404 means the lease is already gone;
- the session routes `POST /sessions`, `POST /sessions/:id/usage` and `POST /sessions/:id/complete`, as the worker spec uses them. That hub keeps a session open until it is completed, and accepts a second session with the same `run_id` (R2's new session after a completed one), is assumed.

**`github.com/agent-fox-dev/aflib/issuex`** (pinned in `go.mod`), from call sites:

- `Client.ReadIssue(ctx, issuex.IssueRef)`, whose thread carries `Issue.Body` and `Comments[].Body` (`internal/triage/land.go`);
- `Client.CreatePullRequest(ctx, issuex.Repo, issuex.CreatePullRequestRequest{Title, Body, Head, Base, Draft})` (`internal/fix/land.go`);
- `Client.CreateIssue(ctx, issuex.Repo, issuex.CreateIssueRequest{Title, Body, Labels})`, answering an `issuex.Issue` with `Number`, `URL` and `HTMLURL` (`internal/triage/land.go`).

There is no list-pull-requests method in the interface (`internal/toolio/dryrun.go`).

**In this repository** (read): `triage.LandParams.Created func() (IssueInfo, bool)` (`internal/triage/land.go`); `checks.Compare(dir, before, after string)` reading stored results under the host directory (`internal/checks/compare.go`); `fix.Finish`'s `exhausted` failure `no_result` with `Retryable` unset (`internal/fix/finish.go`).

**Standard library**: `os`, `encoding/json`, `crypto/sha256`, `errors.Is` and `errors.As`, and `context.Cause`.
