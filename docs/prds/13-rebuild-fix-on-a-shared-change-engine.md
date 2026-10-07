# Rebuild `fix` on a shared change engine

Status: **proposed**. Companion to [PRD 14](14-rebuild-impl-on-the-shared-change-engine.md),
which rebuilds `impl` on the same engine, and to AgentKit
[PRD 06](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/06-support-multi-phase-coding-pipelines.md),
which adds what the engine needs from the SDK. The three are meant to be
specified together and built in the order §9 gives.

## Intent

`fix` keeps its interface: the same positional input, the same flags, the
same envelope, exit codes, events and report file as
[`docs/cli.md`](../cli.md) documents today. Nothing in this PRD changes a
flag, a field or an exit code. What changes is the machine behind the
interface, for three reasons the current code has taught.

**It treats Go as the language.** The analysis of 2026-10-06
(agent-fox #221) found that Go is the only language for which every
mechanism the docs promise actually fires: verify-command detection knows
five manifests and lands a Maven, Gradle, Gemfile or .NET repository
unverified with exit 0 (#215); the revert check classifies Rust inline tests
as implementation, so no Rust fix is ever proven; the structural checks for
long functions, assertion-less tests and unused declarations parse Go and
silently skip everything else; the shell allowlist has `gofmt` and
`goimports` but no `tsc`, `ruff` or `mvn`; read roots come only from
`go.mod replace`; and the prompts show `go test ./pkg -run Name` to a Python
model. Each of these was convenient to write for the repository the tool was
developed in. None of them is a property of the design.

**It is two copies of one machine.** `codefix` and `codeimpl` carry the same
work twice: the untracked-file snapshot, the "commit only what is ours"
rule, the park, the push with retries, the pull request, the verification
and its comparison, the revert check, the structural scan, the index
invalidation, the project-instructions reader, the preflight checklist, the
failure types, the verdict checks. Where the copies have diverged they have
diverged in the wrong direction: `impl` parks under a context cancellation
cannot reach and skips the repository's hooks on a `wip:` commit; `fix` parks
under the run's context with hooks on, and only on the one path where the
checks failed (#216). `impl` fits its pull request body to the forge's
limit; `fix` sends it unbounded.

**It spends tokens it does not need to.** The one run with full accounting
(agent-fox #194, #195, #196, #197, #200: an `impl` run of nine tasks)
carried a prompt of roughly 100 000 cached tokens on every one of its 420
turns, re-sent the whole specification in five phases, ran the whole test
suite 28 times (15 by the program, 13 by the model), and paid for three
full resubmissions of a 9 500-token review over three rows. `fix` is
smaller and shares every one of those habits: the report is sent three
times, the baseline block twice, the project instructions twice, and every
phase is a fresh agent whose tool descriptions carry a random scratch
directory name, so the provider's cache prefix is rebuilt for each.

The engine this PRD specifies is the part of the machine `fix` and `impl`
share. `fix` is specified here in full because it is the smaller tool and
the engine is best read against the simpler pipeline; PRD 14 adds what
`impl` needs on top.

## Goals

1. **Every language is equal.** No `.go`, `go.mod`, `gofmt`, `_test.go`,
   `go test` or `panic("not implemented")` literal exists outside one
   declarative table of language profiles. A profile is data; adding a
   language is adding a row, and a test asserts that every mechanism in the
   tool reads the profile and nothing else.
2. **One engine.** Branching, the untracked snapshot, commit, park, push,
   the pull request, verification, the revert check, the structural and
   scope checks, cancellation and index invalidation live once, in
   `internal/engine`, and both tools are thin pipelines over it.
3. **Nothing lands unverified by accident.** A repository whose checks
   cannot be detected is `unverified` and parked, exit 4, unless the
   operator said `--no-verify`.
4. **Cancellation is `aborted`.** On every path, a cancelled run is reported
   as `aborted`, leaves no dirty tree on the work branch, and is resumable
   by re-running the same command.
5. **Half the tokens for the same outcome.** On the navigation baseline
   ([`docs/development.md`](../development.md)) and on three target
   repositories in three languages, `fix` lands the same fixes for at most
   half of today's `usage.input_tokens + cache_read_tokens` per turn and at
   most 60% of today's cost, with no more full-suite runs than §5 allows.
6. **The interface is untouched.** The `--schema` golden files do not
   change except by additive fields, `schema_version` stays at its major,
   and every documented flag means what it meant.
7. **Every word the model reads is in a document.** No prompt, tool
   description, guideline, refusal or rejection text is a Go string. Each
   is a Markdown file bundled into the binary, composed and filled from
   the run's facts by one templating engine, rendered to golden files a
   reviewer diffs, and written out by every run so what a phase was told
   can be read afterwards.

## Non-goals

- A new flag, a new exit code, a new result field a caller must read, or a
  change to any event type. Additive `result` fields that record what the
  engine measured are allowed and listed in §8.
- A language server, a type checker per language, or exact parity between
  languages in what the structural checks can see. Equality means the same
  mechanism runs for every language with that language's own conventions;
  it does not mean Go loses what `go/ast` gives it.
- Parallel phases or worktrees. A `fix` is one branch and one sequence.
- Changing the spec format, `afspec`, `triage` or `spec`. They keep using
  `internal/project` until PRD 14 retires it.
- Per-project prompt overrides. ADR 03 declined them because a repository
  that can rewrite the system prompt of the agent reading it is a trust
  boundary, and `--trust-project` is the narrow grant. The prompt documents
  of §6.6 are the program's, bundled into the binary; a developer-supplied
  prompt directory would be a new configuration surface and is not part of
  this PRD.

## 1. The language profile

`internal/lang` replaces `internal/project` and the detection half of
`internal/checks`. It has one exported table and one function:

```go
type Profile struct {
    Name       string   // "go", "python", "node", "rust", "jvm", "dotnet", "ruby", "php", "elixir", "swift", "generic"
    Manifests  []string // files whose presence selects it: go.mod; pyproject.toml, setup.py, setup.cfg, requirements.txt; package.json; Cargo.toml; pom.xml, build.gradle, build.gradle.kts, settings.gradle; *.csproj, *.sln; Gemfile; composer.json; mix.exs; Package.swift
    // Commands, each a Command: argv split without a shell, plus the
    // variant to prefer when a lockfile or wrapper is present (uv.lock,
    // poetry.lock, pnpm-lock.yaml, yarn.lock, bun.lockb, gradlew, mvnw).
    Test, Lint, Format, Build Command
    // Targeted is how to run one test file or one test name:
    // go test {pkg} -run {name}; pytest {file}::{name}; npm test -- {file} -t {name};
    // cargo test {name}; mvn -q -Dtest={class} test; dotnet test --filter {name};
    // bundle exec rspec {file}:{line}. Empty when the ecosystem has none.
    Targeted Command
    // Programs the implementing shell may run: the toolchain's own
    // binaries, nothing else. Build programs are the union of every
    // detected profile's Programs plus the detected commands' own programs.
    Programs []string
    // TestFile reports whether a path is a test by this language's
    // conventions; InlineTests reports whether the language keeps tests
    // inside implementation files (Rust #[cfg(test)], Python doctests,
    // Elixir doctests), in which case the revert check works by test
    // name, not by file (§5.3).
    TestFile    func(path string) bool
    InlineTests bool
    // TestID matches a spec test id in a test's source the way this
    // language spells identifiers: TS_16_14 / ts16_14 / TS-16-14 in a
    // function name, a decorator, a describe/it string, a comment.
    TestID *regexp.Regexp
    // Stub is the idiom for "not implemented": panic("not implemented"),
    // raise NotImplementedError, throw new Error("not implemented"), todo!(),
    // throw new UnsupportedOperationException(), raise NotImplementedError.
    Stub string
    // SkipMarkers are the ways a test is disabled; GateConfig the files
    // that decide what the checks run (Makefile, .golangci.*, eslint.config.*,
    // ruff.toml, pytest.ini, tox.ini, clippy.toml, pom.xml, *.csproj, .rspec).
    SkipMarkers *regexp.Regexp
    GateConfig  func(path string) bool
    // ReadRoots lists the local directories this ecosystem's manifest can
    // point a dependency at: go.mod replace, package.json "file:" and
    // workspaces, Cargo [dependencies].path and [workspace], pyproject
    // path/editable and uv/poetry workspace members, Gradle includeBuild.
    ReadRoots func(root string) []string
    // Caches are the download caches a hermetic run keeps: GOPATH,
    // GOMODCACHE, GOCACHE; UV_CACHE_DIR, PIP_CACHE_DIR; npm_config_cache,
    // PNPM_HOME, YARN_CACHE_FOLDER; CARGO_HOME, RUSTUP_HOME; MAVEN_OPTS
    // -Dmaven.repo.local, GRADLE_USER_HOME; NUGET_PACKAGES; BUNDLE_PATH.
    Caches []string
    // Version is the probe for the environment fingerprint: go version,
    // python --version, node --version, cargo --version, java -version,
    // dotnet --version, ruby --version.
    Version Command
}

func Detect(root string) Detection
```

`Detection` is a **set**, not one profile: every profile whose manifest is
at the root, in a fixed order, with `Primary` the first. A `go.mod` beside
a `package.json` is a Go-and-Node repository, the build programs are the
union, the test classifiers are the union, and a `tasks.json` whose
`all_tests` is `npm test` is not refused by the audit. A repository with no
manifest is `generic`: no toolchain, Makefile targets only.

**Verification detection** is one function on the detection, and the same
function for `fix` and `impl`:

1. a `Makefile` target `check`, else `test` (and `lint` for the linter);
2. else, for the primary profile, its `Test` command in the lockfile
   variant, and its `Lint` command when its configuration file is present;
3. else nothing.

Nothing is detected is a first-class outcome, and it is not landable (§5).

**The profile is the only place a language is spelled.** A test in
`internal/lang` walks every non-test Go file under `internal/engine`,
`codefix` and `codeimpl` and fails on any literal from a denylist (`.go"`,
`go.mod`, `gofmt`, `go test`, `go vet`, `_test.go`, `panic("not`,
`pytest`, `cargo`, `npm`, `.py"`, `.ts"`, `.rs"`). The denylist is the
acceptance test for goal 1.

## 2. The engine

`internal/engine` is what both tools drive. It is a library of steps over
one `Run` value, not a framework: each tool's pipeline is still a readable
sequence in its own package.

```go
type Run struct {
    Root      string
    Git       *gitx.Git
    Lang      lang.Detection
    Gate      Gate            // the commands that decide success, §5
    Branch    Branch          // base and work branch, §3
    Ledger    *Ledger         // every change the run owns, §4
    Index     Invalidator     // code-search index and symbol table
    Scratch   string          // one scratch directory per run, §6.2
    Phase     *agentrun.Runner
    Progress  toolio.Progress
    Record    *toolio.Run
}
```

Every step is a function of a `Run` and returns a typed outcome. Both
pipelines are then: preflight, phases, verify, land, each a call into the
engine, and the differences between `fix` and `impl` are the phases they
run and what they put in the prompts.

### 2.1 Facts first, in Go

The rule from ADR 03 stands and is restated as the engine's invariant:
anything the envelope reports as a fact — the branch, the commit, the files
changed, the verdict, the pull request, the proof — is established by the
engine from git, the filesystem, a command's exit status or the forge's
response. A phase's report is labelled `model` and never promoted.

## 3. The branch

- The base is the branch checked out when the run starts (fixed in #225),
  and a run started on a detached HEAD or on a branch the engine itself
  would create (`fix/*`, `impl/*`) is refused in preflight.
- The work branch is created from the base after the analysis phase and
  before anything writes, as today; `UniqueBranchName` asks the remote once
  per run, not once per candidate, and a failed lookup is a warning, not
  "the name is free".
- The engine records the base, the branch and the merge-base in the `Run`
  once. Nothing re-derives them.

## 4. The ledger

The ledger is the single answer to "what did this run change". It replaces
the untracked snapshot, `ChangedFiles`, `DiffSince`, `commitOwn`, `listed`,
`unlisted`, `leftAlone` and the spec-directory revert in both tools.

- Before any writing phase it records the untracked files already in the
  tree and the paths the run **owns** (`.agent-fox/scratch/`, and in `impl`
  the spec package) and the paths that are **protected** (program-owned:
  the spec package, the state file).
- After a phase it computes the change as git's name-status from the last
  commit plus the untracked files not in the snapshot, classifies each path
  with the profiles (`test`, `docs`, `implementation`, `gate-config`,
  `scratch`, `protected`), reverts protected paths with a warning, drops
  scratch paths, and flags gate-config edits, deleted tests, skip markers and
  golden rewrites, as `impl` does today and `fix` does not.
- A commit is "ours": the classified change minus the left-alone untracked
  files, with `git add` by path, never `add -A`. A new file the phase's
  report did not list is committed with an `unlisted_file_committed`
  warning, as today.
- The ledger is what the revert check, the structural scan, the scope check,
  the pull request body and `result.changed_files` all read. One list, one
  classification.

## 5. Verification

`internal/engine` owns the gate; `internal/checks` keeps `Result`, `Compare`
and `Verdict`.

### 5.1 One runner

Every external command — a check, `git`, `gofmt -l`, a version probe — runs
through one runner with a process group, `WaitDelay`, the reduced
environment, and a result that tells a **timeout** from a **cancellation**
from a **failure**: `ExitCode`, `TimedOut`, `Aborted`. `checks.Compare`
returns a new verdict, `aborted`, when either run was cancelled, and no
pipeline compares a cancelled run with anything. This is the single change
that fixes #216's four misreports. The runner is AgentKit's (PRD 06 §6)
when that lands, and the engine's own until then.

### 5.2 Run each tree once

The gate keeps a result cache keyed by the tree hash (`git write-tree` over
the index plus the untracked files the ledger knows) and the command. A
gate asked for a tree it has already measured answers from the cache. This
is what lets the landing gate of the last change and the hermetic gate be
one run (as `impl` does since #207), lets the model's own `run_checks` call
(§6.3) be the landing gate when nothing changed after it, and makes the
accounting in `timings` honest: a cache hit is `cached: true`, not a run.

The full-suite budget of a `fix` run is **two**: the baseline and the
landing gate. The revert check is targeted (§5.3) and counts only when the
profile has no targeted form.

### 5.3 The revert check, per language

The proof that the fix is tested is "the tests the change wrote fail
without it". Today it reverts every non-test file and runs the whole suite,
which is wrong twice: it cannot see an inline test, and it takes minutes.

- The ledger's classification names the tests the change added or edited.
  For a profile with `InlineTests`, the test names come from the outline
  of the changed files (declarations the profile's test convention marks,
  `#[test]` functions, doctests) rather than from file names, and the
  revert puts back the implementation **declarations** rather than the
  file: the outline gives the line ranges, and the engine restores those
  ranges from the base.
- The check runs the profile's `Targeted` command for exactly those tests.
  Only when the profile has none does it run the suite.
- The implementation is held in a commit object (`git stash create` or a
  `wip:` commit) before anything is put back, so a crash or a `kill -9`
  during the check loses nothing, and the check runs under the run's
  context: Ctrl-C aborts it and the tree is restored from the object.
- A compile error with the implementation removed is not a proof. The
  targeted run's output is classified by the profile (a build failure vs a
  test failure) and only a test failure sets `proves`. Where the profile
  cannot tell, `proves` is set and `reason` says the failure was not
  classified, so the report file carries the doubt.

### 5.4 Landing

A change lands only on `pass` or `pass_was_already_failing`. `unverified`
lands only with `--no-verify`. A detection miss is `unverified` and parks:
exit 4, a `no_verify_command` warning, the work on the branch. This is the
documented behaviour; the code now matches it.

### 5.5 Parking, on every path

Any outcome that does not land after a writing phase parks: the ledger's
change is committed as `wip:` with hooks skipped, under a context the
cancellation cannot reach with its own short ceiling, the checkout returns
to the base, and the exit code is 4 for a verdict and the category's own
otherwise (`aborted` → 1, retryable). A park never leaves a dirty tree on
the work branch, so a re-run of the same command is accepted by preflight.
A cancelled run that parks reports `aborted` with `resumable: true`.

## 6. The phases

### 6.1 What a phase is

A phase stays one agent with one terminating tool and the repair loop ADR 03
describes. Three things change about how it is built.

**A stable prefix.** The system prompt and the tool schemas are identical
for every phase of one kind in one run, and identical across runs on the
same repository where the inputs are: the scratch directory is one per run
(§6.2), the per-run and per-task facts (criteria ids, test ids, the branch)
move out of tool descriptions and guidelines into the user prompt, and the
guidelines are rendered once in a fixed order. The first user message of a
phase is the **run brief**: everything that is constant for the run — the
report, the repository map, the project instructions, the steering file,
the language block — rendered once and byte-identical in every phase that
needs it, with a cache breakpoint after it (AgentKit PRD 06 §3). Phase- and
attempt-specific material follows in a second message. A provider serves a
prefix from cache only when the tools, the system prompt and the brief all
match, so the brief pays on every turn of a phase, across the phases of one
kind (`impl`'s tasks, PRD 14), and across runs on the same repository; it
does not make `analyse` and `implement`, whose tools and mandates differ,
share a prefix, and the PRD does not claim it.

**No copies.** The report is sent once per phase, never three times; the
baseline block, the project instructions and the repository map appear in
the run brief and nowhere else; a check's output tail is referenced by its
path in the scratch directory (`.agent-fox/scratch/checks/baseline.txt`,
readable with `read_file`) with the last ten lines inline, not forty to
eighty lines per failing command in every prompt.

**A pruned transcript.** The phase installs AgentKit's pruning transform
(PRD 06 §4) ahead of summarization: a tool result older than eight turns
is replaced in the transcript by a one-line stub naming the call and its
size, and a `read_file` of a path and range the transcript already holds
unchanged returns a reference to that turn. Summarization stays as the
backstop at 60% of the window.

### 6.2 One scratch directory per run

`.agent-fox/scratch/<session_id>/` is created at run start, ignored by git
through its own `.gitignore`, named in the tool descriptions once, and
removed when the run ends. Within it the engine writes `checks/<name>.txt`
for every command it runs, so a phase can read a full failure log on demand
instead of carrying it in the prompt. The directory's name is the session
id, which is in the envelope, so a report file and the logs it mentions
pair up.

### 6.3 `run_checks`: the suite as a tool

The implementing phase gets one program-owned tool in place of running the
suite through the shell:

```
run_checks(scope?: "lint" | "targeted" | "all", tests?: [string])
```

- `lint` runs the profile's linter; `targeted` runs the profile's
  `Targeted` command for the named tests (or the tests the ledger says the
  phase has written so far); `all` runs the gate. The result is the
  `checks.Result` shape: exit code, `ok`, the last twenty lines, and the
  path of the full log in the scratch directory.
- Its results go into the gate's cache (§5.2). A phase that ends by
  running `all` on the tree it submits has already run its landing gate;
  the engine's gate is then a cache hit and the suite ran once.
- The shell refuses the suite's own command and the profile's `Test`
  command in the implementing phase, naming `run_checks`, as it refuses
  `find` naming `find_files`. The refusal is exact: the suite argv, or the
  profile's suite form (`cargo test` with no filter, `pytest` with no path,
  `npm test` with no `--`); a targeted form is never refused (#219).
- The model is told the budget: `run_checks all` at most twice per phase;
  a third call answers from the cache with a note.

### 6.4 Rejections name everything and keep what was right

Every terminating tool follows `submit_review`'s rule since #205: a
rejection lists every problem at once, the rows or fields that were right
are kept in the handler's draft, and the next call carries only the
corrections. The handler's merged view is validated as a whole.
`submit_implementation`, `submit_task`, `submit_repair` and `submit_resolve`
all gain this; a `citation` helper shared with the review resolves a bare
basename, a `file:Symbol` through the symbol table, and a line range, so
the first submission is right more often.

### 6.5 The phases of `fix`

| Phase | Agent | Tools | Prompt | Ends with |
|---|---|---|---|---|
| `analyse` | read-only | the read tools, `execute` reporting | run brief + "diagnose" | `submit_analysis` |
| `implement` | writing | the read tools, `write_file`, `edit_file`, `execute` under the build allowlist, `run_checks` | run brief + diagnosis + criteria + definition of done | `submit_implementation` |
| `review` | read-only, fresh context | the read tools, `execute` reporting | ids in scope + the specs cited, as today | `submit_review` |

The read tools are the six today plus whatever AgentKit PRD 06 adds; the
build allowlist is the profiles' `Programs` union plus the gate's programs
plus `--allow`, generated from the detection and never written in a prompt.
The analysis prompt no longer carries the baseline output; it carries the
baseline verdict and the log's path.

### 6.6 Prompts are documents

`specgen` already keeps its prompts as Markdown files embedded at compile
time, for the reason its source states: they are prose, and a diff against a
document reads better than a diff against escaped string concatenation.
`codefix` and `codeimpl` keep theirs as Go constants, with the run's facts
spliced in by `fmt.Fprintf` across a dozen functions, and the tool
descriptions, the shell guard's refusals and the submit handlers' rejections
are string patches on top of AgentKit's own strings. A reviewer who wants to
know what a phase was told reads four packages. The engine makes every
text a document and every document auditable.

**Where the text lives.** One embedded tree, `internal/engine/prompts/`,
bundled with `go:embed` and loaded by name on demand:

```
prompts/
  brief/fix.md  brief/impl.md             the run brief, one per tool (§6.1)
  fix/analyse.system.md  fix/analyse.md   the mandate and the phase message
  fix/implement.system.md  fix/implement.md
  impl/survey.*  impl/task.*  impl/repair.*  impl/resolve.*   (PRD 14)
  review/review.system.md  review/review.md                   shared by both tools
  partials/                 report  baseline  gate  language  repomap  map_delta
                            instructions  steering  criteria  definition_of_done
                            survey  landed_tasks  previous_attempt  scope
                            external_apis  date  read_roots  scratch
  tools/                    execute  write_file  edit_file  run_checks
                            submit_analysis  submit_implementation  submit_task  …
  refusals/                 programs_not_allowed  suite  git_mutating  path_outside …
  rejections/               missing_verdicts  weak_evidence  unverified_doc_source …
```

Every file opens with a YAML header the loader and the tests read:

```yaml
---
name: fix/implement
kind: message          # system | message | brief | partial | tool | refusal | rejection
view: PhaseView        # the Go type it renders from
stable: false          # true: must render byte-identical for the whole run
max_bytes: 6000        # the rendered size a change may not exceed unnoticed
---
```

**The engine.** Go's `text/template`, from the standard library, with the
`missingkey=error` option and a fixed function map of a dozen helpers:
`fence LABEL TEXT` (the provenance fence every untrusted text is wrapped
in), `code`, `join`, `count`, `lines N TEXT` (the last N lines), `ref PATH`
(the one-line reference to a log in the scratch directory), `ids`, `title`.
A document composes others with `{{template "partials/report.md" .}}`; the
brief and the phase messages are lists of partials in a fixed order, and
the partials are shared by both tools, so the report block, the baseline
block and the instructions block exist once. Nothing in a template
computes: it reads fields and loops over lists. `specgen` avoided
`text/template` because its `fill` would have parsed the data; here the data
is never parsed. `text/template` inserts a value verbatim, so a report that
contains `{{` is text, and the loader's test renders a report made of
nothing but template syntax to prove it. `specgen`'s own `fill` moves to the
same loader when PRD 14 retires `internal/project`.

**The view.** A template is filled from a **view**: a typed, frozen Go
struct the engine builds from the run's facts and nothing else. There are
three:

- `BriefView`, built once per run: the language block, the repository map,
  the report or the spec digest, the instructions, the steering file, the
  gate commands and their log paths, `--context`. A template whose header
  says `stable: true` may reference only this view, and a test asserts
  that it renders byte-identically twice.
- `PhaseView`, built per phase: the branch, the date, the diagnosis, the
  criteria, the task, the baseline verdicts, the landed tasks, the map
  delta, the allowlist, the scratch path.
- `AttemptView`, built per attempt: the previous attempt's failure, the
  reverted declarations, the attempt number.

A value a template needs that is not a fact of the run — a count, a
rendered list, a profile's targeted-test form — is a field the view
computes in Go, so the prompt's logic is testable without rendering it.
The views are what `fix` and `impl` differ in; the engine and the partials
are what they share.

**Tool text.** AgentKit's base tool descriptions and guidelines come from the
SDK as documents (PRD 06 §12). The engine's phase-specific descriptions —
the allowlist, the read roots, the scratch directory, the `run_checks`
budget — are `tools/*.md` templates over the `PhaseView`, rendered once per
phase and **replacing** the SDK's text rather than appended to it, so a
description is one document, not a base string plus three patches. Guard
refusals and handler rejections are `refusals/*.md` and `rejections/*.md`
over small views (the programs refused, the ids missing), so the sentence a
model is corrected with is reviewed like any other prompt.

**Auditable at build time.** A golden test renders every document against
fixture views in `internal/engine/testdata/views/` to
`testdata/prompts/<name>.golden.md`, and `UPDATE_GOLDEN=1` regenerates
them deliberately, as the `--schema` goldens are. A change to a prompt is
therefore a diff of a Markdown file in review. The same test checks the
header of every document, that every partial it names exists, that no
document references a field its view lacks (`missingkey=error` makes that
a failure), that no rendered document exceeds its `max_bytes`, and the
denylist of §1 runs over the templates as it runs over the Go files, so a
language cannot hide in prose either.

**Auditable at run time.** Every run writes what it sent: the system
prompt, the brief, each phase message, and the tool descriptions as
registered, to `<state>/prompts/<tool>-<started_at>-<session_id>/` as
`<phase>[.task-N][.attempt-N].<role>.md`, under the same stem as the report
and the events file, so the three pair up by `session_id`. `--dry-run`
writes them too; they are local state. The full report carries
`prompt_templates`, a map of document name to the SHA-256 of its embedded
source, so a report can be matched to the exact text version that produced
it, and `artifacts` gains a `prompts_dir` entry. A run that cannot write the
directory records a `low` `prompts_not_written` warning and goes on.

## 7. Structural checks for every language

`internal/conform` keeps its checks and changes what feeds them.

- **Formatting and lint** come from the profile's `Format` and `Lint`
  commands, run over the changed files where the command takes paths and
  over the tree otherwise; findings are parsed by a per-profile line regex
  (`path:line:col: message` for gofmt, vet, ruff, eslint `--format unix`,
  clippy, rubocop `--format emacs`). A profile with no formatter has no
  formatting finding, and the pull request says so.
- **Long functions and unused declarations** come from the outline
  (`file_outline`'s backend per language, with end lines from ctags or
  `go/ast`), not from `go/ast` alone. A language whose backend gives no end
  lines gets no long-function finding, and the report says which backend
  judged which file.
- **Tests with no assertion** are judged by the profile's assertion
  pattern (`t.Error`/`t.Fatal`/a helper taking `*testing.T`; `assert`;
  `expect(`; `assert!`/`assert_eq!`; `Assert.`; `expect(...).to`) over the
  test declarations the outline finds.
- **Discarded errors, later-task comments, future dates, errata
  citations, `git init` without `-b`** stay as they are: they are text
  checks and already language-neutral, except that `testRefRe` learns the
  profile's `TestID` and test-declaration spellings.
- Every finding carries `backend` and `profile`, so a reviewer can see what
  judged it.

## 8. What the envelope gains (additive)

- `result.language`: `{primary, detected[], verify: {source: makefile|profile|flag|none}}`.
- `result.revert_check.mode`: `targeted` or `suite`, and `declarations` when
  the revert was by declaration.
- `timings[].cached: true` on a gate answered from the cache.
- `usage.phases[].pruned_tokens`: what the pruning transform removed.
- The `aborted` verdict in `result.verdict` and `result.verification`.
- `prompt_templates` in the full report (document name to SHA-256), a
  `prompts_dir` artifact kind, and a `low` `prompts_not_written` warning
  code.

None of these is required of a caller; `--schema`'s golden files change by
these fields only.

## 9. Order

1. **AgentKit PRD 06 §3, §4 and §6** (the stable prefix and its
   breakpoint, the pruning transform and read deduplication, the process
   runner), with §5 and §7 (honest usage, guidelines under a custom prompt)
   as the small correctness items that go first. `fix` can be built against
   the engine's own runner and without pruning, but the token goal in
   §Goals 5 is not reachable without §3 and §4.
2. **`internal/lang`** with the denylist test, and `internal/engine` with
   the ledger, the gate cache, the runner and parking, each with tests on
   real temporary repositories in at least Go, Python, TypeScript and
   Rust fixtures (`testdata/lang/<profile>/`).
3. **`codefix` rewritten over the engine**, the golden `--schema` files
   updated by the additive fields only, and the baseline tables in
   `docs/development.md` extended with a row per language.
4. **PRD 14** (`impl`), which retires `internal/project` and the duplicated
   halves of `internal/checks`.

## Acceptance criteria

- On a Python, a TypeScript, a Rust and a Go fixture, each with a seeded
  bug and a test the fix must add, `fix` lands a `pass` with
  `closes_issue: true`, the revert check in `targeted` mode, and exactly
  two full-suite runs in `timings`. On the Rust fixture the test is inline.
- On a Maven fixture with no Makefile, `fix` parks with exit 4,
  `verdict: unverified` and the `no_verify_command` warning; with
  `--no-verify` it lands with `verdict: unverified`.
- Ctrl-C during the implement phase, during the landing gate, and during
  the revert check each end with `category: aborted`, the checkout on the
  base branch, no dirty file on the work branch, and a second run of the
  same command accepted by preflight. The revert check's implementation
  files are intact after the abort.
- `grep` for the denylist of §1 over `internal/engine`, `codefix` and
  `codeimpl` finds nothing; the test that does it is in `internal/lang`.
- With `--emit-events`, every `turn` event of the implement phase after the
  first shows `cache_read_tokens` at least 90% of `context_tokens` on the
  Anthropic wire, and the system prompt and tool schemas of two `fix` runs
  on the same repository are byte-identical.
- On the navigation baseline, `fix`'s summed `context_tokens` per phase is
  at most half of today's, its cost at most 60%, and the fixes landed are
  the same.
- No `--schema` golden changes except the additive fields of §8;
  `docs/cli.md`'s flag tables are unchanged.
- `grep -rn '"' --include='*.go'` over `internal/engine`, `codefix` and
  `codeimpl` finds no string literal longer than one sentence that the
  model would read; a test asserts that every system prompt, message, tool
  description, refusal and rejection a run sends was rendered from a
  document under `internal/engine/prompts/`.
- Every document under `internal/engine/prompts/` has a golden rendering
  under `testdata/prompts/`, every `stable: true` document renders
  byte-identically across two builds of the view, and a report whose body
  is `{{.Secret}}{{template "x"}}` reaches the model verbatim.
- After a `fix` run the state directory holds a `prompts/` directory with
  one file per prompt sent, named in `artifacts`, and the report's
  `prompt_templates` hashes match the binary's embedded files.

## Documentation

- `docs/cli.md`: the verdict table (`aborted`; `unverified` parks), the
  revert check section (targeted, per language), the allowlist paragraph
  (generated from the profiles), `run_checks` under *What the model may and
  may not do*, and the additive fields.
- `docs/model-usage.md`: the run brief and the stable prefix, the pruning
  transform, the scratch logs, `run_checks`; the *Prompt templates*
  section rewritten for the engine's documents, views and helpers, with
  the layout of `internal/engine/prompts/` and how to regenerate the
  goldens.
- `docs/configuration.md`: the `prompts/` subdirectory under *State
  directory*.
- `docs/configuration.md`: the language profiles and what each detects.
- `docs/development.md`: `internal/lang` and `internal/engine` in the
  layout; the per-language baseline rows.
- An ADR recording that the engine is shared and that language is data.
