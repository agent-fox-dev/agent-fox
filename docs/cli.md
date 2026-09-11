# Tool reference

Four tools, one interface.

```
spec  [flags] <input>     a product idea      → a validated specification package
issue [flags] <input>     a problem report    → a structured GitHub issue
fix   [flags] <input>     a problem           → a verified change on a branch
impl  [flags] <input>     a specification     → the spec implemented, task by task, on a branch
```

Each takes **exactly one positional input** and writes **exactly one JSON
object** to stdout, on every program-driven path. Progress goes to stderr, so
the two never interleave and a caller can pipe stdout straight into a parser.

Two paths are human-driven rather than program-driven, and print text instead:
`--version` prints the build identity and exits 0, and `-h`/`--help` or a bare
invocation with no positional argument prints the help text and the flag list
to stderr and writes nothing to stdout — a person asking what the tool does
gets an answer they can read, not a JSON object to parse. A bare invocation
exits 2, since nothing was fetched or written; `-h`/`--help` exits 0.

## The input

The single argument is classified in Go, before anything else happens. There is
no flag that selects the kind; the argument's shape decides.

| The argument is | Then it is | What is used |
|---|---|---|
| a GitHub issue or pull-request URL | `github` | the issue, its body, and its comments |
| a path to a readable regular file | `file` | the file's contents |
| `-` | `stdin` | everything piped in |
| anything else | `text` | the text itself |

Two consequences worth stating. A path that does **not** exist is text, not an
error — "the `widget/` package panics" is a plausible report. And a directory
is text too, for the same reason.

Input is bounded at 256 KB, cut at a line boundary, with the cut marked in the
text and reported as `input.truncated` in the envelope. An input that ends
mid-stack-trace with no marker reads to a model as a complete stack trace that
simply had no more frames.

Flags may come before or after the input:

```sh
issue --dry-run ./crash.log
issue ./crash.log --dry-run
kubectl logs deploy/api --since 1h | issue - --repo acme/widgets
```

## The output

```jsonc
{
  "tool": "fix",
  "version": "0.4.0",
  "ok": true,
  "exit_code": 0,
  "input":  { "kind": "issue", "origin": "https://github.com/acme/widgets/issues/42", "bytes": 3184 },
  "model":  { "spec": "STANDARD", "id": "claude-sonnet-5", "vendor": "anthropic",
              "api": "anthropic-messages", "thinking": "high" },
  "usage":  { "input_tokens": 48211, "output_tokens": 3104, "cost_usd": 0.19, "turns": 23,
              "phases": [ { "name": "analyse", "turns": 9, "stop_reason": "tool_terminate", … } ] },
  "result": { /* tool-specific; see below */ },
  "warnings": [ "the summary comment could not be posted on acme/widgets#42: 403" ],
  "duration_ms": 214003,
  "started_at": "2026-09-09T13:20:30Z"
}
```

`ok` is the one field a caller has to read. It is true only when the tool did
the whole job it was asked to do, and it is never true alongside a non-zero
`exit_code` or an `error` object.

`error` is present exactly when `ok` is false:

```jsonc
"error": { "stage": "verify", "category": "unverified",
           "message": "`make check` did not pass after the change (regressed); the work is on fix/issue-42-… and was not landed" }
```

`stage` names the pipeline step in the tool's own vocabulary. `category` says
whether re-running could help:

| Category | Means |
|---|---|
| `usage` | the invocation was wrong; nothing was fetched or written |
| `input` | the input could not be read (a private issue, an unreadable file) |
| `auth` | no credential for the model's vendor, or for GitHub |
| `model` | the model spec could not be resolved |
| `api` | a provider or transport failure |
| `budget`, `max_turns` | a phase hit its ceiling; raising it may help |
| `no_result` | the model finished without calling its terminating tool |
| `git`, `github` | an external system refused |
| `ambiguous` | (`fix`) the input reads two ways; a question was posted |
| `blocked` | (`impl`) the spec cannot be implemented as written, or an upstream spec is not done |
| `unverified` | (`fix`, `impl`) code was written and the checks do not pass |
| `empty_change` | (`fix`, `impl`) work was reported and no file differs |
| `invalid_spec` | (`spec`) the package was written and does not validate; (`impl`) the package does not validate and was not implemented |
| `internal` | a bug in the tool |

### Exit codes

