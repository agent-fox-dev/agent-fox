---
spec_id: "11"
spec_name: "preflight_checks"
title: "`--preflight`: run every refusing check, and nothing else"
status: "active"
created_at: "2026-09-30T11:26:03.145924Z"
updated_at: "2026-09-30T11:26:03.145924Z"
intent_hash: "bb4a48b54239602dedead3718e24a47ddfc1fbbf5d0ba967772f0e54fc0405cf"
schema_version: 2
source: "docs/prds/03-make-the-tools-observable-and-self-describing.md"
---
## Intent

`spec`, `issue`, `fix` and `impl` already refuse a run — before a model is ever called — for a long list of reasons: a dirty tree, an unresolvable model, a missing credential, an invalid spec package, an unreachable repository. Today the only way a caller learns which of those apply to its situation is to start the run for real and pay for the model call that follows the instant every check passes. This spec gives each tool a `--preflight` flag that performs every check the tool already performs before its first model call, reports the outcome, and stops — so a caller can learn "would this run even start" for the cost of, at most, the one local verification command `fix` and `impl` already run to establish what they compare the model's work against.

## Goals

- `fix --preflight`, `impl --preflight`, `spec --preflight` and `issue --preflight` each run every check that would refuse the corresponding ordinary run — including the tool's own pre-model checks already implemented as `codefix.Preflight`, `codeimpl.Preflight`, `specgen.Preflight`, and `issuetriage.ResolveTarget`/`CheckWriteCredential` — and stop before a model phase runs and before any remote write is attempted.
- A run that would fail before its first model call fails identically under `--preflight`: the same `stage`, `category`, `message` and exit code as the ordinary invocation on the same input and flags, verified for `impl` on a dirty tree as a literal byte-for-byte comparison of the two error objects.
- A run every check of which passes exits `0` under `--preflight` with `result.preflight` naming each check performed and `result.estimate` bounding what the real run would spend at most, and creates no branch, no commit, and no remote write beyond the one local verification command `fix`/`impl` already run as part of establishing their baseline.
- The model is resolved and its credential checked — a caller learns about a missing key or an unresolvable tier — but no model phase is ever started: `usage` is absent from the envelope because no phase recorded a turn.

## Non-goals

- New subcommands: `--preflight` is a flag on the four existing tools, per [ADR 03](../adr/03-rebuild-the-skills-as-tools.md).
- The machine-readable progress stream, envelope persistence to a file, `--schema`, `schema_version`, and untrusted-text labelling — `07_progress_event_stream`, `08_envelope_output_file` and `09_tool_self_schema`/`10_untrusted_text_labels`, the four earlier scopes of this split, already written. `--preflight` composes with all four unchanged: no new event type, no special-cased `--output` handling, and the two new `Result` fields this scope adds are described and trust-classified using the mechanisms those scopes already established.
- Sanitising anything `--preflight` reports; nothing here is untrusted text in the first place — every field this scope adds is established by the program itself from git, the filesystem or a command's exit status.
- A dry run of the model, or of anything a model phase would do. `--dry-run` (present on `fix`, `impl`, `spec`, `issue`) means "run for real but make no remote write"; `--preflight` means "stop before the model is ever asked to do anything." The two compose (`fix --preflight --dry-run` behaves exactly as `fix --preflight`, since `--preflight` already implies making no remote write) but are not the same flag and neither implies the other.
- Estimating a repair phase (`impl --repair`) or a specification split (`spec` writing more than one package): both are decisions a model phase makes once it runs, and `--preflight`'s whole point is to cost nothing before that phase starts. `result.estimate` reports the phases already decided by the plan on disk (which tasks are not done, how many spec-generation steps a package needs), not phases a future model call might additionally cause.
- Populating `next[]` (`06_trim_and_chain_results`) for a `--preflight` result — a reasonable follow-on (suggesting the same invocation without `--preflight`) but not required by anything this scope's input asks for, and left for a later change rather than invented here.
- Changing the human-readable progress on stderr. A `--preflight` run still prints the same `Progress.Step`/`Begin` lines the checks it runs already print today; nothing new is added to make it look different from the first few seconds of an ordinary run.

## Background

