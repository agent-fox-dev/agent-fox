---
spec_id: "20"
spec_name: "engine_core"
title: "Shared change engine: Run, Ledger, Gate cache, Parking, Branching and Process runner"
status: "active"
created_at: "2026-10-07T15:17:19.28602Z"
updated_at: "2026-10-07T15:17:19.28602Z"
intent_hash: "dc7f86025b669c48b8817576d48aa5d0f21acfe0f4068e679e2b9e091dcc8f5b"
schema_version: 2
source: "docs/prds/13-rebuild-fix-on-a-shared-change-engine.md"
---
## Intent

Create `internal/engine`, a shared library of deterministic pipeline steps — branching, the untracked-file ledger, commit-own, the gate cache, parking and the process runner — so that `codefix` and `codeimpl` become thin pipelines over one engine instead of two divergent copies of the same machine.

## Goals

1. **One engine package.** `internal/engine` exports a `Run` value and a set of step functions that both `codefix` and `codeimpl` call. Branching, the untracked snapshot, change classification, commit-own, parking, the gate cache, index invalidation and cancellation handling live once, in this package.
2. **The ledger is the single source of change truth.** One `Ledger` type replaces `untrackedNow`/`commitOwn`/`leftAlone`/`unlisted`/`ownChange`/`dirtyAfterCommit` in both packages. It records the pre-phase untracked snapshot, classifies each changed path (test, docs, implementation, gate-config, scratch, protected), and produces the commit's file list. Every consumer — the revert check, the structural scan, the scope check, the PR body, `result.changed_files` — reads the ledger.
3. **Gate results are cached by tree hash.** A `GateCache` keyed by `(tree-hash, command)` prevents redundant verification runs. A cache hit is reported as `cached: true` in timings.
4. **Parking is correct on every path.** A `Park` function commits the ledger's change as `wip:` with hooks skipped, under a context cancellation cannot reach (2-minute ceiling), returns the checkout to the base branch, and never leaves a dirty tree on the work branch. Both tools call the same function.
5. **Cancellation is `aborted`.** The engine's process runner distinguishes timeout, cancellation and failure. `checks.Compare` gains an `aborted` verdict when either run was cancelled, and no pipeline compares a cancelled run.
6. **Branching is done once.** Base-branch capture, work-branch creation via `UniqueBranchName`, and merge-base recording happen through one engine function. A run started on a detached HEAD or on a branch the engine would create (`fix/*`, `impl/*`) is refused in preflight.

## Non-goals

- **Prompt documents** (`prompt_documents`): moving prompts from Go string constants to embedded Markdown templates is a later scope.
- **Polyglot structural checks** (`polyglot_structural_checks`): wiring `internal/conform` to read from `lang.Profile` is a later scope.
- **Rewriting codefix as a thin pipeline** (`codefix_on_engine`): the full pipeline rewrite, the `run_checks` tool, the stable-prefix brief, transcript pruning and the targeted revert check are a later scope. This scope delivers the engine; that scope wires `codefix` to it.
- **The `run_checks` model-facing tool.** That belongs in `codefix_on_engine`.
- **New flags, exit codes or envelope fields.** The `aborted` verdict and `cached: true` timing annotation are additive; no schema version bump.
- **Parallel phases or worktrees.**

## Background

### What exists today

`codefix/pipeline.go` and `codeimpl/pipeline.go` each implement the same lifecycle independently:

- **Untracked snapshot:** `codefix/untracked.go` has `untrackedNow` (returns `map[string]bool`) and `commitOwn`/`commitOwnHooks`. `codeimpl/untracked.go` has `noteUntracked`, `leftAlone`, `commit`, `unlisted`, `dirtyAfterCommit` on `RunState`. The logic is the same; the implementations diverge in details (e.g. `codeimpl` tracks `leftWarned` and `foreignSpecPath`; `codefix` does not).

- **Parking:** `codefix/pipeline.go` has `parkWork` (L828–845) that commits under `background(ctx)` and calls `returnCheckout`. `codeimpl/pipeline.go` has `parkWork` (L1536–1551) that does the same with `st.commit`. The two diverge: `codefix` parks under the run's context with hooks on in some paths (#216); `codeimpl` always skips hooks.

- **Branching:** `codefix` calls `gitx.UniqueBranchName` and `git.CreateBranch` inline in `Run` (L300–305). `codeimpl` does the same in `preflight` (L700–710) with additional logic for existing branches.

- **Verification:** `codefix` uses `checks.Run` directly through `runChecks` (L1015–1030). `codeimpl` uses `runGate` (gate.go L95–115) which runs multiple commands. Neither caches results by tree hash.

