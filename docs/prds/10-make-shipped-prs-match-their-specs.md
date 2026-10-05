# PRD 10: Make shipped pull requests match their specs

Issue #86. Built as described in [ADR 09](../adr/09-grade-the-work-independently.md);
the tool reference is [`impl`'s conformance stage](../cli.md#the-conformance-stage)
and [`fix`'s proof](../cli.md#proving-the-fix).

## Problem

An audit of five `impl`-generated pull requests (agent-fox-dev/hub #37–#41;
findings in hub #42–#47) found that every one self-reported "N of N tasks
landed, `make test` passes", and every merged result had:

- a user-visible contract violation the tool had noticed and buried in a note
  (an exit code documented as 3 that the shared CLI library maps to 2);
- tests that passed without proving their contract (an internal error code
  asserted instead of the process exit code; `errors.Is` on a sentinel instead
  of driving the classifier; a test with no assertions; an assertion inside a
  branch that never runs; wiring tests that re-implement the wiring);
- about 100 tests that fail on a stock git install, because fixtures assumed
  the author's `init.defaultBranch=main`;
- an explicit spec instruction ("extract and share, do not duplicate")
  recorded as a survey decision and then reinterpreted by a task note;
- docs and errata that restated the PRD instead of the code, and an ADR dated
  eleven days in the future;
- a "while here" edit outside the spec's allowed files;
- `_ = err` under "log but don't fail", dead symbols, comments referring to
  "a later task", and gofmt drift.

Root cause: `impl` grades its work with the context that produced it, and its
gates check "tests pass here" rather than "the spec's observable contract holds
in a clean environment".

## Requirements

| Id | Requirement | Built as |
|---|---|---|
| R1 | An independent conformance review before the pull request, with a per-requirement and per-test table; blocking rows block the PR unless resolved or promoted to R2 | The review phase (`internal/conform`), the resolve phase, and the draft PR with exit 4 (`nonconformant`) |
| R2 | Known deviations at the top of the PR body, each tied to an id and tracked by an issue or an erratum; docs describe actual behaviour; the summary does not claim all tasks landed while any remain | `submit_task.deviations`, the resolve phase's declarations, the "Unmet requirements" block, issue filing under `--land pr`, the conditional summary line |
| R3 | Tests assert the observable contract named in the test spec; no-assertion tests, never-true branches and import suppressors fail; a mutation check where test-first is waived | The review's test assessments; the `no_assertions` and `import_suppressor` structural checks; the revert check on waived tasks |
| R4 | Final verification in a hermetic environment with its fingerprint in the PR body; fixtures that `git init` without a branch are flagged | `gitx.HermeticRunner`, `checks.Fingerprint`, `final_verification` and `environment`, the `git_init_branch` check; this repository's own fixtures fixed |
| R5 | Survey decisions become checks the review verifies; a reinterpreted decision is a deviation | Decisions `D-n` in the review's scope; tasks declare a departure as a deviation |
| R6 | gofmt, vet, duplication, discarded errors under "logged" comments, "later task" comments, unused symbols and long functions on the touched files; fixed or listed | `conform.Scan`; findings go to the resolve phase and what is left is listed as unmet |
| R7 | Docs copy from code with a recorded source; errata cite the code line and the test; the real date is injected | `submit_task.doc_sources`, checked against the file; the `errata_citation` and `future_date` checks; `conform.DateLine` in every writing phase |
| R8 | The change is held to the spec's allowed files | Every task's `touches` as the scope (documentation exempt); out-of-scope files are fix-only blockers |
| R9 | `fix` reproduces with a test of the observable contract, updates docs and errata, re-runs the review for cited ids, and does not close the issue if the test passes with the fix reverted | The revert check deciding `Closes` or `Refs`; the review of cited ids; the implement prompt |

## Success criteria

| Metric | Before (hub #37–#41) | Target |
|---|---|---|
| Spec requirements found unmet by an independent post-merge audit | ~35 across 5 PRs | 0 undeclared; all declared under R2 |
| Tests that pass without asserting their contract | ~25 test ids | 0 at merge |
| `make test` on a clean image with default git config | fails (~100 tests) | passes |
| Docs statements contradicting code | 8 in one docs PR | 0 |
| Files outside the spec's allowed scope | 1 | 0 |

This repository's own suite passes under the same clean environment
(`HOME` empty, `GIT_CONFIG_GLOBAL=/dev/null`, `GIT_CONFIG_NOSYSTEM=1`), and a
hygiene test keeps its fixtures naming the branch `git init` creates.

## Non-goals

- Changing the spec format: `touches` already exists and is used as the scope.
- Mutation testing everywhere: the revert check runs only where test-first is
  waived, and in `fix`.
- Fixing the five hub pull requests (hub #42–#47).

## Decisions made while building it

- The scope is the union of every task's `touches`, and applies only when
  every task lists some; documentation and the spec package are exempt.
- `git clone` without `-b` is not flagged; see ADR 09.
- Structural findings are listed as unmet rather than blocking: they are
  heuristics, and a refusal on one would be a false positive's veto.
- A deviation with no erratum in the change is filed as an issue only when the
  run opens a pull request and may write to the forge; otherwise it is listed
  as not tracked, with a `deviation_not_tracked` warning.
- `fix` lands a verified change whose tests pass without it, and references
  the issue instead of closing it: the change is correct as far as the checks
  can tell, and throwing it away would lose work over what is a weak test.
