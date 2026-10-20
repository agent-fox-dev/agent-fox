# Erratum 08: where `af triage` does something other than the spec says

**Spec:** `08_af_triage`. Each divergence below was decided during the survey (the
`D-` numbers of the survey) or on the way, and is recorded here because the spec
stays as it was written. The code and the test of each are named so that a reader
can check what is delivered. Where a commit declares the divergence, its
`Deviation:` trailer is named.

## 1. `af triage run` runs over the directory in a session of its own (task 7)

**Spec:** R4 and 08-REQ-4.7: `af triage run` runs the brain in this process over
`--dir` "without a box".

**Code:** `brain.Execute` takes a `*box.Session`, which is how it places the brief
and reads the submission back. `box.Here` (`internal/box/here.go:27`) is a session
over a directory in place: its tools work on the directory itself, `Sync` is an
error (there is nothing to copy), and it does no scrub of git's state. `triageRun`
(`cmd/af/triage.go:309`) opens it with the host directory and runs the brain on it.
That is the one file of `internal/box` that the spec's `touches` do not name.

**Test:** `TestRunExecutesTheAgentkitBrainInProcessOverDir_TS_08_30`
(`cmd/af/triage_run_test.go`), `internal/box/here_test.go`.

## 2. The refused calls are noted by a hook of the submission kind (task 7)

**Spec:** R6: each refused submit call appends a line to `rejections.jsonl`, "for
both the CLI and the in-process submit". Spec 07's submit core had no place for it.

**Code:** `submission.Kind.OnRefused` (`internal/submission/kind.go:50`) is called
by `Submit` after a refusal (`refuseAndNote`, `internal/submission/submit.go:201`;
a panic of the hook is recovered, and the refusal stands). The triage kind's hook
(`noteRefusal`, `internal/triage/kind.go`) appends the line through the target the
submission was made with, so it lands beside `submission.json` wherever that is,
in a box or in the run directory.

**Test:** `TS-08-39` and `TS-08-37` (`cmd/af/triage_submit_test.go`),
`internal/submission/options_test.go`.

## 3. A submit command can make its kind from `--dir` (task 7)

**Spec:** 08-REQ-4.4 and R5: `af triage submit` validates "against the file view of
the tree they run on" (`--dir`). `submission.Command(path, kind)` takes a kind that
is made before `--dir` is known.

**Code:** `submission.CommandWith(path, kind, Options{Kind, Prepare, Result,
ResultSchema})` (`internal/submission/command.go:67`) takes a function from `--dir`
to the kind, a step before the submit (`placeRunJSON`, `cmd/af/triage.go:229`, which
places the `run.json` that the protocol reads the attempt and the nonce from when a
person drives the steps at their own checkout and no loop placed one) and a
function that makes the result (`submitResult`, which records `triage.submission`
and counts the refusals). `Command` is `CommandWith` with no options.

**Test:** `TS-08-38` (`cmd/af/triage_submit_test.go`),
`internal/submission/options_test.go`.

## 4. The loop is in `cmd/af`, and the allowlist is with the kind (tasks 7 and 9)

**Spec:** task 9 puts `Run(ctx, params)` in `internal/triage/loop.go` and the
allowlist in `internal/triage/allowlist.go`; the survey says the triage package
needs no shell.

**Code:** the loop needs the shell's run directory and ledger, the forge client,
`box.With` and `brain.Execute`, none of which `internal/triage` imports itself.
`triageLoop` (`cmd/af/triage.go:722`) is the loop, with `loopPreflight` (770),
`runInBox` (817) and `staleSubmission` (859); `internal/triage/loop.go` holds what
needs none of them: `RejectionsPath`, `ReadBackRejections` and `StaleReason`. The
allowlist (`Allowlist`, `BoxTools`, `internal/triage/kind.go:134`) is in the file
of the kind, because the kind's tool and the tools the box offers are one decision.
`TS-08-26` was written with task 7. Declared in the `Deviation:` trailer of task 9
(`loop-location`).

