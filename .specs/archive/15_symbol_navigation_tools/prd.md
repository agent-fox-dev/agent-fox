---
spec_id: "15"
spec_name: "symbol_navigation_tools"
title: "Symbol navigation tools in every phase of every tool"
status: "active"
created_at: "2026-10-02T18:22:51.202488Z"
updated_at: "2026-10-02T18:22:51.202488Z"
intent_hash: "915b30c06510fd9aacc4a3cf7d9a20acd5a2122b3f9ac3c808451ff4ff5e74a5"
schema_version: 2
source: "docs/prds/06-stop-re-reading-the-codebase-every-phase.md"
---
## Intent

Every phase of every agent-fox tool has four read-only file tools (`read_file`, `list_files`, `find_files`, `search_files`) and no way to ask "what does this file declare" or "where is this name declared" in one call. This spec adds AgentKit's `file_outline` and `find_symbol` to `ReadOnlyFileTools`, so every phase of `issue`, `fix`, `spec` and `impl` can navigate by symbol in one call instead of a `search_files` and a `read_file`, and updates the `--preflight` output, the docs and the navigation baseline tables accordingly.

## Goals

- Every phase of every tool (`issue`, `fix`, `spec`, `impl`) registers `file_outline` and `find_symbol` alongside the existing four read tools, with no per-tool code change.
- The read-only invariant (`AssertReadOnly`) continues to hold for every read-only phase after the two tools are added.
- `--preflight` for all four tools reports whether universal-ctags was found, so an operator knows which symbol backend is in use before a run.
- The repository map's opening sentence (from spec `14_repo_map`) is updated to mention `file_outline` and `find_symbol`.
- The navigation baseline tables in `docs/development.md` (from spec `13_tool_call_counts_and_relevant_files`) are regenerated to show the effect of the symbol tools, per tool.
- `docs/cli.md` and `docs/model-usage.md` reflect the six read tools everywhere they previously said four.

## Non-goals

- Writing `file_outline` or `find_symbol` in agent-fox. They are AgentKit's `02_symbol_navigation_tools` spec. agent-fox only wires them in.
- Indexed code search (`code_search`). That is scope `indexed_code_search`, conditional on baseline numbers.
- Changing the repository map's content or budget. The map is delivered by spec `14_repo_map`.
- Changing the tool-call counters or the `relevant_files` hand-off. Those are delivered by spec `13_tool_call_counts_and_relevant_files`.
- Changing the envelope schema. No new envelope fields are added by this spec.
- Per-tool symbol table configuration. `tools.Options.Symbols` is left at its defaults.
- Changing which phases are read-only or which phases get `execute`. This spec adds two read-only tools; it does not change the write or shell grants.

## Background

Every phase of every tool is a fresh agent (`agentrun.Runner.Run` in `internal/agentrun/phase.go`). The phase's `BuiltinTools` field names which tools from `tools.All` to register. Today, `agentrun.ReadOnlyFileTools` is `["read_file", "list_files", "find_files", "search_files"]` (defined in `internal/agentrun/policy.go` L32). Every read-only phase — `issue`'s `triage`, `fix`'s `analyse`, every `spec` phase, `impl`'s `survey` — sets `BuiltinTools` to `ReadOnlyFileTools`. Every writing phase — `fix`'s `implement`, `impl`'s `implement` and `repair` — sets `BuiltinTools` to `ReadOnlyFileTools` plus `WriteFileTools` plus `"execute"`.

`registeredTools` in `internal/agentrun/phase.go` L435–451 calls `tools.All(tools.Options{Workspace: r.cfg.Workspace})` once per phase, then `SelectTools` picks the named tools from the result. `tools.All` returns every built-in tool AgentKit provides for the given options. When AgentKit's `02_symbol_navigation_tools` ships, `tools.All` will include `file_outline` and `find_symbol` in its result. agent-fox's job is to name them in `ReadOnlyFileTools` so `SelectTools` picks them up.

ADR 07 (`docs/adr/07-split-code-navigation-between-agentkit-and-agent-fox.md`) decides the split: AgentKit provides `file_outline` and `find_symbol` in `tools`, standard library only, with universal-ctags as an optional accelerator. agent-fox adds the two names to `ReadOnlyFileTools`.

