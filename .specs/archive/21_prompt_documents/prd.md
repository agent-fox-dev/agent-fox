---
spec_id: "21"
spec_name: "prompt_documents"
title: "Prompt documents: embedded Markdown templates, typed views, golden tests and run-time prompt auditing"
status: "active"
created_at: "2026-10-07T15:35:08.293913Z"
updated_at: "2026-10-07T15:35:08.293913Z"
intent_hash: "f51fe39b8bd243797f85cae842ff28b4a88aaaf3ef29c3840cd8068dd1f095ca"
schema_version: 2
source: "docs/prds/13-rebuild-fix-on-a-shared-change-engine.md"
---
## Intent

Move every prompt, system mandate, tool description override, guard refusal and handler rejection from Go string constants and `fmt.Fprintf` assembly in `codefix`, `codeimpl` and `internal/conform` into embedded Markdown template files under `internal/engine/prompts/`, rendered through a single `text/template`-based loader with typed view structs, golden-file tests, and run-time prompt auditing that writes what each phase was told to the state directory.

## Goals

1. **No model-facing prose in Go source.** Every string longer than one sentence that a model reads — system prompts, phase messages, tool description overrides, guard refusals and handler rejections — lives as a `.md` file under `internal/engine/prompts/`, embedded with `go:embed`, and is rendered through one loader. A test asserts this by scanning Go source files.
2. **Typed, frozen views.** Each template renders from one of three typed Go structs — `BriefView`, `PhaseView`, `AttemptView` — built from the run's facts. No template computes; it reads fields and loops over lists.
3. **Golden-file coverage.** Every document under `internal/engine/prompts/` has a golden rendering under `internal/engine/testdata/prompts/`, regenerated with `UPDATE_GOLDEN=1`. A change to a prompt is a diff of a Markdown file in review.
4. **Run-time prompt auditing.** Every run writes the system prompt, the phase message and the tool descriptions it sent to `<state>/prompts/<tool>-<started_at>-<session_id>/`, paired to the report and events files by `session_id`. The full report gains a `prompt_templates` map (document name → SHA-256 of embedded source).
5. **Template-injection safety.** Untrusted text (reports, project instructions, steering) passes through templates verbatim. A test renders a report whose body is `{{.Secret}}{{template "x"}}` and asserts it reaches the model unchanged.
6. **Document metadata.** Every template file carries a YAML header with `name`, `kind`, `view`, `stable` and `max_bytes`, validated by the golden test.

## Non-goals

- **Rewriting `codefix` as a thin pipeline over the engine** (`codefix_on_engine`): this scope creates the template infrastructure and migrates the text; the pipeline rewrite, the `run_checks` tool, the stable-prefix brief and transcript pruning are a later scope.
- **Polyglot structural checks** (`polyglot_structural_checks`): wiring `internal/conform` to read from `lang.Profile` is a separate scope.
- **Changing any flag, exit code, envelope field or schema version.** The `prompt_templates` and `prompts_dir` additions are additive.
- **Per-project prompt overrides.** The prompt documents are the program's, bundled into the binary. A developer-supplied prompt directory is not part of this scope.
- **Migrating `specgen`'s `fill` function.** `specgen` keeps its own `strings.NewReplacer`-based `fill` and its own `templates/` directory until a later scope unifies them. This scope provides the engine's loader; `specgen` is not a consumer yet.
- **The stable-prefix brief or cache breakpoints.** Those require AgentKit PRD 06 and are wired in `codefix_on_engine`. This scope defines the `BriefView` and the brief templates but does not change how phases are built or how the provider cache key works.

## Background

### What exists today

Prompts are authored in three forms across four packages:

1. **Go `const` blocks.** `codefix/phases.go` defines `analysisSystemPrompt` and `implementSystemPrompt` as multi-line string constants (~60 and ~80 lines each). `codeimpl/prompts.go` defines `surveySystemPrompt`, `implementSystemPrompt`, `repairSystemPrompt` and `resolveSystemPrompt` as constants (~40–80 lines each). `internal/conform/prompt.go` defines `ReviewSystemPrompt` (~45 lines).