Every tool shares one shell, `internal/toolio` (`App`, `Common`, `Run`, `Envelope`), built per ADR 03. `App.execute` (`internal/toolio/app.go`) already performs, in order, every check this scope's flag needs to run once and stop after: `a.PreCheck` (flag combinations that cannot hold), `e.common.Workspace()` (`--dir` resolution), `Resolve` (input classification and fetch), `a.CheckInput`, `e.common.ResolveModel()` (model resolution and credential check via `agentrun.CheckRetiredPlatformVars`/`CheckCredentials`), and finally `agentrun.NewRunner(cfg)`, which only builds a `*Runner` — it makes no network call; a model call happens only inside `Runner.Run`, invoked exclusively from each pipeline's own model phase.

Each pipeline already has an exported `Preflight` function doing exactly the tool-specific checks the input asks for, confirmed by reading the code:

- `codefix.Preflight(ctx, o, git, result) (issuex.Repo, string, *Failure)` (`codefix/pipeline.go`): confirms the workspace is a git repository, the tree is clean, checks out and pulls the base branch under `--pull`, resolves the target repository, and checks the forge credential when the run would comment on an issue or open a pull request. It creates no branch — `Run` creates the branch itself, after `Preflight` returns. `fix`'s other checks the input names — verify-command detection and the baseline run — happen in `Run` immediately after `Preflight`, not inside it.
- `codeimpl.Preflight(ctx, o, result) (*RunState, *Failure)`, wrapping the unexported `preflight` (`codeimpl/pipeline.go`): confirms the repository, the clean tree, resolves and (for `--pull`) fast-forwards the base branch, locates the spec package (`Locate`) and confirms it sits inside the repository, resolves and checks out the work branch (checking out an *existing* continuation branch when one is found — see Design Decision 5), validates the package (`spec.Validate()`) and its status (active/draft/other), audits `test_commands` for shape (`checkCommandShape`) and against the project profile (`AuditTestCommands`), checks every upstream spec dependency is sealed or done (`checkUpstream`), selects the tasks still pending (`selectTasks`), and — unlike `fix` — **already runs the baseline gate inside `preflight` itself** (`runGate(ctx, o, st.root, st.gate, "baseline")`, right before it returns), refusing the run if a check could not run at all (`couldNotRun`). This is a real difference from `fix`, where the equivalent baseline run happens in `Run`, one call site later; this scope's `impl --preflight` therefore already includes the baseline run without any extra code, and `--no-verify` already empties `st.gate`, so the "run nothing" case for `impl` needs no special handling either. `Preflight` creates no branch when the target branch does not already exist yet.
- `specgen.Preflight(ctx, o) (*Result, *Failure)` (`specgen/pipeline.go`): checks `--comment`'s two preconditions (the input is an issue, the forge is authenticated), validates `--name` against the format's `[a-z][a-z0-9_]*` rule, confirms a workspace and a runner are configured, and confirms every generation step's schema compiles (`ArtifactSchema`) — an internal invariant, not something an operator's flags can get wrong, but cheap to include. `spec` has no notion of a "spec root" check beyond this: `resolveSpecsDir` never fails (a missing `.specs` directory is not an error; `discoverLandscape` treats it as "no specs yet"), and the eventual package name, when not given by `--name`, is the model's to choose — nothing about it can be checked before the model runs.
- `issuetriage` has no single exported `Preflight`, but the two checks `Run` performs before its one model phase are already exported separately: `ResolveTarget(o) (issuex.Repo, *Failure)` (the target repository, or empty under `--dry-run` with none found) and `CheckWriteCredential(o, target) *Failure` (the forge credential, skipped entirely under `--dry-run`).

None of the four pipelines' `Result` types carry a field for "what `--preflight` found," and no tool has a way to reach any of these functions without also calling `Exec`, which for every one of them eventually calls `agentrun.Runner.Run` and, for `fix`/`impl`, creates a branch. `agentrun.Runner` (`internal/agentrun/phase.go`) already resolves its per-phase ceilings internally (`Bounds.maxTurns()`/`.maxBudget()`, unexported) but exposes no way for a pipeline to read them back; `Runner.Model()` is the only accessor today.

This is scope 5, the last, of the five-scope split of `docs/prds/03-make-the-tools-observable-and-self-describing.md`. The first four scopes — `07_progress_event_stream`, `08_envelope_output_file`, `09_tool_self_schema`, `10_untrusted_text_labels` — are written but not yet implemented in the tree; this scope is written to build on what each of them defines (the event stream, `--output`, `--schema`/`schema_version`/the `description` tag and golden files, the `trust` tag and `untrusted_fields`) without restating any of it. It also builds on `05_envelope_decidable` and `06_trim_and_chain_results` (the two PRDs this whole split follows), neither yet implemented either: `status`, `summary`, `Summary()`/`Resumable()`, structured `Warn`, and `--detail summary|full` with each tool's per-field "kept in summary" list.

