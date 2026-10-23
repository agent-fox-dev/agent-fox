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
- Nothing new is written when a running attempt is cancelled. The fact is in the attempts: an attempt that was in a
  runner at the cancel is `running` still, or `cancelled` with an envelope, while one that was only queued is
  cancelled without one. So it holds after the runner returns and after the grace runs out, and no migration was
  needed beyond the poll's own.

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
