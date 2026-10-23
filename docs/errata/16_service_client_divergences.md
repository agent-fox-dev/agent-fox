# Erratum 16: where the service client does something other than the spec says

**Spec:** `16_af_service_client`. Each divergence below was decided during the survey or on the way, and is
recorded here because the spec stays as it was written. The code and the test of each are named so that a reader
can check what is delivered. Where a commit declares the divergence, its `Deviation:` trailer key is named.

## 1. The loops are in `internal/afcmd`, and the mode hook is a field of the shell (tasks 7, 8, 9)

**Spec:** "Paths under `cmd/af` in this PRD are as they stand before that move, and name the code by its role"
(Design Decision 42), and step 7.2: "a Command may declare a Mode selector consulted in Main between match and parse".

**Code:** `13_agent_fox_worker` had moved the loops to `internal/afcmd`, which `afcmd.New` registers and the worker
runs in process. The client commands are in `cmd/af`, as the spec says, and `newApp()` registers them on the shell
that `afcmd.New` returns. The loops are not the client's to change, so the hook is a field of the shell,
`toolio.App.Mode`, that the binary sets after `afcmd.New`; a caller of `afcmd.New` has none and needs no `--local`
(`modeHook`, `cmd/af/mode.go`). The service mode of each command is a `toolio.Command` that is not registered
(`queueCommand`, `cmd/af/serve_queue.go`), so that the hook can return it. The hook returns the words to parse, with
`--local` taken out, while `Run.Argv` stays as it was received, so that the resume of a local run keeps `--local`.
`toolio` also gained `App.HelpExtra` and `App.HelpFor` (the help of both modes), `App.Command` (the schema of the loops
becomes a `oneOf` of the local result and the job result), and `SplitFlag` and `Command.FlagLookup` (the scan reads words as the
flag parse does). No existing test needed `--local` (step 9.2): the loop tests are in `internal/afcmd` and use
`afcmd.New`, and no test of `cmd/af` runs a loop.
Deviation keys: `mode_hook_on_the_app`.
Tests: `TestTheModeHookChoosesTheCommandAndTheWordsBeforeAnythingIsBuilt_TS_16_1` (`internal/toolio/mode_test.go`),
`TestAnInProcessInvocationOfTheLoopsWithoutLocalStillRunsAndNeverContactsAService_TS_16_14` (`cmd/af/client_compat_test.go`).

## 2. The warning codes of the client are not in `toolio.WarningCodes` (task 1)

**Spec:** step 1.5 adds `server_insecure`, `credential_file_permissions` and `attempt_envelope_unavailable` to
`WarningCodes`.

**Code:** `WarningCodes` lists the codes that the shell itself raises, and `TestShellWarningCodes_TS_05_23` asserts that it
equals them; the codes of other specs (`issue_closed`, `box_unconfined`, …) are not in it either. The three codes are
constants of `cmd/af` (`client_common.go`, `client_follow.go`) and are documented in `docs/cli.md`. The four categories are
in `toolio/failure.go`, as the categories of earlier specs are. Deviation key: `warning_codes_in_cmd_af`.

## 3. Two shell helpers were added in the tasks that use them, and one the spec does not name (tasks 5, 7, 9)

**Spec:** step 1.5 adds the input setter and the resume-argv override to `toolio` in task 1.

**Code:** `Run.SetInput` came with task 7 and `Question.ResumeArgv` with task 5, where their tests are
(`TestARunMaySetTheInputItReportsInTheEnvelope_TS_16_49`, `TestAQuestionMayNameTheCommandThatAnswersIt_TS_16_72`). Task 9 added
`Run.Redact`: a command registers its secrets, and the shell takes them out of the envelope on stdout and its copies and out of
every event, whatever the service repeated in a document the command carries
(`TestAValueARunRedactsIsInNoOutputOfTheInvocation_TS_16_27`). Without it 16-REQ-4.6 would hold only for the words that `af` builds
itself, since the service's documents are carried as received. Deviation key: `toolio_setters_in_their_tasks`.

## 4. The golden of `af --schema` is the binary's, and the shell's own test checks it differently (task 1)

**Spec:** the golden is rewritten once, in the last task.

**Code:** each task that registered a result schema regenerated `cmd/af/testdata/schema.golden.json`
(`go test ./cmd/af -run TestSchemaGolden -update`), so that its own gate stayed green; `TestSchemaGolden_TS_05_70` still compares
it byte for byte. `TestSchemasMatchTheirTypes_TS_06_72` of `internal/afcmd` compared the golden with the `--schema` of the
`afcmd` shell, which no longer has the client commands, so it now checks that the golden holds the envelope schema and every
`$defs` entry of that shell (`goldenCovers`). `127.0.0.2`, a loopback address that `TS-16-21` names, is in the loopback hosts of
`TestTestsStayOffTheNetwork_TS_05_73` and `TestTestsAreOffline_TS_06_75`. Deviation key: `af_golden_covers_afcmd`.

## 5. A followed job's status is read from the job's result, and a failed ending has no result (task 5)