## Requirements

### 1. `--preflight`, a new shared flag

A new boolean flag on `Common` (`internal/toolio/cli.go`), default `false`, registered alongside the rest: `fs.BoolVar(&c.Preflight, "preflight", false, "run every check that would refuse the run, then stop; makes no change beyond a verification baseline")`.

- `-h`/`--help`, `--version`, and a bare invocation are unaffected: all three already return out of `App.Main` before any flag other than themselves is read.
- `--schema` (`09_tool_self_schema`) takes priority when both are given, the same way `--version` already takes priority over every other flag: `--schema` exits before an input is even classified, so there is nothing for `--preflight` to run against.
- Every other flag — `--dry-run`, `--land`, `--repair-model`, `--task`, `--context`, `--events`/`--events-file`, `--output`, `--detail` — is parsed and honoured exactly as it would be for the ordinary run: `--preflight` changes what runs after every check passes, not which checks apply or how the input, the model or the flags are validated.

### 2. Dispatch in `toolio.App`, and one Options builder per tool

`App` (`internal/toolio/app.go`) gains a second exec hook, parallel to `Exec`:

```go
// PreflightExec runs every check that would refuse the run and reports the
// outcome, without calling the model or making any change beyond a
// verification baseline. Required when the tool registers --preflight
// support; a tool that leaves it nil and is asked for --preflight reports
// an internal error rather than silently running Exec instead.
PreflightExec func(context.Context, Deps) (int, any, *ErrorInfo)
```

- In `App.execute`, after `agentrun.NewRunner(cfg)` succeeds — the Runner is still built under `--preflight`; building one makes no network call, and building it unconditionally keeps the dispatch a single branch at the very last step rather than a second copy of the plumbing above it — the branch is: `if e.common.Preflight { return a.PreflightExec(ctx, deps) }`, else `return a.Exec(ctx, deps)`. `Deps` is unchanged; `PreflightExec` receives exactly what `Exec` would have.
- Each `cmd/<tool>/main.go` factors the `codefix.Options`/`codeimpl.Options`/`specgen.Options`/`issuetriage.Options` construction it already does inside `Exec`'s closure into a small named function (e.g. `fixOptions(d toolio.Deps) codefix.Options`) called by both `Exec` and the new `PreflightExec` closure. This is not cosmetic: `PreflightExec` and `Exec` must see identical option values for every flag, or a check performed under `--preflight` could pass against a slightly different configuration than the one the real run would actually use — the one thing that would make `--preflight`'s promise ("the same error the real run would have stopped with") false by construction rather than by a bug.
- `impl`'s `--repair-model` resolution (`d.RunnerFor(repairModel)`, today inline in `Exec`) is factored the same way, into a small shared function both closures call, since resolving and credential-checking the repair model is itself one of the "model resolution and credential check" steps `--preflight` promises to run.

### 3. Two new shared result types, and one new `Runner` accessor

`internal/toolio` gains two exported types, used identically by all four tools' `Result`:

```go
// PreflightCheck is one check --preflight performed and its outcome. OK can
// be false without the run having refused: an entry for a check that is
// advisory rather than refusing (the verification command was not detected;
// the baseline currently fails) reports its own outcome honestly, and is
// only ever present at all because the run reached the point of reporting
// it — a check whose failure refuses the run never produces an entry here,
// because the caller gets the same failing envelope the ordinary run would
// have produced instead (see Requirement 4's parking rule).
type PreflightCheck struct {
    Check  string `json:"check" description:"…" trust:"fact"`
    OK     bool   `json:"ok" description:"…"`
    Detail string `json:"detail,omitempty" description:"…" trust:"fact"`
}

// Estimate bounds what the real run would spend at most, from what
// --preflight already knows without calling a model.
type Estimate struct {
    Phases               int     `json:"phases" description:"…"`
    MaxTurnsPerPhase     int     `json:"max_turns_per_phase" description:"…"`
    MaxBudgetPerPhaseUSD float64 `json:"max_budget_per_phase_usd" description:"…"`
    MaxTotalUSD          float64 `json:"max_total_usd" description:"…"`
}
```

