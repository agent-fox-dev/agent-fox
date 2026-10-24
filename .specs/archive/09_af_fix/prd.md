---
spec_id: "09"
spec_name: "af_fix"
title: "af fix: the fix agent on the toolbelt — preflight, brief, measure, land or park"
status: "active"
created_at: "2026-10-20T08:18:29.202603Z"
updated_at: "2026-10-20T08:18:29.202603Z"
intent_hash: "fdbeb576b414f3d01bdad535b95274fa0f7ed49690f8ca86a5a3f6bc9758daac"
schema_version: 2
source: "docs/prd/02-rebuild-triage-and-fix-on-the-toolbelt.md"
---
## Intent

Give `af` its fix agent. `af fix <issue|file|text|->` turns a problem report into a verified change on a branch and, by default, a pull request. The brain only edits a tree inside a box and hands back one implementation report through `af fix submit`. `af` makes every effect, and makes it from facts it measured itself: the clean-tree preflight, the baseline gate, the brief, applying the brain's tree to the host, the protected-path revert, the gate after the change, the verdict, the revert check that decides `Closes` or `Refs`, the commit with the program's trailer, the push and the pull request. Work that does not land is never thrown away: it is parked as `wip:` and the checkout goes back to the base branch. The subcommands `af fix brief|run|submit|land` expose the same steps to a person at their own checkout, to a box, and later to the service.

## Goals

1. **End to end, offline.** On a temporary repository with a local bare `origin` and an `httptest` GitHub, `af fix <issue URL> --brain scripted --sandbox local --dir <repo>`:
   - creates `fix/<N>-<slug>` from the base with exactly one commit whose last paragraph is `Agent-Fox-Run: <run id>`;
   - pushes that branch to `origin`;
   - opens one pull request whose body contains `Closes <issue url>` when the revert check is `proven`, and `Refs <issue url>` otherwise;
   - prints `status: done`, exits 0, and leaves the checkout on the base branch with a clean tree.

   A commit the brain made inside the box is not on the branch. The same run passes against an `httptest` GitLab, and with `--brain agentkit` on `provider/faux`.
2. **File and text input.** `af fix ./bug.md --repo o/r --brain scripted --sandbox local --land pr` creates one issue from the report, then a pull request that says `Closes` or `Refs` that issue.
3. **A refusal is the brain's next turn.** A scripted brain whose first change makes the gate regress gets the verdict and the failing checks' output in the task block of its next round, within the same attempt. When its second change passes, the run lands. The envelope reports `rounds: 2`.
4. **Work that does not land is parked.** When the gate after the change is still red and the bounds are spent, the run commits the work as `wip: …` on the branch, exits 4 (`unverified`), and leaves the base branch checked out with a clean tree. With `--land pr`, the branch is pushed and the pull request is a draft that says `Refs`.
5. **A question stops the run, and the answer resumes it.**
   - A submission that carries a question exits 3 with `needs_human` and its options. It creates no branch and no commit, and leaves the tree untouched.
   - Re-running `needs_human.resume.argv` with `--context B` appended renders a brief with the same `prefix_sha256`, whose task block holds the earlier question and the answer `B`.
6. **Nothing is written that should not be.**
   - `--dry-run` sends zero write requests to the `httptest` forge, creates no branch and no commit, and lists the branch, commit, push and pull request as `planned` side effects.
   - `--land none` sends no forge request and pushes nothing.
   - A change the brain made under `.specs/` or `.github/workflows/` is not on the branch, and is reported.
7. **Checks and docs.** `make check` passes offline with no vendor key. The `af --schema` golden includes the `fix` result schemas. `docs/cli.md`, `docs/configuration.md`, `docs/model-usage.md`, `docs/architecture.md`, `README.md` and `skills/af-fix` describe what this scope ships.

## Non-goals

- **The other boxes and the `claude` brain** (scope `af_boxes_claude`). That covers `landlock`, `podman` and `none`, running `af fix run` inside a real box, and the Claude Code settings file and hook. In this scope the loop uses whatever `--sandbox` providers `af_brains` registers, which today means `local`.
- **The service, hub, labels as triggers, the `fix_summary` issue comment and status labels** (PRDs 03 and 04). `af fix` writes no comment and no label.
- **Carry patches** and `patch/<N>-<slug>` branches (PRD 05).
- **Fork workflows.** The pull request is opened in the target repository with the pushed branch as head. A cross-repository head is not supported.
- **Fetching or pulling the base branch.** The base is the branch as it is checked out.
- **More than one run on the same checkout at a time.**
- **Resuming a brain's transcript.** The submission file is the only state a brain leaves.
- **Merging, force-pushing or reusing an existing branch name.**
- **Changing `issuex`.**
- **What earlier scopes own.** The envelope, the input resolution and the dry-run decorator belong to `05_af_cli_envelope`. The gate engine, the verdict table, the revert check and the guard belong to `06_af_ring1_facts`. `Execute`, the box session and the submit core belong to `07_af_brains`. This scope calls them; it does not re-specify them.