| Code | Meaning |
|---|---|
| `0` | done |
| `1` | failed; the stage is named in the JSON |
| `2` | usage error — nothing was fetched, nothing was written |
| `3` | stopped on purpose: a person has to answer something (`fix`, `impl`) |
| `4` | work exists but the checks do not pass (`fix`, `impl`) |

## Shared flags

| Flag | Default | Effect |
|---|---|---|
| `--dir` | `.` | the repository to work in; the file tools cannot reach outside it |
| `--model` | `$AF_MODEL`, else `STANDARD` | a tier (`SIMPLE`, `STANDARD`, `ADVANCED`) or any catalog spec |
| `--vendor` | `$AF_MODEL_VENDOR`, else `anthropic` | which tier table the tier names resolve against |
| `--variant` | — | tier variant, e.g. `extended` for the long-context row |
| `--max-turns` | per tool | per-phase turn ceiling, which is also the repair budget |
| `--budget` | per tool | per-phase spend ceiling, in dollars |
| `--phase-timeout` | — | wall-clock ceiling on one phase |
| `--trust-project` | off | admit `AGENTS.md`, `CLAUDE.md` and `.specs/steering.md` into the system prompt |
| `--verbose` | off | trace tool calls and timings on stderr |
| `--quiet` | off | print nothing on stderr |
| `--show-text` | off | stream the model's own prose to stderr |
| `--version` | — | print the build identity and exit |

---

## `issue`

Reads a problem report, traces it through the codebase, and files a structured
GitHub issue with every claim cited to a file it actually read.

```sh
issue "panic: assignment to entry in nil map in loop.go, after an abort"
issue ./crash.log --dir ./service --label af:fix
issue https://github.com/acme/widgets/issues/42 --overwrite
issue ./crash.log --dry-run
```

The analysis is read-only, and that is a mechanism rather than a promise: the
mutating tools are excluded from the resolved set, there is no shell and no
network tool, and the issue is created by a `net/http` call after the run.
**No sequence of model outputs can cause this tool to write to GitHub.**

Every path in `affected_files` is resolved against the workspace before the
diagnosis is accepted; one that is not there comes back to the model as an
error naming the missing path, and the run continues. The count of refusals is
reported as `result.rejected_path_calls` — a nonzero count is the check
working, and a large one means the model was writing from the report rather
than from the code.

| Flag | Default | Effect |
|---|---|---|
| `--repo owner/repo` | the input issue's, else the `origin` remote of `--dir` | where the issue is filed |
| `--label a,b` | — | labels for the created issue, e.g. `af:fix` |
| `--dry-run` | off | make no change on GitHub; report the diagnosis only |
| `--overwrite` | off | rewrite the input issue in place instead of creating a new one; needs an issue URL, and cannot be combined with `--repo` or `--label` |

Bounds: 100 turns, $2.00 per phase.

`result` carries `action` (`created` · `updated` · `none`), `url`, `number`,
the rendered `body`, and the diagnosis as fields: `severity`, `confidence`,
`root_cause`, `affected_files`, `suggested_fix`, `acceptance_criteria`.

---

## `fix`

Diagnoses a problem, writes the change on a branch, verifies it with the
project's own checks, and lands it.

```sh
fix https://github.com/acme/widgets/issues/42 --dir ~/src/widgets
fix ./bug-report.md --land branch
fix "the counter double-counts on retry" --dry-run
```

The working tree must be clean. The run branches from the branch checked out
now, and every check that can refuse the run happens **before** the model is
called and before anything is posted.

Verification is measured, not asserted. The project's checks run once before
any change and once after, and the two are compared:

| Verdict | Means | Landed |
|---|---|---|
| `pass` | green before, green after | yes |
| `pass_was_already_failing` | red before, green after | yes, and the report says so |
| `regressed` | green before, red after | no |
| `still_failing` | red before, red after | no |
| `unverified` | nothing ran | only with `--no-verify` |

A run that does not land parks the work as a `wip:` commit on its branch,
returns the checkout to the base branch, and exits 4. A run that reports a fix
and changed no file exits 1 rather than committing an empty tree — the diff
comes from git, not from the model.

### Acceptance criteria

When the report defines acceptance criteria — the `## Acceptance Criteria`
section `issue` writes, or the same section written by hand — they are
extracted before the model is called and become what the change is measured
against:

- both phases are given them: the analysis phase plans for every one, the
  implementation phase answers for every one;