Both carry the `description` struct tag `09_tool_self_schema`'s exhaustiveness check requires on every field reachable from `toolio.Envelope` or a tool's `Result`; `Check` and `Detail`, being string-typed, additionally carry the `trust` tag `10_untrusted_text_labels` requires — `"fact"` for both, since every value either type carries is this program's own check name or a fact it measured (a branch name, a command's pass/fail summary), never model or external text.

`internal/agentrun/phase.go`'s `Runner` gains one exported accessor, mirroring the existing `Model()`:

```go
// ResolvedBounds reports the per-phase ceilings this Runner will apply —
// the tool's own defaults after any --max-turns/--budget override, the same
// defaulting Run applies internally. It is for a caller that needs to
// report a ceiling before any phase runs, such as --preflight's estimate.
func (r *Runner) ResolvedBounds() (maxTurns int, maxBudgetUSD float64) {
    return r.cfg.Bounds.maxTurns(), r.cfg.Bounds.maxBudget()
}
```

Each of the four `Result` types (`codefix.Result`, `codeimpl.Result`, `issuetriage.Result`, `specgen.Result`) gains two new fields, appended immediately after `DryRun` (the existing last field of each), in this order:

```go
Preflight []toolio.PreflightCheck `json:"preflight,omitempty" description:"…"`
Estimate  *toolio.Estimate        `json:"estimate,omitempty" description:"…"`
```