## Background

The previous `codefix` (`1e6458d`, not readable from the tree) set the fix contract that PRD 01 §4.2 and PRD 02 §5 restate. Its parts are:

- checks before and after;
- the verdict table;
- the revert check deciding `Closes` versus `Refs`;
- parking as `wip:` and returning to the base;
- a pull-request body rendered from facts.

`docs/architecture.md` (*The box*, *What the box makes physical*) adds the shape. The tree is synced into the box with `.git`, comes back out without it, and the host classifies the exported tree against its baseline and makes the commit that counts. PRD 01 §6.4 says a refusal at `measure` "goes back to the brain as its next turn, within the attempt's bounds", and that a forge write failing after the push is a warning on a landed run.

What exists or is specified:

- **`internal/gitx`** (read). It provides:
  - `Slug`;
  - `UniqueBranchName`, which is free locally and on `origin`;
  - `CreateBranch`, `Checkout` and `CommitAll`;
  - `CommitAllNoVerify`, whose doc comment says it exists for the parking commit;
  - `Push(ctx, branch, attempts, log)`, which retries transient failures only;
  - `DirtyFiles`, `CurrentBranch`, `HasRemote`, `Restore`, `CheckoutPaths`, `ResetHard`, `Clean` and `CommitsSince`.

  `BranchName` produces `fix/issue-N-slug`, which is not the `fix/<N>-<slug>` form PRD 01 and PRD 02 name. `Head` returns a short hash.
- **`issuex`** (read). It provides:
  - `CreatePullRequest(ctx, Repo, CreatePullRequestRequest{Title, Body, Head, Base, Draft})`. GitLab prefixes `Draft: ` itself.
  - `CreateIssue`, `GetRepository` (`Archived`) and `Authenticated`.
  - `MaxBodyLength(c)`: 65536 on GitHub, 1048576 on GitLab.
- **Spec 05** provides the `toolio` shell, input resolution, `DryRunForge`, `RunDir` with its ledger and `WriteReport`, `needs_human` with `resume`, and the exit codes `done` 0, `failed` 1, `usage` 2, `needs_human` 3, `unverified` 4.
- **Spec 06** provides:
  - `project.Detect`: the gate table, suite forms and programs;
  - `checks.Run` (cached by tree hash, `Options.Table`) and `checks.Compare` (`pass`, `pass_was_already_failing`, `regressed`, `still_failing`, `unverified`, `landable`);
  - `conform.Check`, the revert check (`proven` gives `closes`);
  - `policy`, with the default protected paths `.git/`, `.agent-fox/`, `.specs/`, `.github/workflows/` and `.gitlab-ci.yml`;
  - `repomap.Build`;
  - `gitx.ChangedPaths`.
- **Spec 07** provides `brain.Brief` (`Prefix`/`Task`), `brain.Execute`, `brain.Flags`, the registry with `agentkit` and `scripted`, `box.With`/`Session` (`Sync`, `Export`, `WriteFile`, `Tools`), and `submission.Kind`/`Submit`/`Command`. It explicitly leaves measuring, attempts, the host-side commit, landing and parking to this scope.
- **`af_triage`** (in generation) adds the `skills` Go package that embeds `skills/af-*`.
- **`skills/af-fix`** today tells an attended agent to branch, commit, push and open the pull request itself. That contradicts the toolbelt rule, so the skill is rewritten here.

## Requirements

### R1. The commands

`cmd/af` registers five commands on the shell. Defaults for the loop are `--brain agentkit`, `--sandbox local`, `--budget 10`, `--max-turns 150`, `--attempts 2`, `--land pr` and `--timeout 45m`.

| Command | Input | What it does |
|---|---|---|
| `af fix <input>` | issue URL, file, text or `-` | The loop: R2 → R3 → (R5 → R6) per round, within R7's bounds, then R8 or R9. Opts into the run directory, the forge, `--repo`, `--dry-run` and `--context`. |
| `af fix brief <input>` | same | R2 and R3 only: preflight, baseline, and the rendered brief in a new run directory. It writes nothing outside `.agent-fox/`. |
| `af fix run --run-id <id>` | none | One round of R5 on an existing run: opens a box on `--dir`, runs the brain, and applies its tree to the checkout. It does not measure. |
| `af fix submit --run-id <id>` | JSON on stdin | `submission.Command("fix submit", FixReport)` from spec 07, with R4's kind. |
| `af fix land --run-id <id>` | none | R6 once, then R8 or R9, on the checkout as it is. This is what a person runs after working on their own checkout. |

