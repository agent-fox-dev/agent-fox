---
spec_id: "23"
spec_name: "codefix_on_engine"
title: "Rewrite codefix as a thin pipeline over the shared engine"
status: "active"
created_at: "2026-10-07T16:09:57.919525Z"
updated_at: "2026-10-07T16:09:57.919525Z"
intent_hash: "a20f0c68390d6cf7bd1ac564685bc926fe64c45eaa184f335331fe9c21db10d1"
schema_version: 2
source: "docs/prds/13-rebuild-fix-on-a-shared-change-engine.md"
---
## Intent

Rewrite `codefix`'s pipeline to call `internal/engine` for branching, the ledger, commit-own, parking, the gate cache and verification, add the `run_checks` model-facing tool, wire the prompt-document loader for a stable-prefix brief and per-phase templates, enable transcript pruning, write check logs to the scratch directory, and implement the targeted revert check — so that `fix` is a thin, readable sequence over the shared engine and meets the token-halving acceptance criteria.

## Goals

1. **`codefix/pipeline.go` calls the engine.** Every duplicated function — `untrackedNow`, `commitOwn`, `commitOwnHooks`, `ownChange`, `parkWork`, `returnCheckout`, `background`, `invalidate`, `runChecks`, `branchPrefix`/branch creation, `openPullRequest` (body truncation) — is replaced by a call to the corresponding `internal/engine` function. The package-local copies are deleted.
2. **The `run_checks` model-facing tool.** The implement phase gains a `run_checks` tool with `scope` (`"lint"`, `"targeted"`, `"all"`) and optional `tests` parameters. Its results feed the gate cache. The shell refuses the suite's own command in the implement phase, naming `run_checks` as the alternative. A third `all` call within one phase answers from the cache with a note.
3. **Prompts are rendered from documents.** System prompts, phase messages, tool description overrides, guard refusals and handler rejections in `codefix` are replaced by calls to the `internal/engine/prompt.Loader`, rendering from the templates and views that `21_prompt_documents` defines. No model-facing string constant longer than one sentence remains in `codefix`.
4. **The stable-prefix brief.** The run brief — report, repository map, project instructions, steering, language block, baseline verdicts, gate commands and `--context` — is rendered once into a `BriefView`, and the system prompt and tool schemas are identical across phases of the same kind within one run. Phase- and attempt-specific material follows in a second user message.
5. **Transcript pruning.** The implement phase installs AgentKit's pruning transform (when available from AgentKit PRD 06 §4): tool results older than eight turns are replaced by a one-line stub. Summarisation stays as the backstop at 60% of the window.
6. **Check logs in the scratch directory.** Every external command the engine runs writes its full output to `.agent-fox/scratch/<session_id>/checks/<label>.txt`. The prompt carries only the last ten lines inline plus a `ref` to the log path. The scratch directory is one per run (not per phase), created at run start and removed at run end.
7. **Targeted revert check.** The revert check runs the profile's `Targeted` command for exactly the tests the change added or edited (from the ledger's classification). For a profile with `InlineTests`, the revert is by declaration (line ranges from the outline) rather than by file. Only when the profile has no targeted form does the full suite run. The implementation is held in a commit object before anything is put back.
8. **Half the tokens.** On the navigation baseline, `fix`'s summed `context_tokens` per phase is at most half of today's, its cost at most 60%, and the fixes landed are the same.
9. **The interface is untouched.** No flag, exit code, event type or required result field changes. The `--schema` golden files change only by the additive fields from §8 of the input PRD.

## Non-goals

