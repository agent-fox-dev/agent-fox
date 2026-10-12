---
spec_id: "17"
spec_name: "find_references_tool"
title: "find_references in every phase of every tool"
status: "active"
created_at: "2026-10-06T19:26:39.689502Z"
updated_at: "2026-10-06T19:26:39.689502Z"
intent_hash: "17a579359b52e8c8df6340c9dccb47816b74979edfeaeb4931572d46e7173252"
schema_version: 2
source: "docs/prds/09-add-a-find-references-tool.md"
---
## Intent

Every phase of every agent-fox tool can ask what a file declares (`file_outline`) and where a name is declared (`find_symbol`). None can ask who uses a declaration. This spec wires AgentKit's `find_references` tool into agent-fox. It joins `agentrun.ReadOnlyFileTools`, so every phase of `triage`, `fix`, `spec` and `impl` can list a declaration's call and reference sites, each attributed to its enclosing declaration. The spec also tells the prompts that send a model looking for "callers" about the tool, reports through `--preflight` whether the workspace's Go code type-checks under AgentKit's bounds, and updates the docs and the navigation baseline to match. The mechanism is AgentKit's. agent-fox decides which phases get the tool, what the prompts say and what the envelope reports, as ADR 07 divides the work.

## Goals

- Every phase of `triage`, `fix`, `spec` and `impl` registers `find_references` beside the six existing read tools, with no per-tool code change. That includes the `fix` and `impl` review phases built in `internal/conform`. A test that goes through the real `tools.All` proves it for a read-only phase and a writing phase.
- `agentrun.AssertReadOnly` passes unchanged for every read-only phase. `find_references` appears in neither `MutatingTools` nor `ShellTools`, and no phase gains a write or shell capability.
- The three system prompts that tell the model to read "callers" (`fix`'s analysis, `impl`'s survey, `triage`'s) name `find_references` at that step. With `--repo-map-tokens 0`, every other byte of every system prompt and every user prompt is unchanged. The only other difference is `find_references` in the generated "Your tools are exactly: …" list.
- `--preflight` of all four tools reports a `go_typecheck` check beside `symbol_backend`. Its detail is the count of Go packages checked and of errors collected under AgentKit's bounds.
- `docs/cli.md`, `docs/model-usage.md` and `docs/development.md` say "seven read tools" everywhere they said six, describe `find_references` and the `go_typecheck` check, and give the navigation baseline a `find_references` column with before and after rows per phase.
- `go.mod` and `go.sum` are unchanged. agent-fox adds no module and runs no type-checker of its own.
- `make check` is green.

## Non-goals

- Writing `find_references`, the Go type-check, the tokenizer, the confidence labels, the ranking, the bounds or the `References` seam. They are AgentKit's spec for PRD 09 §1–§5, which PRD 09 §7 orders first. This spec starts after it is merged and the `replace` target carries it.
- Call edges in the repository map. `Workspace.References` is called here only by the `--preflight` probe. Whether the map's ranking should use call edges is decided on this change's numbers, in a later PRD (PRD 09 §7 step 3).
- Changing `find_symbol`, `search_files`, `code_search`, the repository map's contents, or its opening sentence in `internal/repomap`. That sentence names `file_outline` and `find_symbol` and stays as it is; its golden files do not change.
- Configuring AgentKit's bounds from agent-fox. `tools.Options.Symbols` stays at its zero value, and no flag or environment variable is added.
- Editing the `spec` phase prompts (`specgen/templates`). They do not tell the model to look for callers. The tool list and AgentKit's own prompt guideline are enough for them.
- Envelope changes. No field, schema or schema-golden change; `tool_calls` already counts any tool by name.
- Measuring. No live-model run enters this spec, and no baseline count is invented (design decision 10).
- Exact reference resolution for dependencies. The workspace's Go imports from other modules, AgentKit included, are stubbed by AgentKit's checker, so many sites in agent-fox's own tree are `lexical`, not `resolved`. Cross-module resolution is out of scope for PRD 09 as a whole.

## Background

**The grant.** `agentrun.ReadOnlyFileTools` (`internal/agentrun/policy.go`) is the one list every phase builds its `BuiltinTools` from. It holds `read_file`, `list_files`, `find_files`, `search_files`, `file_outline` and `find_symbol` (spec 15). Its call sites are:

- `issuetriage/triage.go`
- `codefix/phases.go` (analyse and implement)
- `codeimpl/phases.go` (survey, repair, implement and the later phases)
- `specgen/phases.go` (three call sites)
- `internal/conform/review.go` (the independent review)

`code_search` is deliberately not in the list. It is appended by `agentrun.WithCodeSearch` only when the run has an index (spec 16).

**Registration.** `Runner.registeredTools` (`internal/agentrun/phase.go`) calls `tools.All(tools.Options{Workspace: r.cfg.Workspace, Index: r.cfg.Index})` once per phase. `SelectTools` then keeps the tools whose names the phase lists. It iterates the built set, so a name AgentKit does not offer is dropped silently and the phase runs with fewer tools. `toolsNote` appends "Your tools are exactly: …" generated from the registered set. `describeForPhase` customises only `execute`, `run_command`, `write_file` and `edit_file`. `SelectTools` drops a tool's `PromptGuidelines` only when they mention `execute` and the phase has no shell. Spec 15 pinned that `registeredTools` leaves `Options.Symbols` at its zero value (TS-15-8).

**Freshness.** A fresh `tools.All` per phase is what keeps `fix` and `impl` correct across the checkouts, resets, gate runs and commits Go performs between phases (spec 15, REQ-2). Within a phase AgentKit marks written paths dirty. When `Options.Index` is set, the shared run-wide index supplies AgentKit's candidate files, and `fix` and `impl` already invalidate it after every change they make (spec 16).

**Counts.** `toolCallCounter` in `phase.go` counts by tool name, so `usage.phases[].tool_calls` carries `find_references` with no change.

**Preflight.** `agentrun.DetectSymbolBackend` (`internal/agentrun/symbols.go`) is a package variable, so tests can simulate a failing detection. It builds the tools the way `registeredTools` does, confirms `file_outline` is among them, and probes ctags. Each pipeline adds the result as an advisory `symbol_backend` check (always OK) and omits it when detection errors (spec 15, REQ-4):

- `codefix/pipeline.go` (about L713)
- `codeimpl/pipeline.go` (about L524)
- `specgen/pipeline.go` (about L446)
- `issuetriage/pipeline.go` (about L352)

Each is followed by `code_search_index`. `toolio.PreflightCheck.Detail` is a program-measured fact.

**Prompts.** Three system prompts send the model to "callers":

- `codefix/phases.go`, `analysisSystemPrompt` step 2: "Read the file, then its callers and callees…".
- `codeimpl/prompts.go`, `surveySystemPrompt` step 2: "Read the file it lives in, its callers, and its tests."
- `issuetriage/triage.go`, `systemPrompt` step 2: "…read its callers and callees…".

**Docs and tests that pin the count.**

- `internal/agentrun/policy_test.go` (TS-15-1 asserts length 6).
- `internal/agentrun/phase_test.go` (TS-15-2 to TS-15-5 and the `code_search` tests).
- `cmd/triage/preflight_test.go`, which expects exactly four checks with `code_search_index` last.
- `docs_symbols_test.go`, whose `sixReadTools` and "the six read tools" strings pin `docs/cli.md` and `docs/model-usage.md`.
- `docs_codesearch_test.go`, which pins the baseline header `| search_files | file_outline | find_symbol | code_search | all tools |` and selects phase-table rows by "the six read tools".
- `docs_development_test.go`, which checks the navigation baseline section.
- The docs say "six read tools" in `docs/cli.md` (the `triage`, `fix`, `spec` and `impl` sections and `--preflight`), `docs/model-usage.md` (phase table, definition sentence, *Reading the codebase*) and `docs/development.md` (*Navigation baseline*).

**Baseline.** `docs/development.md` holds before/after tables per tool, every count a dash, because no live-model run has been made. `docs/errata/13_`, `15_` and `16_navigation_baseline.md` record this, and `TestTS15_16_NavigationBaselineHasSymbolColumns` keeps the dashes consistent with the `Measured at` line.

**AgentKit.** `go.mod` replaces `github.com/agentfox/agentkit-go` and `…/codesearch` with `../agentkit-go`. That tree is outside the reach of the file tools, and nothing in agent-fox mentions `find_references` or `References` yet. Every AgentKit symbol this spec depends on is therefore unverified (see Verified External API). The implementer verifies them against the replace target, as AGENTS.md requires.

## Requirements

### REQ-1: `find_references` joins the read-only grant

`agentrun.ReadOnlyFileTools` becomes the seven names `read_file`, `list_files`, `find_files`, `search_files`, `file_outline`, `find_symbol`, `find_references`, with `find_references` appended last so the existing order is unchanged. Its doc comment names the new tool.

No call site changes. Read-only phases pass the list as it is. Writing phases append `WriteFileTools` and `execute`. `WithCodeSearch` still appends `code_search` after it. That covers every phase of:

- `triage`;
- `fix`: analyse, implement and review;
- `spec`: prd, each generate phase and architecture;
- `impl`: survey, repair, implement, review and resolve.

`find_references` is not added to `MutatingTools` or `ShellTools`. `code_search` stays outside `ReadOnlyFileTools`.

Because `SelectTools` drops a name AgentKit does not offer, the tests do not rely on the list alone. They assert that each name in `ReadOnlyFileTools` reaches the wire for a read-only phase, a writing phase and the real `fix` brain's analyse and implement phases. A replace target without the tool therefore fails `make test` instead of silently shipping six tools.

### REQ-2: Registration is per phase, at AgentKit's defaults

`registeredTools` is unchanged. It makes one `tools.All` call per phase, with only `Workspace` and `Index` set, so:

- Each phase holds its own reference table, built on its first `find_references` call. Checkouts, resets, gate runs and commits Go makes between phases are covered by the fresh table.
- Inside a phase, AgentKit's own dirty marking covers `write_file`, `edit_file`, `execute`, `run_command` and `powershell`.
- The bounds are AgentKit's defaults for `Symbols` (50 000 files, 2 s per PRD 09 §3). No new invalidation call site is added: the existing spec 16 invalidation already keeps the index's candidate list fresh.
- `toolsNote` lists `find_references` in every phase's system prompt, generated from the registered set.
- AgentKit's prompt guideline for the tool reaches every phase, because it mentions neither `execute` nor any tool outside the grant.

A `find_references` call that fails in AgentKit is a tool result the model reads: a `path` outside the workspace, a `name` over 256 bytes, an unknown `kind`, or a bound hit that returns `partial: true`. It never fails a phase. The existing per-phase tool-call and tool-error accounting counts it like any other read tool, with no change.

If the replace target does not offer the tool, a run still starts with the tools it does have, because `SelectTools` drops the name. In that case `--preflight` omits `go_typecheck` (REQ-4).

### REQ-3: The prompts that say "callers" name the tool

The three system prompts listed in Background gain one short clause at step 2, naming `find_references` as the one-call way to list a declaration's callers. For example: "Read the file, then its callers and callees (`find_references` lists who uses a declaration)…".

Nothing else in them changes. User prompts, including the repository map block and the caller's `--context` block, are untouched. With `--repo-map-tokens 0`, the diff between today's and the new system prompt is that clause. The only runtime difference is `find_references` in the "Your tools are exactly: …" list. Tests assert the clause is present in each of the three prompts and that the prompts minus the clause equal today's text.

### REQ-4: `--preflight` reports `go_typecheck`

`RunPreflight` of `fix`, `impl`, `spec` and `triage` adds an advisory check named `go_typecheck`. It sits directly after `symbol_backend` and before `code_search_index`, so the three informational checks end every tool's list in that order.

- `ok` is always `true`. AgentKit's check stubs every import outside the workspace, so collected errors are expected and are not a refusal.
- `detail` is `<N> packages checked, <M> errors`, with `, partial` appended when AgentKit reports that a bound was hit. A workspace with no Go packages reports `0 packages checked, 0 errors`. The text is a program-measured fact and carries no free text from the workspace.

The detail comes from a new package variable in `internal/agentrun`, `DetectGoTypecheck`, which has the same shape as `DetectSymbolBackend`: it takes the `*tools.Workspace`, returns the detail string and an error, and can be replaced by a test.

- The real implementation builds the tools as `registeredTools` does and confirms `find_references` is among them.
- It then asks AgentKit's exported seam (`Workspace.References` and its result) for the packages-checked count, the errors-collected count and the partial flag, under AgentKit's default bounds. The probe has its own outer timeout (10 s) in case the seam ignores AgentKit's bound.
- It never type-checks in agent-fox.

When detection errors, the entry is omitted and every other check is untouched, as for `symbol_backend`. Detection errors include:

- no workspace;
- `tools.All` failing;
- `find_references` not offered;
- the seam failing or exposing no summary;
- the timeout.

The check never changes a preflight's stage, exit code or estimate. It makes no model call and no remote change, and it writes nothing to the tree.

### REQ-5: The read-only invariant holds

`AssertReadOnly` and the shell guard are unchanged. A set holding all seven read tools passes. A set holding the seven plus `write_file` fails with `ErrNotReadOnly` naming only `write_file`. `find_references` reads inside the workspace root only, as `read_file` does. It does not reach `ReadRoots`, so no read-root note is added for it.

### REQ-6: Documentation

All of this lands in the same session as the code, per the steering's freshness rule.

- **`docs/cli.md`.**
  - Each of the four tool sections lists the seven read tools: `triage` and `spec` where they say "six read tools", and the *What the model may and may not do* sections of `fix` and `impl` by name.
  - The `fix` and `impl` sections say what `find_references` does.
  - The `--preflight` prose says "three informational checks". It describes `go_typecheck` (what the numbers mean and that `ok` is always `true`) beside `symbol_backend` and `code_search_index`.
  - Each of the four preflight examples gains `{"check": "go_typecheck", "ok": true, "detail": "12 packages checked, 0 errors"}` between `symbol_backend` and `code_search_index`.
- **`docs/model-usage.md`.**
  - Every phase-table row and the definition sentence say "seven read tools", and the `triage` row lists `find_references`.
  - *Reading the codebase* describes `find_references`:
    - who uses a declaration, grouped by file, each site with its enclosing declaration;
    - the three confidence labels, `resolved` (Go, through `go/types` over workspace sources with outside imports stubbed), `lexical` and `text`;
    - ranking and the 30-result default, capped at 100;
    - that each phase holds its own reference table, like the symbol table;
    - that the check is bounded and reports `partial`;
    - that sites in this repository that call into AgentKit are mostly `lexical`.
  - It also says `--preflight` reports `go_typecheck`, and "A seventh tool, `code_search`" becomes "An eighth tool".
- **`docs/development.md`.** The *Navigation baseline* intro says seven read tools plus `code_search`, and the sentence after the `jq` snippet counts eight navigation columns.
- **`docs/prds/09-add-a-find-references-tool.md`.** Its status line moves from proposed, and its row in `docs/prds/README.md` moves out of "Proposed".
- **`docs/README.md`.** The errata table gains the erratum row from REQ-7.
- **`docs/architecture.md`.** Not touched. It does not exist in this repository, and the package layout does not change.

### REQ-7: The navigation baseline

Each of the four tables in `docs/development.md` gains a `find_references` column. The header becomes `| Input | Phase | Run | read_file | list_files | find_files | search_files | file_outline | find_symbol | find_references | code_search | all tools |`, and every phase keeps its before and after rows. The `jq` snippet extracts `(.tool_calls.find_references // 0)` in the same position.

The procedure text says what the pair measures: `before` is the tools built from the commit before this spec (where the tool does not exist and its cell is `0`), and `after` is the commit that has it. The effect to read is the fall in the `search_files` and `read_file` columns for `fix`'s analyse and `impl`'s survey, with the same outcome from the tool.

No live model runs in this change, so every count stays a dash and the `Measured at` line stays `— (commit), — (model)`. A new erratum, `docs/errata/NN_navigation_baseline.md` (NN is this spec's number), records that the column exists and the counts do not, in the form of errata 15 and 16. `docs/README.md` lists it.

### REQ-8: Tests change with the code

Every test that pins the number or the wording of the read tools is updated, not loosened:

- `TestTS15_1…` asserts exactly the seven names and length 7.
- The agentrun wire tests include `find_references`.
- `cmd/triage/preflight_test.go` expects five checks, with `code_search_index` last.
- `docs_symbols_test.go` and `docs_codesearch_test.go` use a seven-name list, "the seven read tools", and the new baseline header.

New tests cover:

- a unit test per tool's `RunPreflight` for the `go_typecheck` check, with an injected `DetectGoTypecheck`;
- the omit-on-failure case;
- a smoke test through the real detection for each tool;
- the check's position in the list;
- the four `docs/cli.md` examples carrying it;
- the `find_references` column and `jq` extraction in the baseline;
- the prompt clause (REQ-3) and its absence from every other line.

## Design Decisions

1. **This spec is agent-fox's half only.** PRD 09 §1–§5 is AgentKit's spec in another repository, and §6 is this one. It follows ADR 07 and the precedent of specs 15 and 16, whose non-goals say the same. The `replace` target is read-only from here, and §7 orders AgentKit first. The tests fail until the replace target carries the tool, which is the intended gate.
2. **`find_references` goes in `ReadOnlyFileTools`, not behind a condition like `code_search`.** It needs only a workspace, which every phase has, so a single list reaches every phase with no per-tool change. It is appended last so that the existing order and the `WithCodeSearch` append are untouched.
3. **Every phase gets it, including `spec`'s generation phases and the review phases.** The input says every phase of all four tools, one list serves them all, and a read-only tool cannot change what a phase does to the tree.
4. **`Symbols` stays at its zero value and agent-fox adds no bound flags.** TS-15-8 pins this for the symbol tools, and the bounds are AgentKit's policy (ADR 07). An operator who needs a different bound has no use case yet.
5. **No new freshness code.** The per-phase `tools.All` call and the existing spec 16 invalidation already give the guarantees PRD 09 §4 asks for.
6. **`triage`'s prompt is edited too, though the input names only `fix` and `impl`.** `triage`'s step 2 says "read its callers and callees" in the same words as `fix`'s, and its phase has the same tool. Leaving it out would tell two of three models how to find callers and not the third. `spec`'s prompts and the map's opening sentence are left alone. The map sentence is a golden-pinned artifact of spec 14 and a stated non-goal of PRD 09.
7. **The preflight check is `go_typecheck`, placed after `symbol_backend`.** It is advisory with `ok` always true, like its two neighbours: the Go check is incomplete by design and never fails a call, and a nonzero error count is the expected consequence of stubbing outside imports. The detail is a fixed, unpluralised format so it is easy to parse and cannot carry workspace text. It is omitted when detection fails, per spec 15's REQ-4.3 precedent.
8. **The preflight numbers come from AgentKit's seam, not from a second type-checker in agent-fox.** A second checker would drift from the one the tool uses and break ADR 07's rule that mechanism lives in AgentKit. PRD 09 §5 calls `References` the only new exported API but §6 needs counts, so the packages-checked, errors-collected and partial values must ride on `ReferenceResult`. This spec assumes they do and marks the field names unverified. If the replace target exposes no such summary, the AgentKit spec is amended. In the meantime `DetectGoTypecheck` errors, the check is omitted, and an erratum records why.
9. **The docs say "seven read tools" and `code_search` becomes the "eighth tool".** This follows PRD 09's own wording ("the six read tools become seven") and the wording spec 15 used when it went from four to six.
10. **The baseline gets a column and an erratum, not numbers.** There is no live model here. Specs 15 and 16 handled the same situation with dashes and an erratum, and an invented count would become the figure later work is compared to. PRD 09's goal that `search_files` calls fall is therefore read off the before/after rows when a run fills them. No test asserts a fall.
11. **The new column sits between `find_symbol` and `code_search`.** The three declaration-navigation tools stay together. `TestTS16_29` pins the old header and is updated, and `TestTS15_16`, which pins only `| file_outline | find_symbol |`, still holds.
12. **The erratum is named after this spec's number.** `.specs/` currently ends at 16, so the number is 17, but the spec tool assigns it. The erratum filename follows whatever it assigns.
13. **Tests are updated rather than relaxed.** Membership assertions stay exact (a set equality, not "at least these"), because `SelectTools`' silent drop makes an exact assertion the only guard against a replace target that lacks the tool.
14. **Sites that call into AgentKit are documented as mostly `lexical`.** The checker stubs imports outside the workspace, and agent-fox's own code leans on AgentKit's types. The header's per-confidence counts already tell the model which sites to trust, so no mitigation is added in agent-fox.

## Dependencies

| Spec | Reason |
|---|---|
| `15_symbol_navigation_tools` | Modifies it. `ReadOnlyFileTools` grows from six to seven, and its tests (TS-15-1 to TS-15-5, TS-15-16 and the docs tests) pin six. `DetectSymbolBackend` is the pattern `DetectGoTypecheck` copies. |
| `16_indexed_code_search` | Modifies it. `code_search` stays outside the list, and its docs tests select rows by "the six read tools" and pin the baseline header. `go_typecheck` slots in before `code_search_index`. The shared index feeds `find_references`' candidate search and is already invalidated by `fix` and `impl`. |
| `13_tool_call_counts_and_relevant_files` | The counters already count the new tool by name, and the baseline tables and procedure this spec extends are its. |
| `11_preflight_checks` | The new check follows its contract: advisory entries report their own `ok`, and a refusal carries no checklist. |
| `14_repo_map` | Depends on it, unchanged. The `--repo-map-tokens 0` prompts are the reference for the prompt-diff requirement, and the map's opening sentence is not edited. |

## Verified External API

AgentKit's source is outside the repository and could not be read, so every symbol that does not already appear at an agent-fox call site is **unverified**. The implementer checks each against the replace target before coding, per AGENTS.md.

| Symbol | Signature | Status |
|---|---|---|
| `tools.All` | `func All(opts Options) ([]core.Tool, error)` | Verified at call sites: `internal/agentrun/phase.go` (`registeredTools`) and `symbols.go`. |
| `tools.Options` | `Workspace *Workspace`, `Index Index`; `Symbols` is the field spec 15 requires to stay zero (`phase_test.go` TS-15-8) | `Workspace` and `Index` verified at the `phase.go` call site. The type of `Symbols` is unverified. |
| `tools.Workspace` | `Root string`; `Resolve(path string) (string, error)` | Verified at call sites (`ws.Root`; `r.cfg.Workspace.Resolve`). |
| `tools.Index` | interface; `Invalidate(rel string)` returns nothing | Verified from `docs/errata/16_indexed_code_search.md` and the test doubles in `internal/agentrun/indextest`. |
| `core.Tool` | `Name string`, `Description string`, `PromptGuidelines []string` | Verified in `internal/agentrun/policy.go`. |
| `tools.CtagsRunner` | `CtagsRunner(nil)(ctx, []string) (…, error)`; `tools.ErrCtagsUnavailable` | Verified at the call site in `symbols.go`. |
| `outline.Decl` | fields `Name`, `Kind`, `Container`, `Signature`, `StartLine`, `Exported`; constants `outline.KindMethod`, `outline.LangGo` | Verified at call sites in `internal/repomap/repomap.go`. |
| tool `find_references` | arguments `name`, `path`, `kind`, `include_tests`, `max_results` (PRD 09 §1) | **NOT FOUND** in this repository. It is assumed to be returned by `tools.All` once AgentKit's spec is merged. Unverified. |
| `(*tools.Workspace).References` | `func (ws *Workspace) References(ctx context.Context, target outline.Decl, opts ReferenceOptions) (ReferenceResult, error)` | **Unverified.** Signature taken from PRD 09 §5. |
| `tools.ReferenceOptions`, `tools.ReferenceResult` | Fields not specified by the input. agent-fox needs from the result the packages-checked count, the errors-collected count and the partial flag (design decision 8). | **Unverified assumption.** The field names must be read from the replace target. If the summary is absent, `DetectGoTypecheck` returns an error and the check is omitted. |
| `tools.SymbolOptions` | `MaxFiles`, `MaxDuration`; defaults 50 000 and 2 s | **Unverified.** Taken from PRD 09 §3. agent-fox does not set them. |