- `submit_implementation` refuses a report that skips a criterion, names one
  the report did not define, or answers with a bare word, and says which — so
  the phase cannot end with a criterion unanswered;
- the summary comment, the failure comment and the pull-request body carry a
  **Per-criterion verdicts** section: one line per criterion, `PASS` or `FAIL`,
  each with the evidence given for it.

The verdicts are the model's judgement and are labelled as such. The list of
criteria, the pairing and the outcome are not: a criterion the report stated
appears in the section whatever the run did about it. A criterion reported as
unmet does not by itself stop a change from landing — the project's checks
decide that — but it is a warning on the run, and the comment says plainly
that the work is unfinished.

A report that defines no criteria is unaffected: no section is rendered, and
nothing extra is asked of the model.

Labels are taken from the report (`AC-1`, `NS-REQ-2`) and are `AC-n` by
position when the report used none. Bullets, ordered items, checkboxes and
wrapped items are all read; at most 30 criteria are taken.

| Flag | Default | Effect |
|---|---|---|
| `--pull [branch]` | off | checkout and pull latest changes from `origin` before branching; default origin's default branch |
| `--land` | `pr` | `pr` · `branch` (push only) · `none` (commit only) |
| `--repo owner/repo` | the input issue's, else the `origin` remote | where the pull request is opened |
| `--dry-run` | off | make no *remote* change: push nothing, open nothing, post nothing. The branch and the commit are still made locally |
| `--verify` | detected | the command that decides success |
| `--no-verify` | off | run nothing; the result is then reported as `unverified`, not as a pass |
| `--verify-timeout` | `10m` | timeout for one verification run |
| `--push-attempts` | `4` | push retries, with exponential backoff |
| `--allow a,b` | — | extra programs the implementation phase's shell may run |
| `--draft` | off | open the pull request as a draft |

Bounds: 150 turns, $5.00 per phase.

Detection order for `--verify`: `make check`, `make test`, `go test ./...`,
`npm test`, `pytest`, `cargo test`. When nothing can be detected the run says
so and reports `unverified` rather than inventing a command.

The verification command runs with the model vendors' keys and every `*_TOKEN`,
`*_SECRET`, `*_API_KEY` and `*_PASSWORD` variable stripped from its
environment: a test suite is repository code, and a repository being fixed on a
stranger's report is not something to hand an API key to.

`result` carries `stage`, `branch`, `base_branch`, `commit`, `changed_files`
(from git), `baseline`, `verification`, `verdict`, `pull_request_url`, the
`comments` posted, and the model's own `implementation` report kept separate
from the facts. When the report defined acceptance criteria it also carries
`acceptance_criteria` (the criteria, as extracted), `criteria_outcome`
(`pass` when every one was met, `fail` otherwise), and the verdict and
evidence for each under `implementation.criteria_verdicts`.

### What the model may and may not do

The implementation phase has the file tools and a shell. On top of AgentKit's
program allowlist:

- **git is read-only.** `status`, `log`, `diff`, `show`, `blame`, `rev-parse`,
  `ls-files`, `grep`, `cat-file`, `describe`, `branch --list`, `remote -v`,
  `config --get` and a few more. The branch, the commit and the push belong to
  the tool, so "committed as `abc123`" means one thing.
- **`gh` is refused outright**, so every write to the issue goes through the
  audited path.
- **`find -exec` and `-delete` are refused**, because they turn `find` into a
  write tool.
- Every simple command on a line is checked, not only the first: `ls; git push`
  is two commands, and `GIT_AUTHOR_NAME=x git push` does not hide the program.

A refusal is a blocked tool result, so the model adapts rather than dying, and
the count is reported per phase.

This is a classifier over shell syntax, not a sandbox. `go`, `make` and `uv`
can run arbitrary code from the repository. Anything genuinely untrusted
belongs in a container.

---

## `spec`

Turns a product idea into a complete, validated version 2 specification
package under `.specs/NN_name/`.

```sh
spec "a cache in front of the widget catalog, with a TTL"
spec ./docs/prds/widget-cache.md --architecture
spec https://github.com/acme/widgets/issues/42 --comment
spec ./idea.md --dry-run
```

The run is unattended, and the design follows from that. Nobody is waiting to
answer questions, so the PRD phase resolves every open question itself, records
each in a `## Design Decisions` section, and reports the ones it is least sure
of as `result.open_questions`. A caller that wants a human in the loop reads
that array; a caller that does not gets a finished spec.

