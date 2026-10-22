# Erratum 12: where the jobs layer does something other than the spec says

**Spec:** `12_agent_fox_jobs`. Each divergence below was decided during the survey or on the way, and is
recorded here because the spec stays as it was written. The code and the test of each are named so that a
reader can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is
named.

## 1. Four tests of spec 11 state facts that this spec changes (task 1)

**Spec:** the core's configuration gains `[workers].count` and `[database].retain` (R1), and its migration
list gains three migrations (12-REQ-1.3). Spec 11 had tested, as facts of that release, that those two keys
are refused as unknown and that the list has one migration.

**Code and tests:** the tests keep what they were about and stop naming what changed.

- `TestLoadRefusesUnknownKeysAndWrongTypes_TS_11_14` (`internal/service/config/config_test.go`) and
  `TestUnknownKeysAndWrongTypesExit2NamingTheKey_TS_11_14` (`cmd/agent-fox/start_test.go`) refuse
  `workers.limit` in place of `workers.count`, and a `database.retain` that is not a duration.
- `TestMigrationsApplyOnceInOrderAndRollBack_TS_11_26` (`internal/service/store/store_test.go`) wants each
  migration of the list exactly once, in order, whatever the length of the list is.
- `TestADatabaseFromANewerBinaryIsRefusedAndLeftAlone_TS_11_27` (`cmd/agent-fox/start_test.go`) wants the
  error to name the highest migration the binary knows, which it reads from the file the binary's own code
  has just migrated.
- `docs/configuration.md` no longer says that a `[workers]` table and `database.retain` are refused, and
  `TestConfigurationMdListsTheKeysAndTheKeyRecipe_TS_11_75` still finds the terms it looks for.

## 2. An attempt's queue item is keyed by its nonce, and its group is the workspace (task 4)

**Spec:** R4 and 12-REQ-4.1 have each attempt one `aflib/jobqueue` item with "key: the workspace slug", and
the queue serialize by key.

**Queue:** `Queue.Enqueue` refuses an item whose type and key equal those of an item that is queued or
running (it answers the id of that item and `duplicate = true`, `jobqueue.go:349`). With the slug as the
key, a workspace could hold one attempt in the queue, not the many that wait their turn. The queue has a
second way to serialize, the `Group` of an item: of the items of one type that share a group, one runs at
a time (`claimAndExecute`).

**Code:** the item has type `attempt`, the nonce `<job id>:<n>` as its key and as its nonce, the payload
`{"job_id", "attempt"}`, and the workspace as its group (`internal/service/jobs/queue.go`, `Enqueue`). The
nonce, the payload and the one-at-a-time per workspace are the spec's; only the field that holds the
slug differs.

**Test:** TS-12-23 (`internal/service/jobs/queue_test.go`) reads the queue's table and asserts the type,
the key, the group, the nonce and the payload. It sets the row `failed` with a delay in the future by hand
for the "delayed" case, because the delays of the layer itself come with task 6. Commit trailer key:
`queue_item_key_is_the_nonce`.

## 3. The order of a workspace's attempts is kept by the layer (task 4)

**Spec:** attempts of a workspace run "in the order they were enqueued" (R4); the adapter may add the
per-workspace order "when the module lacks it".

**Queue:** it claims by `available_at` and `created_at`, both RFC 3339 to the second
(`claimAndExecute`), so two attempts made in the same second can be claimed in either order.

**Code:** every attempt has a `queue_seq`, which numbers the attempts of all jobs in the order they were
made (migration 3). `Store.Plan` finds the first open attempt of the workspace by it; a handler whose attempt
is open but is not that one returns an error that the queue is to retry, and its item comes back after a
delay (`internal/service/jobs/store.go`, `queue.go`). The delay is constant: the retry policy is
`Base = Cap = RetryDelay`, `Multiplier = 1`, and a `MaxRetries` that is never reached. Because every open
attempt keeps its own item, the attempt at the head of the line is always in the queue.

**Test:** `TestAnAttemptRunsAfterTheOnesBeforeItInItsWorkspaceWhateverOrderTheQueueHasThem`
(`queue_test.go`) gives the queue five attempts in the opposite order, in the same second, and the runner is
called in the order they were made; TS-12-25 checks the order of a burst.