Both are `omitempty`: absent on every ordinary run, present only on a `--preflight` run whose checks all passed (Requirement 4's failure path never populates them, by construction — see below).

### 4. Each pipeline gains a `RunPreflight` function that reuses its existing checks verbatim

A new exported function per package, called from that tool's `PreflightExec` closure and nowhere else, each built from the *same* checks the package's own `Run`/`Preflight` already perform — never a re-implementation, so the two paths cannot silently diverge:

- **`codefix.RunPreflight(ctx, o Options) (*Result, error)`**: builds a `Result{Stage: "preflight", DryRun: o.DryRun, Verdict: string(checks.VerdictUnverified)}`, calls the existing `Preflight(ctx, o, git, result)` unchanged, and on failure returns exactly that error (the caller maps it through `toolio.ErrorFrom`/`ExitCodeFor` exactly as `Exec` already does for `Run`'s own failures — this is what makes `fix --preflight`'s failure byte-for-byte identical to `fix`'s). On success it resolves the verify command and runs the baseline exactly as `Run` does today (the command-detection-and-baseline-run block is factored out of `Run` into a small unexported helper both `Run` and `RunPreflight` call, so `--no-verify` skips the baseline the same way for both), sets `result.Baseline`, and reports `result.Preflight` as: `git_repository`, `clean_tree`, `base_branch` (detail: the branch name), `pull` (only present when `--pull`/`--pull=<branch>` was given), `forge_credential` (only present when the run would need one), `land_target`/`remote_configured` (only present when `--land=pr`/pushes and not `--dry-run`), `verify_command` (`ok`: a command was found or explicitly given; detail: the command, or "none detected"), and `verify_baseline` (`ok`: the baseline passed; detail: pass/fail and exit code; absent under `--no-verify`). `result.Estimate = &toolio.Estimate{Phases: 2, ...}` (`analyse`, `implement` — fix's two phases are fixed), with `MaxTurnsPerPhase`/`MaxBudgetPerPhaseUSD` from `o.Runner.ResolvedBounds()` and `MaxTotalUSD = 2 * MaxBudgetPerPhaseUSD`.
- **`codeimpl.RunPreflight(ctx, o Options) (*Result, error)`**: builds `Result{Stage: "preflight", DryRun: o.DryRun, Verdict: string(checks.VerdictUnverified)}`, calls the existing (unexported) `preflight(ctx, o, result)` unchanged — which, per Background, already includes the baseline gate run — and on failure returns that error unchanged. On success, `result.Tasks` is already populated by `preflight`'s own `selectTasks` (pending/skipped outcomes, no extra code needed); `result.Preflight` reports: `git_repository`, `clean_tree`, `pull` (when given), `spec_resolved` (detail: the package's relative path), `branch` (detail: created vs. continuing — `st.exists`), `spec_valid`, `spec_status` (detail: active/draft), `test_commands` (the audit and shape checks), `dependencies` (`checkUpstream`), `forge_credential`/`land_target`/`remote_configured` (when applicable), and `verify_baseline` (`ok`: `st.baseline.OK()`; detail: which commands failed, if any; absent under `--no-verify`, since `st.gate` is then empty and nothing ran). When `--repair-model` was given, the repair runner's resolution (Requirement 2) is reported too, as `repair_model_credential`. `result.Estimate.Phases = len(st.todo) + (1 unless --no-survey)` — the deterministic count Requirement Non-goals already scopes to exclude any repair phase. `impl 09 --preflight` on a dirty tree therefore returns the identical error `impl 09` would, because both call the identical `preflight` function; on a clean tree it creates no branch, because branch creation happens in `Run`, one call site after `preflight` returns, which `RunPreflight` never reaches.
- **`specgen.RunPreflight(ctx, o Options) (*Result, error)`**: calls the existing `Preflight(ctx, o)` unchanged; on failure, returns that error. On success, builds `result.Preflight` from: `name_flag` (present only when `--name` was given), `comment_target`/`comment_credential` (present only when `--comment` and not `--dry-run`), `schemas_valid` (the `ArtifactSchema` compile check), and, when `!o.DryRun`, `split_plan` (calling the same unexported `findSplitPlan` `Run` calls; `ok: false` with a detail naming why only if `findSplitPlan` itself would refuse the run — in which case `RunPreflight` returns that failure instead of a checklist entry, matching every other tool's rule). `result.Estimate.Phases` is one PRD phase plus one per `afspec.GenerationSteps` entry (currently three: requirements, test_spec, tasks), plus one more when `--architecture` is given — the phase count for the next package the run would write. When a split plan already exists and is being resumed, this per-package figure is multiplied by `plan.Pending()`; a fresh, unsplit input is reported as the one-package figure, which is a lower bound when the PRD phase — not yet run — turns out to call for a split, since how many scopes a not-yet-generated PRD will name cannot be known without running it.
- **`issuetriage.Preflight(o Options) (issuex.Repo, *Failure)`**, a new exported function composing the two checks `Run` already performs in order: `target, err := ResolveTarget(o); if err != nil { return issuex.Repo{}, err }; if err := CheckWriteCredential(o, target); err != nil { return issuex.Repo{}, err }; return target, nil`. **`issuetriage.RunPreflight(o Options) (*Result, error)`** calls it, and on success reports `result.Preflight` as `target_repository` (detail: the resolved repository, or "none (dry run)") and, when `!o.DryRun`, `forge_credential`. `result.Estimate = &toolio.Estimate{Phases: 1, ...}` (the one triage phase), bounds from `o.Runner.ResolvedBounds()`.

On any failure at any of the four, the envelope is exactly what the ordinary run's failure would have produced: same `stage`, `category`, `message`, same exit code, same `status` (via `toolio.ExitCodeFor`/05's exit-code-to-status table) — never a partial `result.preflight` listing which checks got run before the one that failed. This matches how every one of the four underlying `Preflight`/`preflight` functions already works: each refuses at the first bad check it finds and returns, rather than collecting every outcome first.

### 5. `Summary()` and `Resumable()` under a preflight stage

`05_envelope_decidable`'s per-tool `Summary() string`/`Resumable() bool` implementations are extended to special-case `Stage == "preflight"` on a successful (`ok: true`) result, rather than falling through logic written for a stage that stage value cannot otherwise reach on success:

- `fix`: `"fix: preflight passed (N checks)"`, counting `len(result.Preflight)`.
- `impl`: `"impl: preflight passed; N of M tasks remain"` (from `TasksTotal`/`TasksRemaining`, already populated).
- `spec`, `issue`: `"<tool>: preflight passed (N checks)"`.
- `Resumable()` returns `false` whenever `Stage == "preflight"`, on every tool — most visibly for `impl`, whose ordinary rule (`Result.Branch != ""`) would otherwise report `true`: `preflight` populates `Result.Branch` with the name the branch *would* have, before checking whether it exists, so the field being non-empty here means "this is what it would be called," not "there is something to resume."

### 6. `--detail summary|full` and the other three scopes' mechanisms apply unchanged