Then three phases, in the order the format fixes:

1. `requirements.json` — EARS criteria and end-to-end execution paths
2. `test_spec.json` — one flat list of tests
3. `tasks.json` — one flat list of tasks

Each artifact is submitted through a tool whose schema is the format's own JSON
Schema, and whose handler runs every cross-file rule decidable at that point. A
violation comes back to the model naming the rule, and it corrects itself — the
repair loop is the loop, and the turn ceiling is the repair budget.

The order is the rule and the reason is concrete: a generator that cannot see a
test id cannot own it, which is how the previous format produced specs whose
edge-case tests belonged to no task at all.

### More than one spec's worth of work

A spec is one cohesive feature — at most 10 requirements and 8 tasks — and a
PRD for a whole subsystem is several. The PRD phase says so rather than
producing an oversized spec: it writes the PRD for the first, foundational
scope and reports the full split, every scope with the name its package will
carry. `spec` then writes **all of them**, one package per scope, in order:

```
spec: the input is 4 specs' worth of work; writing every scope: issuex_core, issuex_github, issuex_gitlab, issuex_adopt
spec: scope 1 of 4 (issuex_core): the forge-neutral contract and the null client
spec:   wrote .specs/01_issuex_core
spec: scope 2 of 4 (issuex_github): the GitHub implementation over the shared transport
...
spec: the split is complete: 4 packages
```

Each later scope gets its own PRD phase, told which scope it writes and shown
the packages before it, so it builds on them — `## Dependencies` and the tasks
artifact's `dependencies` name the earlier specs — instead of restating them.
The plan names the package; a model that renames a scope is overruled, with a
warning.

The split is recorded in the spec root as `<first_scope>.split.json` while it
is unfinished. A run that stops early — a budget hit on the third of four
scopes, a lost connection — leaves the plan and the packages it wrote behind,
and **running `spec` on the same input again resumes from the scope that
failed**: no PRD phase for the whole input, no second copy of the first
package. A file or issue input is matched by where it came from, so an input
edited between runs still resumes; text and stdin are matched by content. The
plan is removed by the run that writes the last package, so its presence means
exactly one thing: `spec` stopped before it was done. A plan for a *different*
input is reported as a warning and left alone.

A package that does not validate stops nothing: it is on disk, its errors name
the rules, and the scopes after it are written. The run then exits 1 with
`category: "invalid_spec"` naming the packages to fix. Every other failure
stops the run on the scope it happened in, with the scope named in the message
and `result.split` showing what exists.

| Flag | Default | Effect |
|---|---|---|
| `--specs-dir` | `<dir>/.specs`, or `$AF_SPEC_DIR` | where `NN_name` packages live |
| `--name` | the model's choice | override the spec name; must match `[a-z][a-z0-9_]*` |
| `--architecture` | off | also write the optional `architecture.md` |
| `--no-activate` | off | leave a valid package in `draft` instead of activating it |
| `--comment` | off | post the finished PRD back to the issue the input came from |
| `--dry-run` | off | write nothing to disk or GitHub; report the package that would be written |

Bounds: 60 turns, $5.00 per phase.

### The project audit

The skill this replaces asks a human to read `tasks.json` afterwards and check
that `test_commands` names the project's real runner, because a model planning
work in a repository it did not look at reaches for whichever ecosystem its
training favours. Here the project's language is detected from its manifest and
a plan naming another ecosystem's runner is **refused** before the file is
written, with the real commands in the message:

```
this project is go (detected from go.mod), but test_commands.all_tests is
"pytest -q", which is a python command.
Use the project's real commands: all_tests "go test ./... -count=1",
linter "go vet ./...". Check task.steps, task.touches and task.done_when for
the same mistake — steps must use go constructs, touches must name paths that
fit this project's layout, and a stub marker here is `panic("not implemented")`.
```

The check is narrow on purpose: `go test` in a Python project is unambiguously
wrong, while `./scripts/ci.sh` is legitimate and a stricter check would reject
it.

### The result

`result` describes the first package this run wrote: `spec_dir`, `spec_id`,
`spec_name`, `title`, `status`, `source`, the `artifacts` written, the counts,
and:

- `validation` — the format's verdict, with every error naming its rule
  (`C1`…`C11`, `json_schema`, `completeness`)
- `traceability` — derived, never stored: `criteria_covered`,
  `criteria_uncovered`, `paths_covered`, `paths_uncovered`, `tests_unowned`