## 4. The runner returns the envelope as the JSON it was written as (task 4)

**Spec:** "a result: the envelope, the report text, …" (R4), and `GET …/envelope` serves "the stored
`envelope.json` verbatim" (12-REQ-9.6); the names and types are left to design.

**Code:** `jobs.Result.Envelope` is a `json.RawMessage`: the bytes a command wrote, which the layer stores
as they are and decodes into a `toolio.Envelope` to decide what the attempt came to. A value of
`toolio.Envelope` would have been encoded again, and `TestTheEnvelopeRouteServesTheStoredBytes` (task 9) would
have had nothing to compare with. `fakerunner.Env` writes an envelope the way a command does, indented, with a
newline at the end.

## 5. Some tests of the spec are in two parts, and the part about a route comes with the route (tasks 5, 7, 9)

TS-12-9 and TS-12-10 are owned by task 5 and say "read through GET /jobs/:id and GET /jobs"; those routes are
task 9's (12-REQ-9). Task 5 asserts what the tests are about, the documents, on `Service.Get`
(`finish_test.go`: `TestEveryJobDocumentHasExactlyTheDocumentedFields…_TS_12_9`,
`TestResultArtifactsAndSideEffects…_TS_12_10`); task 9 asserts the same on the routes. TS-12-36 likewise: task 5
checks the stored files and task 9 the route that serves `envelope.json` (TS-12-62). TS-12-42 has a case "returned
after a cancel without an envelope"; a cancel comes with task 7, which adds that case under the same id.

## 6. An automatic attempt goes to the back of its workspace's line (task 5)

**Spec:** an automatic attempt is "queued" (12-REQ-6.5); attempts of a workspace run in the order they were
enqueued (12-REQ-4.3). Only a transient backoff is said to keep its place (12-REQ-4.4).

**Code:** attempt n+1 gets the next `queue_seq`, so it runs after every attempt of the workspace that is
open already, and not before them. That is what "first come, first served per workspace" says, and it keeps one
job that fails again and again from holding the workspace; the price is that a job's own retry waits for the jobs
that were made before it.

**Test:** `TestResultArtifactsAndSideEffects…_TS_12_10` and `TestAFailedEnvelopeOfTheBrain…_TS_12_40` run one
job at a time because of it.

## 7. The wait after a transient failure is the handler's, on the layer's clock (task 6)

**Spec:** step 6.2 has the same attempt handed over again after 30 s and 2 min "through an injectable backoff
function/timer (use the queue's delayed retry; the attempt keeps its workspace place without a worker slot)".

**Queue:** a retry of `aflib/jobqueue` waits for a delay that its policy computes from the number of retries
(`computeBackoff`) and from the system's time, so a test can neither inject nor record it (TS-12-44 asks for
the waits to be recorded and the clock to move), and an item that waits is not `running`, so its group is free
and the next item of the workspace is claimed: the place is lost.

**Code:** the handler that runs the attempt stays in the handler while the attempt waits. The item stays
claimed, so the queue serializes the workspace behind it (the place is kept), and the handler gives up its
slot for the wait (`internal/service/jobs/queue.go`, `attempt`). That needs more workers in the queue than
slots, so that the handler of an attempt that waits, and of one that waits for a slot, holds a worker and no
slot: the queue has `[workers].count` + 16 workers (`service.go`, `parkedWorkers`), and the slots are a
semaphore of `[workers].count` tokens. With more than 16 workspaces waiting at once, a workspace whose item
is not yet claimed waits for a worker. The waits are 30 s and 2 min on the `Clock` of the layer
(`transient.go`). What the layer does ask of the queue's retry is the hand-back (items 3 and 8).

**Test:** TS-12-44 and TS-12-45 (`recovery_test.go`) record the waits on the fake clock;
`TestATransientBackoffKeepsItsWorkspacesPlaceAndFreesItsSlot_TS_12_26` is the half of TS-12-26 about a backoff
(the half about an overrun comes with task 7). Commit trailer key: `transient_backoff_waited_in_the_handler`.

## 8. A shutdown hands the item back to the queue; it does not complete it (task 6)

