# Erratum 15: where the poll does something other than the spec says

**Spec:** `15_agent_fox_poll`. Each divergence below was decided during the survey or on the way, and is recorded
here because the spec stays as it was written. The code and the test of each are named so that a reader can check
what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is named.

## 1. Hub's issues list has no pages, and cannot say that it cut a list short (task 1)

**Spec:** R3 and "Verified External API": hub is assumed to honour "a page size of 100 and say when a further page
exists, either with a cursor field or a `Link` header", and "the hub client confirms the real paging before
implementation". 15-REQ-3.3: "`Incomplete` is true only when the client stopped at `Limit` while hub reported a
further page".

**Code:** hub's OpenAPI document (`../hub/docs/openapi.yaml`, `listForgeIssues`) and its `docs/api.md` ("the list
routes answer the bare array and do not show those two flags") say that `GET /api/v1/workspaces/{slug}/issues`
answers a bare array, takes `limit` (1 to 100, 30 when it is absent, always passed on to the forge) and has no
pages, no cursor and no `Link` header; it leaves pull requests out itself. The client (`ListIssues`,
`internal/hub/forge.go`) therefore:

- asks for `state=open&labels=<label>&limit=100`, which is the page of 100;
- follows a further page only if an answer names one in a `Link` header with `rel="next"`, by asking again with
  `&page=N`. Real hub never does, so a list is one request; the fake hub does, so that `Limit` above 100 and the
  further-page rule of the spec are tested (`TestListIssuesFollowsFurtherPagesAndIsIncompleteOnlyWhenItStoppedAtTheLimitWithMoreToRead_TS_15_16`);
- leaves out an item marked `is_pr`, as the spec says, although real hub has done so already;
- sets `Incomplete` when it stopped at `Limit` with issues left, as the spec says, **and also when the last page
  is as long as the page asked for and says nothing of a further page**. Hub cannot say it cut a list at 100, so a
  list of exactly 100 is not known to be complete, and release and settlement (R5, R6) must not treat it as complete.
  The cost is a `WARN` and a hold or an owed removal left for a cycle, for a workspace with 100 labelled issues.

A list of 100 issues with bodies can be longer than the 1 MiB that every other call of the hub client reads, and a list that was
refused for its size would never shorten, since the labels on its issues are only taken off when they are listed; so a list
answer is read up to 32 MiB (`maxListBody`, `internal/hub/forge.go`; `operation.maxBody`, `internal/hub/calls.go`, the five
lines of the hub client outside the tasks' `touches`, which also carry the headers of an answer for the `Link`). The poll also
takes no more than it asked for: an issue that is `closed`, or that does not carry the label (whatever its case), is not handed to
the rules (`asked`, `internal/service/poll/cycle.go`), so that a hub that dropped a parameter cannot turn the issues of a
repository into jobs. And hub's 502 for a forge that refuses its credential (`forge_unauthenticated`, `forge_forbidden`), which
the hub client takes as a rejection since a retry cannot cure it, is an `ERROR` that ends the cycle on a label removal as well as on a
read, where the spec's table puts every 5xx with the unavailable (`WARN`).

The fake hub serves the same route from its issue state, with `Link` paging that a test can switch off to behave
as the real hub does (`SetPageLinks`), the two lags, and an order a test can set (`internal/hub/fakehub/issues.go`).
Deviation key: `issues_list_has_no_pages`.

## 2. The duplicate check is atomic already, and the read of a job by id is `Standing` (task 3)

**Spec:** 15-REQ-9.1 and step 3.3: "make Create run the duplicate check and the insert in one transaction (or under one
serialising lock)"; 15-REQ-9.2 and step 3.4: "add an exported `Get(ctx, id)`" giving the state, the source, the request
label, `CancelledInRunner` and `RunnerInProgress`; step 3.5: "record the cancel-reached-a-runner fact durably when a
running attempt's job is cancelled".

**Code:**