- **Rewriting `codeimpl`** on the engine. That is `14-rebuild-impl-on-the-shared-change-engine.md` / a future spec.
- **New flags, exit codes or event types.** Additive result fields (`result.language`, `result.revert_check.mode`, `timings[].cached`, `usage.phases[].pruned_tokens`, `prompt_templates`, `prompts_dir` artifact, `prompts_not_written` warning) are allowed and listed below; nothing a caller must read changes.
- **Parallel phases or worktrees.** A `fix` is one branch and one sequence.
- **Per-project prompt overrides.** The prompt documents are the program's, bundled into the binary.
- **Changing the spec format, `afspec`, `triage` or `spec`.** They keep using `internal/project`.
- **AgentKit PRD 06 implementation.** This spec consumes what AgentKit PRD 06 delivers (stable prefix breakpoint, pruning transform, process runner). Where those are not yet available, the engine's own equivalents are used and the token goal is aspirational until they land.
- **Creating `internal/lang` or `internal/engine`.** Those are delivered by `19_language_profiles` and `20_engine_core` respectively. This spec wires `codefix` to them.
- **Creating the prompt template infrastructure.** That is delivered by `21_prompt_documents`. This spec uses the loader, views and templates it defines.
- **Wiring `internal/conform` to the profile.** That is delivered by `22_polyglot_structural_checks`. This spec passes `lang.Detection` to `conform.Scan` via `ScanInput.Lang`.

## Background

### What exists today

`codefix/pipeline.go` is a 1087-line file that implements the full lifecycle inline: preflight, branch creation, the analysis phase, the implement phase, verification, the revert check, commit, push, pull request, and parking. It duplicates logic that `codeimpl/pipeline.go` also carries:

- **Untracked snapshot and commit:** `codefix/untracked.go` has `untrackedNow` (returns `map[string]bool`) and `commitOwn`/`commitOwnHooks`. `codeimpl/untracked.go` has `noteUntracked`, `leftAlone`, `commit`, `unlisted`, `dirtyAfterCommit`. The logic is the same; the implementations diverge.
- **Parking:** `codefix/pipeline.go` L828–845 has `parkWork` that commits under `background(ctx)`. `codeimpl/pipeline.go` L1536–1551 has its own `parkWork`. The two diverge: `codefix` parks under the run's context with hooks on in some paths (#216); `codeimpl` always skips hooks.
- **Branch creation:** `codefix` calls `gitx.UniqueBranchName` and `git.CreateBranch` inline at L300–305. `codeimpl` does the same at L700–710.
- **Background context:** Both define identical `background` functions.
- **Index invalidation:** Both define identical `invalidate` functions.
- **PR body truncation:** `codeimpl` truncates to the forge's limit; `codefix` sends unbounded.

Prompts are Go string constants (`analysisSystemPrompt`, `implementSystemPrompt` in `codefix/phases.go`) and `fmt.Fprintf` assembly (`analysisPrompt`, `implementPrompt`, `reportBlock`, `baselineBlock`, `criteriaBlock`, `contextBlock`, `instructionsBlock` in `codefix/prompts.go`). Tool descriptions are patched inline by `internal/agentrun/policy.go`'s `describeForPhase`, `noteScratch` and `noteReadRoots`.

The revert check (`codefix/pipeline.go` L793–813, `internal/conform/revert.go`) runs the full suite with every non-test file put back. It cannot see inline tests (Rust `#[cfg(test)]`), and it takes minutes on a large suite.

The verification command runs three times in a successful `fix`: baseline, landing gate, and revert check — each a full-suite run. There is no result cache.

The scratch directory is per-phase (random subdirectory under `.agent-fox/scratch/`), created and removed by `internal/agentrun/scratch.go`. Check output is carried inline in prompts (40–80 lines per failing command).

### Why this matters

The engine, the prompt documents, the language profiles and the polyglot structural checks are infrastructure. This spec is where they become a working tool: `fix` rebuilt on the shared engine, with measurable token savings and correct behaviour on every path.

## Requirements

### Pipeline rewrite

`codefix/pipeline.go`'s `Run` function is rewritten as a sequence of engine calls. The structure becomes:

