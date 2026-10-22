# Erratum 13: where the worker does something other than the spec says

**Spec:** `13_agent_fox_worker`. Each divergence below was decided during the survey or on the way, and is
recorded here because the spec stays as it was written. The code and the test of each are named so that a
reader can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is
named.

## 1. The tests that run the binary share their helpers with the moved tests by copy (task 1)

**Spec:** "Unit tests move with the code. Tests that build and run the binary stay in `cmd/af`" (R1).

**Code:** `smoke_test.go`, `schema_test.go` and the three signal and hook tests of the ring-1 commands
(`cmd/af/ring1_binary_test.go`) stay in `cmd/af`. They use about twenty helpers that the moved tests define
(`gitRepo`, `field`, `writeGate`, …), and two packages cannot share the helpers of their tests, so
`cmd/af/helpers_test.go` holds copies of them. Deviation key: `binary_tests_helpers_copied`.

## 2. `gateway.Dial` takes a context and options, and `gatewayd` is a gRPC server in a module of its own (task 2)

**Spec:** "Assumed: `gateway.Dial(url, token string) (sandbox.Provider, error)`" and "a `gatewayd.Server` that is
an `http.Handler`… served from `httptest.NewServer`" (Verified External API).

**Code:** `gateway.Dial(ctx, rawURL, gateway.Options{Token})` returns a `*gateway.Provider`
(`../agentkit-go/sandbox/gateway/dial.go:36`), and a plaintext `http://` URL is allowed only for a loopback
host. `gatewayd.New(gatewayd.Config{…})` makes a `*gatewayd.Server` that serves gRPC on a `net.Listener`
(`gatewayd/server.go:16`), and it is a separate Go module, so `go.mod` gains a `require` and a `replace` for
`../agentkit-go/sandbox/gatewayd`, as `../hub/go.mod` has them. The fake hub serves it on a loopback TCP port
(`internal/hub/fakehub/gateway.go`), in callback mode, where the token of a lease that is held and has time
left is the one that opens it. Test: `TestFakehubServes…_TS_13_80` (`internal/hub/fakehub/worker_test.go`).
Deviation key: `gatewayd_is_grpc`.

## 3. Hub's real API is not what the spec assumed for five of its calls (task 2)

**Spec:** "Hub's HTTP API. Every shape is **unverified**" (Verified External API).

**Code:** hub's OpenAPI document is at `../hub/docs/openapi.yaml`, and `internal/hub/calls.go` follows it where
it differs from the spec's assumption:

| Call | The spec assumed | Hub's |
|---|---|---|
| resolved variables | `{"vars": {NAME: value}}`, or a list of `{name, value}` | a list of `{key, value, origin}` (all three encodings are read) |
| the mode of a workspace | `mode` | `workspace_mode` (`mode` is read too) |
| post an events batch | `{events: [{seq, ts, type, data}]}` | a bare array of `{id, run_id, event_type, timestamp, payload}`, at most 1000, `payload` an object; the id is the run and the number of the event, so a batch posted twice adds nothing |
| post usage | `{model, input_tokens, output_tokens, cost_usd, turns}` | `{model, input_tokens, output_tokens, cache_read_tokens}`; the client sends the cost, the cost source, the brain and the turns beside them, which hub ignores |
| complete a session | the status of the envelope | `completed`, `failed` or `timeout` (`hub.SessionCompleted`, …); the worker maps the status of the envelope onto them |

The branch push route takes the branch as one path segment with each `/` escaped (`fix%2F42-nil-map`), which
`url.PathEscape` gives. The fake hub serves these shapes (`internal/hub/fakehub`), so that the client is tested
against what hub does. Tests: `TestEveryWorkerCallIs…_TS_13_11`, `TestBothEncodingsOf…_TS_13_19` and
`TestTheRecordTheVariablesAndTheBodiesOfTheCalls` (`internal/hub/calls_test.go`). Deviation key:
`hub_shapes_from_openapi`.

## 4. The test that the worker's calls leave the caches alone is in `internal/service/api` (task 2)

**Spec:** TS-13-12 is "a hub client with warm identity and visibility caches".

**Code:** the hub client has no cache. The two caches are the fields of `auth` in `internal/service/api`, which
nothing outside that package can read, so `TestTheWorkersCallsDoNotTouchTheIdentityOrVisibilityCaches_TS_13_12`
(`internal/service/api/workercalls_test.go`) is there.

