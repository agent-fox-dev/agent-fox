# 04. Implement a spec as a tool

**Status:** accepted
**Date:** 2026-09-10
**Supersedes:** nothing
**Related:** [ADR 01](01-adopt-spec-format-v2.md), [ADR 03](03-rebuild-the-skills-as-tools.md)

## Context

`spec` writes a version 2 specification package and `fix` turns a problem
report into a verified change on a branch. Nothing in this repository turns a
spec into code. The previous generation of agent-fox
([`agent-fox-legacy`](https://github.com/agent-fox-dev/agent-fox-legacy),
Python) did exactly that with `af code`: an orchestrator in
`packages/agentfox/agentfox/engine` walked a task graph built from the specs,
spun up one coder session per task group in its own worktree, harvested the
commits into an integration branch, ran a reviewer and a verifier on the
result, retried on failure with the failure's context, persisted every state
transition to a database, and extracted "learnings" into a knowledge store.

That system worked, and it was thirteen thousand lines of engine and session
code before counting the packages it depended on. Reading it end to end
turns up the same lesson ADR 03 drew from the skills, from the other side:
where the skills had control flow in prose, the engine had control flow in
code — and almost all of the code was there to manage the state of things the
model had been allowed to decide.

What the legacy engine got right, and what this design keeps:

- **One task per session, with the scoped render.** The coder receives the
  spec scoped to its task (`render_individual_scoped`), never the whole
  package; the legacy coder profile forbids starting the next task group.
- **The orchestrator owns `tasks.json`.** The coder is told "do not modify
  tasks.json — the orchestrator updates subtask states after your session
  completes", and `_mark_subtasks_done` sets the state and commits it beside
  the coder's work.
- **Pre-flight before dispatch.** A task whose checkboxes are ticked, whose
  findings are resolved and whose tests pass is skipped without spending a
  session on it.
- **Drift analysis before coding.** The reviewer's pre-flight mode compares
  the spec's assumptions with the codebase as it stands and hands the coder a
  drift report to adapt to, "rather than stale spec assumptions".
- **A failed attempt is retried with its failure in the prompt**, and the
  branch is the deliverable when the merge strategy is `branch` (issue #780).
- **Never modify the spec; write errata if the implementation must diverge.**
- **Session summaries** carried what was surprising — gotchas, rejected
  approaches, assumptions — into later sessions.

What it got wrong, or what does not survive contact with ADR 03:

- The coder committed. It was asked to run the quality gates and "fix any
  failures before finalizing the commit", and the orchestrator then harvested
  whatever it found. The thing that decided the work was done was the thing
  that did it.
- The verifier's verdicts were "informational: they do not by themselves
  advance, block, or re-run any part of the pipeline". A verdict that changes
  nothing is a comment.
- Requirement-to-test coverage was established by grepping test files for the
  requirement id, a string match the v2 format was designed to make
  unnecessary (C4, C7, C8).
- Worktrees, an integration branch, a merge lock, a DuckDB knowledge store,
  a circuit breaker, hot reload and a configurable retry ladder are the
  machinery of a daemon that runs many specs at once. They are not the shape
  of a tool that implements one.

## Decision

A fourth tool, `impl`, with the interface of the other three: one positional
input naming a spec, one JSON object on stdout, progress on stderr, the shared
exit-code table.

```
impl [flags] <spec-dir | NN | name | NN_name | path to a spec file | ->
```

It implements the spec's tasks **in order, one model phase per task, on one
branch**, and it lands the branch the way `fix` does. The pipeline lives in
`codeimpl`, the command in `cmd/impl`, and the two things it needs that the
other tools also need move to `internal/`.

The design rule is ADR 03's: the model does the part that needs judgment and
Go does everything else. Applied here:

| The legacy engine left to the coder | `impl` does in code |
|---|---|
| "do not modify tasks.json" (a profile rule) | the write tools refuse paths under the spec directory, and any change that reaches one anyway is reverted before the state is written |
| "commit on the feature branch with a conventional message" | the commit is made by the program, after the checks pass, and the message names the spec and the task |
| "run the test suite and linter; fix failures before committing" | `test_commands.linter` and `test_commands.all_tests` run in Go before any change and after every task, and the two runs are compared |
| "one task group per session; do not begin the next" | the phase is given one task's scoped render and one submit tool; the next task is a new agent |
| the verifier's PASS/FAIL per requirement, "informational" | `submit_task` refuses a report that leaves one of the task's tests without a verdict and evidence; a `fail` verdict is a warning on the run and a line in the pull request |
| retry with the failure in the prompt (a retry ladder in config) | one bounded re-attempt, from a hard reset to the last good commit, with the gate's output in the prompt |
| a drift report from a reviewer session, stored as findings | one read-only `survey` phase before the branch exists, whose brief is rendered into every task prompt |
| session summaries in a knowledge store | each task's report is rendered into the prompts of the tasks after it, for this run only |

### 1. The input names a spec, and the format decides what a spec is

The argument goes through `toolio.Resolve` like every other input. `impl`
then reads what it was handed as a *reference* to a spec package rather than
as a problem: a directory path, a spec id (`09`), a spec name (`agent_mode`),
a directory name (`09_agent_mode`), or the path of a file inside the package
(`.specs/09_agent_mode/tasks.json` — `Resolve` reads the file, and `impl`
takes its directory). Resolution is against the spec root (`--specs-dir`,
`$AF_SPEC_DIR`, else `<dir>/.specs`) using `afspec.DiscoverSpecs`; a reference
that matches two packages is a usage error naming both.

A GitHub URL is refused as a usage error. It is the one input shape the other
tools accept that has no meaning here, and inventing one ("an issue whose
body names a spec") would be guessing.

### 2. A spec is implemented only when it is a complete plan

Pre-flight loads the package and runs `Validate`. A package that fails C1–C11
is refused with `category: "invalid_spec"` and the rules named, before a
token is spent. This is the whole reason for format v2 (ADR 01): a valid spec
is a complete plan by construction, so a task's `tests` and `criteria` are
what the phase is answerable for, and the coverage check the legacy verifier
did by grepping test files is a property of the input.

Pre-flight also refuses a `sealed`, `superseded` or `archived` spec (there is
nothing to implement, and `Save` would refuse the state update), a dirty
working tree, a repository that is not one, a `test_commands` entry that is
a compound shell command (`internal/checks` runs a program, not a shell, so
`make test && make lint` cannot be what "the checks pass" means), and a
`test_commands` whose program belongs to another ecosystem than the
project's — the audit `spec` runs at generation time, applied to a package
that may have been written by hand.

A spec whose `dependencies` name an upstream spec that is neither sealed
nor fully done is not refused but stopped, with exit 3: the format says
every task of this spec runs after the upstream spec is sealed or its
integration task is done (§8.2), and the person or program driving `impl`
is the one that can run the upstream first. A `draft` package is
implemented with a warning: it validates, and activation freezes the intent
rather than the plan. A dependency that names no package in the spec root cannot be checked
and is reported as a warning; `Validate` does not cross specs, and a package
copied out of its repository should still be implementable.

### 3. The gate is the spec's, not a guess

`fix` detects a verification command. `impl` has one written down. The
format's implicit definition of done (§8.3) has four clauses: the task's
own tests exist and pass, `test_commands.all_tests` passes,
`test_commands.linter` passes, and every `done_when` entry holds. The
program measures the two it can — the commands — and holds the model to the
other two at the tool boundary (§6). Both run, in that order, once before
any change and once after each task, through `internal/checks`, and each
before/after pair is compared with `checks.Compare`. The task's verdict is the
worst of the two. The gate that passed after task N is the baseline for task
N+1, so `regressed` always means "this task broke it" and a repository that
was red at the start is reported as `pass_was_already_failing` exactly once —
by the task that made it green. `--verify` replaces the pair with one command
for a project whose `tasks.json` is wrong about itself; `--no-verify` runs
nothing and the run is reported as unverified.

### 4. One branch, tasks in order, a commit per task

The branch is `impl/<NN>-<slug>`, unique the way `fix`'s is. Tasks run in
array order, which the format guarantees is a valid topological order of
`depends_on` (C6: every dependency is a lower id). Tasks already `done` or
`dropped` are skipped, and a task left `in_progress` by a parked run is
reopened, which is what makes a second run of `impl` on a half-implemented
spec continue rather than start over. `--task N` runs one
task and `--branch name` works on a named branch, creating it if it does not
exist, so a driver that wants to dispatch one task at a time can.

For each task, in this order:

1. The task moves `pending → in_progress` through the format's state machine,
   in memory.
2. The `implement` phase runs with the task's scoped render, the survey
   brief, the reports of the tasks before it, the baseline, the project's
   own instructions, and — on a second attempt — the output of the gate that
   failed the first one.
3. Any change under the spec directory is reverted and recorded as a
   warning; the state file is the program's.
4. The change is measured: `git` lists what differs, and a task that changed
   nothing is a failed attempt — every task, the integration task included,
   owns tests it has to write.
5. The gate runs and is compared with the baseline.
6. A landable verdict — and a report that answers `pass` for every test the
   task owns and every `done_when` entry — marks the task `done`, writes
   `tasks.json`, and commits everything as `feat: <subject>` with a `Spec:`
   trailer naming the package and the task. A report that answers `fail`
   for one of them is an honest report of a task that is not done: it is
   accepted at the tool boundary and treated like a failed gate. The state
   write is `tasks.json` alone, through the library's own encoder and a
   temp-and-rename; `afspec.Save` would rewrite every artifact from memory
   and refuse an active spec whose intent drifted, both of which are right
   for a tool that owns the package and wrong for one that only records
   progress in it. The PRD's `updated_at` is therefore not touched by a
   task landing. A verdict that is not landable and an attempt to spare
   resets the branch to the last commit and cleans the tree; the last
   attempt's failure parks the work as a `wip:` commit — with `tasks.json`
   recording the task as `in_progress`, so the branch says where it stopped —
   returns the checkout to the base branch, and exits 4.

A phase that ends without a report is an attempt outcome too, by category:
`no_result` and `max_turns` are the model's, so they get the second attempt
with the stop reason and the discarded diff's stat in the prompt;
`budget`, `api`, `auth` and `aborted` are not, so the work is parked at once
under that category. The git steps that land or park — the state write, the
commit, the checkout of the base branch, the reset and clean of a discarded
attempt, the revert of the spec package — run under a context detached from
the run's cancellation with its own short ceiling, because the case parking
exists for most is Ctrl-C, and a park the cancelled context killed mid-commit
would leave the tree dirty on the work branch, which is what parking is
meant to prevent. The parked commit is made with the repository's hooks
skipped: its point is that the work is unverified. A landed commit is not,
and a hook that leaves the tree dirty after it fails the run naming the
files rather than attributing them to the next task.

Between tasks the run checks two ceilings that the legacy circuit breaker
also had: `--total-budget`, the spend across every phase of the run, and the
context's cancellation. A run that stops on either leaves every landed task
committed and names the next task in the result; nothing is undone.

Parallel dispatch is deliberately absent. Independent tasks *could* run in
worktrees, as the legacy engine did; the cost is a merge step, a conflict
policy and a second copy of every state, and the whole of that machinery
existed to save wall-clock time in a daemon. A tool that a program drives can
be driven twice.

### 5. The survey phase is where the spec meets the code

Before the branch is created, one read-only phase reads the whole spec and
the repository and submits a brief: where the things the spec names actually
live, the conventions a coder must follow, and every place the spec's
assumptions and the code disagree, each with a resolution. It is the legacy
reviewer's pre-flight and drift analysis as one phase whose result is used
rather than stored.

`--no-survey` skips it, for a driver that runs one task at a time and would
otherwise pay for a survey per task. It is otherwise the one place the run
may stop to ask. A spec that cannot be
implemented as written — it names a module that does not exist and the PRD
does not say to create it, or it contradicts an invariant the code enforces —
is reported as a `blocker`, and the run exits 3 with the question, having
created no branch and written no file. The bar is the same as `fix`'s
ambiguity: one time in twenty. A task phase may raise the same blocker when
it discovers one the survey could not see; the work so far stays on the
branch and the checkout returns to base.

### 6. What the model reports is enforced at the tool boundary

`submit_task` takes the task's test ids. A submission that skips one, names
one the task does not own, answers with a word, or has no commit subject is
refused with the ids named, and the phase continues. The verdicts are the
model's judgement and are labelled as such in the result and the pull
request; the gate is the program's measurement and is what decides whether
the task lands. A task that lands with a `fail` verdict on one of its tests
is a warning on the run and a visible line in the report — the reasoning is
the one `fix` gives for acceptance criteria: a run that refused to land on a
self-reported failure would be a run that taught the model not to report one.

### 7. Nothing the model emits reaches git, GitHub or the spec

The implementation phase has the file tools and a shell under the same guard
as `fix`'s: git read-only, `gh` refused, `find -exec` refused. Two things are
added to the guard for this tool and stay available to the others:

- `GuardOptions.ProtectedPaths` refuses `write_file` and `edit_file` under
  named directories. `impl` names the spec package's own directory — not the
  whole spec root, and not `docs/errata/`, which is where the project's own
  instructions send a divergence.
- The verification command's program and the build programs are allowed, so
  the phase can run the suite it will be judged by.

The branch, every commit, the push and the pull request are the program's.
There are no comments — the input is a spec, not an issue.

## Consequences

**Two things move into `internal/`.** `specgen`'s project profile — language
detection and the test-command audit — becomes `internal/project`, because
`impl` needs the same audit and a tool package importing another tool package
for it would be the wrong dependency. `gitx` gains `Clean`, and `agentrun`'s
guard gains `ProtectedPaths`.

**`impl` is the first tool with a loop.** `fix` is two phases; `impl` is one
phase per task plus one before. The per-phase bounds are per phase, so a
twelve-task spec at the default ceiling can cost twelve times what a `fix`
does. The defaults are the same as `fix`'s (150 turns, $5.00) because a task
is roughly a fix's worth of work, and the envelope's `usage.phases[]` names
every one.

**Resume is a property of `tasks.json`, not of a state file.** The legacy
engine kept execution state in a database and a `plan_hash` to detect a
changed plan. Here the state is the format's own `state` field, committed
beside the work, so "where did it stop" is answered by `git log` and by the
file. A spec edited between runs is re-validated; a task marked `done` by hand
is skipped, which is what marking it done means.

**Lifecycle stays library API.** A spec whose tasks are all `done` is not
sealed by `impl`. ADR 03 kept `seal` and its siblings out of the tools'
interface, and a review that rejects the pull request may reopen a task,
which a sealed spec cannot record.

**The reviewer archetype does not come back.** Its audit-review mode checked
test coverage against the test spec, which C4/C7 now establish before the run,
and its findings fed a retry loop whose verdicts were, by the profile's own
words, informational. What survives of it is the survey phase and the
per-test verdicts, both of which have a consequence.
