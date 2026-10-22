# Erratum 14: where the recovery of the worker does something other than the spec says

**Spec:** `14_agent_fox_worker_recovery`. Each divergence below was decided during the survey or on the way,
and is recorded here because the spec stays as it was written. The code and the test of each are named so that
a reader can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is
named.

## 1. Hub lists leases in one array, without pages (task 1)

**Spec:** "following hub's pagination if it has any" (R8, 14-REQ-8.2), step 1.5 ("`ListSandboxes` (following
pagination)"), and TS-14-51, which lists an orphan "across two pages".

**Code:** hub's OpenAPI document (`../hub/docs/openapi.yaml`, `listSandboxLeases`) lists the leases of a
workspace as a bare array, newest first, at most 200, with the live ones only (`leased` and `active`) unless
`all=true` is asked for, and has no pages. `hub.Client.ListSandboxes` (`internal/hub/calls.go`) reads that one
array and keeps the id, the purpose, the state, the labels and the end of each lease; the fake hub answers the
same way (`listLeases`, `internal/hub/fakehub/gateway.go`). There is nothing to follow, so the "two pages" of
TS-14-51 are several leases in one listing
(`TestTheLeasesOfAWorkspaceAreListedWithTheirLabelsAndEnd`, `internal/hub/calls_test.go`). Deviation key:
`lease_listing_has_no_pages`.

## 2. The plan of a landing and the forge writes that a failed plan keeps from happening (task 2)

**Spec:** 14-REQ-1.4 "make no push to hub's git server, no call of hub's branch push route and no forge write
in that delivery", TS-14-4 ("zero forge writes for the delivery").

**Code:** the delivery of TS-14-4 also marks the issue `in-progress` before it clones, and the transient error it
ends in takes the label off again, as 14-REQ-1.7 says it must. Those two writes are the worker's own and are not
part of a landing, so "no forge write" is read as no write of the landing: no issue created, no pull request, no
comment, and no call of hub's branch push route (`TestWhenTheLandingPlanCannotBeJournalledNothingGoesToHubsGitServerOrTheForge_TS_14_4`).
The commit has been made when the plan is written (it holds the hash), so it stays on its branch in the clone
for the delivery that comes next (`fix.Land`, `landing.plan`). Deviation key: `plan_failure_keeps_the_labels`.

## 3. The tests of spec 13 that assert what a transient error reports (task 2)

**Spec:** 14-REQ-1.6 and the paragraph "A delivery that stops without an outcome" of R1: the worker no longer
posts the usage and the events and no longer completes the session when it returns a transient error or is
stopped for a shutdown. Spec 13 (R8, R9) had the worker post the events and complete the session `failed` on a
transient error.

**Code:** `finish` in `internal/worker/runner.go` reports only when the delivery comes to an outcome. The tests
of spec 13 that asserted the report of a transient error now assert the new rule instead:
`TestAfterTheReleaseOneUsageRecordTheEventsInBatchesAndTheCompletionAreReported_TS_13_76` (a transient error),
`TestAFailingUsageEventsOrCompleteCallAddsAWarningAndNothingElseChanges_TS_13_77` (a transient error),
`TestALeaseRefusedWith503ComesBackAsATransientErrorAndIsRetried_TS_13_87` (which also expects the second delivery to report to the session of the first), and the two tests that list the entries
of the attempt directory, which now holds `recovery/`
(`TestTheWorkerCreatesOnlyCloneAndInputWithMode0700AndLeavesTheRestAlone_TS_13_33` and
`TestNeitherThePATNorTheLeaseTokenIsInAnyOutputOrFileAfterAFullFixAttempt_TS_13_79`). Nothing was skipped or
weakened: each asserts what the spec of this change requires. Deviation key: `spec_13_report_tests_follow_the_change`.

## 4. The journal calls the clone URL of the workspace `clone_url` (task 2)

**Spec:** R1 and 14-REQ-1.2: the journal records "the workspace's `hub_url` without credentials".

**Code:** the journal holds that URL (`recovery.Journal.HubURL`, `internal/worker/recovery/journal.go`) under the
key `clone_url`. A test of spec 13 (`TestBothEncodingsOfTheResolvedVariablesDecodeToTheSameMap_TS_13_19`) keeps
every field name of hub out of the packages but `internal/hub`, and `hub_url` is one of them; the value is the
same and so is the field in Go. `TestAJournalWrittenAfterAFullFixDeliveryDecodesWithEveryDocumentedField_TS_14_2`
reads the key. Deviation key: `journal_key_clone_url`.

## 5. The seams that task 2 needed outside the files it lists (task 2)

**Spec:** the `touches` of task 2 are `internal/worker/recovery/`, `runner.go`, `run.go`, `stageabort.go` and
`cleanup.go`.

**Code:** a journal that follows what the loop does in its own process, and a delivery that can be stopped
after a stage inside the loop, need more than those files:

- `internal/toolio` gains `App.LedgerSink` (every record of the ledger is handed to the caller once it is
  written, by the goroutine that records, with no lock held) and `App.ReuseRunDir` (a run id that names a
  run directory that exists opens it, and its ledger and events go on), and `Run.Warnings()`. The worker reads
  `fix.brief`, `submission_accepted` and `apply` from the ledger to learn the base of the run and to reach
  the stages `briefed`, `submission_accepted` and `applied`. `ReuseRunDir` is for the resume of task 4.