## 5. The bounds are durations of the client, and the tests scale them (task 2)

**Spec:** "a hub client with an injectable clock" (TS-13-13), "with a fake clock" (TS-13-15).

**Code:** the bound of an ordinary call is `Client.HTTP.Timeout` (10 s, as the core's test already asserts) and
the bound of the lease and the push of a branch is `Client.LongTimeout` (5 minutes when zero). A test sets them
to tens of milliseconds and delays the fake hub's route for longer, which is the same experiment without a clock
to inject; the defaults are asserted as they are (`TestARequestIsBoundedTo…_TS_13_13`,
`TestTheLeaseAndTheBranchPushUseTheLongBound_TS_13_15`).

## 6. The commands a gateway box runs are recorded where its tools are called (task 4)

**Spec:** "the gateway's backend records every spec it creates and every command it executes"
(R10, TS-13-49, TS-13-50, TS-13-54).

**Code:** `gatewayd` relays the agent protocol to the box it holds without looking into it
(`gatewayd/relay.go`), so a backend that wraps the sandboxes it creates sees the specs and the
closes, and no command. The fake hub's backend records those
(`fakehub.Gateway.Created`, `Closed`), and the commands are recorded by wrapping the provider that
dials the gateway, on the side of the client, where the execute tool of the box is called
(`fakehub.Gateway.Instrument`, `Gateway.Commands`), in the same order as the gateway's own events.
A test of the worker that needs the commands installs that wrapper in the registry it gives the
attempt. Deviation key: `commands_recorded_at_the_tool`.

## 7. The box measures through `Config.InBox`, and the profile is named by it (task 4)

**Spec:** "In this mode the fix pipeline opens the box before its own preflight's detection" (R7).

**Code:** the mode is `afcmd.Config.InBox` (`InBox{Profile}`), which the worker sets and `af` never
does. With it `afcmd.fixLoop` opens the box first (`internal/afcmd/fix.go`, `fixAttempt`), then runs
the preflight, the brief and the loop in it, with the detection, the gate and the revert check of
`fix.BoxMeasurer` (`internal/fix/boxmeasure.go`) in place of the host's. `fix.PreflightParams.Detect`
is the new seam of the detection; `Gate` and `LoopParams.Conform` were there. The host's seams are
still `Config.Gate` and `Config.Conform`, which the tests give counters. Tests:
`TestDetectionBaselineGateAndRevertCheckRunInTheBoxAndNeverOnTheHost_TS_13_49` and the others of
`internal/afcmd/inbox_test.go`.

## 8. A lost box is retryable only for a box that says it is remote (task 4)

**Spec:** "A ring-1 command that cannot run because the box or the gateway was lost ends the loop
with a `sandbox` failure marked retryable" (R7).

**Code:** `box.Entry.Remote` (set for the gateway) makes `Session.Exec` return its failures with
`Retryable` true (`internal/box/exec.go`); under `podman` a box that cannot run a command is still a
failure that is not retryable, as the tests of spec 10 have it. `gateError` and the revert check of
`fix.Measure` return a `*toolio.Failure` of the measuring as it is, which they wrapped as `io`
before. Test: `TestABoxLostDuringMeasuringEndsTheLoopWithARetryableSandboxFailure_TS_13_56`, which
loses the gateway during the probe, the detection, the gate and the revert check.

## 9. The worker half of TS-13-56 waits for the worker (task 4)

**Spec:** TS-13-56 "the worker returns an error carrying `*toolio.Failure` … and no envelope".

**Code:** task 4 delivers the loop's side: the envelope is `failed`, category `sandbox`, retryable.
The worker that turns it into a transient error is task 8 (TS-13-58).

## 10. A refused clone is recognised by what git prints (task 6)

**Spec:** "hub's git server refuses the clone's credential with 401 or 403 … `auth`; any other reason …
transient" (R5.5, R5.6).

**Code:** `git clone` does not give its caller the status of the answer, so `refusedCredential`
(`internal/worker/clone.go`) reads what git printed: `The requested URL returned error: 401` or `403`, and
`Authentication failed`, `could not read Username` and `terminal prompts disabled`, which is what git says
when a server asks for a credential that it may not give (prompts are off). The tests run the real git
against the fake hub's git server with 401 and 403 injected (`TestACloneRefusedWith401Or403…_TS_13_37`) and
with 500, 503, a hang-up, a delay past the bound and a missing base branch
(`TestAnyOtherCloneFailureIsATransientGitErrorWithNoEnvelope_TS_13_38`), so a git whose words differ fails
them. The mirror of the failure to the issue, which TS-13-37 also asks for, is task 9's.

## 11. The PAT is in the environment of the commands that reach hub, and of no other (task 6)

**Spec:** "run every git command that carries the PAT or can reach a remote (the clone, every git command of
the fix pipeline and the push) under a hermetic runner" (R5.4), and "pass the PAT to git only as the header …
through the command's environment" (R5.3).