**Flags:**

- `--base <branch>` is accepted by `fix` and `fix brief`.
- `--attempts` (1–5), `--land pr|branch|none` and `--no-verify` are accepted by `fix`. `--land` is also accepted by `fix land`.
- The brain flags of `brain.Flags` are accepted by `fix` and `fix run`.

An input URL naming a pull or merge request is a `usage` error. A `--run-id` that `toolio.OpenRunDir` rejects, or a run directory without `fix.json`, is a `usage` error. Each command declares a result schema that appears in `af --schema`.

### R2. Preflight and baseline

Before any model call, in this order, the run checks the following. The first that fails stops the run.

1. **`--dir`** must be the top level of a git work tree (`usage`).
2. **The checked-out branch** must exist (a detached HEAD is `usage`). The base is `--base` when given, else the checked-out branch. A `--base` other than the checked-out branch is `usage`, with the message "check out <base> first".
3. **The tree must be clean.** `gitx.DirtyFiles` must be empty, so tracked changes and untracked files that are not ignored both fail it. A dirty tree is `failed`, category `preflight`, naming up to 20 paths.
4. **There must be a gate.** `project.Detect` must give a table with at least one available check. If not, the run fails with category `preflight` and `fix_hint {flag: "--no-verify"}`. With `--no-verify` this check is skipped.
5. **The landing must be possible.**
   - `--land pr|branch` (not under `--dry-run`) needs an `origin` remote (`preflight`).
   - `--land pr` needs a valid target repository, or it is `usage` with `fix_hint {flag: "--repo"}`.
   - It also needs an authenticated forge client, or it is `auth` with `fix_hint {env}` naming `GITHUB_TOKEN` or `GITLAB_TOKEN`.
   - And it needs `GetRepository` to succeed on a repository that is not archived (`forge`).
   - Under `--dry-run`, a missing token is the warning `no_forge_token` instead.
6. **The brain must be constructible.** This is spec 07's registry, so a missing vendor key is `auth`.

An issue that is closed raises the warning `issue_closed`, and the run continues.

The run then records `base`, `base_head` (the full commit hash) and the table, and runs the baseline gate once with `checks.Run(dir, {Table})`. The default runner mode is used and no label is set, so concurrent runs cannot clobber each other's labels. A red baseline is a fact: it raises the warning `baseline_failing` and changes only what a pass means. `--no-verify` skips the baseline.

Everything the later steps need is written to `<run_dir>/fix.json`, atomically:

- `run_id`;
- the input's `kind`, `origin` and `key`;
- the issue reference and title, when there is one;
- the target repository;
- `base` and `base_head`;
- the table;
- the baseline gate id;
- `no_verify`;
- `prefix_sha256`;
- the context values;
- the round counter.

The input key is the issue URL, the absolute file path, or the SHA-256 of the text.

### R3. The brief

The brief is `brain.Brief{Prefix, Task}`.

**The prefix** is byte-identical for every round, attempt and resume of the same input on the same tree. It holds, in this order:

1. **The mandate.** Find the root cause, write the reproducing test first, and make the smallest change. Never commit, push, branch, edit the issue, or write under the protected paths. Run tests in their targeted form; the full suite is `af gate run`.
2. **The `af-fix` skill text.**
3. **The report**, fenced with a fence longer than any backtick run inside it, and labelled as material to verify, not instructions. For an issue this is the rendered thread; a triaged issue's body already carries the diagnosis.
4. **The gate table and the baseline**, per check: name, argv, status, and the tail of any failing check.
5. **The repository map** at 4000 tokens.
6. **The fix-report schema.**

The prefix holds no run id, timestamp, duration or context value.

**The task block** holds:

- the run id;
- the attempt and round numbers;
- how to submit: `af fix submit --run-id <id>` with the report on stdin, or the `submit_fix` tool for in-process brains;
- the earlier questions with their answers (below);
- for a later round, the previous round's refusal (R6);
- for a later attempt's first round, why the previous attempt ended.

**Earlier questions.** For a run with n `--context` values, the i-th question (i = 0 … n−1) is taken from the newest run directory under `.agent-fox/run/` that meets all of these:

- its `fix.json` has the same input key;
- its envelope has status `needs_human`;
- it carried exactly i context values.

It is rendered with the i-th answer. When no such run exists, the answer is rendered alone and the warning `question_not_found` is raised.