1. `engine.NewRun` — build the shared `Run` value from the workspace, git handle, and `lang.Detect`.
2. `engine.CommonPreflight` — repository, clean tree, base branch, remote, forge auth.
3. Fix-specific preflight — verify-command detection via `lang.Detection.VerifyCommand`, baseline via `engine.GateCache.Run`.
4. Analysis phase — read-only agent, prompts rendered from `fix/analyse.system.md` and `fix/analyse.md`.
5. `engine.SetupBranch` — create the work branch.
6. `engine.Ledger.Snapshot` — record untracked files.
7. Implement phase — writing agent with `run_checks` tool, prompts from `fix/implement.system.md` and `fix/implement.md`.
8. `engine.Ledger.Changes` and `engine.Ledger.Commit` — classify and commit the change.
9. Verification — `engine.GateCache.Run` for the landing gate.
10. Revert check — targeted, via the engine's revert function.
11. Structural scan — `conform.Scan` with `ScanInput.Lang` set.
12. Review — unchanged, on a fresh context.
13. Push and PR — `engine.Push`, `engine.OpenPullRequest`.
14. Parking on every non-landing path — `engine.Park`.

Every duplicated function in `codefix` (`untrackedNow`, `commitOwn`, `commitOwnHooks`, `ownChange`, `parkWork`, `returnCheckout`, `background`, `invalidate`) is deleted. `codefix/untracked.go` is removed entirely.

### The `run_checks` tool

A new tool `run_checks` is registered in the implement phase:

- **Parameters:** `scope` (enum: `"lint"`, `"targeted"`, `"all"`, default `"all"`) and `tests` (optional array of strings, the test names or files to run).
- **Behaviour:**
  - `"lint"` runs the profile's `Lint` command.
  - `"targeted"` runs the profile's `Targeted` command for the named tests, or the tests the ledger says the phase has written so far when `tests` is empty.
  - `"all"` runs the full gate (the verification command).
- **Result shape:** exit code, `ok` boolean, the last twenty lines of output, and the path of the full log in the scratch directory.
- **Gate cache integration:** results are stored in the `GateCache`. A phase that ends by running `all` on the tree it submits has already run its landing gate; the engine's gate is then a cache hit.
- **Budget:** `run_checks` with scope `"all"` is allowed at most twice per phase. A third call answers from the cache with a note saying the budget is exhausted.
- **Shell refusal:** the shell refuses the suite's own command and the profile's `Test` command (in its untargeted form) in the implement phase, naming `run_checks` as the alternative. A targeted form (with a file, a test name, a package) is never refused. This uses the existing `GuardOptions.Suite` mechanism.

The tool is defined in `codefix` (or in `internal/engine` if both tools will use it), registered as a `core.Tool` alongside `submit_implementation`, and its description is rendered from `tools/run_checks.md`.

### Prompt rendering

All model-facing text in `codefix` is rendered through the `internal/engine/prompt.Loader`:

- `analysisSystemPrompt` → `loader.Render("fix/analyse.system.md", view)`
- `implementSystemPrompt` → `loader.Render("fix/implement.system.md", view)`
- `analysisPrompt(in)` → `loader.Render("fix/analyse.md", phaseView)`
- `implementPrompt(in)` → `loader.Render("fix/implement.md", phaseView)`
- `reportBlock`, `baselineBlock`, `criteriaBlock`, `contextBlock`, `instructionsBlock` → partials composed by the phase templates.
- Tool descriptions for `execute`, `write_file`, `edit_file` → `tools/*.md` templates.
- Guard refusals → `refusals/*.md` templates.
- Handler rejections in `submitAnalysisTool` and `submitImplementationTool` → `rejections/*.md` templates where the text exceeds one sentence.

The Go constants `analysisSystemPrompt` and `implementSystemPrompt` in `codefix/phases.go` are deleted. The builder functions in `codefix/prompts.go` (`analysisPrompt`, `implementPrompt`, `reportBlock`, `baselineBlock`, `criteriaBlock`, `contextBlock`, `instructionsBlock`, `projectInstructions`) are deleted; their logic moves to the view construction and the templates.

### The stable-prefix brief

The run brief is built once per run as a `BriefView` and rendered from `brief/fix.md`. It contains:
- The language block (from `lang.Detection`).
- The repository map.
- The project instructions (AGENTS.md / CLAUDE.md).
- The steering file.
- The gate commands and their baseline verdicts.
- The `--context` value.
- The report (fenced, with provenance).