**Code:** every git command of the attempt runs hermetically, as the spec says, but only `clone`, `fetch`,
`pull`, `push` and `ls-remote` are given the header (`gitRunner`, `reachesRemote` in
`internal/worker/gitrunner.go`). A commit runs the hooks of the repository, which is the stranger's code that
the fix is about; giving them an environment with the PAT in it is a leak that no requirement asks for. The
test records the environment of every command (`TestThePATReachesGit…_TS_13_35`). Deviation key:
`pat_only_where_git_reaches_hub`.

## 12. The envelope's `next` for a branch that hub did not publish is `git show --stat <branch>` (task 6)

**Spec:** "the envelope's `next` names the branch" (R8.6).

**Code:** the landing's `next` for a push that failed is `git push -u origin <branch>`. For a branch that is
pushed and that hub did not publish that command would do nothing, so `landing.pushFailure`
(`internal/fix/land.go`) names the branch in `git show --stat <branch>` instead, with the reason, and the
failure says the branch "is pushed, and could not be published". A parked change says the same in the
warning `push_failed`. Tests: `TestAPostPushHookThatFailsEndsTheLandingFailedWithTheBranchKept_TS_13_63`
(`internal/fix`), `TestTheBranchIsPublishedThroughHub…_TS_13_63` (`internal/worker`) and the run through the
command (`internal/afcmd`).

## 13. A lease with a second to spare is not enough (task 7)

**Spec:** "the lease has less than 2 minutes left before `expires_at`" (R6.3).

**Code:** the timeout of the loop is `expires_at − now − 2m` cut to a whole number of seconds, and a
lease whose cut would be under a second is refused as having too little time, since a timeout of 0
means no timeout (`Runner.lease`, `internal/worker/lease.go`). Test:
`TestOtherLeaseOrBoxProblemsAreTransientSandboxErrors_TS_13_43` (a lease of 90 seconds; the gateway
that is gone and the gateway that refuses the box are the real loop's, which ends failed, `sandbox` and
retryable, and so a transient error). Deviation key: `lease_too_short_below_a_second_of_cut`.

## 14. The warnings of the worker are added to the encoded envelope (task 7)

**Spec:** "the envelope's warnings include `lease_shortened_timeout`" and "`lease_release_failed`"
(R6.6, R6.8).

**Code:** the envelope is written by the in-process command before the box is closed and the lease
released, so `lease_release_failed` cannot be known when it is made. `Runner.finish` adds both warnings
to the encoded envelope with `toolio.AddWarnings`, which changes nothing else in the document, not the
order of its fields and not its result (`internal/toolio/envelope.go`; test
`TestAddWarningsChangesNothingButTheWarnings`). The status of the envelope is never touched.

## 15. The tests of the order, the stop points and the events are completed by the tasks that add the steps (task 8)

**Spec:** TS-13-57 (the order of the requests, with the label and the comment, the usage, the batch of
events and the completion), TS-13-60 (the stop points, with `in-progress` removed, the events posted and the
session completed `failed`) and TS-13-64 (the step events for `mirror` and `report`).

**Code:** the steps that write the mark, the mirror and the report to hub are tasks 9 and 10, and their
steps do not exist in task 8. The tests of task 8 assert the order of the steps that exist (the reads, the
session, the clone, the lease, the requests of the loop, the push, the publication, the pull request and the
release), the stop points as far as they go (no forge write, no lease and no report for a refusal at resolve
and a session that is not opened; a granted lease released for the rest), and the step events from `resolve`
to `release`. Tasks 9 and 10 add the rest to the same tests. Deviation keys:
`order_and_stop_points_completed_by_tasks_9_and_10`, `step_events_completed_by_tasks_9_and_10`.