2. **`fmt.Fprintf` assembly.** `codefix/prompts.go` has `analysisPrompt`, `implementPrompt`, `reportBlock`, `baselineBlock`, `criteriaBlock`, `contextBlock`, `instructionsBlock` and `projectInstructions` — functions that build user messages by concatenating blocks with `strings.Builder` and `fmt.Fprintf`. `codeimpl/prompts.go` has `surveyPrompt`, `repairPrompt`, `taskPrompt`, `resolvePrompt`, `gateBlock`, `repoMapBlock`, `surveyBlock`, `previousAttemptBlock`, `languageBlock`, `externalAPIsBlock` and `sectionTitle` — the same pattern, duplicated and diverged.

3. **Inline tool descriptions.** `internal/agentrun/policy.go`'s `describeForPhase` patches tool descriptions with phase-specific text (the allowlist, the scratch directory, the read roots) via string concatenation. `noteScratch` and `noteReadRoots` append sentences to `execute`, `write_file` and `edit_file` descriptions. Guard refusals in `internal/agentrun/guard.go` are inline strings. Handler rejections in `codefix/phases.go` and `codeimpl/phases.go` are inline `core.ErrResult` calls.

4. **`specgen`'s embedded templates.** `specgen/prompts.go` already uses `go:embed templates/*.md` with a `fill` function based on `strings.NewReplacer`. It deliberately avoids `text/template` because its values are prose that may contain `{{`. The comment at `specgen/prompts.go:34` explains the reasoning.

The duplication is concrete: `projectInstructions` is identical in `codefix/prompts.go:219` and `codeimpl/prompts.go:202`. `instructionsBlock` in `codefix` and the inline equivalent in `codeimpl` do the same fencing with different wording. `baselineBlock` in `codefix` and `gateBlock` in `codeimpl` serve the same purpose with different shapes. `languageBlock` exists only in `codeimpl`; `codefix` has no language block at all.

A reviewer who wants to know what a phase was told reads `codefix/phases.go`, `codefix/prompts.go`, `codeimpl/prompts.go`, `internal/conform/prompt.go`, `internal/agentrun/policy.go` and `internal/agentrun/guard.go`. There is no way to see the rendered prompt without running the tool.

### Why this matters

The input PRD's §6.6 documents the cost: prompts are prose a reviewer maintains, and a diff against a document reads better than a diff against escaped string concatenation. The run-time audit trail — what a phase was actually told — does not exist today; a surprising model behaviour requires reproducing the run to see the prompt.

## Requirements

### The template tree

A directory `internal/engine/prompts/` is created with the following layout, embedded into the binary with `//go:embed prompts/**/*.md`:

```
prompts/
  fix/analyse.system.md
  fix/analyse.md
  fix/implement.system.md
  fix/implement.md
  impl/survey.system.md
  impl/survey.md
  impl/task.system.md
  impl/task.md
  impl/repair.system.md
  impl/repair.md
  impl/resolve.system.md
  impl/resolve.md
  review/review.system.md
  review/review.md
  brief/fix.md
  brief/impl.md
  partials/
    report.md
    baseline.md
    gate.md
    language.md
    repomap.md
    instructions.md
    steering.md
    criteria.md
    definition_of_done.md
    survey.md
    landed_tasks.md
    previous_attempt.md
    scope.md
    external_apis.md
    date.md
    context.md
    read_roots.md
    scratch.md
  tools/
    execute.md
    write_file.md
    edit_file.md
  refusals/
    programs_not_allowed.md
    suite.md
    git_mutating.md
    path_outside.md
  rejections/
    missing_verdicts.md
    weak_evidence.md
```

Each file is a Markdown document. The exact set of partials, tool overrides, refusals and rejections is determined by extracting every inline string from the current code that a model reads.

### Document headers

Every template file opens with a YAML front-matter block:

```yaml
---
name: fix/implement
kind: message          # system | message | brief | partial | tool | refusal | rejection
view: PhaseView        # the Go type it renders from
stable: false          # true: must render byte-identical for the whole run
max_bytes: 6000        # the rendered size a change may not exceed unnoticed
---
```

