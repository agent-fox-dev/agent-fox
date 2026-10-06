# Erratum: where spec 16's index is built, how it is created, and when impl invalidates it

Recorded because the `impl` run of `.specs/16_indexed_code_search` declared
eight requirements and tests unmet (agent-fox issues 179 to 186). Three are now
met, and the last section lists them. In the others, what the spec wants of the
run holds, but what it says about the surrounding code does not match: it
describes code that does not exist, or an AgentKit API it guessed before
AgentKit shipped one. The spec itself marks those signatures **Unverified**.

## 16-REQ-3.1 and 16-REQ-1.3..1.6: the shell builds the index, and the pipeline checks it

**Spec:** each `cmd/` entry point's `Exec` calls `codesearch.New` "before
constructing the Runner", and each tool's `Options.Index` "is forwarded to
`agentrun.Config.Index` when building the Runner".

**Is:** no entry point and no pipeline builds a Runner. The shared shell
builds it, before any `Exec` runs (`internal/toolio/app.go`). So the shell is
also where the index is built: `App.execute` calls `openIndex`
(`internal/toolio/app.go:665`) before it builds the Runner. It puts the one
index on the Runner's `Config.Index` (`internal/toolio/app.go:669`) and on
`Deps.Index` (`internal/toolio/app.go:691`). It closes the index with a
`defer`, on every way out of the run. Each entry point copies `Deps.Index` into
its tool's `Options.Index`. The lifecycle is written once, not four times, so
the four `cmd/` mains have no index code of their own.

The forward the spec asks for therefore runs the other way, and the pipeline
checks it. Each pipeline's `Run` and `RunPreflight` call `agentrun.RunIndex`
(`internal/agentrun/index.go:26`; for instance `codefix/pipeline.go:188`). That
call sets the index the pipeline grants `code_search` for, and invalidates, to
the index its Runner's phases read: `Options.Index` when the Options carry one,
or the Runner's `Config.Index` when they carry none. If the two are different
indexes, the run is refused before any phase runs. Otherwise a pipeline could
invalidate one index while `code_search` and `find_symbol` read another, and
hand the model results from a tree that no longer exists. `impl` holds its
repair runner to the same index.

`TestTS16_2/3/4/5_TheRunnersIndexIsTheRunsIndex`, one per pipeline, check the
refusal and the adoption. `TestRunIndex` (`internal/agentrun/index_test.go:47`)
checks every combination.

## TS-16-14: the Runner's own index is observable

**Spec:** "the same index instance is passed to every phase".

**Is:** `agentrun.Runner` now reports the index it was built with:
`Runner.Index()` (`internal/agentrun/index.go:13`). `indextest.TS14`
(`internal/agentrun/indextest/lifecycle.go:191`) runs for each entry point. It
checks that the one built index is the pipeline's `Deps.Index`, the Runner's
`Config.Index`, and the index of a second runner derived from the run's
configuration (impl's repair runner is built that way). TS13 checks that a run
whose index could not be built has a Runner without one.

## 16-REQ-3.1: `codesearch.New` takes no context

**Spec:** `codesearch.New(ctx, ws)`, returning `(tools.Index, error)`; and
`tools.Index` with `Invalidate(path string) error`.

**Is:** AgentKit's signature is
`func New(ws *tools.Workspace, opts Options) (tools.Index, error)`, and
`tools.Index.Invalidate(rel string)` returns nothing. The shell calls
`codesearch.New(ws, codesearch.Options{})` (`internal/toolio/index.go:13`). It
passes the zero `Options`, because the Runner's `tools.Options` has no `Ignore`
configuration either.

There is no context to pass, because `New` does no work that could be
cancelled. It starts no goroutine, runs no process and does not walk the tree.
It fails only for a nil workspace, and with `codesearch.ErrUnsupported` on
Windows. The index reads the tree on the first `code_search` call, under that
call's context. So cancelling a run cancels the build, and a build that fails
there fails that tool call, not the run. TS-16-12 checks the cancellation that
is left: `Close` is called exactly once when the run's context is cancelled
(`indextest.TS12`, `internal/agentrun/indextest/lifecycle.go:117`).
`docs/model-usage.md` and `docs/cli.md` say when the index reads the tree.

## TS-16-34 and 16-REQ-4.2: impl surveys before it creates the branch

**Spec:** `Invalidate("")` is called "after branch creation before the survey".

**Is:** in `codeimpl.Run` the survey runs before a new branch is created
(`codeimpl/pipeline.go:165`, then `codeimpl/pipeline.go:190`). That order is
deliberate: a survey that finds the spec cannot be implemented stops the run
before it leaves a branch behind (`TestSurveyBlockerStopsBeforeAnyBranch`,
`codeimpl/pipeline_test.go:480`). Moving branch creation in front of the survey
to meet this sentence would break that. The survey also does not need the
invalidation: a new branch starts at the commit the survey read, so the tree is
the same.

What the requirement is for, that no phase searches a tree from before a
checkout, holds in both orders:

- A new branch: the index is invalidated right after `CreateBranch`, before
  the first phase that follows it, repair or task 1
  (`codeimpl/pipeline.go:195`).
- An existing branch: it is checked out in pre-flight, and any parked attempt
  on it is reset there, so the index is invalidated in pre-flight, before the
  survey (`codeimpl/pipeline.go:648`).

`TestTS16_16_InvalidatesAfterBranchCreationBeforeThePhasesThatFollow` and
`TestTS16_16_InvalidatesAfterCheckingOutAnExistingBranchBeforeTheSurvey`
(`codeimpl/pipeline_test.go:1968`, `:1983`) check each order. So do the two
subtests of `TestTS16_34_ImplInvalidatesTheIndexAcrossTreeChanges_Smoke`
(`cmd/impl/smoke_codesearch_test.go:130`), which run a two-task spec through
the real `impl` entry point.

## Resolved since the issues were filed

- **16-REQ-2.1, the review phase** (issues 179 and 184). `conform.RunReview`
  is the review phase `fix` and `impl` share. It grants `code_search` when
  `ReviewInput.CodeSearch` is set (`internal/conform/review.go:220`). Each
  tool's brain sets that field from the run's index (`codefix/phases.go:292`,
  `codeimpl/phases.go:310`). `TestRunReviewOffersCodeSearchOnlyWithAnIndex`
  and `TestTheReviewPhaseIsGrantedCodeSearch` in `codefix` and `codeimpl`
  check it.
- **TS-16-30, the two-task run** (issue 185). The test runs a two-task spec,
  as the test spec asks. The example's first two tasks are folded into task 1
  and its integration task becomes task 2. That is `twoTasks`, the plan
  TS-14-37 already runs, and it passes the afspec validity checks. Each task's
  requests are checked for `code_search`.