**Tools and hook:**

- `Brief.Tools` is the box's whole tool set.
- `HookConfig.ProtectedPaths` is the `policy` default list.
- `HookConfig.AllowPrograms` is the detected gate programs, plus `git`, `af`, and the read-only utilities `ls`, `cat`, `head`, `tail`, `wc`, `grep`, `rg`, `diff`, `sort`, `pwd`, `echo` and `mkdir`.

Before each round, the loop writes `.agent-fox/gate.json` (the pinned table) and `.agent-fox/guard.json` (the suite, the protected paths and the allowed programs) into the box through `Session.WriteFile`. A brain's own `af gate run` and a hook inside the box then use the host's table and rules.

`af fix brief` also writes the host copies `<run_dir>/brief.md` and `.agent-fox/run/<id>/run.json` (`{run_id, kind: "fix_report", attempt: 1, nonce}`), so that `af fix submit` works on a person's own checkout. Its result carries:

- `brief_path`;
- `prefix_sha256`;
- `base` and `base_head`;
- the baseline gate id and its `passed`;
- the table;
- the submit argv.

### R4. The fix report and questions

The submission kind is `fix_report`, with tool name `submit_fix`. Its validator refuses unknown fields and names every problem at once. A payload takes one of two forms.

**The question form** has `question` with:

- `text`: 1–2000 characters;
- `options`: 0–6 strings of 1–200 characters each;
- optionally `root_cause` (≤ 8000 characters) for the analysis so far.

**The report form** has no `question`, and these fields:

| Field | Required | Rule |
|---|---|---|
| `type` | no | One of `fix`, `feat`, `refactor`, `perf`, `docs`, `test`, `chore`. Default `fix`. |
| `subject` | yes | 1–72 characters, one line, no trailing period, no leading `<type>:`. |
| `root_cause` | yes | 1–4000 characters. |
| `change` | yes | 1–4000 characters. |
| `files` | yes | 1–500 entries of `{path, why}`. `path` is a valid relative slash path, not under `.git/` or `.agent-fox/`, with no duplicates. `why` is 1–300 characters. |
| `tests` | no | 0–100 entries of `{path, name?, proves}`. `path` must be a regular file in the submitted tree. |
| `criteria` | no | 0–50 entries of `{criterion, status: met\|unmet\|partial\|not_applicable, evidence}`. |
| `decisions` | no | 0–30 strings of at most 500 characters. |

**Normalisation:** strings trimmed, CRLF turned into LF, `type` defaulted, `files` sorted by path.

**A question in the loop** ends the run with `needs_human`:

- the question;
- options with ids `A`, `B`, …;
- the shell's `resume`.

The brain's tree is not applied, no branch or commit is made, and the checkout is untouched.

**A question to `af fix land`** is `failed`/`submission`. The message tells the person to re-run `af fix brief <input> --context <answer>`.

### R5. Applying the brain's tree to the host

After a round whose submission `brain.Execute` accepted:

1. **Export.** The loop exports the box (`Session.Export`).
2. **Mirror** the export onto the host checkout:
   - Every file of the export, except `.git/`, `.agent-fox/` and paths the host's git rules ignore, is written to the host when it is absent there or differs in content, mode or symlink target. Symlinks are recreated, never followed.
   - Every file in the host's file view (`checks.FileView`) that is absent from the export is deleted.
   - An export with no files at all is `failed`/`sandbox` and is not applied.
3. **Record** the ledger entry `apply {written, deleted, ignored}`.

The host never commits during the rounds. HEAD stays at `base_head`, and the change lives in the working tree.

**Between rounds**, the loop re-syncs the box from the host (`Session.Sync`), so the box holds exactly what was measured, protected paths reverted.

A round without an accepted submission applies nothing. Its edits are lost by design.

### R6. Measuring

Measuring runs on the host checkout, in the loop after each applied round and once in `af fix land`.

1. **Foreign commits.** The current branch must be the base and HEAD must equal `base_head`. Otherwise the result is `failed`/`git`, naming the commits since `base_head`, because `af` makes the commit that counts.
2. **Protected-path revert.** Every changed path (`gitx.ChangedPaths(base_head)`) under a protected path is returned to its content at `base_head`, or deleted when it did not exist there. Each one is listed in `reverted_paths` and raises the warning `protected_path_reverted`.
3. **Scope.** When no changed path remains, the refusal is `no_change`. The report's `files` are compared with the measured paths. Undeclared changes and declared paths that did not change are listed in `scope` and raise the warning `scope_mismatch`; they never refuse.
4. **The gate after.** `checks.Run(dir, {Table: pinned})`, then `checks.Compare` against the baseline by id. `--no-verify` skips this step and the verdict is `unverified`.
5. **The revert check.** Only when the verdict is landable, `conform.Check(dir, since=base_head)` runs with the pinned table. `closes` is true only for `proven`. Any other outcome passes on its `fix_not_proven` warning.