The loader parses and validates the header. A file without a valid header is a build error (the golden test fails). `kind` is one of `system`, `message`, `brief`, `partial`, `tool`, `refusal`, `rejection`. `view` names the Go struct the template renders from. `stable: true` means the document must render byte-identically across two builds of the same view within one run; the golden test asserts this for every `stable: true` document.

### The loader

`internal/engine/prompt` (a sub-package of `internal/engine`) exports a `Loader` type:

- `NewLoader()` returns a `*Loader` that has parsed every embedded template and validated every header. It panics on a missing or malformed template, as `specgen/prompts.go:28` does, because a missing template is a build mistake.
- `Render(name string, view any) (string, error)` renders the named template against the given view. It returns an error when the view is missing a field the template references (`missingkey=error`).
- `RenderBytes(name string, view any) ([]byte, error)` is the same, returning bytes.
- `Hash(name string) string` returns the SHA-256 hex digest of the named template's embedded source (before rendering), for the `prompt_templates` map.
- `Names() []string` returns every template name, sorted.
- `Header(name string) Header` returns the parsed header of the named template.

The loader uses Go's `text/template` from the standard library with `Option("missingkey", "error")`. It registers a fixed function map:

| Helper | Signature | Purpose |
|---|---|---|
| `fence` | `func(label, text string) string` | Wraps untrusted text in `--- BEGIN {LABEL} ---\n{text}\n--- END {LABEL} ---` |
| `code` | `func(text string) string` | Wraps text in triple backticks |
| `join` | `func(sep string, items []string) string` | `strings.Join` |
| `count` | `func(items any) int` | `reflect.ValueOf(items).Len()` |
| `lines` | `func(n int, text string) string` | Last N lines of text |
| `ref` | `func(path string) string` | One-line reference to a log in the scratch directory |
| `ids` | `func(items any) string` | Comma-separated list of `.ID` fields |
| `title` | `func(key string) string` | Section title from a key (prd → PRD, etc.) |
| `trimSpace` | `func(s string) string` | `strings.TrimSpace` |
| `firstNonEmpty` | `func(ss ...string) string` | First non-empty argument |

A document composes others with `{{template "partials/report.md" .}}`; the brief and the phase messages are sequences of partials in a fixed order, and the partials are shared by both tools.

### Template-injection safety

The input PRD notes that `specgen` avoided `text/template` because its `fill` would have parsed the data. The engine's case is different: the data is never parsed as a template. All untrusted text — reports, project instructions, steering, survey summaries — is a field on the view struct, and `text/template` inserts field values verbatim (it does not re-parse them as template syntax). The `fence` helper wraps untrusted text in provenance markers but does not interpret it.

A test in the loader's test file renders a view whose `Report` field is `{{.Secret}}{{template "x"}}` and asserts the output contains that string literally, proving that `text/template` does not interpret data values as template actions.

### The view types

Three typed structs, defined in `internal/engine/prompt`:

**`BriefView`** — built once per run, from the `engine.Run` and the tool's options:
- `Root string` — the repository root.
- `Language string` — the language block (from `lang.Detection`).
- `RepoMap string` — the rendered repository map.
- `Instructions string` — the project's AGENTS.md/CLAUDE.md content.
- `Steering string` — the project's steering.md content.
- `VerifyCommands []string` — the gate commands.
- `BaselineResults []BaselineEntry` — each command's verdict and log path.
- `Context string` — the `--context` value.
- `Report ReportData` — the input report/spec (kind, origin, body).
- `Spec SpecData` — the spec digest (for `impl`).