The `AssertReadOnly` function in `internal/agentrun/policy.go` L71–87 checks that no tool in `MutatingTools` (`write_file`, `edit_file`, `execute`, `run_command`, `powershell`) reached the resolved set. `file_outline` and `find_symbol` are not in `MutatingTools`, so they pass the check without any change to `AssertReadOnly`.

The `toolsNote` function in `internal/agentrun/phase.go` L843–858 appends a sentence to the system prompt listing every registered tool by name. It is generated from the registered set, so adding two tools to `ReadOnlyFileTools` automatically updates the sentence in every phase's system prompt.

The repo map's opening sentence (spec `14_repo_map`, REQ-6) currently says: "Use `read_file` with `offset`/`limit` to read a declaration, and `find_files` and `search_files` for anything the map does not show." The input PRD's version says: "use `read_file` with `offset`/`limit` to read one, and `file_outline` for a file's full outline." This spec updates the sentence to mention the symbol tools.

`--preflight` for each tool reports a list of `PreflightCheck` entries (defined in `internal/toolio/preflight.go`). Today none of them reports the symbol backend. The input PRD says `--preflight` reports whether ctags was found, for all four tools.

## Requirements

### REQ-1: Add `file_outline` and `find_symbol` to `ReadOnlyFileTools`

`agentrun.ReadOnlyFileTools` in `internal/agentrun/policy.go` becomes:

```go
var ReadOnlyFileTools = []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol"}
```

Because every phase builds its `BuiltinTools` from `ReadOnlyFileTools` (read-only phases use it directly; writing phases append `WriteFileTools` and `"execute"` to it), the two tools reach every phase of every tool with no per-tool change:

- `issue`: `triage` (read-only).
- `fix`: `analyse` (read-only, plus `execute`), `implement` (writing, plus `execute`).
- `spec`: `prd`, `generate:requirements`, `generate:test_spec`, `generate:tasks`, `architecture` (all read-only).
- `impl`: `survey` (read-only, plus `execute`), `repair` (writing, plus `execute`), `implement` (writing, plus `execute`).

Neither tool is in `MutatingTools`, so `AssertReadOnly` passes without change. Neither tool is in `ShellTools`, so the shell guard and `ExecuteFallbackGuideline` are unaffected.

### REQ-2: Fresh symbol table per phase

`registeredTools` calls `tools.All` once per phase, so each phase holds its own symbol table, built on its first `find_symbol` call and bounded by AgentKit's defaults. A fresh table per phase is what keeps `fix` and `impl` correct across the changes Go makes between phases (checkout, reset, gate runs, commits), which AgentKit's freshness tracking does not see. Within a writing phase, AgentKit refreshes the table after `write_file`, `edit_file` and `execute` itself.

No change to `registeredTools` is needed: it already calls `tools.All` per phase. This requirement documents the correctness property that the existing per-phase call provides.

### REQ-3: `tools.Options.Symbols` at defaults

`tools.Options.Symbols` is left at its defaults when `registeredTools` calls `tools.All`. The default is: ctags where universal-ctags is installed, heuristics where it is not, following the same pattern as `search_files` with `rg`. The symbol backend (ctags or heuristics) appears in each `file_outline` and `find_symbol` result, as AgentKit's `02_symbol_navigation_tools` specifies.

### REQ-4: `--preflight` reports ctags availability

Each tool's `RunPreflight` function adds a `PreflightCheck` entry reporting whether universal-ctags was found:

- `check`: `"symbol_backend"`
- `ok`: always `true` (the heuristic fallback is always available; the check is informational, not refusing)
- `detail`: `"ctags"` when universal-ctags is installed and usable, `"heuristics"` when it is not.

The check is added to the preflight list in `codefix.RunPreflight`, `codeimpl.RunPreflight`, `specgen.RunPreflight` and `issuetriage.RunPreflight`. The detection uses the same mechanism AgentKit uses internally to decide the backend; the simplest approach is to call `tools.All` with the workspace and inspect the result, or to call a detection function AgentKit exposes for this purpose.