- `06_trim_and_chain_results`'s per-tool "kept in `--detail summary`" field lists each gain `preflight` and `estimate`: a `--preflight` run's entire purpose is exactly this information, so it must survive the default view, not only `--detail full`. Every other field of the four `Result` types keeps whatever `--detail summary` already trims for it; a `--preflight` result's `Baseline`/`Gate` fields are trimmed by the same rule an ordinary run's are.
- `artifacts[]` and `side_effects[]` (`06_trim_and_chain_results`) are empty on a `--preflight` result: no branch, commit, pull request, comment or forge write happens on this path, so nothing is ever appended to either array — no special-casing needed, since every call site that appends to them sits after the point `--preflight` stops.
- `--events`/`--events-file` (`07_progress_event_stream`) continue to emit `run_start`, one `step` per `Progress.Step`/`Begin` call the reused checks already make (unchanged call sites), and `run_end` — no new event type. A run with a phase over 15 seconds of silence gets a `heartbeat` the same way any other run would; a `--preflight` run is typically too fast to ever produce one.
- `--output <path>` (`08_envelope_output_file`) receives the `--preflight` envelope exactly as it would any other — no special case in `App.Main`'s two `Emit` call sites.
- `--schema` (`09_tool_self_schema`) picks up `--preflight` in its `flags` document automatically, via the `flag.FlagSet` walk; the two new `Result` fields (`preflight`, `estimate`) appear in `result` automatically, via the reflection-based generator, once they carry the `description` tag Requirement 3 gives them. The four golden files are regenerated as part of this scope's implementation, and `09`'s exhaustiveness test over `description` tags fails until every new field (`PreflightCheck.Check`/`.OK`/`.Detail`, `Estimate`'s four fields, each tool's `Preflight`/`Estimate` field) carries one.
- `10_untrusted_text_labels`'s `CheckTrust` exhaustiveness test fails until `PreflightCheck.Check` and `PreflightCheck.Detail` carry `trust:"fact"` (Requirement 3); `Estimate`'s fields need no tag, being numeric. A `--preflight` result's `untrusted_fields` array is therefore always empty when nothing else in the (trimmed) result carries model or external text — expected, since nothing on this path involves the model or anything external.

### 7. Documentation

- `docs/cli.md`: a `--preflight` row in the shared-flags table; a new "Preflight (`--preflight`)" section stating what it runs, what it never does (no branch, no commit, no push, no comment, no issue — the one exception, the baseline/gate check `fix`/`impl` already run, named explicitly, along with the note that a `--pull`-driven fetch-and-fast-forward and an `impl` continuation checkout are not suppressed, since both already happen identically in the ordinary run before its own first model call), and one worked example per tool showing `result.preflight`/`result.estimate`.

## Design Decisions