**Spec:** after a shutdown the attempt stays `running` and "the queue hands those attempts over again under the
same attempt number" (R9), by the queue's crash recovery.

**Queue:** a handler that returns without an error completes its item, and crash recovery only resets items that
are `running` when the queue starts, which an item whose handler returned is not.

**Code:** when the runner returns and the cause of its context is a shutdown, nothing it returned is kept, and
the handler returns an error that the queue is to retry (`errHandedBack`). The item is `failed` with a delay
of `RetryDelay` (1 s), the queue puts it back to queued on its own, and the new service claims it: the attempt
is `running`, so it is handed over again with one more delivery, with no wait of the layer's and no transient
failure. A crash leaves the item `running`, and the queue's own recovery does the same. The queue's grace at
shutdown is 200 ms, because the layer waits for the runners itself (30 s on its clock) before it stops the
queue.

**Test:** TS-12-46, TS-12-47 and TS-12-48 (`recovery_test.go`).

## 9. What a cancel does that the spec leaves open (task 7)

The spec says a cancel makes a queued attempt "cancelled" and a running one cancelled "when the runner returns"
(R6). Three things it does not say are decided as follows.

- **An attempt that never ran has no envelope and no `attempt_end`.** A queued attempt, one in its backoff
  included, becomes `cancelled` with its `finished_at`, and the job's `job_state` for the cancel is the only
  event. `attempt_start` was never emitted for it, and `attempt_end` closes a start. A job cancelled while it is
  queued therefore has `result` null. (`internal/service/jobs/cancel.go`, `CancelJob`; TS-12-50, TS-12-43.)
- **A runner is not called for a job that is cancelled.** The attempt is made known to the layer before its
  request is read, and the read checks the job: a cancel that lands between the claim and the call is seen
  there, the runner is not called, and the attempt is recorded cancelled with an envelope of the category
  `cancelled` (`queue.go`, `invoke`; `TestACancelBeforeTheRunnerIsCalledStopsTheAttemptWithoutCallingIt`).
- **An attempt left `running` under a cancelled job is closed when its item comes up.** A crash between a cancel
  and the return of the runner leaves it so, and nothing else would ever end it; it would stay in the way of the
  purge. The handler that finds it records it cancelled, with an envelope of the category `cancelled` and without
  calling a runner (`store.go`, `Plan`, `dispReap`; `TestAnAttemptLeftRunningUnderACancelledJobIsClosedAfterARestart`).

The category `cancelled` is also what a runner gets that returns an error, a transient failure or something that
is not an envelope after a cancel, and what an attempt gets when the grace runs out (TS-12-42, TS-12-51).
A DELETE whose answer or retry half is TS-12-56's comes with task 8.

## 10. "Exactly one of two racing actions" holds for actions taken on the same state (task 8)

**Spec:** 12-REQ-8.6 and TS-12-55: for any two actions racing on one job, exactly one is applied through a
conditional state transition, and the other answers 409 `invalid_state`.

**What can be true:** two different actions that arrive one after the other are both valid in that order. On a
blocked job an answer queues it and a cancel of the queued job is valid; a retry of a failed job that fails again at
once can be retried again. Which of the two requests of a test comes first is not the test's to say, so that the
outcome "exactly one" is not a property of requests that arrive close together. It is a property of requests that
were taken on the same state of the job, which is what a conditional transition gives.

**Code:** a cancel, an answer and a retry each look at the state of the job and change it only if it is still that
state (`Store.CancelJob(ctx, id, from)`, `Store.Requeue(ctx, id, from, …)`); the second of two actions that saw
the same state finds it gone and is a `*StateError`, which the routes answer 409 `invalid_state`. A cancel does
not apply to any state that a cancel could start from, but to the state it saw (`internal/service/jobs/cancel.go`,
`actions.go`). A lost change is not tried again: the person is told, and may ask again.

**Test:** `TestOfTwoActionsTakenOnTheSameStateExactlyOneIsApplied_TS_12_55` (`finish_internal_test.go`) holds both
actions, with the seam `OpObserved`, after each has seen the state and lets them go together, for every pair that
the state allows, mixed ones included: exactly one is applied every time. `TestForAnyTwoActionsRacing…_TS_12_55`
(`actions_test.go`) fires the pairs over HTTP, and accepts for a pair of two different actions the outcome "both
applied" only as an answer and then a cancel, with the job ending cancelled.