**`PhaseView`** — built per phase, embeds `BriefView`:
- `Branch string` — the work branch name.
- `Date string` — today's date from the system clock.
- `Diagnosis DiagnosisData` — the analysis result (for implement).
- `Criteria []CriterionData` — the acceptance criteria.
- `Task TaskData` — the current task (for impl).
- `BaselineVerdicts []VerdictEntry` — per-command verdicts.
- `LandedTasks []LandedTaskData` — tasks landed before this one.
- `MapDelta string` — the repo map delta.
- `Allowlist []string` — the shell allowlist programs.
- `ScratchPath string` — the scratch directory path.
- `Survey SurveyData` — the survey (for impl).
- `Scope []string` — the scope paths (for impl).
- `ExternalAPIs []APIData` — external API entries (for impl).
- `Suite []string` — the suite commands (for shell refusal).
- `TargetedRun string` — the targeted test form.
- `ReadRoots []ReadRootData` — the read roots.
- `AttemptNumber int` — the current attempt number.
- `TotalAttempts int` — the total attempts allowed.
- `PreviousAttempt *PreviousAttemptData` — the previous attempt's failure.

**`AttemptView`** — built per attempt, embeds `PhaseView`:
- `PreviousFailure string` — the previous attempt's failure reason.
- `RevertedDeclarations []string` — the reverted declarations.
- `AttemptIndex int` — the attempt number (1-based).

A value a template needs that is not a raw fact — a count, a rendered list, a profile's targeted-test form — is a computed field on the view, so the prompt's logic is testable without rendering.

### Migration of existing prompts

Each existing prompt constant and builder function is replaced by a template file and a view construction. The mapping:

| Current location | Template file |
|---|---|
| `codefix/phases.go` `analysisSystemPrompt` | `fix/analyse.system.md` |
| `codefix/prompts.go` `analysisPrompt` | `fix/analyse.md` (composes partials) |
| `codefix/phases.go` `implementSystemPrompt` | `fix/implement.system.md` |
| `codefix/prompts.go` `implementPrompt` | `fix/implement.md` (composes partials) |
| `codeimpl/prompts.go` `surveySystemPrompt` | `impl/survey.system.md` |
| `codeimpl/prompts.go` `surveyPrompt` | `impl/survey.md` |
| `codeimpl/prompts.go` `implementSystemPrompt` | `impl/task.system.md` |
| `codeimpl/prompts.go` `taskPrompt` | `impl/task.md` |
| `codeimpl/prompts.go` `repairSystemPrompt` | `impl/repair.system.md` |
| `codeimpl/prompts.go` `repairPrompt` | `impl/repair.md` |
| `codeimpl/prompts.go` `resolveSystemPrompt` | `impl/resolve.system.md` |
| `codeimpl/prompts.go` `resolvePrompt` | `impl/resolve.md` |
| `internal/conform/prompt.go` `ReviewSystemPrompt` | `review/review.system.md` |
| `internal/conform/prompt.go` `ReviewPrompt` | `review/review.md` |

Shared blocks become partials:

| Current function | Partial |
|---|---|
| `codefix/prompts.go` `reportBlock` | `partials/report.md` |
| `codefix/prompts.go` `baselineBlock` | `partials/baseline.md` |
| `codeimpl/prompts.go` `gateBlock` | `partials/gate.md` |
| `codefix/prompts.go` `criteriaBlock` | `partials/criteria.md` |
| `codefix/prompts.go` `instructionsBlock` | `partials/instructions.md` |
| `codeimpl/prompts.go` `languageBlock` | `partials/language.md` |
| `codeimpl/prompts.go` `surveyBlock` | `partials/survey.md` |
| `codeimpl/prompts.go` `previousAttemptBlock` | `partials/previous_attempt.md` |
| `codeimpl/prompts.go` `externalAPIsBlock` | `partials/external_apis.md` |
| `conform.DateLine` | `partials/date.md` |
| `codefix/prompts.go` `contextBlock` | `partials/context.md` |
| `repomap.Block` | `partials/repomap.md` |

Tool description overrides become `tools/*.md`:

| Current function | Template |
|---|---|
| `agentrun/policy.go` `describeForPhase` (execute, read-only) | `tools/execute.md` |
| `agentrun/policy.go` `describeForPhase` (write_file) | `tools/write_file.md` |
| `agentrun/policy.go` `describeForPhase` (edit_file) | `tools/edit_file.md` |