## 16. "The jobs layer's fake retry" is a second delivery of the same request (task 8)

**Spec:** TS-13-58 "with the jobs layer's fake retry the same attempt is handed over again".

**Code:** the jobs layer's service needs a store, a clock and an HTTP mux, and its tests are in another
package, so the test delivers the same `jobs.Request` twice with `Delivery` 2 the second time, after the
cause of the transient error is gone, and checks that the second delivery runs from the start: a clone that
the first kept is removed and made afresh, and a new lease is taken
(`TestInfrastructureFailuresReturn…_TS_13_58`). What the layer does with a transient error is
`jobs.IsTransient`, which the test checks on the error. Deviation key: `fake_retry_is_a_second_delivery`.

## 17. A cancelled attempt returns a result, a shut down one its cause (tasks 8 and 9)

**Spec:** "a result carrying the loop's envelope … whose labels show the request label set and in-progress
removed" (R9.7), and "the jobs layer discards whatever the worker returns" on a shutdown (R9.8).

**Code:** `loopOutcome` keeps the envelope the loop wrote when it was stopped, with the cause of the stop
beside it. `Runner.finish` (`internal/worker/runner.go`) returns a result and no error for a cancel or an
unsubscribe, with the envelope of the loop or, when the loop had not begun, a failed envelope of category
`cancelled`; and the cause alone for a shutdown. Tests: `TestACancelledRunningAttemptRestores…_TS_13_72` and
`TestAShutdownWritesNothing…_TS_13_73`. `jobs.Request.Round` is the position the guard of 13-REQ-9.4 reads;
spec 13 adds it to what the runner is given, and `jobs.Store.RunRequest` fills it from the attempts of the
job (`TestTheRunnerIsGivenThePositionOfTheAttemptInItsRound`).

## 18. The labels of the result are the net of the attempt, and the mirror writes are side effects (task 9)

**Spec:** "the labels it set and removed, in steps 3 and 7 and in cancel cleanup" (R8.8), "a matching side
effect" for a write that fails (R9.6).

**Code:** a label that the mark set and the mirror took off again is reported as removed and not as set, so
that a landed fix reports `landed` set and `in-progress` removed, and a cancel `af:fix` set and `in-progress`
removed, as the paths of the PRD say. Every write of the mirror to the forge is a side effect of the envelope
(`add_labels`, `remove_label`, `add_comment`), of the state `done` or `failed`. A label that is gone when it is
removed (404) is removed. A label that cannot be set removes none of the others. Deviation keys:
`result_labels_are_net`, `mirror_writes_are_side_effects`.

## 19. The events posted to hub are the attempt's, the loop's and the worker's steps (task 10)

**Spec:** "post the loop's events in sequence order in batches of at most 500" (R10.3), and "events posted" for an
attempt that stopped before the loop (the table of the PRD).

**Code:** an attempt that stopped at the clone, the lease or the box has no event of the loop, and the table
says its events are posted, so the events are the attempt's: every line of the loop and every `step` event of the
worker, in the order they happened (`attempt.keep`, `internal/worker/events.go`), numbered 1, 2, … for the attempt
whatever the loop numbered them, so that the id hub takes each event once by, `<run id>-<number>`, is unique. The
loop's events are in their order, and 1200 events of an attempt arrive as 500, 500 and 200
(`TestAfterTheReleaseOneUsage…_TS_13_76`, whose loop emits 1192 beside the 8 steps of the worker). A batch that hub
does not take ends the posting of events, so that what hub holds is a prefix of them, and is the warning
`hub_events_failed`. Deviation key: `events_are_the_attempts_not_only_the_loops`.

## 20. TS-13-81 compares docs/cli.md with main (task 10)

**Spec:** "docs/cli.md is unchanged".

**Code:** `TestTheDocumentationDescribesTheWorker_TS_13_81` (`internal/worker/docs_test.go`) checks the phrases
that 13-REQ-10.8 names for each document, and that `git diff main -- docs/cli.md` is empty; it is skipped where there
is no git or no branch `main`. `make check` runs the docs tests of the other packages, which hold the other phrases.