**What happens next:**

- A non-landable verdict, or `no_change`, is a **refusal**.
  - The next round's task block carries the refusal kind, the verdict, each failing check's name and tail (at most 4 KiB each), and the reverted paths.
  - The ledger records `measure {verdict, after_gate_id, revert_outcome, closes, changed, reverted}` and `refusal {kind}`.
- `af fix land` takes no further round. A refusal there goes straight to R9.

### R7. Attempts, rounds and bounds

**An attempt** is a budget scope. It has `--budget` USD and `--max-turns` turns, and there are up to `--attempts` of them.

**A round** is one `brain.Execute` call. `Brief.Attempt` carries the run-wide round sequence number, so run files never collide.

**Within an attempt**, a measured refusal starts another round, with the cost and turns that remain to the attempt. A round starts only while:

- more than USD 0 remains;
- at least 1 turn remains;
- at least 1 minute of brain time remains.

Brain time for a round is the time left until the command deadline, minus a measuring reserve of three times the baseline gate's duration plus one minute.

**An attempt ends** when:

- the verdict is landable (go to R8);
- a question arrives (R4);
- its bounds are spent;
- a round ends without an accepted submission.

**The next attempt** starts from the current host tree, with a fresh budget and a fresh brain context. It does not start after a failure that `Execute` marked non-retryable, such as `budget`, `auth` or a non-retryable `sandbox` failure.

**When no attempt remains:**

- If the host tree holds an applied change, it is parked (R9). When the last report is from an earlier round, its subject is used. When the last round failed, its failure is added as the warning `attempt_failed`.
- Otherwise the run fails with the last round's failure.

**On cancellation or the command deadline,** an applied change is parked locally, without push or forge write, on a context detached from cancellation and bounded to 10 seconds. Under `--dry-run` it is discarded instead. The context error is then returned to the shell.

**The box** is held across every round through `box.With` and is closed on every path. The envelope's `usage` is spec 07's `Tally` across all rounds.

### R8. Landing

A landable verdict lands. So does any measured result under `--no-verify`.

1. **Text or file input with `--land pr`:** first create one issue in the target repository. Its title is the subject, and its body is the original input under `## Original report`, followed by a line naming the run.
2. **Branch name:** `fix/<N>-<slug>` for an issue, or `fix/<slug>` without one. The slug is `gitx.Slug` of the issue title, or of the subject for text and file input. The name is made unique with `gitx.UniqueBranchName` and created from `base_head` with the working tree carried along.
3. **Commit** with `gitx.CommitAll`. The message is:
   - `<type>: <subject>`;
   - a blank line, then `change` (capped at 2000 bytes);
   - a blank line, then `Closes <url>` or `Refs <url>` when there is an issue;
   - a blank line, then `Agent-Fox-Run: <run id>`.

   A failed commit hook parks the work (R9) with the warning `commit_hook_failed`.
4. **Push**, unless `--land none`, with `Push(ctx, branch, 3, log)`. Each retry raises the warning `push_retried`. A push that still fails is `failed`/`git`; the branch and commit stay locally and `next` gives `git push -u origin <branch>`.
5. **Pull request**, with `--land pr`: `CreatePullRequest(target, {Title: "<type>: <subject>", Body, Head: branch, Base: base, Draft})`.
   - It is a draft under `--no-verify`, or when any criterion is `unmet` or `partial` (warning `criteria_unmet`).
   - When it fails after the push, the run is still `done`, with the warning `pull_request_failed` carrying the forge's error.
6. **Return** the checkout to the base branch.

**The pull-request body** is rendered from facts, in this order:

1. A marker comment with the run id.
2. The verification section, labelled *measured by af*: the per-check baseline and after table, the verdict, the revert-check outcome with its reason, and the reverted protected paths.
3. The root cause, the change, the tests, the criteria and the decisions, each labelled *reported*.
4. The changed files from git, each with its reported `why` or "(not declared)".
5. The `Closes`/`Refs` line.
6. A footer with the run id, the brain, and the cost with its source.

When the body exceeds `issuex.MaxBodyLength`, the reported sections are cut first, with a visible marker. The verification section and the `Closes`/`Refs` line are never cut.

The body is also written with `RunDir.WriteReport`, so `report.md` is the `report` artifact.

**Status:** `done`, exit 0. Under `--no-verify` the status is `unverified`, exit 4, with the warning `not_verified`.