Guard refusals become `refusals/*.md`:

| Current location | Template |
|---|---|
| `agentrun/guard.go` disallowed-program message | `refusals/programs_not_allowed.md` |
| `agentrun/guard.go` suite-refused message | `refusals/suite.md` |
| `agentrun/guard.go` git-mutating message | `refusals/git_mutating.md` |
| `agentrun/guard.go` path-outside message | `refusals/path_outside.md` |

After migration, the original Go constants and builder functions are removed. The `codefix`, `codeimpl` and `internal/conform` packages import `internal/engine/prompt` and call `loader.Render(name, view)` where they previously called the builder function or referenced the constant.

### Tool description integration

`internal/agentrun/policy.go`'s `describeForPhase`, `noteScratch` and `noteReadRoots` are updated to render from the `tools/*.md` templates instead of concatenating strings. The `SelectTools` function receives a `*prompt.Loader` and a `PhaseView` and calls `loader.Render("tools/execute.md", view)` to produce the description. The view carries the allowlist, the read roots and the scratch path, so the description is one rendered document rather than a base string plus three patches.

### Refusal and rejection integration

Guard refusals in `internal/agentrun/guard.go` are updated to render from `refusals/*.md` templates. Each refusal template receives a small view (the programs refused, the command attempted, the alternative to use). The `refusal` method on `GuardOptions` calls `loader.Render("refusals/programs_not_allowed.md", view)` instead of building the message inline.

Handler rejections in `codefix/phases.go` and `codeimpl/phases.go` (the `core.ErrResult` calls in `submitAnalysisTool`, `submitImplementationTool`, `submitTaskTool`, etc.) are updated to render from `rejections/*.md` templates where the rejection text is longer than one sentence. Short, mechanical rejections (e.g. "title is empty") remain inline.

### The no-prose-in-Go test

A test `TestNoPromptLiteralsInGo` in `internal/engine/prompt` walks every non-test `.go` file under `internal/engine`, `codefix`, `codeimpl` and `internal/conform` and fails on any string literal longer than 120 bytes that contains model-facing keywords (a heuristic: "You are a", "Method:", "Call this once", "submit_", "is refused", "not allowed"). The test is the acceptance gate for goal 1. Exceptions are allowed for import paths, test fixtures and the template files themselves.

### Golden tests

A test `TestGoldenPrompts` in `internal/engine/prompt`:

1. Loads every template via `NewLoader()`.
2. For each template, reads a fixture view from `internal/engine/testdata/views/<name>.json` (or builds one programmatically from test fixtures).
3. Renders the template against the view.
4. Compares the output to `internal/engine/testdata/prompts/<name>.golden.md`.
5. When `UPDATE_GOLDEN=1` is set, writes the output as the new golden file.
6. Validates every header: `name`, `kind`, `view`, `stable`, `max_bytes` are present and well-formed.
7. For every `stable: true` document, renders it twice with the same view and asserts byte-identity.
8. For every document, asserts the rendered size does not exceed `max_bytes`.
9. Asserts that every partial referenced by `{{template "partials/..."}}` exists in the embedded tree.
10. Asserts that no template references a field its view type lacks (enforced by `missingkey=error`).

The language denylist from `19_language_profiles` also runs over the template files, so a language literal cannot hide in prose.

### Run-time prompt auditing

Every run writes the prompts it sent to the state directory:

- Directory: `<state>/prompts/<tool>-<started_at>-<session_id>/`, using the same stem as the report and events files.
- Files: `<phase>[.task-N][.attempt-N].<role>.md` where `role` is `system`, `user` or `tools`.
- The directory is created with mode 0755 and files with mode 0644.
- `--dry-run` writes them too (they are local state).
- A run that cannot write the directory records a `low` `prompts_not_written` warning and continues.

The `toolio.Run` gains a method `WritePrompts(stateDir string, phase string, task string, attempt int, system, user string, toolDescs []string)` that writes the files. Each tool's pipeline calls it after building the `agentrun.Phase` and before calling `runner.Run`.