- **Background context:** Both define identical `background` functions that create `context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)`.

- **Index invalidation:** `codefix` has `invalidate` (L1006–1010) calling `o.Index.Invalidate("")`. `codeimpl` has `st.invalidate()` (L91–95) doing the same on `st.index`.

- **Process runner:** `internal/gitx/exec.go` provides `Runner`, `ExecRunner` and `ReducedEnvRunner`. `internal/checks/checks.go` provides `Run` which uses `gitx.Runner`. Neither distinguishes timeout from cancellation in a way the caller can branch on without inspecting the context — `checks.Result` has `TimedOut` and `Aborted` booleans, but `checks.Compare` does not use `Aborted`.

### Why this matters

The input PRD documents that the two copies have diverged in the wrong direction: `impl` parks under a context cancellation cannot reach and skips hooks; `fix` parks under the run's context with hooks on, and only on one path. `impl` fits its PR body to the forge's limit; `fix` sends it unbounded. The engine eliminates these divergences by making both tools call the same code.

## Requirements

### The `Run` type

`internal/engine` exports a `Run` struct that holds the shared state of one pipeline execution:

- `Root string` — the repository root, symlink-resolved.
- `Git *gitx.Git` — the git handle.
- `Lang lang.Detection` — the detected language profiles (from `internal/lang`).
- `Branch BranchInfo` — base branch, work branch, merge-base commit, and whether the branch already existed.
- `Ledger *Ledger` — the change tracker (§Ledger below).
- `Gate *GateCache` — the verification result cache (§Gate cache below).
- `Index tools.Index` — the code-search index, nil when absent.
- `Scratch string` — the scratch directory path relative to root (from `agentrun.newScratch`).
- `Progress toolio.Progress` — the progress reporter.
- `Record *toolio.Run` — the run recorder for warnings, timings and side effects.

The `Run` is built by a `NewRun` function that takes the root, git handle, options and detection, and returns the `Run` or an error. It does not own the pipeline's control flow; each tool's pipeline calls engine functions in sequence.

### The Ledger

The `Ledger` type replaces all untracked-file tracking and commit logic in both packages.

**Snapshot.** `Ledger.Snapshot(ctx)` records the untracked files currently in the tree. It is called before each writing phase. The snapshot is a `map[string]bool`.

**Owned and protected paths.** `Ledger.SetOwned(paths ...string)` marks paths the run owns (e.g. `.agent-fox/scratch/`). `Ledger.SetProtected(paths ...string)` marks paths that are program-owned and must not be changed by a phase (e.g. the spec package in `impl`). Protected paths that a phase changed are reverted with a warning.

**Change computation.** `Ledger.Changes(ctx, git, since string) ([]Change, error)` computes the change as git's name-status from `since` plus untracked files not in the snapshot. Each `Change` carries:
- `Path string`
- `Status string` (A, M, D, from git or "?" for new untracked)
- `Kind string` — one of `"test"`, `"docs"`, `"implementation"`, `"gate-config"`, `"scratch"`, `"protected"`, classified using the `lang.Detection`'s `TestFile`, `project.IsDocsFile`, the gate-config classifier (moved from `codeimpl/gateedits.go`), and path-prefix checks for scratch and protected.