### R9. Parking and dry runs

**Parking** applies to a non-landable result after the last attempt or in `af fix land`, to an interrupted run (R7), and to a failed landing commit.

1. Create the R8 branch.
2. Commit with `gitx.CommitAllNoVerify`. The message is:
   - `wip: <subject>`;
   - a blank line, then `Parked by af fix: verdict <v>`, with the failing checks;
   - a blank line, then `Refs <url>` when there is an issue;
   - a blank line, then the trailer.
3. Push, unless `--land none`.
4. Open a draft pull request that says `Refs`, under `--land pr`, with the same issue creation for text and file input. This step is skipped for an interrupted run.
5. Return to the base branch with a clean tree.

The status is `unverified`, exit 4, with the warning `fix_not_landed` naming the verdict. `next` gives `git switch <branch>`.

**`--dry-run`** runs everything up to the landing decision, then:

- discards the applied change (`ResetHard(base_head)` and `Clean`);
- creates no branch and no commit, and pushes nothing;
- lists `branch`, `commit` and `push` side effects as `planned`. Forge writes are refused and listed by spec 05's `DryRunForge`;
- returns the status the real run would have had.

### R10. Result, offline tests and documentation

**The `fix` and `fix land` result** holds:

- `run_id`;
- `issue` (url or null) and `issue_created` (`{number, url}` or null);
- `base` and `base_head`;
- `attempts`, `rounds` and `attempts_max`;
- `outcome`: `landed`, `parked`, `needs_human`, `failed` or `dry_run`;
- `verdict`, `landable` and `closes`;
- `baseline` and `after`, each `{gate_id, passed}`;
- `revert_check` (`{outcome, reason}` or null);
- `changed_files`, `reverted_paths`, and `scope` (`{undeclared, unchanged}`);
- `land`;
- `branch`, `commit` (full hash) and `pushed`;
- `pull_request` (`{number, url, draft}` or null);
- `box` (`{provider, image, network}`). `network` is `"host"` for `local`.

**Artifacts:** `branch`, `commit`, `pull_request`, `issue` (when created), `report`, and spec 07's `submission`, `transcript` and `run_dir`.

**Side effects:** `branch`, `commit`, `push`, `create_issue` and `create_pull_request`, each with `done`, `planned` or `failed`.

**The scripted brain becomes multi-round** (an additive change to spec 07). A script may be `{"rounds": [{"steps": [...]}, ...]}`. The k-th `Run` on the same brain value replays round k, and a call past the last round ends at once with `ended`. A plain `{"steps": [...]}` file is one round.

**Tests** run offline in `make check`:

- temporary repositories built like gitx's `newRepo`, with a local bare `origin`;
- a fixture gate made of a Makefile `check` target running a shell test under `tests/` against a non-test script (skipped when `make` is absent);
- `httptest` GitHub and GitLab through an injected `App.NewForge`;
- `--sandbox local`;
- scripted scripts, and `provider/faux` for the agentkit path.

They cover every goal above, plus:

- the attended flow: `fix brief`, then edits made directly to the checkout, then `fix submit`, then `fix land`;
- a foreign commit refused by `land`;
- cancellation mid-run parking locally;
- the byte-identical prefix across rounds and resumes.

**Documentation:** run `make doc` first. Then update:

- `docs/cli.md`: the five commands, their flags and defaults, results, warnings, the `preflight` category and the exit codes;
- `docs/configuration.md`: the box-side `gate.json` and `guard.json` the loop writes, and the token needed for `--land pr`;
- `docs/model-usage.md`: the fix bounds per attempt, attempts and rounds, and that dry runs spend model money;
- `docs/architecture.md`: the fix loop as built, the apply and measure steps, the `fix.json` and `report.md` run files, and the *What moves where* `codefix` row;
- `README.md`: `internal/fix/`;
- `skills/af-fix`: rewritten to the toolbelt shape. It keeps the method (read, refuse early, diagnose, test first, verify with targeted tests and `af gate run`). Every branch, commit, push and pull-request step is replaced by `af fix brief` → work → `af fix submit` → `af fix land`. It states that the agent never commits, pushes, or edits the issue, and the branch form becomes `fix/<N>-<slug>`.

## Design Decisions

