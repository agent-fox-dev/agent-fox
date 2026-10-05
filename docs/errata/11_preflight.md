# Erratum: four places where the code departs from spec 11

Recorded because `.specs/11_preflight_checks` reads as unmet in four places, and
each departure is the more honest behaviour.

## 11-REQ-1.4 and TS-11-4: `--dry-run` skips the forge checks under `--preflight`

**Spec:** `--preflight --dry-run` produces "the identical envelope `--preflight`
alone would produce, since `--preflight` already implies making no remote write".

**Is:** the forge checks are gated on `--dry-run`: `codefix`, `codeimpl` and
`issuetriage` do not require a forge credential, a land target or a remote when
the run would write nothing to a forge. `fix --preflight` with no token and the
default `--land pr` is refused (`preflight`/`auth`, exit 1); `fix --preflight
--dry-run` exits 0, and its checklist lacks `forge_credential`, `land_target` and
`remote_configured`. That answers the question worth asking, "would `fix
--dry-run` start?". `TS-11-4` covers only `--land none`, where the two coincide.

## 11-REQ-5.5: `impl`'s estimate is zero phases when no task is pending

**Spec:** the estimate counts "the pending tasks plus 1 unless `--no-survey`".

**Is:** with no pending task, `impl --preflight` reports `estimate.phases: 0`
(`codeimpl/pipeline.go`): there is nothing to survey or implement, and the formula
would claim a survey phase that never runs. The code is the more honest ceiling.

## 11-REQ-5.5 and TS-11-31: `impl`'s estimate counts the conformance review

**Spec:** the estimate counts "the pending tasks plus 1 unless `--no-survey`".

**Is:** it also counts the independent conformance review that runs after the
last task, unless `--no-review` (`codeimpl/pipeline.go:438`). Three pending tasks
estimate five phases, and four under `--no-review`
(`TestTS11_31_EstimateCountsPendingTasksPlusSurvey`, `codeimpl/preflight_test.go:184`).
The review is a phase the plan decides as surely as the survey; the resolve phase
that may follow it runs only on what the review finds, so, like a repair, it is not
counted.

## TS-11-43: a `--preflight` event stream includes the baseline's `check` event

**Spec:** the stream contains only `{run_start, step, heartbeat, run_end}`.

**Is:** `fix` and `impl` run the project's verification command once, as the
ordinary run does, to establish what the model's work is compared with, and that
run reports as a `check` event; a `warning` event appears when no verification
command is found. `fix --preflight --emit-events --land none` emits `run_start,
check, run_end`. `TestTS11_43_...` allows `step`, `check`, `warning` and
`heartbeat` between `run_start` and `run_end`, and now asserts that the baseline's
`check` event is present when a `--verify` command ran.