The system prompt and tool schemas are identical for every phase of one kind in one run. The brief is the first user message, followed by a cache breakpoint (when AgentKit PRD 06 §3 is available). Phase-specific material (diagnosis, criteria, branch, date) follows in a second user message.

When AgentKit's cache breakpoint is not yet available, the brief is still rendered once and placed first in the user prompt, but the cache benefit is limited to what the provider achieves from prefix matching alone.

### Transcript pruning

The implement phase installs AgentKit's pruning transform (PRD 06 §4) when available:
- A tool result older than eight turns is replaced by a one-line stub naming the call and its size.
- A `read_file` of a path and range the transcript already holds unchanged returns a reference to that turn.
- Summarisation stays as the backstop at 60% of the window.

When the pruning transform is not yet available from AgentKit, the implement phase runs without it and the token goal is met by the brief and the check-log changes alone, or is deferred.

### Scratch directory: one per run

The scratch directory changes from per-phase to per-run:
- Created at run start as `.agent-fox/scratch/<session_id>/`.
- The session id is from the envelope, so the report and the logs pair up.
- Ignored by git through its own `.gitignore`.
- Named in tool descriptions once (stable across phases).
- Removed when the run ends.

Within it, the engine writes `checks/<label>.txt` for every command it runs (baseline, verification, revert check, and every `run_checks` call). The prompt carries only the last ten lines inline plus a `ref(path)` reference to the full log. This replaces the current 40–80 lines of inline output per failing command.

The `internal/agentrun/scratch.go` per-phase scratch creation is replaced by the engine's per-run scratch for `codefix`. The `newScratch` function remains available for other tools that have not migrated.

### Targeted revert check

The revert check is rewritten to use the profile's `Targeted` command:

1. The ledger's classification names the tests the change added or edited. For a profile with `InlineTests`, the test names come from the outline of the changed files (declarations the profile's test convention marks) rather than from file names.
2. The check runs the profile's `Targeted` command for exactly those tests. Only when the profile has no targeted form does the full suite run.
3. Before anything is put back, the implementation is held in a commit object (`git stash create` or a `wip:` commit on the work branch), so a crash or `kill -9` during the check loses nothing.
4. The check runs under the run's context: Ctrl-C aborts it and the tree is restored from the commit object.
5. A compile error with the implementation removed is classified by the profile (build failure vs test failure). Only a test failure sets `proves`. Where the profile cannot tell, `proves` is set and `reason` says the failure was not classified.
6. For inline-test profiles, the revert puts back the implementation declarations (by line range from the outline) rather than the whole file, so the tests the change wrote stay in place.

`result.revert_check` gains a `mode` field: `"targeted"` or `"suite"`, and `declarations` when the revert was by declaration.

### Additive envelope fields

- `result.language`: `{primary: string, detected: []string, verify: {source: "makefile"|"profile"|"flag"|"none"}}`.
- `result.revert_check.mode`: `"targeted"` or `"suite"`.
- `result.revert_check.declarations`: the declarations reverted, when the revert was by declaration.
- `timings[].cached`: `true` on a gate answered from the cache.
- `usage.phases[].pruned_tokens`: tokens the pruning transform removed (when available).
- `prompt_templates`: map of document name to SHA-256 (in the full report, from `21_prompt_documents`).
- `prompts_dir` artifact kind (from `21_prompt_documents`).
- `prompts_not_written` warning code (from `21_prompt_documents`).

None of these is required of a caller. `--schema` golden files change only by these fields. `schema_version` stays at its current major.

### Cancellation

