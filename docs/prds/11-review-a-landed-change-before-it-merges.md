# Review a landed change before it merges

Status: **proposed**. A fifth tool, `review`, on the shared shell. Follows
[PRD 10](10-make-shipped-prs-match-their-specs.md) and
[ADR 09](../adr/09-grade-the-work-independently.md), which put an
independent conformance review *inside* `impl` and `fix`; this PRD puts one
*after* them, in a program that can also act on the answer.

## Intent

`impl` ends by opening a pull request and `fix` ends by opening one and
commenting on its issue. Neither merges. That is right for the tool that
wrote the change: ADR 03 found `af-fix` opening a pull request and
squash-merging the same branch, and ADR 09 found five pull requests that
said "N of N tasks landed" over requirements they did not meet. The author
of a change must not be what decides it is done.

So today a person decides. They open the pull request `impl` wrote, read an
"Unmet requirements" table and a "Not ready" header, open the issues the run
filed, decide whether the gaps are the code's or the spec's, and either merge
or go back to `fix`. Every step of that is reading things a program wrote,
re-checking things a program already measured, and then clicking a button.
Until it is clicked, the next `impl` cannot start on a spec that depends on
this one, and `fix` cannot work on the gaps from `main`.

[Pull request #174](https://github.com/agent-fox-dev/agent-fox/pull/174) is
the worked example. `impl` implemented spec `15_symbol_navigation_tools`,
landed five of five tasks, passed `make lint` and `make test` on the machine
and in a clean environment, and opened a *draft* headed "Not ready: blocking
findings". Its own conformance stage found:

- three files outside the spec's `touches` — `cmd/triage/preflight_test.go`,
  `docs_symbols_test.go`, `internal/repomap/repomap_test.go` — each a test
  file that *had* to change for the suite to pass. The spec's file list was
  incomplete, not the change;
- `15-REQ-8.1` unmet: the navigation baseline tables gained their two columns
  but no counts, because a count needs a live-model run and spec 13's "before"
  tables were never filled either ([#173](https://github.com/agent-fox-dev/agent-fox/issues/173));
- `TS-15-16` weaker than its contract, for the same reason;
- the scope finding declared and tracked ([#172](https://github.com/agent-fox-dev/agent-fox/issues/172)).

An independent reading of the diff against the spec (every requirement, every
test, `gofmt`, `go vet`, the TS-15 tests run on the branch) finds nothing
else: the change is correct, lint-clean, test-green and honest about its one
gap. A person would merge it, file one issue for the test, and amend the spec's
file list. `impl` cannot: by its own rule, a file out of scope is a blocker
that cannot be declared, and `fix` cannot answer "the spec was wrong". Nothing
in the toolset can say "this is good enough to merge, and here is what is
still owed."

This PRD adds `review`: given the pull request `impl` opened, or the issue
`fix` worked on, it establishes the facts a merge decision needs (the forge's
state of the pull request, the project's checks on the tree the merge would
produce, in a clean environment, the structural and scope checks, an
independent conformance review on a fresh context), decides `merge` or
`correct`, and acts: it merges, or it files one issue per gap in the shape
`fix` consumes, and in both cases it says on the pull request what it found
and why. It follows the same rule as the four tools: the model does the one
step that needs judgment (reading the change against the spec) and Go does
everything else, so the decision cannot be a sentence the model wrote.

## Goals

- A pull request `impl` or `fix` opened is decided and acted on without a
  person: merged when it is safe and every gap is tracked, or left open with
  one issue per gap and a comment saying why.
- The decision is derived from facts in Go — the forge's answer, git's diff,
  a measured check run, the review's rows — never from the pull request's
  prose, which a model wrote.
- Every gap `review` finds becomes an issue `fix` can take as its input:
  the requirement or test id in the title, the spec's text and the review's
  evidence in the body, acceptance criteria `fix` extracts, and the branch to
  work from.
- An issue `impl` already filed for a gap is found, not duplicated.
- After a merge, the next `impl` or `fix` can start from `main` with the
  change in it, and the issue a proven `fix` closes is closed by the forge.
- `review` speaks the shared interface: one positional input, one JSON
  object on stdout, progress on stderr, the exit-code table, the report and
  events files, `--dry-run`, `--preflight`, `--schema`, `--output`,
  `--emit-events`, and the trust labels on text a stranger wrote.
- Nothing in the four tools changes behaviour. `review` reuses
  `internal/conform` (the review phase, the structural scan, the scope
  check, the renderers), `internal/checks`, `internal/gitx` and `issuex`
  rather than copying them.

## Non-goals

- **Writing code.** `review` has no writing phase. A gap is an issue for
  `fix`, never a commit by `review`; a spec that is wrong is an issue for a
  person. The tool that grades is not the tool that fixes.
- **Reviewing arbitrary pull requests.** `review` answers for a pull request
  it can find a spec, or cited ids, or acceptance criteria for. A pull
  request with none of those is a `needs_human` stop, not a best-effort
  opinion.
- **Approving.** A forge "approval" is a person's signature. `review` posts a
  comment and merges or does not; it never submits an approving review in its
  own name, and it does not bypass branch protection: a merge the forge
  refuses is reported as the forge's refusal.
- **Marking a draft ready.** GitHub exposes that only over GraphQL, which
  `issuex` deliberately does not speak (PRD 08). A draft whose review passes
  is a `needs_human` stop naming the one click needed; see design decision 7.
- **Reverting a merge.** A merge `review` made and later regrets is a person's
  call.
- **Waiting on CI by default.** `--ci-timeout` is opt-in; without it a
  pending check is reported and the run stops.
- **Sealing the spec package.** A merged `impl` branch leaves every task
  `done` in `tasks.json`, which is what `impl`'s upstream check reads.
  `review` writes nothing under `.specs`.
- **A second model phase to write the issues.** The review phase's rows,
  the spec's own text and git's facts are enough for Go to render an issue
  (§7). The one model phase is the review.

## Background: what exists and what is missing

**The conformance review is a package.** `internal/conform.RunReview` runs a
read-only phase on a fresh context that sees a rendered spec, a review scope
(requirement, test and decision ids), the changed files, the diff stat and the
check commands, and returns a `conform.Review`: one row per id, each with a
status from a closed set and a `file:line` the submit tool checks exists.
`conform.Scan` runs the structural checks, `conform.Scope.Outside` the scope
check, `conform.FindCited` reads spec ids out of free text, and
`RenderReview`, `RenderFindings` and `RenderUnmet` render the tables the
pull-request body carries. `impl` and `fix` are two callers. `review` is a
third, in another process, on a model the operator chooses.

**The forge client has every call needed but one.** `issuex.Client` reads a
pull request (`ReadPullRequest`, with `Draft`, `Merged`, `HeadSHA`,
`HeadBranch`, `BaseBranch`), its changed files, its CI check runs and its
reviews; files issues; comments; and merges with the repository's own merge
policy, pinning the head SHA so a push between the review and the merge makes
the merge fail (`MergeOptions.SHA`). It does not expose whether the forge
considers the pull request mergeable; `review` learns that by merging locally
(§4), which is also what gives it a tree to run the checks on.

**The shell resolves an issue or pull-request URL as an input.** `toolio`
classifies the argument, reads the thread from the forge before `Exec`, and
hands `Deps.Input.Issue` with `IsPullRequest` set from the URL's path or the
forge's answer. `review` takes exactly that input.

**What `impl` and `fix` leave behind is program-written where it matters.**
`impl` files its deviation issues as `Spec <id>: <key> is not met` with a
fixed body; `fix`'s summary comment carries `- Pull request: <url>` as one
line Go rendered; both pull-request bodies end in a fixed footer. `review`
reads those lines as facts to *find* things (the issue for a key, the pull
request for an issue) and reads nothing else from the prose.

**What `impl` cannot decide.** Its scope check treats any file outside the
union of the tasks' `touches` as a blocker that cannot be declared
(`codeimpl/conformance.go`, `assessment.blockers`). That is the right gate
against a model's "while here" edits during a run. After the run, the file is
in the diff and the question has changed: not "was this asked for" but "is it
correct, and should the spec have listed it". The review phase answers the
first; the second is an issue against the spec. §5 makes that the policy.

## Functional requirements

### 1. The program

- `cmd/review` is a fifth tool. It is built by `make build`, cross-built by
  `make build-all` (`TOOLS` gains `review`), installed by `install.sh`
  (`TOOLS` default gains `review`), and copied into the tools container.
- Its pipeline lives in a package of its own, `codereview`, beside
  `issuetriage`, `codefix`, `codeimpl` and `specgen`: the input resolution,
  the preflight, the checks, the review phase, the decision, the actions,
  the `Result` type and its summary view, the artifacts, side effects and
  `next[]` mapping, and every rendered comment and issue body.
  `cmd/review/main.go` is the `toolio.App` value and an `os.Exit`, as the
  other four are.
- The envelope's `tool` is `review`; the report and events files are
  `review-<started_at>-<session_id>.json` and `.jsonl`; the progress prefix
  on stderr is `review:`; the pull-request comment's footer is
  `*Written by [review](…). Trust, but verify!*`.
- `toolio` learns the fifth name wherever it lists the four: the `tool`
  field's description, `Next.Tool`'s, the flag-ownership table that names
  which tools accept a flag (`toolFlags`), and `docs/cli.md`.

### 2. The input

The single positional is one of:

| The argument is | Then it is | What `review` does with it |
|---|---|---|
| a GitHub or GitLab pull or merge-request URL | the change under review | reviews it |
| a GitHub or GitLab issue URL | the issue `fix` worked on | finds the pull request `fix` opened for it (§2.1) and reviews that; the issue is also the review's context and the place a verdict comment goes |
| a path to a readable file, `-`, or other text | — | a usage error (exit 2): `review takes a pull request or an issue on GitHub or GitLab` |

`--input-kind issue` is accepted and redundant; every other `--input-kind`
value is a usage error.

#### 2.1 From an issue to its pull request

`fix` posts a summary comment on the issue it fixed whose "Where it is"
section has the line `- Pull request: <url>` (`codefix/render.go`). `review`
reads the thread the shell already fetched, takes the **last** such line
from a comment whose footer is `fix`'s, and resolves the URL. When no comment
carries one: the issue has no pull request `review` can find, and the run
stops with exit 3 (`needs_human`), `needed`: give the pull request URL as the
input. A pull request named by hand in the issue body is not used: that text
is a stranger's.

### 3. Establishing the facts

Every step below is Go's. The order is the order of cost: forge reads first,
git next, the project's checks after that, the model last.

#### 3.1 The pull request

`ReadPullRequest` gives `number`, `title`, `state`, `draft`, `merged`,
`head_sha`, `head_branch`, `base_branch`. From them:

- **Already merged**: the run reports `verdict: "already_merged"` and exits 0
  with nothing done. A merged pull request is finished, and re-running the
  chain on it must be harmless.
- **Closed, not merged**: exit 3, `needed`: reopen it or discard the branch.
- **Open**: the review proceeds.

`ReadChangedFiles` gives the paths. `GetCIChecks` gives the check runs:
`passed`, `failed`, `pending` or `none`, counted per conclusion. `GetPRReviews`
gives the forge reviews; a `CHANGES_REQUESTED` review by a person is
recorded as a fact and makes the verdict `needs_human` (§5): a person has
spoken, and a program does not overrule them.

#### 3.2 Which tool opened it, and what it is held to

The **origin** is decided from facts, in this order, and recorded as
`result.origin`:

| Fact | Origin | Scope of the review |
|---|---|---|
| the changed files include exactly one spec package's files (`.specs/<NN>_<name>/…`), or `--spec` names one | `impl` | every criterion and test id owned by a task that is `done` in that package's `tasks.json` **as it is on the head commit** — the same rule as `impl`'s own review (`codeimpl.reviewScope`) |
| the head branch is `fix/…` or `feature/…`, or the input was an issue | `fix` | the requirement and test ids the issue thread and the pull-request body cite that a spec under `--specs-dir` defines (`conform.FindCited`), plus the acceptance criteria the issue defines (`codefix`'s extraction, `AC-n`), given to the review as requirement rows with their text |
| neither | `unknown` | none: exit 3, `needed`: name a spec with `--spec` |

The spec is read from the **head commit**, not from `--dir`'s checkout: the
tasks' states `impl` committed are on the branch. A package on the head that
does not validate is `category: "invalid_spec"`, exit 1.

Survey decisions (`D-n`) are not in scope: the survey lives in `impl`'s report
file, not on the branch, and the pull-request body's rendering of it is prose.

#### 3.3 The tree the merge would produce

`review` needs a clean working tree in `--dir`, as `fix` and `impl` do, and
leaves it as it found it: the checkout is restored at the end of every path,
and a failure to restore is a `checkout_not_restored` warning.

1. `git fetch origin <head_branch>` and `origin <base_branch>`. The fetched
   head must equal the forge's `head_sha`; a mismatch is `category: "git"`
   — the branch moved while the pull request was being read, and the run
   says so rather than reviewing one thing and merging another.
2. The **merge base** of head and base is computed. When base's tip is an
   ancestor of head, the head is the merge result. Otherwise `review`
   checks out base's tip detached, merges head with `--no-ff`, and commits
   with a fixed message; that local commit is the tree under review, and
   nothing is pushed. A conflict is a fact: `result.merge.conflict` lists the
   paths, and the verdict is `needs_human` (§5).
3. The change is `git diff <merge-base>..<tree>`: `changed_files` and the
   diff stat, with the spec package's own files left out, as `impl` does.

`internal/gitx` gains `Fetch`, `MergeBase` (exists), `IsAncestor` and
`MergeNoFF` for this; they are plain `git` invocations under the reduced
environment runner.

#### 3.4 The checks, in a clean environment

The project's checks run once on the tree from §3.3, under
`gitx.HermeticRunner` (an empty `HOME`, no global or system git configuration,
no credential prompt), with the fingerprint recorded as `result.environment`
— exactly as `impl`'s final verification does. Which checks:

- origin `impl`: the spec's `linter` and `all_tests`, as `impl`'s gate;
- origin `fix`: `--verify`, else the command `checks.Detect` finds, as `fix`;
- `--verify` overrides either; `--no-verify` runs nothing and the verdict
  can then never be `merge` (§5).

There is no baseline run: the question is not "did the change regress"
but "does the tree a merge would produce pass". A red result names the
failing command and its tail output, as the other tools' `verification` does.

#### 3.5 Structural and scope checks

`conform.Scan` runs over `changed_files` with the same inputs `impl` gives it
(`--max-func-lines` is a flag here too). `conform.Scope` is built from the
spec's tasks as `impl` builds it (documentation and the package exempt), and
`Outside` lists what is out of scope. Both are facts on the result
(`structural`, `out_of_scope`); what they mean for the verdict is §5.

#### 3.6 The independent review

`conform.RunReview` on the run's model, with: the rendered spec (or, for a
`fix` origin, the cited requirements' and tests' text and the acceptance
criteria), the scope from §3.2, the changed files and diff stat from §3.3,
the check commands from §3.4, and as `Context` the issue thread when there is
one, labelled as such. The phase is read-only with `execute` under the
reporting allowlist, as `impl`'s review is. Nothing of the pull-request
body, `impl`'s report or `fix`'s comments reaches it: it is the fresh
context ADR 09 asks for, in a separate process.

Two additions to `conform`, both additive:

- `RequirementRow` and `TestRow` gain an optional `remedy`: for a status
  other than `implemented` / `asserts_contract`, what would have to change,
  as `file:line` and one sentence. The submit tool requires it on those rows
  and checks the `file:line` as it checks `evidence`. `impl`'s pull-request
  rendering shows it when present; `review`'s issues are built from it (§7).
- `ReviewInput.Criteria`: acceptance criteria given as requirement rows with
  text, for a `fix` origin, so the schema's "id exactly as listed" rule
  applies to `AC-n` as it does to `20-REQ-1.2`.

`--no-review` skips the phase; `review_not_run` is then a high warning and
the verdict can never be `merge` (§5).

### 4. What the model may and may not do

One phase, read-only: the six read tools and `execute` under the read-only
program allowlist. No write tool, no network, no forge. The phase ends by
calling `submit_review`, which refuses a review that skips an id, invents
one, or cites a `file:line` that does not exist. The model cannot merge,
cannot file an issue, cannot comment, and cannot set the verdict: it fills
rows, and Go reads them.

### 5. The decision

The verdict is one of four, decided in Go from the facts above, in this
order; the first rule that applies wins, and `result.reasons` lists every
fact that contributed, as program-written sentences.

| Verdict | When | Exit |
|---|---|---|
| `already_merged` | §3.1 | 0 |
| `needs_human` | the pull request is closed; the local merge conflicts; a person requested changes; a CI check is pending after `--ci-timeout`; no spec or ids can be found; the pull request is a draft and every other rule would say `merge` (design decision 7); the forge refused the merge | 3 |
| `correct` | any **blocking** item, or the checks did not pass, or a CI check failed, or `--no-verify` / `--no-review` left the verdict unprovable | 4, `category: "nonconformant"` |
| `merge` | none of the above: the checks pass in a clean environment, CI is green or absent, nothing is blocking, and every **unmet** item is tracked by an erratum in the change or by an issue (found or filed by this run) | 0 |

**Blocking** (the change must change before it merges): a requirement
`missing` or `different`; a test `tautological`, `no_assertions` or
`missing`; a documentation statement the code contradicts; the checks failing
on the merge tree; a failing CI check. These are what ADR 09 calls blocking,
less scope, plus the two measured failures.

**Unmet** (the change may merge, but something is owed): a requirement
`partial`; a test `weaker`; a structural finding; a file outside the spec's
scope. The last is the departure from `impl`, and the reason is in
[Background](#background-what-exists-and-what-is-missing): after the run, an
out-of-scope file is in the diff, the review phase has read it, and the open
question is the spec's file list. The issue `review` files for it is against
the spec (§7), with the paths, so a person amends `touches`.

The rule that makes `merge` honest is the tracking one: `review` never merges
a change with an unmet item nothing follows up. An item is tracked when the
change adds or edits an erratum under `docs/errata/` that names it, or when
an open issue for it exists (§6), or when this run files one (§7). Under
`--no-issues` an untracked item therefore keeps the verdict at `correct`.

`--merge` decides whether a `merge` verdict is acted on (§8). Without it the
verdict and exit code are the same, `result.merged` is absent, and `next[]`
suggests the command with `--merge`.

### 6. Finding the issues that already exist

Before filing anything, `review` lists the repository's open issues
(`ListIssues`, state open, at most 200) and matches each unmet and blocking
item by key against two program-written title shapes:

- `impl`'s: `Spec <id>: <key> is not met`, where `<key>` is a requirement id,
  a test id, or the word `scope`;
- its own (§7): `Spec <id>: <key> — <short what>` and `Review of
  <owner>/<repo>#<N>: <key>`.

A match is the item's `tracking` and is cited in the comment; it is not
re-filed, and nothing is posted on it. Keys compare case-insensitively; the
scope finding's key is `scope` whatever the path, so #172 tracks every
out-of-scope file of spec 15's change and `review` files no second issue for
the other two.

### 7. The issues `review` files

One issue per blocking item and per untracked unmet item, when the run may
write to the forge (`--land pr`-style conditions: not `--dry-run`, not
`--no-issues`, forge authenticated, target repository known). Each body is
rendered by Go and shaped for `fix`:

```
`review` reviewed <pr-url> (spec `<spec-dir>`, head `<sha>`) and found this
requirement not met.

- **Requirement:** 15-REQ-8.1        (or **Test:** TS-15-16, or **Scope**)
- **Status:** partial                (the review's status, labelled as the model's)
- **Branch:** impl/15-symbol-navigation-tools-every-phase

## What the specification says
<the requirement or criterion text, or the test's contract, verbatim from the spec>

## What the change does
<the review row's evidence>

## What would have to change
<the review row's remedy>

## Acceptance Criteria
- [ ] 15-REQ-8.1: <criterion text>
- [ ] TS-15-16 asserts its contract
```

The acceptance-criteria section is what `fix` extracts and holds its own
implementation to (`fix`'s per-criterion verdicts), and the ids in it are
what `fix`'s review cites, so a `fix` run on the issue is reviewed against
exactly the ids `review` found short. The branch line tells the operator
where `fix` must branch from while the pull request is open (§10).

A scope item's issue is titled `Spec <id>: scope — <n> file(s) outside the
tasks' touches`, lists the paths, and asks for the spec's `touches` to be
amended; its acceptance criterion is that `impl`'s scope check passes on the
branch. A failing-checks item's body carries the failing command and its
tail output; a failing CI check's carries the check's name, summary and URL.

Labels: `--label a,b` as `triage`'s; default none. Every filed issue is an
`issue` artifact and a `create_issue` side effect with kind `review`.

### 8. Acting

- **Comment.** On every verdict but `already_merged`, one comment on the
  pull request: the verdict and the reasons, the facts (checks and
  environment, CI, merge state), `RenderReview`, `RenderFindings`,
  `RenderUnmet` with the tracking column filled, and the issues filed. When
  the input was an issue, the same verdict in one paragraph is posted on the
  issue too. Both are `comment` side effects with kinds `verdict` and
  `verdict_issue`; a failure to post is a `comment_not_posted` warning.
- **Merge** (`--merge`, verdict `merge`). `MergePullRequest` with `--method`
  (default: the repository's policy, as `issuex` resolves it), a commit title
  `<pr title> (#<N>)`, and `SHA` pinned to the head reviewed. A forge
  refusal — a draft, branch protection, a push since the review — is exit 3
  with the forge's message in `needed` and nothing retried. On success:
  `result.merged` (`sha`, `method`), a `commit` artifact on the base branch,
  and a `merge_pr` side effect. The remote branch is left for the forge's
  own auto-delete setting; `review` deletes nothing.
- **The issue, after a merge.** A `fix` pull request whose body says
  `Closes #N` is closed by the forge. One that says `Refs #N` (the fix was
  not proven) stays open: `review` closes it with a comment only when every
  requirement id the issue cites is `implemented` in the review; otherwise
  it comments that the change merged and the issue stays open, and says why.
  `result.closes_issue` records which. The issue's number is the input's
  parsed reference, never a number parsed out of the body.
- **`--pull`.** Before anything, check out the base branch and fast-forward
  it from `origin`, as `fix --pull` does; after a merge, fast-forward it
  again, so the checkout in `--dir` holds the merged tree and a following
  `impl` or `fix` starts from it.

`--dry-run` suppresses every write: no comment, no issue, no merge, no close.
The run still fetches, merges locally, runs the checks and the review, and
decides; `result.would_write` lists the comment, the issues and the merge
request as they would have been sent, and each is an artifact and side
effect marked `dry_run`.

### 9. Flags

Shared flags are the shell's, unchanged: `--dir`, `--model`, `--vendor`,
`--effort`, `--max-turns`, `--budget`, `--phase-timeout`, `--total-budget`
(one phase: the lower of the two applies, as for `triage`), `--context`,
`--trust-project`, `--verbose`, `--quiet`, `--show-text`, `--detail`,
`--report-file`, `--output`, `--dry-run`, `--emit-events`, `--input-kind`,
`--preflight`, `--repo-map-tokens` (the review phase gets the map, as
`impl`'s does not; design decision 9), `--schema`, `--version`.

| Flag | Default | Effect |
|---|---|---|
| `--merge` | `$AF_REVIEW_MERGE`, else off | act on a `merge` verdict: merge the pull request |
| `--method merge\|squash\|rebase` | the repository's policy | the merge method |
| `--no-issues` | off | file no issue; an untracked gap then keeps the verdict at `correct` |
| `--label a,b` | — | labels on the issues filed |
| `--spec <ref>` | from the changed files | the spec package the change is held to: a directory, id, name or directory name under the spec root |
| `--specs-dir` | `<dir>/.specs`, or `$AF_SPEC_DIR` | where `NN_name` packages live |
| `--repo owner/repo` | the input URL's | where issues are filed; `group/subgroup/project` on GitLab |
| `--verify` | the spec's checks, or detected | one command that decides the checks |
| `--no-verify` | off | run no checks; the verdict can then never be `merge` |
| `--verify-timeout` | `10m` | timeout for one check command |
| `--no-review` | off | skip the model phase; the verdict can then never be `merge` |
| `--max-func-lines` | `100` | as `impl` |
| `--ci-timeout` | `0` | how long to wait for pending CI checks, polling every 30 s; `0` means do not wait |
| `--pull` | off | fast-forward the base branch from `origin` before, and after a merge |

`--merge` with `--dry-run` is accepted: it answers whether the run *would*
merge. `--no-issues` with `--merge` is accepted and means a change with an
untracked gap is not merged. `--spec` with a `fix`-origin pull request is a
usage error only when the package is not found; otherwise it adds the spec's
ids to the scope.

### 10. `next[]`

Derived from the result, never from the model:

| Verdict | Entries |
|---|---|
| `merge`, not merged (`--merge` absent) | `review <pr-url> --merge` |
| `merge`, merged | none for the pull request; `impl <spec-ref>` for every spec under the spec root whose `dependencies` name this spec and whose tasks are not done — the chain continues |
| `correct` | one `fix <issue-url>` per issue filed or found, `why` naming the branch to check out first (`git switch <head_branch>`; see design decision 6), then `review <pr-url>` to re-review |
| `needs_human` | `review <same input>` with `--context "<answer>"` built by `toolio.ResumeNext`, equal to `needs_human.resume` |

### 11. The result, the envelope and the versions

`result` carries: `stage` (`preflight`, `reading`, `fetching`, `checking`,
`reviewing`, `deciding`, `acting`, `done`), `origin`, `pull_request`
(`url`, `number`, `title`, `head_branch`, `head_sha`, `base_branch`,
`draft`, `state`), `issue` (`url`, `number`) when the input was one, `spec`
(`dir`, `id`, `name`, `title`) when found, `scope` (the ids), `merge`
(`base_tip`, `merge_base`, `tree`, `fast_forward`, `conflict`), `ci`
(`state`, one entry per check), `forge_reviews` (state and author per review),
`verification` and `environment` (§3.4), `structural`, `out_of_scope`,
`review` (the `conform.Review`), `blocking`, `unmet` (with `tracking`),
`verdict`, `reasons`, `issues` (filed or found: `key`, `number`, `url`,
`filed`), `comments`, `merged`, `closes_issue`, `would_write`,
`cost_usd`, `preflight` and `estimate`.

Trust: `pull_request.title`, `ci[].summary`, `forge_reviews[].body` and the
issue's text are `external`; the review's rows, `remedy` included, are
`model`; `verdict`, `reasons`, `scope`, `merge`, `verification`, `issues`
and `merged` are `fact`. `CheckTrust` applies to the new type.

Under `--detail summary`, `result` keeps `stage`, `origin`,
`pull_request.url`, `spec.id`, `verdict`, `reasons`, `blocking`, `unmet`,
`issues`, `merged`, `closes_issue`, `preflight`, `estimate`, and
`verification` only when it did not pass.

`SideEffect.Action` gains `merge_pr` and `close_issue`; its description says
the set is open, as the warning codes' does. `Artifact` needs no new kind: a
merge is a `commit` on the base branch, an issue is an `issue`, a comment a
`comment`. This is additive: `schema_version` becomes `3.2.0` with a line in
[Interface versions](../cli.md#interface-versions). New warning codes:
`review_merge_refused` (the forge refused), `issue_not_filed` (a gap could
not be filed; the verdict is then `correct`), `ci_pending`.

### 12. Preflight

`--preflight` runs every check that would refuse the run, in the ordinary
run's order, and stops before the fetch: the repository and the clean tree,
`--pull`, the input (an open, unmerged pull request, or an issue with a
pull request to find), the forge credential when the run would write
(skipped under `--dry-run`, as for the four tools), the spec package on the
head when the origin is `impl` (read through the forge's file contents or a
fetch of the single blob; `--preflight` does not fetch the branch), the
verification command, and `--spec`. `estimate.phases` is `1`, or `0` with
`--no-review`.

### 13. Progress and events

The shared shell's: human progress lines on stderr by default, the JSONL
stream under `--emit-events`, the events file always. `review` emits `step`
events at each stage of §11, `check` events for the check run as `fix` does,
`phase_start`/`phase_end` for the review phase with its `tool_calls`, and
`forge` timings for every call. No new event type.

## Design decisions

1. **A fifth tool, not a flag on `impl`.** `impl --merge` would make the
   author the judge, which is the thing ADR 09 removed. A separate process
   can run on another model (`--model ADVANCED` for the review of a
   `STANDARD` implementation), on another machine, on a pull request a
   person edited after `impl`, and on `fix`'s pull requests, which `impl`
   never sees. It is also the only shape that lets a person be the reviewer
   some days and the program others, with the same artefacts.

2. **`--merge` is opt-in, with an environment default.** The four tools
   write to the forge by default: `impl` and `fix` push and open pull
   requests, `triage` files issues. All of those are additive. A merge
   changes `main` and is undone only by a revert, so it is the one action a
   caller names explicitly — once, in the environment (`AF_REVIEW_MERGE=1`)
   for a chain that is meant to run unattended, as `AF_LAND=none` keeps a
   run on the machine.

3. **Out-of-scope files are unmet, not blocking.** `impl`'s scope check
   guards a run in progress against the model's "while here" edits, and a
   blocker that cannot be declared is the right strength for that. `review`
   sees the pull request after the run, with the file in the diff and the
   review phase's reading of it. Three test files that the suite needs are
   not a reason to keep a correct change off `main`; they are a reason to
   amend the spec, and that is an issue. The review phase still reads them:
   a wrong change in an out-of-scope file shows up as a contradicted
   document, a weaker test or a structural finding, which the rules above
   handle on their own terms.

4. **Tracking is the merge condition, not emptiness.** `impl` opens a pull
   request with an "Unmet requirements" table because a run that stops
   short should still land its work. The same reasoning says a reviewed
   change with a tracked gap should merge: the gap does not get smaller by
   waiting on the branch, and `fix` works from `main`. What must not happen
   is a gap that nobody tracks. So `review` merges only when every unmet
   item has an erratum or an issue, and it files the issues itself when it
   may.

5. **The merge tree is built locally and the checks run on it.** The forge
   says `mergeable` only after a background job, and `issuex` does not read
   it. A local `--no-ff` merge of the fetched head into the fetched base
   answers conflict-or-not deterministically, and gives the checks the tree
   `main` will have. It costs one fetch and one detached commit that is
   never pushed. The hermetic runner is reused unchanged.

6. **Corrections go to the branch while the pull request is open.** `fix`
   branches from the current branch and opens its pull request against it.
   With the `impl` branch checked out, a `fix` on one of `review`'s issues
   opens `fix/…` → `impl/…`; `review` of that merges it into the `impl`
   branch; `review` of the `impl` pull request then sees the correction.
   That is stacked work with the tools as they are, and `next[]` says it:
   the `why` names the branch. After a merge, corrections go to `main`
   like any other issue. Nothing in `fix` changes.

7. **A passing draft stops for one click.** `impl` opens a draft when it has
   blocking findings; GitHub refuses to merge a draft and exposes "ready for
   review" only over GraphQL, which PRD 08 keeps out of `issuex`. `review`
   therefore reports `needs_human` with `needed: mark the pull request ready
   for review and re-run`, and `next[]` carries the re-run. When `issuex`
   gains the mutation this becomes an action; the verdict and the rest do
   not change. GitLab, whose REST API toggles the draft, is handled when
   that call exists.

8. **One model phase, and Go writes the issues.** The review's rows already
   say which id, what the code does and where; the spec says what was asked;
   `remedy` says what would have to change. An issue is those three with a
   heading each, plus the acceptance criteria `fix` extracts. A second phase
   to "write the issue" would add prose the first phase did not check, and
   a place for a claim to enter.

9. **The review phase gets the repository map.** `impl`'s conformance review
   deliberately omits the map (`--repo-map-tokens`' row in `docs/cli.md`)
   because that review runs in the same process that built one for the
   tasks and the diff is what it reads. `review` starts cold in another
   process, and the review phase is its whole model spend; the map is the
   cheap way to orient it. The flag is shared and `0` disables it.

10. **The pull-request body is not parsed.** It is model prose rendered by
    Go. The two lines `review` reads from the forge's text — `impl`'s issue
    titles and `fix`'s `- Pull request:` line — are fixed strings Go wrote,
    matched exactly, and used only to *find* things, never to decide.

## Success criteria

| Metric | Today (PR #174) | Target |
|---|---|---|
| A correct `impl` pull request with tracked gaps reaches `main` without a person | no: a draft, a person must read and merge | `review <pr> --merge` merges it, with one new issue (TS-15-16) and #172, #173 cited |
| Gaps become issues `fix` can run on | partly: `impl` files one per declared deviation, with no acceptance criteria | one per blocking or untracked unmet item, with the spec text, the evidence, the remedy and `AC` lines |
| A wrong change (`different`, failing checks, contradicted docs) is never merged | n/a | verdict `correct`, exit 4, issues filed, nothing merged — covered by smoke tests on a scripted review |
| A merge after the branch moved | n/a | the forge refuses the pinned SHA; exit 3 |
| Decision derived from prose | — | zero: every input to the verdict is a fact or a review row |

The spec written from this PRD holds `review` to the same smoke tests the
other tools have: the shell's golden `--schema` file, the envelope on every
path, `--dry-run`'s `would_write`, a scripted review driving each verdict,
`httptest` forges for GitHub and GitLab, and real temporary repositories for
the fetch, the local merge and the checkout restore.

## Changes outside the new package

| Where | Change |
|---|---|
| `internal/conform` | `remedy` on requirement and test rows (optional, checked when present); `ReviewInput.Criteria`; nothing else |
| `internal/gitx` | `Fetch`, `IsAncestor`, `MergeNoFF` (detached, fixed message) |
| `internal/toolio` | `review` in the tool lists and `toolFlags`; `merge_pr` and `close_issue` side-effect actions; three warning codes; `schema_version` 3.2.0 |
| `issuex` | nothing required. Recommended later: `Mergeable` on `PullRequest`, and a ready-for-review call (design decision 7) |
| `codefix` | export the acceptance-criteria extraction the issue path reuses; nothing behavioural |
| `Makefile`, `install.sh`, `containers/tools` | the fifth tool |
| `docs/cli.md` | a `review` section with its flags, result, summary view and `--preflight` example; the shared-flags and exit-code prose that says "four tools" |
| `docs/configuration.md` | `AF_REVIEW_MERGE` |
| `docs/model-usage.md` | the `review` phase row |
| `README.md` | five programs |
| `docs/adr/10-…` | the decision that a program may merge, and on what evidence; written with the implementation |

## Worked example: pull request #174

What `review https://github.com/agent-fox-dev/agent-fox/pull/174 --merge`
does on the tree as it is today:

1. Reads the pull request: open, draft, head `6cc0751`, base `main`, 23
   changed files including `.specs/15_symbol_navigation_tools/tasks.json`;
   no CI check runs; no forge reviews. Origin `impl`, spec 15, scope: every
   criterion and test of its five done tasks.
2. Fetches both branches; `main` (`2dae58d`) is an ancestor of the head, so
   the head is the merge tree. Runs `make lint` and `make test` under the
   hermetic runner: pass. Structural scan: no findings. Scope: three files
   outside `touches`.
3. Review phase: 15-REQ-1 to 7, 9 and 10 `implemented`; 15-REQ-8 `partial`
   (columns, no counts); TS-15-16 `weaker`; every other test
   `asserts_contract`; no document contradicted.
4. Blocking: none. Unmet: 15-REQ-8 (`partial`), TS-15-16 (`weaker`), three
   scope paths. Tracking: #173 matches `15-REQ-8.1` by title (and the
   erratum `docs/errata/15_navigation_baseline.md` in the change names
   15-REQ-8); #172 matches `scope`; TS-15-16 has nothing — one issue is
   filed, `Spec 15: TS-15-16 — the test does not assert the before-and-after
   counts`, with the test's contract, the review's evidence, the remedy and
   an `AC` line.
5. Verdict `merge`; the pull request is a draft: `needs_human`, exit 3,
   `needed`: mark it ready for review and re-run. The comment with the
   tables, the verdict and the filed issue is posted. After the click,
   the re-run merges at `6cc0751` with the repository's default method,
   `next[]` names `impl 16` (spec 16 depends on 15), and `fix` can take the
   TS-15-16 issue from `main`.

Had `impl` not opened it as a draft — or once `issuex` can mark it ready —
step 5 merges on the first run.