1. **`--preflight` is dispatched as a second exec hook (`PreflightExec`) on `toolio.App`, parallel to `Exec`, rather than a parameter `Exec` itself inspects.** Every one of the four tools' `Exec` closures already assumes it is building a real run's `Options` and calling `Run`; branching inside it would mean every `Exec` body carries an `if preflight { … } else { … }` the size of half the function. A second hook keeps each path a single, readable function, and the shared Options-builder (Requirement 2) is what keeps the two from drifting apart.
2. **Each `RunPreflight` calls the pipeline's existing `Preflight`/`preflight` function unchanged, never a re-implementation.** This is the only way "the same error the real run would have stopped with" (the acceptance criterion this whole scope is built to satisfy) can be guaranteed rather than merely intended: two independent implementations of "is the tree clean" will eventually disagree about a message, a stage, or an edge case, and only one of them is the one that decides the real run's exit code.
3. **`PreflightCheck.OK` can be `false` on a successful (`ok: true`) `--preflight` run.** A refusing check that fails never reaches the checklist at all — its failure *is* the run's failure, handled by Requirement 4's pass-through. An entry that does reach the checklist with `OK: false` (no verify command detected; the baseline currently fails) is reporting a fact the ordinary run already tolerates and works around, and hiding that fact behind an always-`true` field would make the checklist less useful than the plain-text progress lines a person watching the terminal already sees today.
4. **`impl`'s baseline gate run is not treated as the "one exception to nothing runs" the parent input describes only for `fix`.** Reading `codeimpl.preflight` shows the baseline gate is already run inside the same function `impl`'s ordinary preflight calls, before any task — a real difference in the current code from `fix`, where the equivalent call sits one line later, inside `Run`. Since `RunPreflight` reuses each pipeline's existing function verbatim (Decision 2), `impl --preflight` runs the baseline the same way `fix --preflight` does; the parent PRD's own acceptance criterion ("on a clean tree creates no branch") holds for both, and both need `--no-verify` to skip the same check, which the existing code already arranges by leaving `st.gate` empty.
5. **`impl --preflight` may still check out an existing branch, and `--pull` may still fetch and fast-forward, even though "no local change" is the framing this scope's Goals repeat.** Both behaviours are inherited unchanged from `codeimpl.preflight`/`codefix.Preflight`, which already perform them before the model is ever called in an *ordinary* run — refusing to perform them under `--preflight` specifically would mean the checklist describes a hypothetical tree state rather than the one the real run would actually start from (a clean-tree check performed before a `--pull` fetch could pass here and fail once the real run's own `--pull` changes the tree). Checking out an *already-existing* branch is not creating one, and a fast-forward pull from origin does not touch anything the operator has not already pushed there themselves. This is flagged as an open question below, since a reviewer may reasonably want `--preflight` to make even fewer assumptions about the working tree's final state than the checks it borrows already do.
6. **`result.estimate.phases` excludes any repair phase (`impl --repair`) and any additional split scope (`spec`) a future model call might decide to add.** Both are decisions a model makes once it runs; estimating them would mean either running the very phase `--preflight` exists to avoid, or guessing, and a guess dressed up as a ceiling is worse than an honest, documented lower bound.
7. **`spec`'s `Estimate.Phases`, when resuming an existing split plan, multiplies one package's phase count by `plan.Pending()` rather than inspecting each pending scope's own eventual shape.** Every pending scope's package has not been generated yet — its own generation-step count is fixed by the format (`afspec.GenerationSteps`) regardless of scope, so the multiplication is exact for the steps every package needs and silent only about whether a pending scope will itself want `--architecture`, which is an operator flag applied uniformly to the whole run, not a per-scope choice.
8. **`Summary()`/`Resumable()` gain a `Stage == "preflight"` special case rather than the field-derivation rules already written for `05_envelope_decidable` being redefined.** Both interfaces are read via a type assertion on the tool's own `Result`; adding one more branch inside each tool's own implementation is strictly additive to that spec's design, and keeps the "what does a `Branch` field being non-empty mean" question answerable per-stage rather than needing every consumer of `Result.Branch` throughout the codebase to also check `Stage`.
9. **`PreflightCheck`/`Estimate` are new shared types in `internal/toolio`, not four separate near-identical types, one per pipeline package.** `internal/checks.Result` already sets the precedent — a value type owned by a lower-level package and embedded verbatim by multiple `Result` types above it — and duplicating an identical struct four times would only give `09_tool_self_schema`'s golden-file tests four chances to drift out of sync with each other for no reason.

## Dependencies

| Spec | Reason |
|---|---|
| `05_envelope_decidable` | Supplies `status` (derived from the exit code `RunPreflight`'s failure path reuses unchanged), and the `Summary() string`/`Resumable() bool` result interfaces this scope extends with a `Stage == "preflight"` case. |
| `06_trim_and_chain_results` | Supplies `--detail summary\|full` and each tool's per-field "kept in summary" list, which this scope's `preflight`/`estimate` fields are added to so a `--preflight` result is not trimmed away by the default view; also supplies `artifacts[]`/`side_effects[]`, which this scope confirms stay empty on a `--preflight` run without further code. |
| `07_progress_event_stream` | Supplies `--events`/`--events-file` and the closed event-type set a `--preflight` run continues to emit unchanged (`run_start`, `step`, `run_end`, and a `heartbeat` on a slow run), with no new type added. |
| `08_envelope_output_file` | Supplies `--output <path>`, which persists a `--preflight` envelope exactly as it would any other, with no special case needed in either of `App.Main`'s `Emit` call sites. |
| `09_tool_self_schema` | Supplies `--schema`, `schema_version`, the reflection-based `result`-schema generator, its `description`-tag exhaustiveness check, and the per-tool golden files; this scope's two new `Result` fields need `description` tags to satisfy that check, and the four golden files are regenerated to include them. |
| `10_untrusted_text_labels` | Supplies the `trust` struct tag and its `CheckTrust` exhaustiveness check over every string/`[]string` field of the four `Result` types; `PreflightCheck.Check`/`.Detail`, both new string fields, must carry `trust:"fact"` to satisfy it. |

## Open Questions