On every path, a cancelled run is reported as `aborted` (using the `aborted` verdict from `20_engine_core`), leaves no dirty tree on the work branch, and is resumable by re-running the same command. The engine's `Park` function handles this. Specifically:
- Ctrl-C during the implement phase: the phase is cancelled, `engine.Park` commits the work as `wip:`, the checkout returns to the base branch, exit 4 with `category: aborted`.
- Ctrl-C during the landing gate: the gate result has `Aborted == true`, `checks.Compare` returns `VerdictAborted`, `engine.Park` is called, exit 4.
- Ctrl-C during the revert check: the check is cancelled, the implementation is restored from the commit object, `engine.Park` is called, exit 4.

### `conform.Scan` integration

The `conform.Scan` call in `prove` passes `ScanInput.Lang` set to the `lang.Detection` from the engine's `Run`, so the structural checks use the profile's classifiers (from `22_polyglot_structural_checks`).

### Error handling

- When `internal/engine` or `internal/lang` is not yet built, the spec cannot be implemented. This spec depends on those being built first.
- A `run_checks` call that fails to compute the tree hash for the cache falls through to an uncached run.
- A `run_checks` call with `scope: "targeted"` on a profile with no `Targeted` command falls back to `scope: "all"` with a note.
- The scratch directory creation failure is a warning, not a fatal error; the run continues without check logs in the scratch directory.
- The targeted revert check's outline failure (cannot parse the file for declarations) falls back to the file-level revert.

### Documentation updates

Per the steering directives:
- `docs/cli.md`: the verdict table gains `aborted`; the revert check section describes targeted mode; the allowlist paragraph says it is generated from the profiles; `run_checks` is documented under *What the model may and may not do*; the additive fields are listed.
- `docs/model-usage.md`: the run brief and the stable prefix; the pruning transform; the scratch logs; `run_checks`; the *Prompt templates* section is rewritten for the engine's documents, views and helpers.
- `docs/configuration.md`: the `prompts/` subdirectory under *State directory*; the language profiles and what each detects.
- `docs/development.md`: `internal/lang` and `internal/engine` in the layout; per-language baseline rows.

## Design Decisions

1. **The `run_checks` tool lives in `codefix`, not in `internal/engine`.** The tool is specific to the implement phase's agent loop and registers as a `core.Tool`. The engine provides the `GateCache` it writes to; the tool is the glue between the agent and the cache. If `codeimpl` needs the same tool (likely), it can import it or the tool can move to the engine at that point.

2. **The `run_checks` budget is two `all` calls per phase, not per run.** The input PRD says "at most twice per phase." Since `fix` has one implement phase, this is effectively two per run. The budget is enforced by a counter in the tool handler, not by the cache.

3. **The scratch directory is per-run, keyed by session id.** The input PRD says "one scratch directory per run" with the session id as the name. This replaces the per-phase random directory from `internal/agentrun/scratch.go`. The session id is available from the envelope and is already in the report, so the pairing is automatic.

4. **The targeted revert check falls back to the full suite when the profile has no `Targeted` command.** The input PRD says "only when the profile has none does it run the suite." The `generic` profile and any profile without a targeted form get the full suite, which is the current behaviour.

5. **The implementation is held in a commit object, not a stash.** `git stash create` creates a commit object without updating the stash reflog, which is cleaner than a `wip:` commit that must be undone. However, `git stash create` requires a dirty tree, which may not be the case after a commit. The engine uses `git stash create` when the tree is dirty and a `wip:` commit (with `git reset --soft` to undo) when it is clean. The choice is an implementation detail; the requirement is that the implementation is recoverable after a crash.

6. **Transcript pruning is optional.** The input PRD ties the token goal to AgentKit PRD 06 §3 and §4. If those are not available when this spec is built, the brief and the check-log changes provide partial savings, and the pruning is wired in when AgentKit delivers it. The spec does not block on AgentKit.

7. **The brief is placed as the first user message, not as a separate message with a cache breakpoint.** The cache breakpoint is an AgentKit PRD 06 §3 feature. Without it, the brief is the first part of the user prompt, and the provider's prefix cache handles what it can. When the breakpoint is available, the brief becomes a separate message with the breakpoint after it.