## 11. A retry that would make a second open job for an issue is 409 duplicate_job (task 8)

**Spec:** 12-REQ-3.5 refuses a second open job of a kind for an issue at creation. A retry of a failed job puts
the job open again, and the spec does not say what happens if another open job of the kind has taken the issue
since.

**Code:** the partial unique index of migration 2 refuses it, and the retry is 409 `duplicate_job` naming the job
that holds the issue (`actions.go`, `requeue`), as a creation would be. The reason is the one of Design Decision
14: two fix attempts on one issue would collide on its branch.

## 12. An answer with no body at all is 400, not 415 (task 8)

An answer without a body has no content type either, and the check of the content type would have made it 415.
It is the body that is missing, so it is a 400 `invalid_request` (`readBodyOrNothing`, `internal/service/api/server.go`);
a body that is there but is not `application/json` is still 415.

## 13. Jobs with one `created_at` are ordered by `id`, descending (task 9)

**Spec:** a list is "newest first by `created_at` then `id`" (12-REQ-9.4), and TS-12-60 says "ordered by
created_at descending then id". Neither says which way the `id` runs.

**Code:** both run down: `ORDER BY created_at DESC, id DESC` (`internal/service/jobs/list.go`, `Store.List`). It is the
order of the index `af_jobs_by_created (created_at, id)` read backwards, and the cursor is the position of the
last job of the page, `(created_at, id)`, compared with `<`. A job id is random, so the direction says nothing
about which job is newer; what matters is that it is the same on every page.

**Test:** `TestFollowingNextCursorYieldsEveryFilteredJobOnceNewestFirst_TS_12_60` seeds 150 jobs on ten distinct
times, so that the ties are the rule, and compares the pages with that order.

## 14. A list parameter that is there is checked whatever its value, and the workspaces a list asks hub about are those with a matching job (task 9)

**Spec:** "A bad value answers 400" (R7); `GET /jobs` answers 502 when "hub is unavailable for any workspace
GET /api/v1/jobs needs to check" (12-REQ-9.2).

**Code:**

- A parameter that is present is checked even when it is empty (`kind=`, `limit=`, `cursor=`), a repeated one is
  refused (`kind=fix&kind=triage`), and a number is written as a person writes one (`007`, `+5`, `1.0` are
  not). A parameter that the list does not have is left alone (`ParseList`, `list.go`). The first that is wrong, in the
  order kind, state, issue, limit, cursor, is the one named.
- A cursor is the one the service gave: base64 without padding of `{created_at, id}`, with nothing else in it, and a
  spelling that decodes to the same bytes but is not the one given is refused (`parsePosition`).
- `GET /jobs` asks hub about the workspaces that have a job which the kind, state and issue filters match, and not about
  the others (`Store.Workspaces`). A workspace that the filter leaves no job on is not "needed", so a hub
  that is down for it does not fail the list. It asks about the same workspaces for every page of one list.
- A caller's token is checked first (401), then the parameters (400), then hub (502); on a route of one workspace the
  guard comes before the parameters, so that a workspace the caller cannot see is a 404 whatever the query says.

**Test:** `TestInvalidParametersOfAListAnswer400NamingTheParameter_TS_12_61` (the cases of the spec and their
neighbours, on both routes) and `TestListingJobsAnswers502WithNoPartialPage…_TS_12_58`.

## 15. A file of an attempt is served only when the attempt's row lists it (task 9)

**Spec:** the envelope and report routes answer 404 for "an attempt that has no envelope yet" and "an attempt with
no report" (12-REQ-9.7).

**Code:** the row of the attempt says whether it has an envelope and a report (`has_envelope`, `has_report`), and the file
is read only when it does (`internal/service/jobs/read.go`). A runner that has begun to write `envelope.json` into the
directory of a running attempt does not make it served; a file that the row lists and that is not on disk is the
same 404, with a WARN that names the job, the attempt and the file.

**Test:** `TestAFileOnDiskThatTheStoreDoesNotListIsNotServed_TS_12_63`.