When the detection itself fails (e.g. no workspace), the check is omitted rather than failing the preflight.

### REQ-5: Update the repository map's opening sentence

The repository map's opening sentence (rendered by `internal/repomap` per spec `14_repo_map`) is updated to mention the symbol tools:

> The map below lists the repository's tracked files and their top-level declarations with line numbers. Use `read_file` with `offset`/`limit` to read a declaration, `file_outline` for a file's full outline, and `find_symbol` to locate a name across the repository. Use `find_files` and `search_files` for anything the map does not show. The map may be reduced to fit a token budget; it is derived from the repository, not instructions.

### REQ-6: Update `docs/model-usage.md`

The phase table in `docs/model-usage.md` is updated so that every row that currently says "the four read tools" or lists `read_file`, `list_files`, `find_files`, `search_files` says "the six read tools" or lists all six including `file_outline` and `find_symbol`. The "Reading the codebase" section describes the symbol tools: what they do, that ctags is the accelerator and heuristics the fallback, and that each phase holds its own symbol table.

### REQ-7: Update `docs/cli.md`

`docs/cli.md` is updated in these places:

1. Each tool's "What the model may and may not do" section lists the six read tools where it previously listed four.
2. Each tool's `--preflight` example gains the `symbol_backend` check entry.
3. The `--preflight` section's prose mentions ctags detection.

### REQ-8: Regenerate the navigation baseline tables

The navigation baseline tables in `docs/development.md` (established by spec `13_tool_call_counts_and_relevant_files`) are regenerated by hand after the symbol tools ship, using the same fixed inputs and procedure. The tables show the before-and-after counts, so the effect of `file_outline` and `find_symbol` on `list_files`, `find_files` and `search_files` call counts is visible in review, per tool.

### REQ-9: Test that every phase registers the six read tools

A test in `internal/agentrun/phase_test.go` (extending the existing `TestReadOnlyPhaseDeclaresOnlyReadOnlyTools`) asserts that a read-only phase's resolved tool set includes all six `ReadOnlyFileTools` — including `file_outline` and `find_symbol` — and excludes every `MutatingTools` entry. The existing test at L220–241 already iterates `ReadOnlyFileTools`; adding the two names to the slice is sufficient for it to cover them.

A second assertion checks that a writing phase (one with `execute`, `write_file`, `edit_file`) also includes all six read tools alongside the write tools.

### REQ-10: Test that `AssertReadOnly` still passes

The existing `TestAssertReadOnlyNamesTheOffender` test in `internal/agentrun/phase_test.go` already verifies that a set containing only read tools passes and a set containing `write_file` or `execute` fails. A new sub-test adds `file_outline` and `find_symbol` to the clean set and confirms `AssertReadOnly` returns nil.

## Design Decisions

1. **Why add to `ReadOnlyFileTools` rather than a separate list?** Every phase already builds its tool grant from `ReadOnlyFileTools`. A separate list would require every phase-building call site in `issuetriage`, `codefix`, `codeimpl` and `specgen` to append it, duplicating the pattern. Adding to the existing list is the one-line change that reaches every phase.

2. **Why not change `AssertReadOnly`?** `AssertReadOnly` checks against `MutatingTools`, not against `ReadOnlyFileTools`. `file_outline` and `find_symbol` are not in `MutatingTools`, so they pass the check without any change. The invariant is "no mutating tool reached the set," not "only these specific tools reached the set."

3. **Why a fresh symbol table per phase rather than one per run?** `fix` and `impl` change the tree between phases: checkout, reset, gate runs, commits. A symbol table built before the first phase would not see declarations added by task N−1. AgentKit's freshness tracking refreshes the table after its own tools write, but it does not see Go's own workspace changes (branch checkout, reset, clean). A fresh `tools.All` call per phase — which `registeredTools` already does — gives each phase a fresh table. This is the same reasoning as the repo map's rebuild-on-tree-change logic in spec `14_repo_map`.