8. **`codefix/prompts.go` is deleted, not refactored.** Every function in it (`reportBlock`, `baselineBlock`, `criteriaBlock`, `contextBlock`, `analysisPrompt`, `implementPrompt`, `instructionsBlock`, `projectInstructions`) is replaced by a template partial or a view field. The file is removed entirely.

9. **`codefix/untracked.go` is deleted.** All untracked-file logic moves to the engine's `Ledger`. The file is removed.

10. **The `run_checks` tool description is a template.** It is rendered from `tools/run_checks.md` with the `PhaseView`, which carries the suite commands, the targeted-test form, and the budget. This keeps the description auditable and consistent with the other tool descriptions.

11. **The revert check's inline-test handling generalises `keepRustTests`.** Today `keepRustTests` in `internal/conform/revert.go` is hardcoded for Rust's `#[cfg(test)]` modules. The targeted revert check uses the profile's `InlineTests` flag and the outline's declaration ranges to revert by declaration for any language with inline tests. The Rust-specific `rustTestModule` function remains as the backend for the Rust profile; other profiles with inline tests get their own backends as they are added.

12. **The `result.language` field is populated from `lang.Detection`.** `Primary` is the detection's primary profile name, `detected` is the list of all detected profile names, and `verify.source` is how the verification command was resolved (`"makefile"`, `"profile"`, `"flag"` for `--verify`, `"none"` for `--no-verify`).

## Dependencies

| Spec | Reason |
|---|---|
| `19_language_profiles` | The pipeline uses `lang.Detect` for language detection, `lang.Detection.VerifyCommand` for verification command resolution, `lang.Detection.TestFile` for test classification, `lang.Profile.Targeted` for the targeted revert check, and `lang.Profile.Programs` for the shell allowlist. |
| `20_engine_core` | The pipeline calls `engine.NewRun`, `engine.SetupBranch`, `engine.Ledger`, `engine.GateCache`, `engine.Park`, `engine.Push`, `engine.OpenPullRequest`, `engine.CommonPreflight`, `engine.Background`, and `engine.Invalidate`. The `aborted` verdict from `checks.Compare` is used for cancellation handling. |
| `21_prompt_documents` | The pipeline uses `prompt.Loader` to render all system prompts, phase messages, tool descriptions, refusals and rejections from embedded Markdown templates. The `BriefView`, `PhaseView` and `AttemptView` types are used to build the views. Run-time prompt auditing writes to the state directory. |
| `22_polyglot_structural_checks` | The `conform.Scan` call passes `ScanInput.Lang` so the structural checks use the profile's classifiers. The revert check uses `Profile.InlineTests` to decide file-level vs declaration-level revert. |

## Verified External API

All external APIs are already in use in the repository or are defined by the earlier specs in this split. No new external packages are introduced.