- `internal/fix` (`LandSteps` and `Plan` in `land.go`, `park.go`, `finish.go`) and `internal/afcmd`
  (`Config.Landing`) tell the worker the plan of a landing, after the commit and before the push, and the
  steps `pushed`, `hub_push` and `pull_request` as they are done. Task 6 lists these files; task 2 owns
  TS-14-2 and TS-14-4, which cannot pass without them.
- `internal/worker/journal.go` and `internal/worker/recovery/` hold the journal; there is no `cleanup.go`:
  `cleanup` stays in `runner.go` and removes `input/` and `clone/` as before.

Deviation key: `task_2_seams_outside_its_touches`.

## 6. A disagreement the rules do not cover is mirrored as a failed attempt (task 3)

**Spec:** 14-REQ-2.7 and TS-14-16: the attempt ends with a failed envelope of category `internal`, "no push,
pull request or comment is made", "cleanup is as for any failed attempt".

**Code:** `disagrees` in `internal/worker/start.go` ends the attempt with that envelope before the clone, the
lease or the loop, and publishes nothing: no push, no pull request, no branch push route
(`TestADisagreementTheResumeRulesDoNotCoverEndsTheAttemptFailedWithCategoryInternal_TS_14_16`). The worker's
cleanup of any failed attempt is the mirror of spec 13 (R8): the issue gets the label `failed` and the one
comment that a failure has, and the lease is released and the clone removed. That comment is not a landing, and
it is how the person learns that the attempt ended; "no comment" is read as no comment of a landing. Deviation key:
`catch_all_is_mirrored_as_a_failure`.

## 7. Until the later tasks, a resumed delivery runs the brain again (task 3)

**Spec:** R2 and R3: a resumed delivery skips the brain when a submission was accepted and applied (task 4),
finishes a landing from its plan (tasks 6 and 7), and returns a stored outcome (task 8).

**Code:** task 3 makes the decision whether a delivery begins afresh, resumes or discards, and what a resume keeps
(the settings, the session, the leases, the spend). What a resumed delivery does is, until the tasks that follow
land, the one rule that needs none of them: it puts the clone back to the base the run began at
(`resetClone`, `internal/worker/restore.go`) and runs the loop again in the same run directory with what is left
of the budget (`afcmd.Config.Resume`, `fixAttempt.resume`). Task 4 ended it for a fix report that the host
accepted and applied (the change is restored into the working tree, measured and landed or parked without the
brain: `skipStage`, `restoreChange`, `fixAttempt.landApplied`); tasks 6 to 8 do the same for a landing that was
begun, a question or a diagnosis, and a stored outcome, and the key below ends with task 8. Deviation key: `resume_runs_the_brain_until_task_8`.

## 8. What is left of the budget was taken from the ledger in task 3 already (task 5)

**Spec:** task 5 owns TS-14-22, which asserts that the brain of a resumed delivery gets the pinned budget and turns
less what the attempt spent (14-REQ-4.1), and lists its steps 5.2 to 5.6 as the work that makes it so.

**Code:** a delivery that resumes, or that begins from a fresh clone after a discard, has to give the loop the
bounds that are left, and the test of that (TS-14-13, TS-14-10) is task 3's; `leave` in `internal/worker/start.go`
already took the spend of the ledger, plus the carried spend, off the pinned bounds. TS-14-22 therefore passed
the first time it was run, as a pin of that code (`TestABrainRunOnAResumedDeliveryGetsThePinnedBudgetAndTurnsLessWhatTheAttemptSpent_TS_14_22`).
Task 5 adds what was not there: the refusal when nothing is left (`nothingLeft`, a failure of category `budget`
that is not retryable, with the variable to raise, before the clone and the lease), the warning for a brain run
that the process died during (`scanLedger`, `brain_spend_unrecorded` naming the ledger entry and the round), and
the refresh of the spend of the journal at the end of each brain run (`refreshSpend`, from the ledger sink).
Deviation key: `ts_14_22_passes_before_task_5`.

## 9. The plan was written before the push in task 2, and the envelope of a published change is rebuilt (task 6)

**Spec:** task 6 owns TS-14-29 (the landing plan is journalled before the first push) and lists steps 6.2 and 6.3
(the hook before the push, the steps as hub answers) as its work; 14-REQ-5.2 says a resumed delivery that finds the
branch of the plan on hub's git server reports "the same branch, commit, verdict and pull request that an
uninterrupted landing would have".

**Code:** the hook before the push and the recording of `pushed`, `hub_push` and `pull_request` were written
in task 2, whose TS-14-2 and TS-14-4 cannot pass without them, so TS-14-29 passed when it was first run
(`TestTheLandingPlanIsJournalledBeforeTheFirstPush_TS_14_29`). Task 6 adds what was not there: `created_issue`
(an issue made for a text or a file, for a triage and for a fix), the detection of the published change
(`disagrees` returns the commit of the plan's branch on hub's git server, `internal/worker/start.go`) and
`finishPublished` (`internal/worker/published.go`). The envelope of a change that was published is built by the
worker from the plan, since no loop runs: its result is the result of the run as it stood when the plan was
written, with the push and the pull request of this landing; its warnings are the plan's; its artifacts are the
branch, the commit, the pull request, the run directory and its report; its usage is the attempt's. The side
effects and the artifacts that the loop of the first delivery made (the brief, the submission, the transcript, the
steps of the commit and the push) are not in it: the first delivery's events hold them. Deviation keys:
`ts_14_29_passes_before_task_6` and `published_envelope_is_rebuilt_from_the_plan`.