**Commit.** `Ledger.Commit(ctx, git, message string, noVerify bool, reported []FileChange) (string, error)` commits everything except the left-alone untracked files (those in the snapshot). It:
1. Calls `git.UntrackedFiles` to find current untracked files.
2. Separates them into `leave` (in the snapshot or foreign-spec paths when configured) and `unlisted` (new files the phase's report did not list).
3. Warns with `WarnUntrackedFilesLeftAlone` for `leave` and `WarnUnlistedFileCommitted` for `unlisted`.
4. Calls `git.CommitAllExcept(ctx, message, noVerify, leave)`.

The `foreignSpecPath` logic from `codeimpl` is supported via a configurable predicate `Ledger.SetForeignPath(fn func(string) bool)`.

**Gate-edit detection.** `Ledger.FlagGateEdits(ctx, git, since string, run *toolio.Run)` replaces `codeimpl/gateedits.go`'s `flagGateEdits`. It uses the same `gateEdits` function (moved to the engine) and warns via the run recorder. The `gateConfigFile`, `testFile`, `goldenFile` and `skipMarker` classifiers move to the engine; `testFile` delegates to `lang.Detection.TestFile` instead of hardcoding suffixes.

**OwnChange.** `Ledger.OwnChange(ctx, git) bool` reports whether the tree has changes the run made (anything not in the snapshot), replacing `ownChange` in `codefix`.

### The Gate cache

`GateCache` caches verification results keyed by `(treeHash, command)`.

**Tree hash.** The tree hash is computed by `git write-tree` over the current index (after staging), combined with the sorted list of untracked files the ledger knows about. This is a content-addressable key: two runs on the same tree with the same command produce the same key.

**Lookup.** `GateCache.Run(ctx, runner, root, command, timeout, label string) checks.Result` checks the cache first. On a hit it returns the cached result and records a timing entry with `cached: true`. On a miss it calls `checks.Run`, stores the result, and returns it.

**Multi-command gate.** `GateCache.RunGate(ctx, runner, root string, commands []string, timeout, label string) GateResult` runs multiple commands (as `codeimpl/gate.go`'s `runGate` does), checking the cache for each. The `GateResult` type (currently in `codeimpl/types.go`) moves to the engine with its methods (`Ran`, `OK`, `couldNotRun`, `aborted`, `failing`).

**CompareGate.** The `compareGate` function and `landable` function move from `codeimpl/gate.go` to the engine.

### Parking

`engine.Park(ctx context.Context, run *Run, message string, reported []FileChange) (commit string, err error)` parks the current work:

1. Creates a background context via `context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)`.
2. Checks whether the tree has own changes via `run.Ledger.OwnChange`.
3. If yes, commits via `run.Ledger.Commit` with `noVerify: true`.
4. Checks out the base branch via `run.Git.Checkout`.
5. Returns the commit hash, or empty string if nothing was committed.

On any error during commit, it warns with `WarnCommitNotParked`. On any error during checkout, it warns with `WarnCheckoutNotRestored`. The function never returns an error that would prevent the pipeline from reporting its result; errors are warnings.

`engine.ReturnCheckout(ctx, run *Run)` is the checkout-only variant for paths that have nothing to park.

### Branching

`engine.SetupBranch(ctx context.Context, run *Run, prefix string, number int, title string) error` handles branch creation:

1. Captures the base branch via `git.BaseBranch(ctx)`.
2. Refuses if the base is a detached HEAD (empty string from `CurrentBranch`), or if the base matches `fix/*` or `impl/*` patterns.
3. Computes the work branch name via `gitx.BranchName` and `gitx.UniqueBranchName`.
4. Creates the branch via `git.CreateBranch`.
5. Records base, work branch and merge-base on `run.Branch`.
6. Invalidates the index.

For `codeimpl`'s case where the branch may already exist, `engine.ResumeBranch(ctx, run *Run, branch string) error` checks out an existing branch, handles parked-attempt detection and reset, and records the branch info.

### The `aborted` verdict

`checks.Compare` is extended: when either the baseline or the after result has `Aborted == true`, it returns a new `VerdictAborted Verdict = "aborted"`. `VerdictAborted` is not landable. No pipeline compares a cancelled run with anything; the `aborted` verdict is the mechanism that enforces this.

### Index invalidation

`engine.Invalidate(run *Run)` invalidates the code-search index when the run has one. This replaces the per-package `invalidate` functions.

### Background context

`engine.Background(ctx context.Context) (context.Context, context.CancelFunc)` returns `context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)`. This replaces the identical `background` functions in both packages.

### Process runner integration

The engine does not introduce a new process runner; it uses `gitx.Runner` and `checks.Run` as they exist. The `checks.Result` type already has `TimedOut` and `Aborted` fields. The engine's contribution is that `checks.Compare` now uses `Aborted` to produce the `aborted` verdict, and the `GateCache` and `Park` functions use the runner consistently.

### Preflight helpers

`engine.CommonPreflight(ctx, run *Run, opts PreflightOpts) error` performs the checks both tools share:
- Repository existence (`git.IsRepo`).
- Clean working tree (`git.DirtyFiles`).
- Base branch capture and validation (not detached, not a work branch).
- Remote existence when pushing.
- Forge authentication when opening PRs.

Each tool's preflight calls this first, then adds its own checks (spec validation for `impl`, verify-command detection for `fix`).

### Push with retries

`engine.Push(ctx context.Context, run *Run, attempts int) error` wraps `git.Push` with progress reporting and side-effect recording, replacing the inline push logic in both pipelines.

### Pull request

`engine.OpenPullRequest(ctx context.Context, run *Run, forge issuex.Client, target issuex.Repo, req issuex.CreatePullRequestRequest) (string, error)` opens a PR and truncates the body to the forge's limit (using `issuex.MaxBodyLength`), replacing the unbounded send in `codefix` and the bounded send in `codeimpl`.

### Migration path

Both `codefix` and `codeimpl` are updated to call engine functions instead of their own copies. The migration is incremental: each duplicated function is replaced by a call to the engine, and the package-local function is removed. The `codeimpl.RunState` retains fields specific to `impl` (spec, tasks, survey, prior deviations) and gains a `*engine.Run` field for the shared state. `codefix.Options` gains a `*engine.Run` field similarly.

The migration does not change any flag, exit code, envelope field or schema version. The `--schema` golden files do not change.

### Error handling

- `NewRun` returns an error if the root is not a git repository or the detection fails (detection never fails per `lang.Detect`'s contract, so this is only for git).
- `Ledger.Snapshot` never fails: if `git.UntrackedFiles` errors, the snapshot is nil (meaning "unknown, commit everything").
- `GateCache.Run` propagates `checks.Run`'s result as-is; a cache miss that fails to compute the tree hash falls through to an uncached run.
- `Park` never returns an error that stops the pipeline; failures are warnings.
- `SetupBranch` returns an error for a refused base branch (detached HEAD, work-branch pattern); the caller maps it to a preflight failure.

### Testing

- Tests in `internal/engine` use real temporary git repositories (via `gitx.New` on `t.TempDir()`), not mocks.
- A test creates a repository, makes changes, and verifies that `Ledger.Changes` classifies them correctly using `lang.Detect`.
- A test verifies that `GateCache` returns cached results for the same tree hash and re-runs for a different tree.
- A test verifies that `Park` commits under a cancelled context and returns the checkout to the base branch.
- A test verifies that `checks.Compare` returns `VerdictAborted` when either result has `Aborted == true`.
- A test verifies that `SetupBranch` refuses a detached HEAD and a `fix/*` base branch.

## Design Decisions

1. **The engine is a library of steps, not a framework.** The input PRD says "a library of steps over one `Run` value, not a framework: each tool's pipeline is still a readable sequence in its own package." This means `internal/engine` exports functions like `Park`, `SetupBranch`, `Ledger.Commit` that the pipeline calls in order, rather than a `Run` method that orchestrates everything. This keeps each tool's `pipeline.go` readable and avoids a God object.

2. **`GateResult` moves to the engine.** Today `GateResult` lives in `codeimpl/types.go` and `codefix` uses `checks.Result` directly (it runs one command, not a gate of multiple). The engine's `GateResult` supports both: a single-command gate is a `GateResult` with one check. This unifies the verification interface.

3. **The `aborted` verdict is added to `checks.Compare`, not as a separate function.** The input PRD says "a new verdict, `aborted`, when either run was cancelled, and no pipeline compares a cancelled run with anything." Adding it to `Compare` is the minimal change that fixes the four misreports from #216, because every caller already uses `Compare`.

4. **The ledger's `foreignSpecPath` is a configurable predicate, not hardcoded.** `codeimpl` needs to exclude files under the specs directory outside its own spec package. `codefix` has no such concept. Rather than hardcoding `impl`-specific logic in the engine, the ledger accepts a predicate via `SetForeignPath`.

5. **Tree hash for the gate cache uses `git write-tree`.** The input PRD says "keyed by the tree hash (`git write-tree` over the index plus the untracked files the ledger knows)." `git write-tree` requires staging first, which is a side effect. Instead, the engine computes the key as `git rev-parse HEAD^{tree}` (the tree of the current commit) when the tree is clean, and falls back to staging + `git write-tree` + unstaging when there are uncommitted changes. This avoids unnecessary staging in the common case where the gate runs right after a commit.

6. **`Park` always skips hooks.** The input PRD says "hooks skipped." Today `codefix` sometimes runs hooks on park commits and `codeimpl` always skips them. The engine always skips hooks on `wip:` commits, matching `codeimpl`'s behaviour, because a park commit is temporary and running hooks on it can fail and leave the tree dirty.

7. **The `clean` (hermetic) gate logic stays in `codeimpl` for now.** The hermetic/clean-environment gate (`cleangate.go`) is specific to `codeimpl`'s conformance stage. Moving it to the engine would pull in conformance logic that `codefix` does not use. It can move later when `codefix_on_engine` needs it.

8. **`projectInstructions` stays in each package for now.** Both `codefix/prompts.go` and `codeimpl/prompts.go` define identical `projectInstructions` functions. Moving them to the engine would pull prompt-related code into a package that is about git and verification. The `prompt_documents` scope will unify them.

9. **The engine does not own the `agentrun.Runner` or phase execution.** Phase execution (model calls, tool registration, the agent loop) stays in `internal/agentrun`. The engine owns the deterministic steps around phases: branching, committing, verifying, parking. This keeps the engine testable without a model.

10. **`gateConfigFile` and `testFile` classifiers move to the engine but delegate to `lang.Detection`.** The `testFile` function in `codeimpl/gateedits.go` hardcodes suffixes; in the engine it delegates to `lang.Detection.TestFile`. The `gateConfigFile` function uses `lang.Profile.GateConfig` when available and falls back to the current hardcoded list for files not covered by any profile (e.g. `.github/workflows/`).

## Dependencies

| Spec | Reason |
|---|---|
| `19_language_profiles` | The engine's `Run` holds a `lang.Detection` and the ledger's change classification uses `lang.Detection.TestFile` and `lang.Profile.GateConfig`. The gate cache uses `lang.Detection.VerifyCommand` as a fallback. |

## Verified External API

All external APIs used by this scope are already in use in the repository. No new external packages are introduced.

| Symbol | Package | Signature | Source |
|---|---|---|---|
| `gitx.Git.CommitAllExcept` | `internal/gitx` | `func (g *Git) CommitAllExcept(ctx context.Context, message string, noVerify bool, leave []string) (string, error)` | `internal/gitx/git.go` L232 |
| `gitx.Git.UntrackedFiles` | `internal/gitx` | `func (g *Git) UntrackedFiles(ctx context.Context) ([]string, error)` | `internal/gitx/git.go` L62 |
| `gitx.Git.ChangedFiles` | `internal/gitx` | `func (g *Git) ChangedFiles(ctx context.Context, since string) ([]string, error)` | `internal/gitx/git.go` L151 |
| `gitx.Git.CreateBranch` | `internal/gitx` | `func (g *Git) CreateBranch(ctx context.Context, name string) error` | `internal/gitx/git.go` L137 |
| `gitx.Git.Checkout` | `internal/gitx` | `func (g *Git) Checkout(ctx context.Context, name string) error` | `internal/gitx/git.go` L143 |
| `gitx.Git.BaseBranch` | `internal/gitx` | `func (g *Git) BaseBranch(ctx context.Context) string` | `internal/gitx/git.go` L92 |
| `gitx.Git.MergeBase` | `internal/gitx` | `func (g *Git) MergeBase(ctx context.Context, ref string) (string, error)` | `internal/gitx/git.go` L504 |
| `gitx.Git.Push` | `internal/gitx` | `func (g *Git) Push(ctx context.Context, branch string, attempts int, log func(string)) error` | `internal/gitx/git.go` L280 |
| `gitx.UniqueBranchName` | `internal/gitx` | `func UniqueBranchName(ctx context.Context, g *Git, name string) string` | `internal/gitx/branch.go` L71 |
| `gitx.BranchName` | `internal/gitx` | `func BranchName(prefix string, number int, title string) string` | `internal/gitx/branch.go` L55 |
| `checks.Run` | `internal/checks` | `func Run(ctx context.Context, r gitx.Runner, dir, command string, timeout time.Duration) Result` | `internal/checks/checks.go` L99 |
| `checks.Compare` | `internal/checks` | `func Compare(baseline, after Result) Verdict` | `internal/checks/checks.go` L205 |
| `checks.Result` | `internal/checks` | `type Result struct { Command, Output string; Skipped, OK, TimedOut, Aborted bool; ExitCode int; DurationMS int64 }` | `internal/checks/checks.go` L24 |
| `checks.Verdict` | `internal/checks` | `type Verdict string` | `internal/checks/checks.go` L186 |
| `issuex.MaxBodyLength` | `issuex` | `func MaxBodyLength(c Client) int` | `issuex/bodylimit.go` L23 |
| `toolio.WarnCommitNotParked` | `internal/toolio` | `WarnCode = "commit_not_parked"` | `internal/toolio/warncode.go` |
| `toolio.WarnCheckoutNotRestored` | `internal/toolio` | `WarnCode = "checkout_not_restored"` | `internal/toolio/warncode.go` |
| `toolio.WarnUntrackedFilesLeftAlone` | `internal/toolio` | `WarnCode` | `internal/toolio/warncode.go` |
| `toolio.WarnUnlistedFileCommitted` | `internal/toolio` | `WarnCode` | `internal/toolio/warncode.go` |
| `toolio.WarnGateEdited` | `internal/toolio` | `WarnCode` | `internal/toolio/warncode.go` |
| `tools.Index` | `agentkit-go/tools` | `interface` | **unverified** — used via `internal/agentrun/index.go` |
| `context.WithoutCancel` | `context` (stdlib) | `func WithoutCancel(parent Context) Context` | stdlib |
