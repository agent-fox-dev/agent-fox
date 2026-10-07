# Rebuild `impl` on the shared change engine

Status: **proposed**. Builds on [PRD 13](13-rebuild-fix-on-a-shared-change-engine.md),
which specifies the engine (`internal/lang`, `internal/engine`, the gate
cache, the ledger, parking, the run brief, `run_checks`) and rebuilds `fix`
on it. This PRD adds what `impl` needs: the spec as the plan, one phase per
task, the conformance stage, continuation, and the token discipline of a run
that is ten times longer than a `fix`. AgentKit
[PRD 06](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/06-support-multi-phase-coding-pipelines.md)
supplies the SDK side.

## Intent

`impl` keeps its interface. Every flag in [`docs/cli.md`](../cli.md)'s
`impl` table, the envelope, the exit codes, the events, the branch name, the
commit shapes and the pull request's sections stay as documented. The machine
behind it changes, for the reasons PRD 13 gives and three of its own.

**Its cost is proportional to the wrong things.** The one fully accounted run
(spec 16, nine tasks: agent-fox #190 to #200) cost $15.38 over 420 turns and
2 h 28 min. Of that, 42 minutes were the program running the suite fifteen
times, 30 minutes were the model running it thirteen more, and every turn
carried a prompt of about 100 000 cached tokens: the whole specification
(22 000 tokens) was sent to the survey, the repair, the resolve and both
reviews, the scoped specification plus the survey brief plus the project
instructions plus the steering file plus a growing list of prior tasks to
every task, and every task phase was a fresh agent whose tool schemas carried
the task's own test ids and a random scratch path, so the provider's cache
had to be rebuilt nine times. A discarded attempt threw away 42 turns of
work over a weak test (#200) and the second attempt re-read every file the
first one had read.

**Its judgement is Go's.** The gate commands come from the spec, which is
right; but the audit that checks them knows six ecosystems and refuses
`npm test` in a repository that also has a `go.mod`; the scope check exempts
a test file only when it spells a test id the Go way; the revert check
cannot see a Rust inline test; the structural stage runs `gofmt` and
`go vet` and parses Go, and for a Python or TypeScript change reports
nothing about formatting, lint, long functions or assertion-less tests
while the pull request presents the same table; the hermetic environment
pins Go, Rust, npm, uv and pip caches and no JVM, .NET or Ruby ones; the
fingerprint records `go_version` only.

**Its state is partly in memory.** A task's declared deviations live in the
run's result, so a second run on a half-implemented spec cannot see what the
first one declared and turns it into a blocker (#218). The resolve phase is
not shown the out-of-scope files or the review's `partial` and `weaker`
rows, and `--total-budget` is not checked before the two review phases. A
cancellation inside the review is a warning and the run can exit 0.

## Goals

1. Every goal of PRD 13, for `impl`: language as data, one engine, nothing
   lands by accident, cancellation is `aborted`, the interface is untouched.
2. **A task costs what the task is.** The prompt a task phase sends is the
   run brief (constant, cached) plus the task: its own requirements, tests
   and steps, the baseline verdict, one line per landed task. On spec 16
   the first turn of a task phase sends at most 35 000 context tokens, and
   the brief is served from cache on every task after the first.
3. **The suite runs once per landed change.** A green run of N tasks runs
   the full gate N+1 times (baseline, one landing gate per task, the last
   one hermetic) and not once more; the model's own runs are the landing
   gate when nothing changed after them.
4. **A failed attempt keeps what was right.** The second attempt continues
   the first one's agent with the failure appended, on the first attempt's
   tree, instead of discarding both.
5. **Continuation is complete.** A second run sees everything the first
   one established: landed tasks, their declared deviations, the survey's
   decisions.
6. **Half the tokens.** On spec 16 and on one spec each in a Python and a
   TypeScript repository, `impl` lands the same tasks for at most half of
   today's summed `context_tokens` and at most 60% of the cost.

## Non-goals

- Changing the spec format or `afspec`. `tasks.json` stays the state file
  and `touches`, `tests`, `done_when` and `test_commands` keep their
  meaning.
- Parallel tasks, worktrees, or a daemon. ADR 04's argument stands.
- A new flag. `--task-attempts`, `--repair*`, `--no-survey`, `--no-review`,
  `--no-test-first` and the rest keep their documented meaning; §4.3 says
  what "attempt" means now.
- Resuming a phase across a process restart. AgentKit's session store
  (`session`, `NewAgentFromSession`, `Agent.Continue`) makes it possible;
  it is a later PRD.

## 1. The gate is the spec's, audited against every language

- `test_commands.linter` and `test_commands.all_tests` stay the gate, run
  in that order, lint first so a formatting slip does not cost a suite run.
- The audit (`internal/lang`) refuses a command whose program belongs to an
  ecosystem the detection did **not** find. A repository that is Go and
  Node accepts either; a `pytest` in a Gradle repository is refused with
  the profile's own commands in the message, as today. A `./gradlew test`
  or `./scripts/ci.sh` is a script and passes.
- The targeted form, the test-file convention, the inline-test rule, the
  skip markers, the gate-config files, the hermetic caches and the version
  probe all come from the detected profiles. `environment` in the result
  gains one `<profile>_version` per detected profile and loses nothing.
- A `test_commands` entry that needs a shell is still refused in preflight;
  the message now names the Makefile target or script that would carry it.

## 2. The run brief

The engine's run brief (PRD 13 §6.1) is one message, rendered once at run
start and byte-identical for every task, repair and resolve phase. For
`impl` it holds, in this order:

1. the language block (detected profiles, stub idiom, targeted form);
2. the repository map at run start, at `--repo-map-tokens`;
3. the specification's constant part: the PRD and the architecture
   document in full, the external APIs, and a **digest** of the rest: every
   requirement id with its title, every test id with its title, every task
   as one line with its kind and `touches`;
4. the survey brief;
5. the scope list, when the spec restricts it;
6. the gate commands and where their logs are written;
7. the project instructions and the steering file, labelled as
   repository-authored material;
8. `--context`.

The specification's files are in the repository under the spec root, so a
phase that needs a contract the digest only names reads it with `read_file`
or the symbol tools; the message says so and names the package directory.
The survey phase, whose job is to read the whole spec, gets it in full as
its task message and is the only phase that does.

**The map does not churn.** The brief's map is built once. Each later phase
message carries a **map delta**: files the ledger added or removed since the
brief, and declarations that changed in files the run touched, from the
outline. A delta is a few lines; the map in the brief stays cached.

## 3. The task message

What follows the brief in a task phase, in this order, and nothing else:

1. the task's entry from `tasks.json` in full (`steps`, `touches`, `tests`,
   `done_when`, `depends_on`), and the requirements and tests it owns,
   rendered in full from the spec;
2. the definition of done, as today, with `DW-n` ids;
3. the gate's baseline for this task: each command's verdict and the path
   of its log; the last ten lines inline only for a command that failed;
4. the tasks landed before it, **one line each** (id, title, files), and
   their gotchas; a task's full summary is in its commit, which the model
   can `git show`;
5. the map delta;
6. on a continued attempt, the failure (§4.3);
7. today's date and the branch, which change per run or per day and so sit
   last.

The per-task ids leave the tool schema and the guidelines: `submit_task`'s
schema is the same bytes for every task, and the ids the handler checks
against are given to it in Go, not described to the model in the schema.

## 4. One task

### 4.1 The phase

Tools: the read tools, `write_file`, `edit_file`, `execute` under the
profiles' build allowlist, and `run_checks` (PRD 13 §6.3) with one more
scope:

```
run_checks(scope: "lint" | "targeted" | "all" | "revert", tests?: [string])
```

`revert` runs the revert check (PRD 13 §5.3) on the current tree for the
tests the task owns and reports `proves` and the declarations put back, so
the model learns the verdict before it submits instead of after a discarded
attempt (#200). The shell refuses the suite's own form and names
`run_checks`; a targeted run is never refused.

The spec package is protected: `write_file` and `edit_file` under it are
refused, and a change that reaches it through the shell is reverted by the
ledger before the gate, as today.

### 4.2 Landing

After the phase, in order, each a step of the engine:

1. the ledger classifies the change, reverts protected paths, drops scratch
   paths, flags gate-config edits, deleted tests, skip markers and golden
   rewrites;
2. an empty change is a failed attempt;
3. the gate runs on the tree, or is answered from the cache when the phase's
   last `run_checks all` ran on this tree; for the last task it runs in the
   hermetic environment and the one run is both the landing verdict and
   `final_verification`;
4. the revert check runs when test-first was waived, targeted;
5. a landable verdict with a report that answers `pass` for every owned
   test and `done_when` lands: `tasks.json` written, the commit made with
   the `Spec:` trailer and one `Deviation:` trailer per declared deviation
   (§5.3), the index invalidated, the gate result stored as the next task's
   baseline.

### 4.3 A failed attempt continues

`--task-attempts` keeps its meaning: the number of phases a task may cost
before the run parks. What changes is the second phase. Today it starts from
a hard reset with the failure in its prompt and no memory of the first; it
re-reads every file and often re-creates the same work (#200).

- The second attempt **continues the first agent**: the first attempt's
  tree is kept, its transcript is kept, and the failure is appended as the
  next user turn (AgentKit's follow-up): which command failed with its
  verdict and log path, which owned tests the report answered `fail`, what
  the revert check put back and that the tests still passed. The model's
  file reads stay cached; the prompt grows by the failure, not by a second
  copy of everything.
- A discard happens in one case only: the first attempt's change made the
  gate `regressed` **and** the model's own report answered `fail` for a test
  it owns, which is the model saying the approach is wrong. Then the second
  attempt is fresh, from the last commit, with the failure in its message,
  as today.
- The last attempt's failure parks as today, with `tasks.json` recording
  `in_progress`, and exits 4 on every category except usage; `aborted`
  exits 1 and is resumable.

### 4.4 Between tasks

Before each task, as today: the dependency check, `--total-budget` (now
counting compaction and summarization spend, AgentKit PRD 06 §5), the
context's cancellation. A cancelled run between tasks leaves everything
landed and reports `aborted`.

## 5. The conformance stage

The stage keeps ADR 09's shape: facts first, an independent review, one
chance to answer, the pull request says what is true. Five things change.

### 5.1 Facts for every language

The structural scan, the scope check and the hermetic gate are PRD 13 §7's:
formatting and lint from the profiles' commands, long functions and unused
declarations from the outline, assertion-less tests from the profile's
assertion pattern, test-id matching by the profile's spelling, and the
hermetic environment pinning the detected profiles' caches. The scope
exemption for a test file that names an in-scope test id (#203) uses the
profile's `TestID`, so a `test_ts16_14` in Python is exempt like a
`TestTS16_14` in Go.

### 5.2 The review reads contracts, not the package

The review's message carries the ids in scope with their **contracts in
full** — the requirement texts, the test entries, the decisions — and the
digest of the rest, instead of the whole combined render. The diff is not
inlined: the message names the merge-base and the ledger's file list, and
the model reads the diff with the shell as today. The second review after a
landed resolve sees only the rows the resolve phase changed and the rows
that were not `implemented`/`asserts_contract`/`followed`, with the rest
carried over from the first review's accepted table; it is a fraction of
the first.

### 5.3 Declarations live in git

A deviation a task declares is written as a `Deviation: <key>: <reason>`
trailer on the task's commit, beside `Spec:`. The conformance stage reads
declarations from `git log <base>..HEAD` and from the resolve phase's
submission, merged by key with the later reason winning. A second run on
the branch therefore sees the first run's declarations; a review that finds
a declared key met retracts it, as #202 built. Nothing about declarations
lives only in the run's memory.

### 5.4 Resolve sees everything, and is budgeted

The resolve phase is triggered by, and shown, every kind of finding: the
blockers, the structural findings, the hermetic failure, the out-of-scope
files, and the review's `partial` and `weaker` rows (which it may fix or
declare, with the row then re-reviewed). `--total-budget` is checked before
the review, before resolve and before the second review; a run that stops
there says so in `error.message` and lists what was not reviewed as unmet,
instead of silently promoting it to blocking.

### 5.5 Cancellation is a stop

A cancelled review or resolve phase ends the run with `aborted`: the
branch is left as it is, nothing is pushed, and a re-run continues from the
conformance stage because every task is already `done`.

## 6. Repair

`--repair`, `--repair-model` and `--repair-model-effort` keep their
meaning. The repair phase is a writing phase over the engine with the run
brief, the failing commands and their log paths, and the digest of the
spec; it has `run_checks` with `all` allowed, since its job is the suite.
Its attempts continue like a task's (§4.3). A repair's files are exempt
from the scope check, since the pull request already presents the repair
as outside the specification.

## 7. Prompts and tool text have no language

The implement, repair, resolve and survey mandates lose every Go example;
the targeted-test hint, the stub idiom, the formatter's name and the
`file:line` examples are rendered from the detected profile. The tasks
template of `spec` (which this PRD does not rebuild) is listed in
PRD 13 §1's denylist test so its Go-only worked example is replaced in the
same session.

## 8. What the envelope gains (additive)

- Everything PRD 13 §8 adds.
- `result.tasks[].attempt_mode`: `fresh` or `continued`.
- `result.tasks[].deviations[]`: the keys and reasons the commit carries.
- `result.environment.<profile>_version` per detected profile.
- `result.conformance_budget_stop`: present when `--total-budget` stopped
  the stage, naming the phase it stopped before.
- `usage.phases[].cached_prefix_tokens`: the cache-read tokens of the
  phase's first turn, which is what the run brief saved.

## 9. Order

1. PRD 13 steps 1 to 3: AgentKit PRD 06, `internal/lang`,
   `internal/engine`, `fix`.
2. `codeimpl` rewritten over the engine: the gate audit on the detection
   (§1), the brief and the task message (§2, §3), `run_checks revert`,
   continuation (§4.3), the deviation trailers (§5.3).
3. The conformance stage on the language-neutral scan and the contract
   review (§5).
4. `internal/project` and the detection half of `internal/checks` deleted;
   `spec` and `triage` moved to `internal/lang` for the audit and the
   language block.
5. The baseline tables and a per-language fixture set under
   `testdata/lang/<profile>/spec/`.

## Acceptance criteria

- On spec 16 replayed against the scripted provider, the task phases' tool
  schemas and system prompt are byte-identical across all nine tasks, and
  the run brief is byte-identical in every phase that carries it.
- On a Python and a TypeScript fixture spec with three tasks each,
  `impl` lands every task with `verdict: pass`, the revert check in
  `targeted` mode, `environment.python_version` or `node_version` set, a
  formatting finding when a file is left unformatted, and exactly four
  full-gate runs in `timings`.
- A polyglot fixture (`go.mod` and `package.json`) with `all_tests:
  "npm test"` passes preflight.
- A task whose gate regresses on attempt 1 is continued, not discarded:
  attempt 2's phase has `attempt_mode: continued`, its first turn's
  `cache_read_tokens` cover attempt 1's context, and the attempt-1 files
  are still in the tree at its start.
- A deviation declared by a task in run 1 is present as a commit trailer,
  reported under `unmet` by run 2 on the same branch, and not reported as
  blocking.
- Ctrl-C during the review phase ends with `aborted`, exit 1, nothing
  pushed, and a re-run resumes at the conformance stage.
- `--total-budget` reached before the resolve phase ends the run with
  `category: budget` and the blockers listed under `unmet`, not `blocking`.
- On spec 16, the summed `context_tokens` per turn is at most half of the
  recorded run's, the cost at most 60%, full-gate runs are ten (nine tasks
  plus the baseline) plus one per resolve landing, and the model never runs
  the suite through the shell.
- The denylist test of PRD 13 §1 covers `codeimpl`.

## Documentation

- `docs/cli.md`: the `impl` sections on the gate, one task (continuation),
  the conformance stage (contract review, trailers, resolve inputs, budget),
  `run_checks revert`, and the additive fields.
- `docs/model-usage.md`: the `impl` phase table (tools and prompts), the
  brief and the task message, the map delta.
- `docs/adr/04-implement-a-spec-as-a-tool.md` amended by a new ADR: a
  failed attempt continues; declarations live in git; the brief.
- `docs/development.md`: the retired packages and the fixture set.