- The check was atomic with the insert before this spec: the partial unique index `af_jobs_one_open_per_issue`
  (migration 2) refuses a second open job of a kind for an issue inside the transaction of `Store.CreateJob`, however
  the creators race. The one gap was after the refusal: `Service.insert` looked up the job that held the issue to name it
  in the refusal, and if that job had ended in between, the creator was answered "job not found". It now tries to make
  its job again (up to three times) and is refused with `*DuplicateError` only when an open job is there to name
  (`TestACreatorThatLosesToAJobThatEndsBeforeItLooksItUpIsNotToldThatTheJobIsMissing`, `internal/service/jobs/duplicate_test.go`;
  the seam is `OpDuplicate` of the store's fault hook). The 50 races of 15-REQ-9.1 are TS-15-60, task 6.
- `Service.Get` exists and returns the document of a job (`Doc`) for the API's routes, so the new read is
  `Service.Standing` (`internal/service/jobs/read.go`), which returns `Standing{State, Source, RequestLabel,
  CancelledInRunner, RunnerInProgress}` and `ErrNotFound` for a job that was purged
  (`TestTheJobsLayerReadsAJobByIdWithItsStateSourceLabelAndTheCancelAndRunnerFacts_TS_15_61`).
- The fact that a cancel reached a runner is read from the attempts, and so holds after the runner returns and after the
  grace runs out. First read as "the attempt is `running` still, or `cancelled` with an envelope", it took for a runner an
  attempt that was claimed and whose job was cancelled before the runner was called (the layer never calls it, and the
  worker's cleanup never puts a label back), which made a hold of a label that nothing restored. So the attempt carries a
  mark, `in_runner` (migration 6), that a claim clears and the call of the runner sets, and `CancelledInRunner` is a
  cancelled job with an attempt that has the mark and is `running` still or `cancelled` with an envelope
  (`TestAJobCancelledAfterItsAttemptWasClaimedAndBeforeItsRunnerWasCalledIsNotCancelledInARunner`, with the seam `OpInvoke`
  of the store's fault hook). `queue.go` and `store.go` of the jobs layer are outside the tasks' `touches` for it.

Deviation key: `jobs_read_is_standing`.

## 3. The order of the start and the stop is read from the log, and a restart of the poll is a new supervisor (tasks 5 and 8)

**Spec:** TS-15-1 and TS-15-7: "a recording start hook on the jobs layer and on the poll supervisor", "recording stop hooks on the
poll and on the jobs layer"; TS-15-46: "the service restarts on the same database file".

**Code:**

- `serve` has no hooks to record. The tests that only the binary can show (`cmd/agent-fox/poll_test.go`) run `serve` and read the
  order of its own log records instead: the jobs layer's `queue started` before the poll's `the label poll started`, one
  `the poll started for a workspace` for each subscribed workspace, and the poll's `the label poll stopped` before the jobs layer's
  `queue stopped`, with a cycle held on hub when the service is told to stop
  (`TestThePollStartsAfterTheJobsLayerWithOneLoopForEachSubscribedWorkspace_TS_15_1`,
  `TestShutdownStopsThePollAndCancelsItsCycleBeforeTheJobsLayerStops_TS_15_7`). The only seam `serve` gained is `pollFirstDelay`
  (`cmd/agent-fox/run.go`), which lets a test choose the first-cycle delay of the service.
- The restart of TS-15-46 is a new `Supervisor` over the same database file with the same jobs layer, which loses all that the poll
  keeps in memory (what it last read, what it has warned of) and keeps all that it keeps in the file (the records and so the holds)
  (`TestAHoldKeepsTheLabelAloneBeforeAndAfterTheRunnerReturnsAndAfterARestart_TS_15_46`). The jobs layer does not restart in the
  test: a restart of it is shown by spec 12's tests, and what the poll reads from it is a row of the file.

Deviation key: `start_order_from_the_log`.

## 4. The end-to-end test with the real worker is in `internal/worker` (task 11)

**Spec:** task 11 touches `internal/service/poll/smoke_test.go` and lists TS-15-65 among the tests of that file; R8 says that goal 8
"uses `agent_fox_worker`'s test harness".

**Code:** the harness that runs the worker end to end, with the fake hub, its git server, the in-process gateway over the local
provider and the fake `af` and `claude` of the test binary, is `loopEnv` and `TestMain` of `internal/worker`, and the seam that chooses
the scripted brain (`Runner.loopFlags`) is unexported on purpose: no variable of a workspace may name it. TS-15-65 is therefore
`TestATriageLabelBecomesOneJobThatTheRealWorkerRunsToDoneEndToEnd_TS_15_65` in `internal/worker/poll_smoke_test.go`, which builds the
poll (`poll.New`, `Cycle`), the state file and the real jobs layer around that harness; the package `poll` imports nothing of the
worker, so there is no cycle. TS-15-66 to TS-15-68 are in `internal/service/poll/smoke_test.go`, as the task says.

Deviation key: `worker_smoke_in_the_worker_package`.

## What is left

- A fix or a triage of a label on a workspace whose forge URL is none of a repository ends the cycle with a `WARN`; the spec
  does not say what the poll does there.
- The poll does not retry a failed record write other than by the next cycle's records read, and an unreadable table ends the cycle
  with the label left (the choice that never makes a second job of a label that may be owed).
- Hub's issues list cannot say that it cut a list at its limit (item 1): a workspace with 100 or more issues that carry one
  request label at once has an incomplete listing every cycle until the backlog drains, which holds no hold and settles no owed
  removal meanwhile.
- The tests drive `Supervisor.Cycle` directly for the rules and use the loop with the injected clock only for the scheduling
  (TS-15-1 to TS-15-8); the loops and the rules are shown together by the service test of TS-15-7 and by TS-15-65.
- `TS-11-81` of `cmd/agent-fox` and `TS-12-70` of `internal/service/jobs` failed once each under the load of the full suite while this
  spec was implemented, and passed in 20 and 15 runs of their own; neither touches the poll. The first asserts the exact requests that
  hub saw with a token and races the reconciliation pass of spec 14 at start; the second times a subscriber that falls behind.