- `open_questions` — the decisions made under uncertainty

When the input was more than one spec's worth of work, three fields join them:

- `follow_on_specs` — the further packages this run wrote, in order, each
  with the same fields as the top level
- `split` — every scope of the split, first one first, with its `name`,
  `scope`, and `status`: `done`, `invalid` (on disk, does not validate),
  `failed` (this run stopped here) or `pending`; `spec_id` and `spec_dir`
  once the package exists
- `split_plan` — the plan file, present only while the split is unfinished

`ok` is true only when every scope has a valid package. A caller that wants
to know what happened reads `split`; a caller that wants the rest written runs
`spec` on the same input again.

A package that does not validate is still written, and the run exits 1 with
`category: "invalid_spec"`. A spec you can read and fix is worth more than no
spec at all, and the errors name the rules that broke. It is not activated.

---

## `impl`

Implements a version 2 specification package in a repository: one model phase
per task, in the plan's order, each verified by the spec's own checks before
it is committed, on one branch that is then landed the way `fix` lands.

```sh
impl 09 --dir ~/src/widgets
impl .specs/09_agent_mode --land branch
impl agent_mode --task 2 --no-survey
impl 09_agent_mode --dry-run --total-budget 20
impl 09 --repair --repair-model ADVANCED
```

The input names a spec package rather than describing a problem: a directory,
a spec id, a spec name, a directory name, or a file inside the package. It is
resolved against the spec root (`--specs-dir`, else `$AF_SPEC_DIR`, else
`<dir>/.specs`). A GitHub URL is refused: it is the one input shape the other
tools accept that has no meaning here. Note that a directory argument is
classified as `text` by the shared input rules, which is expected — `impl`
reads the text as a reference, not as a document.

Pre-flight refuses, before a token is spent: a dirty tree; a package that does
not validate (`invalid_spec`, with the rules named); a `sealed`, `superseded`
or `archived` package; a `test_commands` entry that needs a shell or belongs
to another ecosystem than the project's; a check that cannot run before any
change; and, for `--land=pr`, a missing credential or target repository. A
`draft` package is implemented with a warning. A package whose `dependencies`
name an upstream spec that is neither sealed nor done stops the run with exit
3; an upstream that is not in the spec root is a warning.

### The branch

The run works on `impl/<NN>-<slug>`. When that branch already exists it is
checked out and **continued**: tasks marked `done` are skipped, a task a
parked run left `in_progress` is reopened, and a parked `wip:` commit at the
tip is discarded so the task starts again from the last landed commit. That
is what makes a second run of `impl` on a half-implemented spec finish it
rather than start over. `--branch` names the branch explicitly, with the same
continue-or-create rule.

The branch is chosen and checked out before the package is read and before
the checks run, so the state a second run reads is the state the first one
committed.

### The gate

The format's implicit definition of done has four clauses: the task's own
tests exist and pass, `all_tests` passes, `linter` passes, and every
`done_when` entry holds. `impl` measures the two it can — `test_commands.linter`
then `test_commands.all_tests`, run before any change and after every task,
and compared pair by pair as `fix` compares its single command — and holds
the model to the other two at the tool boundary. The gate that passed after
task N is the baseline for task N+1, so `regressed` always means "this task
broke it".

| Verdict | Means | Landed |
|---|---|---|
| `pass` | green before, green after | yes |
| `pass_was_already_failing` | red before, green after | yes, and the report says so |
| `regressed` | green before, red after | no |
| `still_failing` | red before, red after | no |
| `gate_failed` | a check could not run after the task | no, and no retry: the work was never measured |
| `unverified` | nothing ran | only with `--no-verify` |

`--verify` replaces the pair with one command; `--no-verify` runs nothing.

### Repairing the checks

A repository whose checks fail before any change is not refused: the run
records the red baseline and judges every task by comparison, so a task that
leaves the failure exactly as it found it lands as `pass_was_already_failing`
once it is green, and one that keeps it red is `still_failing`. That is the
right default for a suite with one known-broken test, and the wrong one for a
suite whose failure hides every regression the tasks might introduce.

`--repair` makes a red gate a phase of its own at the two points where the
whole suite is what matters: **before the first task**, when the baseline is
red, and **after the integration task**, when its checks fail. On a green
gate the flag does nothing. The `repair` phase gets the failing commands and
their output, the survey brief, and the spec for orientation only; it has the
same tools as the implementation phase and the same refusal of the spec
package. After it, the gate runs again, and the bar is green, not "no worse
than before".