### Envelope additions (additive)

- `prompt_templates` in the full report: a `map[string]string` of document name to SHA-256 hex digest of the embedded source. Present in the `full` detail view only.
- `ArtifactPromptsDir ArtifactKind = "prompts_dir"` added to the artifact kind set, with a `Path` field pointing to the prompts directory.
- `WarnPromptsNotWritten WarnCode = "prompts_not_written"` added to the warning code set.

These are additive: `--schema` golden files change only by these fields. `SchemaVersion` stays at its current major.

### `projectInstructions` unification

The identical `projectInstructions` functions in `codefix/prompts.go:219` and `codeimpl/prompts.go:202` are replaced by a single function in `internal/engine/prompt` (or `internal/engine` if the engine package exists). Both packages import and call it. The `maxInstructionBytes` constant (24 KiB) moves with it.

### Error handling

- `NewLoader()` panics on a missing or malformed embedded template. This matches `specgen/prompts.go:28`'s convention: the files are embedded at compile time, so a missing one is a build mistake.
- `Render` returns an error on a missing field (`missingkey=error`). The caller (the phase builder) treats this as a programming error and panics, since the view type is known at compile time.
- `WritePrompts` never returns an error that stops the pipeline. Failures are warnings.
- `Hash` panics on a name that does not exist in the embedded tree.

### Backward compatibility

- The rendered prompts are textually equivalent to the current Go-assembled prompts. A test renders each migrated prompt with the same inputs the current builder would receive and asserts the output matches (modulo whitespace normalization).
- No flag, exit code, envelope field (other than the additive ones above) or schema version changes.
- The `--schema` golden files change only by the additive fields.

## Design Decisions

1. **`text/template` is safe for this use case.** `specgen` avoided `text/template` because its `fill` function's values are prose that may contain `{{`, and `text/template` would interpret them as actions. The engine's case is different: `text/template` does not re-parse field values inserted by `{{.Field}}`; only the template source itself is parsed. A test proves this by rendering a view whose field contains `{{.Secret}}{{template "x"}}` and asserting it appears verbatim in the output. The `fence` helper wraps untrusted text in provenance markers but does not interpret it.

2. **The loader is a sub-package `internal/engine/prompt`, not in `internal/engine` itself.** The engine package (from `20_engine_core`) is about git, the ledger, the gate cache and parking. Prompt rendering is a separate concern with its own `go:embed` directive, its own test fixtures and its own golden files. A sub-package keeps the engine's import graph clean and lets the loader be tested without importing `gitx` or `checks`. If `internal/engine` does not yet exist when this scope lands, the package is `internal/engine/prompt` and the engine scope creates its parent.

3. **Three view types, not one.** The input PRD specifies `BriefView`, `PhaseView` and `AttemptView`. `BriefView` is what is constant for the run; `PhaseView` adds what varies per phase; `AttemptView` adds what varies per attempt. Embedding rather than composition: `PhaseView` embeds `BriefView`, `AttemptView` embeds `PhaseView`. This lets a template that needs only the brief reference `{{.Root}}` directly.

4. **Partials are shared by both tools.** The `partials/` directory holds blocks used by both `fix` and `impl`: `report.md`, `baseline.md`, `gate.md`, `language.md`, `instructions.md`, `steering.md`, `criteria.md`, `repomap.md`, `date.md`, `context.md`. A partial that is tool-specific (e.g. `definition_of_done.md` for `impl`) still lives in `partials/` because it is referenced by `{{template}}` and the namespace is flat.

5. **Tool description templates replace `describeForPhase`, not extend it.** Today `describeForPhase` in `internal/agentrun/policy.go` patches the SDK's base description with phase-specific text. The templates replace the entire description for the tools they cover (`execute`, `write_file`, `edit_file`), so a description is one document. The SDK's base description is not referenced; the template is self-contained.

6. **Guard refusals are templates only where the text is substantial.** Short refusals like `"backtick in command"` remain inline. Only refusals that carry a paragraph of explanation (the program-not-allowed message, the suite-refused message, the git-mutating message, the path-outside message) become templates. The threshold is roughly one sentence: anything longer than that is prose a reviewer should see as a document.

