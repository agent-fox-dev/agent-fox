# 09. Grade the work independently of the phase that did it

**Status:** accepted
**Date:** 2026-10-05
**Supersedes:** nothing
**Related:** [ADR 03](03-rebuild-the-skills-as-tools.md), [ADR 04](04-implement-a-spec-as-a-tool.md), [PRD 10](../prds/10-make-shipped-prs-match-their-specs.md), issue #86

## Context

ADR 03 moved the facts out of the model's reach: the checks are run by Go,
the diff comes from git, and no sentence the model writes can assert a
verification result. An audit of five `impl` pull requests (agent-fox-dev/hub
#37–#41, findings in hub #42–#47) showed that this was not enough. Every one
reported "N of N tasks landed" over a passing `make test`, and every one
shipped requirements it did not meet:

- an exit code the tool knew was wrong, recorded as "a gotcha for a
  follow-up" in a note, with the docs claiming the wrong value;
- tests that passed without proving their contract — an internal error code
  asserted instead of the process exit code, an assertion in a branch that
  never ran, wiring re-implemented in the test;
- about a hundred tests that failed on a clean image because the fixtures
  relied on the author's `init.defaultBranch`;
- a survey decision ("extract and share") recorded and then reinterpreted
  away by a task;
- docs and errata restating the PRD instead of the code, and an ADR dated
  eleven days in the future.

The checks were run honestly. What they measured was "the tests pass here",
and what graded the work against the spec was the phase that wrote it.

## Decision

**The work is graded by something that did not do it, against the spec's
observable contract, in an environment that is not the author's.** For
`impl` this is a conformance stage between the last task and the push; for
`fix`, a proof step between verification and the commit.

1. **Facts first, from Go.** Structural checks over the touched files
   (`internal/conform`), the spec's `touches` as a scope, and the gate run
   again under `gitx.HermeticRunner` — an empty `HOME`, no global or system
   git configuration — with its fingerprint in the pull request.
2. **An independent review.** A read-only phase on a fresh context sees the
   spec, the diff and the repository, and never a task's report. It answers
   every requirement, test and survey decision in scope; the submit tool
   refuses a review that skips an id or cites a `file:line` that does not
   exist. That is the most a program can hold a reviewer to; the rest is the
   point of a fresh context.
3. **One chance to answer.** A single writing phase fixes what was found or
   declares it a known deviation, tracked by an erratum in the change or an
   issue the run files. Its change lands only on checks that still pass and
   is reviewed again. A finding that can only be fixed — a file out of scope,
   a document that contradicts the code — cannot be declared.
4. **The pull request says what is true.** It opens with every unmet item and
   does not claim the work is complete while any remains. What was neither
   fixed nor declared makes it a draft and the run exits 4 (`nonconformant`).
5. **Measure the red run when test-first is waived.** The tests run with the
   implementation taken out, and must fail. In `fix` the same check decides
   whether the issue is closed (`Closes #N`) or only referenced (`Refs #N`).
6. **The date is the program's.** Every writing phase is told today's date,
   and a future date in an ADR or erratum is a structural finding.

## Consequences

- A run costs one more phase (the review), and a second review and a resolve
  phase when anything is found. `--preflight` counts the review. `--no-review`
  removes both model phases; the program's own checks still run.
- A run can now land work and still exit 4. That is deliberate: the work is
  safe on its branch, and a caller that reads the exit code alone is not told
  the spec was met when it was not.
- A spec whose every task lists `touches` is held to them. Documentation is
  exempt, because the project's own rules ask for it to change with the code.
- `git clone` is not flagged as `git init` is: a clone checks out the
  remote's HEAD, which the fixture's own `git init -b` already pins, and `-b`
  on a clone of an empty repository fails rather than pins anything.
- The structural checks are heuristics that report rather than block: a
  finding left in the change is an unmet item the pull request lists, not a
  refusal. Several of them read Go source (formatting, vet, long functions,
  unused declarations, assertion-less tests) and find nothing in other
  languages; `docs/cli.md` lists which apply where (issue #221).