**Spec:** R7 and step 5.3 take `result.status` and `result.error` "of the envelope" and give the result `{job, envelope}` "once the
job is at rest".

**Code:** the `result` the table speaks of is the `result` of the job document, which the service fills from the latest finished
attempt (`internal/service/jobs/document.go`: status, exit code, summary, error). The follower reads it from the read of the job that
found the job at rest, and fetches the attempt's envelope for the result and its warnings (`jobOutcome`, `cmd/af/client_follow.go`).
The shell clears the result of a failed or usage envelope, so a followed job that failed, was cancelled or ended `usage` has the
error and no result; the job is in `artifacts` and `af jobs get` shows it. Deviation keys: `status_from_the_job_result`,
`failure_has_no_result`. Tests: `TS_16_69` to `TS_16_74` (`cmd/af/client_outcome_test.go`).

## 6. Details of the request layer that the spec leaves open (tasks 2, 3, 4, 6)

- A 409 `not_subscribed` or `token_unreadable` on a job route has no slug in its path, so its hint is
  `af workspace subscribe <slug> --hub-token-file FILE` with the placeholder; on a workspace route the slug is the real one
  (`answerError`, `internal/afclient/errors.go`; `TestAFailureCarriesTheFixHintOfItsAnswer_TS_16_43`). Key:
  `hint_slug_placeholder_on_job_routes`.
- A first `af login` writes `token = "<credential>"` and one newline. Key: `single_line_ends_with_newline`.
- A refused connection is worded "nothing is listening there", so that the message of a check that could not be made never says
  "refused" (`transportError`; `TS_16_34`). Key: `connection_refused_wording`.
- A request for the event stream that fails with a 5xx or a transport error is a failed try that reads no job, since a try is a
  request for the stream and, once the stream ends, one read of the job (`Follow`, `internal/afclient/follow.go`). Key:
  `failed_events_request_reads_no_job`.
- The `next` of `af jobs list` has the cursor as its last two words, with `--server` before them (`TS_16_56`). Key:
  `list_next_puts_cursor_last`.
- A credential shorter than 8 bytes is not taken out of messages and outputs, since it would be in ordinary words.

## 7. Tests that name a command of a later task (tasks 2, 4, 5)

`TS-16-40` to `TS-16-44` name `af workspace get`, `af jobs get` and `af jobs list`, `TS-16-63` names `af fix --wait` and
`TS-16-75` runs through `af fix --wait`; those commands came later. The tests ran through `af workspace list` and the library at first,
and each new command carries its own (`TS_16_55`, `TS_16_56`, `TS_16_57`, the `--wait` of `TS_16_63`, `TS_16_75` for both `watch` and
`fix --wait`). Keys: `task_2_tests_through_workspace_list`, `task_4_tests_through_watch`, `ts_16_75_through_watch_in_task_5`.
Several tests passed on their first run because an earlier task had already delivered the behavior (`TS_16_28`, `TS_16_38`,
`TS_16_40`, `TS_16_44`, `TS_16_3`, `TS_16_6`, `TS_16_11`, `TS_16_14`, `TS_16_75` for `fix --wait`); the mutation that removes the refusal of
redirects makes `TS_16_28` and `TS_16_40` fail.

The end-to-end tests of the integration task (`TS_16_76` to `TS_16_78`) start the service of specs 11 and 12 in process from its own packages
(`api.NewServer`, `jobs.New`, `store.Open`, `fakehub`, `fakerunner`) in `cmd/af/smoke_client_test.go`; step 10.1's helper in
those packages was not needed. All six smoke tests passed on their first run, as that task expects.

## 8. No test of the content of the documents (task 9)

**Spec:** 16-REQ-2.8 and `TS-16-16`: "all checked by a docs test".

**Code:** `.specs/steering.md`, changed after the spec was written, says: "Do NOT write tests for testing the presence or the
content of the docs or PRDs." The documents are written (`docs/cli.md`, `docs/configuration.md`, `docs/model-usage.md`,
`docs/architecture.md`, `docs/development.md`, `README.md`) and the phrases that the docs tests of earlier specs check are all still
there, but `TS-16-16` is not written. This is an open item of the pull request.

## 9. A transitional check of spec 13 is gone (task 9)

**Spec:** 16-REQ-2.8 and task 9 update `docs/cli.md`, and "no phrase an existing docs test checks is removed".

**Code:** `TestTheDocumentationDescribesTheWorker_TS_13_81` (`internal/worker/docs_test.go`) also compared `docs/cli.md` with
`main:docs/cli.md`, to show that the worker spec had not changed it, with an exemption for the one change that spec 14 made. Any
branch that edits `docs/cli.md` fails it until it is merged, and spec 16 edits many lines of it, so that exemption cannot be
extended. That half of the test and its helper are removed; every phrase it checks in the five other documents is still checked and
still there. Nothing else was weakened. Deviation key: `worker_docs_test_main_comparison`.

## 10. The gate

The spec's `test_commands` name `make test` and `make lint`. The steering file and `CLAUDE.md` allow only `make check` while
implementing; every task was measured with it, and `make check-release` is left to the person who merges.