7. **Handler rejections follow the same threshold.** Mechanical rejections like `"title is empty"` or `"summary is empty"` remain inline `core.ErrResult` calls. Rejections that carry multi-sentence guidance (e.g. the criteria-verdicts rejection, the weak-evidence rejection) become `rejections/*.md` templates.

8. **The golden test uses fixture views, not the full pipeline.** The golden test builds views from JSON fixtures or programmatic construction, not by running the full pipeline. This keeps the test fast, deterministic and independent of model responses.

9. **`projectInstructions` moves to the prompt sub-package.** Both `codefix` and `codeimpl` define identical copies. The unified function lives in `internal/engine/prompt` because it reads a file and returns text for a view field — it is prompt infrastructure, not engine infrastructure.

10. **Run-time prompt files use the same stem as report and events files.** The state directory already pairs reports and events by `session_id`. The prompts directory uses the same `<tool>-<started_at>-<session_id>` stem, so all three pair up. The directory (not a single file) is used because a run has multiple phases, each with system, user and tools files.

11. **`specgen`'s templates are not migrated.** `specgen` has its own `templates/` directory and its own `fill` function. Migrating it to the engine's loader would couple `specgen` to `internal/engine`, which is not appropriate: `specgen` does not use the engine. A future scope may unify them; this scope does not.

12. **`max_bytes` is advisory, not enforced at runtime.** The golden test checks that rendered output does not exceed `max_bytes`. At runtime, the loader does not truncate or refuse; it renders and returns. The field exists so that a change that doubles a prompt's size is caught in review, not in production.

## Dependencies

| Spec | Reason |
|---|---|
| `19_language_profiles` | The `BriefView.Language` field and the `partials/language.md` template render from `lang.Detection`, which this spec provides. The denylist test from `19_language_profiles` is extended to cover the template files. |
| `20_engine_core` | The `internal/engine` package is the parent of `internal/engine/prompt`. The `BriefView` references `engine.Run` fields (root, branch, scratch path). The run-time prompt auditing integrates with `toolio.Run` which the engine uses. |

## Verified External API

| Symbol | Package | Signature | Source |
|---|---|---|---|
| `template.New` | `text/template` (stdlib) | `func New(name string) *Template` | stdlib |
| `template.Template.Option` | `text/template` (stdlib) | `func (t *Template) Option(opt ...string) *Template` | stdlib |
| `template.Template.Funcs` | `text/template` (stdlib) | `func (t *Template) Funcs(funcMap FuncMap) *Template` | stdlib |
| `template.Template.Parse` | `text/template` (stdlib) | `func (t *Template) Parse(text string) (*Template, error)` | stdlib |
| `template.Template.Execute` | `text/template` (stdlib) | `func (t *Template) Execute(wr io.Writer, data any) error` | stdlib |
| `embed.FS` | `embed` (stdlib) | `type FS struct{}` | stdlib |
| `embed.FS.ReadFile` | `embed` (stdlib) | `func (f FS) ReadFile(name string) ([]byte, error)` | stdlib |
| `embed.FS.ReadDir` | `embed` (stdlib) | `func (f FS) ReadDir(name string) ([]fs.DirEntry, error)` | stdlib |
| `crypto/sha256.Sum256` | `crypto/sha256` (stdlib) | `func Sum256(data []byte) [32]byte` | stdlib |
| `core.Tool` | `agentkit-go/core` | `type Tool struct { Name, Description string; InputSchema *schema.Schema; PromptGuidelines []string; Execute func(context.Context, json.RawMessage) ToolResult; ... }` | **unverified** — used via `internal/agentrun/phase.go` |
| `core.ErrResult` | `agentkit-go/core` | `func ErrResult(code, detail string) ToolResult` | **unverified** — used via `codefix/phases.go` |
| `core.OKResult` | `agentkit-go/core` | `func OKResult(v any) ToolResult` | **unverified** — used via `codefix/phases.go` |