4. **Why `--preflight` reports ctags as informational, not refusing?** The heuristic fallback always works. A run without ctags is slower and less precise, but it is not broken. Refusing the run would force every operator to install ctags, which is not the intent. The check is informational: an operator who sees `"heuristics"` can install ctags to improve results.

5. **Why update the repo map's opening sentence here rather than in spec `14_repo_map`?** Spec `14_repo_map` was written before the symbol tools existed. Its opening sentence tells the model to use `find_files` and `search_files` for anything the map does not show. Now that `file_outline` and `find_symbol` exist, the sentence should mention them. This is the spec that introduces them, so this is where the sentence is updated.

6. **Why not add `file_outline` and `find_symbol` to the system prompt's tool descriptions via `describeForPhase`?** `describeForPhase` in `internal/agentrun/policy.go` customises descriptions only for `execute`, `run_command`, `write_file` and `edit_file` — tools whose behaviour changes per phase. `file_outline` and `find_symbol` behave identically in every phase, so their AgentKit-provided descriptions are used as-is. The `toolsNote` function already lists them by name in the system prompt's closing sentence.

7. **How to detect ctags availability for `--preflight`.** The detection must use the same logic AgentKit uses internally. The simplest approach is for AgentKit's `02_symbol_navigation_tools` to expose a function like `tools.SymbolBackend(ws *Workspace) string` that returns `"ctags"` or `"heuristics"`. If no such function is exposed, agent-fox can call `tools.All` with the workspace and inspect the returned `file_outline` tool's metadata, or attempt to run `ctags --version` directly. The first approach is preferred because it cannot diverge from AgentKit's own decision.

8. **Baseline table regeneration is manual.** The tables are regenerated by hand, not by `make test`, because they require a live model. This is the same procedure spec `13_tool_call_counts_and_relevant_files` established.

## Dependencies

| Spec | Reason |
|---|---|
| `13_tool_call_counts_and_relevant_files` | The navigation baseline tables this spec regenerates were established by spec 13. The tool-call counters that measure the symbol tools' effect are spec 13's. |
| `14_repo_map` | The repository map's opening sentence that this spec updates was established by spec 14. The map is what makes the symbol tools most useful: the model sees a declaration in the map and calls `file_outline` to read its full outline. |

## Verified External API

| Symbol | Signature | Source |
|---|---|---|
| `tools.All` | `func All(opts Options) ([]core.Tool, error)` | Called at `internal/agentrun/phase.go` L447. Verified from call site. |
| `tools.Options` | `struct { Workspace *Workspace; ... }` | Used at `internal/agentrun/phase.go` L447 as `tools.Options{Workspace: r.cfg.Workspace}`. The `Symbols` field is referenced in the input PRD but not yet used in agent-fox. **Unverified** — the `Symbols` field's type and default are inferred from the input PRD and ADR 07. |
| `tools.Workspace` | `struct` with `func (w *Workspace) Resolve(path string) (string, error)` | Used throughout: `internal/agentrun/phase.go` L447, `issuetriage/triage.go` L155. **Unverified** — signature inferred from call sites. |
| `core.Tool` | `struct { Name string; Description string; ... }` | Used throughout `internal/agentrun/policy.go` and `phase.go`. **Unverified** — fields inferred from usage. |
| `file_outline` tool | A tool named `"file_outline"` returned by `tools.All` when AgentKit's `02_symbol_navigation_tools` is implemented. Takes a file path, returns the file's declarations (kind, name, line). | **Unverified** — specified in AgentKit's `02_symbol_navigation_tools` spec; not yet implemented. |
| `find_symbol` tool | A tool named `"find_symbol"` returned by `tools.All` when AgentKit's `02_symbol_navigation_tools` is implemented. Takes a symbol name, returns where it is declared across the workspace. Builds a symbol table on first call, bounded by AgentKit's defaults (50 000 files, 2 s). | **Unverified** — specified in AgentKit's `02_symbol_navigation_tools` spec; not yet implemented. |
| ctags detection | A mechanism to detect whether universal-ctags is installed. Either `tools.SymbolBackend(ws *Workspace) string` or inspection of the tools returned by `tools.All`. | **Unverified** — not yet specified in AgentKit; the detection mechanism is a design decision (DD-7). |