1. **Measuring runs on the host checkout, not inside the box.** `conform.Check` needs git's index and refs, and it runs in place because a separate worktree lacks ignored dependencies (spec 06 DD13). The host has both, and `local` gives no isolation to protect. `af_boxes_claude` may move the gate inside a real box behind the same measure step.
2. **The brain's tree is applied by mirroring the export onto the clean host checkout, and the commit happens only at landing.** The clean-tree preflight makes every working-tree change the run's own. Branching late means a dry run, a question or a failure never leaves a branch behind.
3. **The protected-path revert is a real revert in the measure step, not a filter in the apply step.** The same code then covers a person who edited their own checkout before `af fix land`, and the coming `none` box.
4. **A refusal starts another round within the same attempt, and `--attempts` counts budget scopes.** PRD 01 §6.4 says a refusal "is an attempt that continues … within the attempt's bounds". Counting each brain run as an attempt would give a refused brain a fresh budget.
5. **A new attempt continues from the current tree with a fresh context.** "An attempt that dies is re-run from the branch": the tree is that branch's content before the commit, so no measured work is lost.
6. **Each round is a fresh `Brain.Run` with the same prefix, and the refusal goes in the task block.** Spec 07's brains have no resumable conversation, and a byte-identical prefix keeps the driver's prompt cache across rounds.
7. **A non-landable result parks. Under `--land pr`, parking pushes the branch and opens a draft pull request that says `Refs`.** PRD 01 §4.2 says "draft when the verdict did not land". The service's clone is discarded after an attempt, so unpushed parked work would be lost there. `--land branch|none` keeps parking local.
8. **`--land none` makes the local branch and commit and pushes nothing.** PRD 02 §5 says it stops "after the commit". The checkout must still return to the base branch with the work kept somewhere. `--dry-run` is the mode that makes no commit. Acceptance criterion 4's "commits nothing" is read as "nothing reaches the remote".
9. **Text and file input under `--land pr` creates one issue at landing time.** PRD 02's acceptance criterion 1 expects `Closes`/`Refs` for `./bug.md`, and triage does the same for text. The issue is created only once the work exists and is measured.
10. **`--no-verify` skips the gate (baseline, after and revert check), lands as a draft that says `Refs`, and reports `unverified`.** The skill's table allows "unverified only if the user accepts that", and status `unverified` (exit 4) is exactly that acceptance. Git hooks are skipped only for the parking commit, as gitx documents.
11. **The branch is `fix/<N>-<slug>`, built from `gitx.Slug` and `UniqueBranchName`, not `gitx.BranchName`.** PRD 01 and PRD 02 name that form, and the skill is updated to match. `BranchName` stays for any other caller.
12. **The gate table is pinned at preflight.** It is passed to every host gate run and written into the box as `.agent-fox/gate.json`. A brain editing the Makefile must not change which checks are compared, and `checks.Compare` refuses mismatched check names.
13. **Baseline and after use `checks.Run`'s default runner mode and are addressed by id, not label.** The same mode makes a brain's in-box `af gate run` the same cache key. Labels live in a shared `labels.json` that concurrent runs would overwrite.
14. **`--timeout` keeps the shell's meaning (the whole command, default 45m).** Brain time per round is what remains minus a measuring reserve. One flag with one meaning across `af` beats a second per-attempt timeout, and the reserve means a deadline rarely cuts off the landing.
15. **The revert check runs only on a landable verdict, and `not_proven` lands with `Refs` instead of refusing.** PRD 01 rules "passing means they do not (`Refs`, with a `fix_not_proven` warning)", and spec 06 makes the revert check inconclusive on a red tree anyway.
16. **The scope check compares the report's `files` with git and only warns.** Every effect comes from facts. A mismatch is worth showing in the body, but not worth a round. The empty change is the only scope refusal.
17. **A pull-request failure after a successful push is a warning on a `done` run. A push failure is `failed`/`git` with the branch kept locally.** PRD 01 §6.4 and PRD 03 §6 rule "a forge write failing after the push is a warning on a landed run". The skill says to leave the branch for the user to push.
18. **Preflight refusals that are facts about the repository use a new `preflight` category.** Examples are a dirty tree, a missing gate and a missing `origin`. They are not invocation errors, so `usage` is wrong, and `config` (spec 06) means a broken `gate.json`.
19. **Earlier questions are found by scanning `.agent-fox/run/*/fix.json` and `envelope.json` by input key and context count.** Spec 05's `resume` is the invocation as received, so a run id cannot be carried in it, and the shell keeps no state between runs.
20. **`af fix land` refuses a question submission instead of answering `needs_human`.** Its resume line could only re-run `land`, which cannot take an answer. The person at an attended checkout is the human already.
21. **`af fix run` applies the tree but does not measure, and `af fix land` measures once and never takes another round.** The steps then compose as brief → run → land, the same functions the loop calls. A brain cannot call `run` or `land` (spec 06's guard), so rounds belong to the loop.
22. **The scripted brain gains a `rounds` form.** Spec 07's replay restarts on every `Run`, so a multi-round script is the smallest additive way to test a refusal followed by a fix offline.
23. **The fix report carries a commit `subject` and `type`, and the title, commit and body are built from them.** PRD 01 §4.3 refuses "a report that names no commit subject". The subject is the brain's to choose, and it is plain text that `af` composes, not an effect.
24. **The default brain in this scope is `agentkit`.** `claude` (PRD 01 D8's choice for fix) arrives with `af_boxes_claude`, which may change the default.
25. **Any criterion reported `unmet` or `partial` makes the pull request a draft.** The skill says the pull request must then say the work is unfinished. Acting on this brain claim only reduces the effect.

## Dependencies

| Spec | Relationship | Reason |
|---|---|---|
| `05_af_cli_envelope` | depends on | It provides the things this scope consumes: the `toolio` shell (`Command`, `Run`, `Outcome`, `Failure`, warnings, artifacts, side effects, `next`); input resolution and `Input.Thread`; `DryRunForge` and `Run.DryRun`; `RunDir` (`Record`, `WriteReport`, `OpenRunDir`); `needs_human` with `resume` and `--context`; `--timeout`; `--schema` and its golden. |
| `06_af_ring1_facts` | depends on; may extend | It provides `project.Detect` (table, programs, suite), `checks.FileView`, `checks.Run` with `Options.Table`, `checks.Compare`, `conform.Check`, the `policy` default protected paths, `repomap.Build` and `gitx.ChangedPaths`. If `conform.Check` cannot take an explicit table, this scope adds that parameter. |
| `07_af_brains` | depends on; extends | It provides `brain.Brief`, `Execute`, `Flags`, the registry and `Tally`; `box.With` and `Session` (`Sync`, `Export`, `WriteFile`, `Tools`, `Provider`, `Spec`); and `submission.Kind`, `Submit` and `Command`. This scope adds the scripted brain's `rounds` form. |
| `08_af_triage` | depends on | It provides the `skills` Go package that embeds `skills/af-*`, used for the skill text in the prefix, along with the Makefile's narrowed `install-skills`. If it also provides a run-time forge constructor for a recorded repository (for `land`) or a shared earlier-question lookup, this scope reuses them rather than adding second copies. |

## Verified External API

This scope calls no agentkit symbol directly. Everything from agentkit-go reaches it through spec 07's `brain` and `box` packages, whose agentkit signatures that spec records as unverified. The in-repository signatures below were read:

| Symbol | Signature | File |
|---|---|---|
| `issuex.Client.CreatePullRequest` | `(ctx, Repo, CreatePullRequestRequest) (PullRequest, error)` | `issuex/client.go` |
| `issuex.CreatePullRequestRequest` | `struct{ Title, Body, Head, Base string; Draft bool }`. GitLab prefixes `Draft: ` to the title itself (`issuex/gitlab_mr.go:108`). | `issuex/types.go` |
| `issuex.PullRequest` | `Number int`, `URL`, `HTMLURL string`, `Draft bool`, … | `issuex/types.go` |
| `issuex.Client.CreateIssue` | `(ctx, Repo, CreateIssueRequest{Title, Body, Labels, Assignees}) (Issue, error)` | `issuex/client.go` |
| `issuex.Client.GetRepository` | `(ctx, Repo) (Repository, error)`. `Repository.Archived bool`. | `issuex/client.go`, `types.go` |
| `issuex.Client.Authenticated` | `() bool` | `issuex/client.go` |
| `issuex.MaxBodyLength` | `func MaxBodyLength(c Client) int` | `issuex/bodylimit.go` |
| `issuex.IssueRef.URL` | `func (IssueRef) URL() string` | `issuex/types.go` |
| `gitx.Slug` / `UniqueBranchName` | `func Slug(title string) string`; `func UniqueBranchName(ctx, g *Git, name string) string` | `internal/gitx/branch.go` |
| `gitx.Git` methods | `DirtyFiles(ctx) ([]string, error)`; `CurrentBranch(ctx) (string, error)`; `HasRemote(ctx) bool`; `CreateBranch(ctx, name) error`; `Checkout(ctx, name) error`; `CommitAll(ctx, msg) (string, error)`; `CommitAllNoVerify(ctx, msg) (string, error)`; `Push(ctx, branch string, attempts int, log func(string)) error`; `ResetHard(ctx, ref) error`; `Clean(ctx) error`; `CheckoutPaths(ctx, ref string, paths ...string) error`; `ShowFile(ctx, ref, path) (string, bool, error)`; `CommitsSince(ctx, since) ([]Commit, error)`. `CommitAll` returns the short hash. | `internal/gitx/git.go` |