Before the first task:

- Green: the change is committed as `fix: <subject>` with a `Spec: <dir>,
  repair` trailer, the first commit on the branch, and the green gate is the
  baseline the first task is compared with. The result's `baseline` still
  records the red one — that is the truth about where the branch started —
  and the pull request says the checks were repaired first, with the cause.
- Still red: the attempt is discarded and tried again with the failing output
  and its diff stat in the prompt, up to `--repair-attempts` (default 3).
  The last failure parks the attempt as a `wip:` commit, returns the checkout
  to the base branch, and exits 4 with **no task implemented**. A second run
  discards the parked repair and starts it again.
- A `blocker` — the checks need a credential, a service or a tool the
  machine does not have, so no change to the code could fix them — exits 3
  with the question.

After the integration task, which is the last task and the one that runs the
spec's smoke tests against the real components, its verification is the run's
final "run all tests". Without the flag a red gate there is a failed attempt
like any other: discarded and retried from scratch. With it, the failure is
repaired **on top of the task's work** instead, because what the smoke tests
find is usually a wiring gap between earlier tasks, which redoing the last
one would not close:

- The task's change is held in a provisional commit, so every repair attempt
  starts from it and a discarded attempt goes back to it, not to the commit
  before the task. The phase is told where the work stands, which tests the
  task owns, and what the earlier tasks did.
- Green: the hold is undone and task and fix land as **one** `feat:` commit,
  whose body says the checks failed after the task and were repaired, with
  the cause. The task's entry in the result carries the `repair` report.
- Still red after `--repair-attempts`: task and last attempt are parked
  together as the task's `wip:` commit, the run exits 4, and the next run
  discards it and implements the task again.
- A task whose own report answers `fail` for a test it owns is not repaired:
  its author says it is not done, and it is retried from scratch as before.
  An earlier task's red gate is never repaired either.

`--repair-model` runs the repair phase on another model — a tier such as
`ADVANCED`, or a catalog spec — resolved against the same `--vendor` and
`--variant` as the run's model and checked for its credential before anything
runs. It implies `--repair`. The rest of the run stays on `--model`. It is
for the repository whose failure needs more reading than the model chosen for
the tasks would do; the tasks themselves are not made cheaper or dearer by it.

### One task

For each task that is not done, in array order:

1. The task moves `pending → in_progress`, in memory.
2. The `implement` phase runs with the spec rendered scoped to the task
   (§11.1 of the format), the survey brief, the reports of the tasks before
   it, the gate and its baseline, the project's `AGENTS.md` and
   `.specs/steering.md` as labelled material, and — on a second attempt —
   why the first one did not land.
3. Any change under the spec package is reverted and reported as a warning:
   the state file is the program's.
4. `git` lists what differs. A task that changed nothing is a failed attempt.
5. The gate runs and is compared with the baseline.
6. A landable verdict, with a report that answers `pass` for every test the
   task owns and every `done_when` entry, marks the task `done`, writes
   `tasks.json`, and commits everything as `feat: <subject>` with a `Spec:`
   trailer naming the package and the task.

Anything else is a failed attempt. The first one is discarded — the branch is
reset to the last commit and the tree cleaned — and the task is tried once
more with the failure in its prompt (`--task-attempts`, default 2). The last
failure parks the work as a `wip:` commit with the task recorded as
`in_progress`, returns the checkout to the base branch, and exits 4. A run
cancelled mid-task is parked the same way, under a context the cancellation
does not reach, so Ctrl-C never leaves a dirty tree on the branch.

`submit_task` refuses a report that skips a test the task owns, names one it
does not, answers with a bare word, or has no commit subject — the phase
cannot end without an answer for every test. A report that answers `fail`
is accepted: it is an honest account of a task that is not done, and it is
treated as a failed attempt rather than refused, so the model is not taught
to hide a failure.

### The survey

Before the branch is created, one read-only phase reads the whole spec and
the repository and submits a brief: where the things the spec names actually
live, the conventions a coder must follow, and every place the spec's
assumptions and the code disagree, each with a resolution. The brief goes
into every task prompt. It is also the one place the run may stop to ask: a
spec that cannot be implemented as written is reported as a `blocker`, the
run exits 3 with the question, and no branch exists. A task phase may raise
the same blocker; the work so far stays on the branch. `--no-survey` skips
it, for a driver that runs one task at a time.