| Symbol | Package | Signature | Source |
|---|---|---|---|
| `engine.NewRun` | `internal/engine` | `func NewRun(root string, git *gitx.Git, opts RunOpts) (*Run, error)` | Defined by `20_engine_core` — **NOT FOUND** (package does not exist yet); signature assumed from the spec's PRD |
| `engine.SetupBranch` | `internal/engine` | `func SetupBranch(ctx context.Context, run *Run, prefix string, number int, title string) error` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.Park` | `internal/engine` | `func Park(ctx context.Context, run *Run, message string, reported []FileChange) (string, error)` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.Push` | `internal/engine` | `func Push(ctx context.Context, run *Run, attempts int) error` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.OpenPullRequest` | `internal/engine` | `func OpenPullRequest(ctx context.Context, run *Run, forge issuex.Client, target issuex.Repo, req issuex.CreatePullRequestRequest) (string, error)` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.CommonPreflight` | `internal/engine` | `func CommonPreflight(ctx context.Context, run *Run, opts PreflightOpts) error` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.Invalidate` | `internal/engine` | `func Invalidate(run *Run)` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.Background` | `internal/engine` | `func Background(ctx context.Context) (context.Context, context.CancelFunc)` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.Run` | `internal/engine` | `type Run struct { Root string; Git *gitx.Git; Lang lang.Detection; Branch BranchInfo; Ledger *Ledger; Gate *GateCache; Index tools.Index; Scratch string; Progress toolio.Progress; Record *toolio.Run }` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.Ledger` | `internal/engine` | `type Ledger struct { ... }` with methods `Snapshot`, `Changes`, `Commit`, `OwnChange`, `SetOwned`, `SetProtected`, `FlagGateEdits` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `engine.GateCache` | `internal/engine` | `type GateCache struct { ... }` with method `Run(ctx, runner, root, command, timeout, label) checks.Result` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `prompt.Loader` | `internal/engine/prompt` | `type Loader struct { ... }` with methods `Render(name string, view any) (string, error)`, `Hash(name string) string`, `Names() []string` | Defined by `21_prompt_documents` — **NOT FOUND**; assumed from the spec's PRD |
| `prompt.BriefView` | `internal/engine/prompt` | `type BriefView struct { Root, Language, RepoMap, Instructions, Steering, Context string; VerifyCommands []string; BaselineResults []BaselineEntry; Report ReportData }` | Defined by `21_prompt_documents` — **NOT FOUND**; assumed from the spec's PRD |
| `prompt.PhaseView` | `internal/engine/prompt` | `type PhaseView struct { BriefView; Branch, Date string; Diagnosis DiagnosisData; Criteria []CriterionData; ... }` | Defined by `21_prompt_documents` — **NOT FOUND**; assumed from the spec's PRD |
| `lang.Detect` | `internal/lang` | `func Detect(root string) Detection` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from the spec's PRD |
| `lang.Detection` | `internal/lang` | `type Detection struct { Primary *Profile; All []*Profile }` with methods `VerifyCommand`, `TestFile`, `Programs`, `IsSourceFile` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from the spec's PRD |
| `lang.Profile` | `internal/lang` | `type Profile struct { Name string; Targeted Command; InlineTests bool; Programs []string; ... }` | Defined by `19_language_profiles` — **NOT FOUND**; assumed from the spec's PRD |
| `conform.ScanInput.Lang` | `internal/conform` | `Lang lang.Detection` field on `ScanInput` | Defined by `22_polyglot_structural_checks` — **NOT FOUND**; assumed from the spec's PRD |
| `checks.VerdictAborted` | `internal/checks` | `const VerdictAborted Verdict = "aborted"` | Defined by `20_engine_core` — **NOT FOUND**; assumed from the spec's PRD |
| `core.Tool` | `agentkit-go/core` | `type Tool struct { Name, Description string; InputSchema *schema.Schema; Execute func(context.Context, json.RawMessage) ToolResult; ... }` | **unverified** — used via `codefix/phases.go` and `internal/agentrun/phase.go` |
| `core.ErrResult` | `agentkit-go/core` | `func ErrResult(code, detail string) ToolResult` | **unverified** — used via `codefix/phases.go` |
| `core.OKResult` | `agentkit-go/core` | `func OKResult(v any) ToolResult` | **unverified** — used via `codefix/phases.go` |
| `schema.Object` | `agentkit-go/schema` | `func Object(props ...Property) *Schema` | **unverified** — used via `codefix/phases.go` |
| `schema.Enum` | `agentkit-go/schema` | `func Enum(desc string, values ...string) *Schema` | **unverified** — used via `codefix/phases.go` |
| `gitx.Git.Head` | `internal/gitx` | `func (g *Git) Head(ctx context.Context) (string, error)` | `internal/gitx/git.go` |
| `gitx.Git.ResetSoft` | `internal/gitx` | `func (g *Git) ResetSoft(ctx context.Context, ref string) error` | `internal/gitx/git.go` |
| `gitx.Git.ShowFile` | `internal/gitx` | `func (g *Git) ShowFile(ctx context.Context, ref, path string) (string, bool, error)` | `internal/gitx/git.go` |
| `gitx.ReducedEnvRunner` | `internal/gitx` | `var ReducedEnvRunner Runner` | `internal/gitx/hermetic.go` |