**Test:** `TS-08-1`, `4`, `5`, `8`, `24`, `25`, `28`, `29` (`cmd/af/triage_loop_test.go`),
`internal/triage/loop_test.go`, `TS-08-26` (`internal/triage/kind_test.go`).

## 5. A credential that the forge refuses is `auth`, also at the repository check (task 4)

**Spec:** 08-REQ-2.7: `IsNoToken` is `auth`, not found is `forge` and not
retryable, a rate limit or a transport error is `forge` and retryable.

**Code:** a 401 or a 403 that is not a rate limit is neither of those. `isUnauthorized`
(`internal/triage/preflight.go:171`) makes it `auth`, with the token variable of the
forge as the hint, since a token that is wrong is the same trouble for the person as
one that is missing. The retry of every other failure of a 4xx is `false`.

**Test:** `TS-08-14` (`internal/triage/preflight_test.go`).

## 6. The resume argv of a question in `af triage land` names `land` (tasks 8 and 10)

**Spec:** R9 and 08-REQ-9.1: `af triage land` ends `needs_human` with "the shell's
resume argv", which with `--context <answer>` "re-runs the loop".

**Code:** the shell derives `resume.argv` from the invocation, so for `land` it is
`af triage land --run-id … --dir …`, and `land` takes no `--context`. `landOutcome`
(`cmd/af/triage.go:618`) returns the question and the options as the spec asks; to
answer, a person runs `af triage <input> --context <answer>` (or `af triage brief`).
The loop's own resume argv is the loop, and does re-run it. Not changed: the shell
would need to let a command give its own resume argv.

**Test:** `TestAQuestionStopsTheRunAndWritesNothing_TS_08_57` (`cmd/af/triage_outcome_test.go`).

## 7. TS-08-59 asks for a scenario that 08-REQ-2.2 refuses (task 10)

**Spec:** TS-08-59 has "a `--land none` run with duplicate labels `a`, `b`, `a`";
08-REQ-2.2 refuses `--label` with `--land none`.

**Code:** the test dedupes the labels on a landing (`["a", "b"]` from `a, b, a`) and
checks the result of a `--land none` run separately, with no labels. Declared in the
`Deviation:` trailer of task 10 (`ts-08-59-land-none-labels`).

**Test:** `TestTheResultsValidateAgainstTheDeclaredSchemas_TS_08_59`
(`cmd/af/triage_outcome_test.go`).

## 8. `af fix` is suggested for any issue in the result (task 10)

**Spec:** 08-REQ-9.5: after a `done` for an issue whose diagnosis is `confirmed` or
`probable`, `next` has `af fix <issue url> --dir <dir>`.

**Code:** `FixSuggested` (`internal/triage/result.go:5`) reads "an issue" as the
`issue` of the result: the issue that was written to, that was created from a text
or a file, or that a dry run would have written to. `--land none` has none, and a
question is not `done`. `af triage land` suggests it too, since 08-REQ-9.5 names both
commands; the suggestion to land again (08-REQ-9.6) is the loop's only.

**Test:** `TestAFixIsSuggestedAfterAConfirmedOrProbableDiagnosis_TS_08_61`,
`TestAFailedLandingSuggestsLandingAgain_TS_08_62` (`cmd/af/triage_outcome_test.go`).

## 9. `--run-id`, and not `--run` (survey)

**Spec:** the PRD writes `--run <id>`; 08-REQ-1.1, 1.2 and the tests write `--run-id`,
as `submission.Command` does.

**Code:** `--run-id` everywhere.

## 10. What else the spec's `touches` do not name

`internal/toolio` gained `Run.ForgeFor` (`internal/toolio/forge_for.go:21`) for 08-REQ-1.6,
with the `app` and `closers` fields it needs on `Run` and a call that closes the
clients when a command finishes, and `toolio.TruncationMarker`
(`internal/toolio/input.go:27`), exported so that the brief ends a cut report the way
the shell ends a cut input. `Makefile` loops `install-skills` and `uninstall-skills`
over `skills/af-*` (08-REQ-10.3), since `skills/` now holds Go files as well.
`docs/doc.go` makes `docs` a package of its own, so that `TS-08-70` has somewhere to
live and `make doc` has something to document.