| Flag | Default | Effect |
|---|---|---|
| `--specs-dir` | `<dir>/.specs`, or `$AF_SPEC_DIR` | where `NN_name` packages live |
| `--task N` | every task not done | implement only task `N`; its dependencies must be done |
| `--branch` | `impl/<NN>-<slug>` | the branch to work on, created if missing and continued if present |
| `--land` | `pr` | `pr` · `branch` (push only) · `none` (commit only) |
| `--repo owner/repo` | the `origin` remote | where the pull request is opened |
| `--dry-run` | off | make no *remote* change: push nothing, open nothing. The branch and the commits are still made |
| `--verify` | the spec's `linter` and `all_tests` | one command that decides success instead |
| `--no-verify` | off | run nothing; every task is then `unverified`, not a pass |
| `--verify-timeout` | `10m` | timeout for one check command |
| `--push-attempts` | `4` | push retries, with exponential backoff |
| `--allow a,b` | — | extra programs the implementation phases' shell may run |
| `--draft` | off | open the pull request as a draft |
| `--pull` | off | checkout and pull the base branch from `origin` first |
| `--no-survey` | off | skip the survey phase |
| `--task-attempts` | `2` | implementation attempts per task before the run parks |
| `--total-budget` | none | spend ceiling for the whole run; a run that reaches it stops between tasks, with everything landed so far committed |
| `--repair` | off | repair the checks when they fail before the first task or after the integration task; the run stops if it cannot |
| `--repair-attempts` | `3` | repair attempts before the run gives up |
| `--repair-model` | the run's model | model tier or catalog spec for the repair phase alone; implies `--repair` |

Bounds: 150 turns, $5.00 per phase — and there is one phase per task, so a
twelve-task spec can cost twelve times what a `fix` does. `--total-budget`
caps the run.

`result` carries `stage`, the package (`spec_dir`, `spec_id`, `spec_name`,
`title`, `status`), `branch`, `base_branch`, `resumed`, the counts
(`tasks_total`, `tasks_done`, `tasks_skipped`, `tasks_remaining`), one entry
per task under `tasks`, in the plan's order — its `outcome` (`pending`,
`done`, `skipped`, `unverified`, `blocked`, `failed`, `aborted`), `attempts`, `commit`, `changed_files` and
`diff_stat` from git, its `verification` gate and `verdict`, `tests_outcome`,
and the model's own `submission` kept separate — plus `gate`, `baseline`,
`verification`, `verdict`, `pushed`, `pull_request_url`, the `survey`, any
`blocker`, and `cost_usd`. With `--repair`, a repair is reported the same
way — `outcome`, `attempts`, `model` when it differed, the `failing` gate,
`commit`, `changed_files`, `diff_stat`, `verification`, and the model's
`submission` with its `cause` — as `repair` at the top level for the
baseline and on the integration task's entry for the one after it.

### What the model may and may not do

The survey phase is read-only, with `execute` under the reporting allowlist.
The implementation and repair phases have the file tools and a shell under the same guard
as `fix`'s — `git` read-only, `gh` refused, `find -exec` refused — with one
addition: `write_file` and `edit_file` refuse any path under the spec package,
so "do not modify the spec" is a refusal rather than a request. The task's
state, the commit, the push and the pull request are the program's.

## Environment

| Variable | Purpose |
|---|---|
| `AF_MODEL` | model tier or catalog spec for every phase; `AGENTKIT_MODEL` is a fallback |
| `AF_MODEL_VENDOR` | which tier table `SIMPLE`/`STANDARD`/`ADVANCED` resolve against |
| `AF_SPEC_DIR` | the spec root (`spec` and `impl`); `--specs-dir` wins |
| `GITHUB_TOKEN`, `GH_TOKEN` | GitHub credential. Reading a public issue needs none; every write does |
| `GITHUB_API_URL` | a GitHub Enterprise host; its host is then also accepted for `origin` |
| vendor keys and base URLs | see [Configuration](configuration.md) |

## See also

- [Configuration](configuration.md) — credentials, model selection, bounds
- [Model Usage](model-usage.md) — what each phase sends, and how a failure is repaired
- [ADR 03](adr/03-rebuild-the-skills-as-tools.md) — why the tools are shaped this way
- [ADR 04](adr/04-implement-a-spec-as-a-tool.md) — how `impl` differs from the orchestrator it replaces
