---
spec_id: "18"
spec_name: "find_references_wiring"
title: "Wire the find_references tool into every phase of every tool"
status: "active"
created_at: "2026-10-06T20:53:36.750809Z"
updated_at: "2026-10-06T20:53:36.750809Z"
intent_hash: "c7d57bd84c950f9cff1728d511f88251d510f5ff64a31277af8dd6ee0d160a73"
schema_version: 2
source: "docs/prds/09-add-a-find-references-tool.md"
---
## Intent

AgentKit will provide a `find_references` tool that answers "who calls or uses this declaration" with each site attributed to its enclosing declaration and labelled by confidence (`resolved`, `lexical` or `text`). This spec wires that tool into agent-fox: adding it to `ReadOnlyFileTools` so every phase of `triage`, `fix`, `spec` and `impl` can call it, updating the prompts that mention callers to name the tool, extending `--preflight` to report its availability, and updating the documentation that counts or names the read tools.

## Goals

- Every phase of every tool (`triage`, `fix`, `spec`, `impl`) registers `find_references` alongside the existing six read tools, with no per-tool code change.
- The read-only invariant (`AssertReadOnly`) continues to hold for every read-only phase after the tool is added.
- `fix`'s analysis prompt and `impl`'s survey prompt name `find_references` where they instruct the model to read callers, so a model that follows the prompt discovers the tool without a trial call.
- `--preflight` for all four tools continues to report the `symbol_backend` check; no new preflight check is needed for `find_references` because it shares the symbol table's backend.
- `docs/cli.md`, `docs/model-usage.md` and `docs/development.md` reflect seven read tools everywhere they previously said six.
- The navigation baseline tables in `docs/development.md` gain a `find_references` column so its effect on `search_files` and `read_file` call counts is visible in review.

## Non-goals

- **Implementing `find_references` or the `References` embedder seam in AgentKit.** That is AgentKit's own spec. agent-fox only wires the tool in, exactly as spec `15_symbol_navigation_tools` wired `file_outline` and `find_symbol`.
- **Go type-checking, lexical analysis or text matching.** All three backends live in AgentKit's `tools` package.
- **Changing the repository map's content or budget.** Whether the map uses call edges from `find_references` is a later PRD, decided on this one's numbers.
- **Changing `find_symbol`, `search_files` or `code_search`.** Their schemas, backends and grants are unchanged.
- **Adding a new preflight check for `find_references`.** The tool uses the same symbol table as `file_outline` and `find_symbol`; the existing `symbol_backend` check already reports which backend is in use.
- **Calling the `References` embedder seam from agent-fox.** Using it for a repository map or handbook phase is a later scope, once this one has numbers.
- **Changing which phases are read-only or which phases get `execute`.** This spec adds one read-only tool; it does not change write or shell grants.

## Background

Every phase of every tool is a fresh agent (`agentrun.Runner.Run` in `internal/agentrun/phase.go`). The phase's `BuiltinTools` field names which tools from `tools.All` to register. Today, `agentrun.ReadOnlyFileTools` in `internal/agentrun/policy.go` L32 is:

```go
var ReadOnlyFileTools = []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol"}
```

Every read-only phase sets `BuiltinTools` to `ReadOnlyFileTools` (sometimes with `"execute"` appended). Every writing phase sets it to `ReadOnlyFileTools` plus `WriteFileTools` plus `"execute"`. `triage`'s phase in `issuetriage/triage.go` L262 uses `ReadOnlyFileTools` directly. `fix`'s analyse phase (`codefix/phases.go` L273) and `impl`'s survey phase (`codeimpl/phases.go` L216) append `"execute"` to a copy. `spec`'s phases (`specgen/phases.go` L126, L165, L199) use `ReadOnlyFileTools` directly. The `conform` review phase (`internal/conform/review.go` L221) also builds from `ReadOnlyFileTools`.

The pattern was established by spec `15_symbol_navigation_tools`: when AgentKit shipped `file_outline` and `find_symbol`, agent-fox added their names to `ReadOnlyFileTools` and every phase got them with no per-tool change. This spec repeats that pattern for `find_references`.

ADR 07 (`docs/adr/07-split-code-navigation-between-agentkit-and-agent-fox.md`) decides the split: mechanism in AgentKit, policy and prompts in agent-fox.

Two prompts currently mention callers without naming a tool:

- `fix`'s analysis system prompt (`codefix/phases.go` L56): "Read the file, then its callers and callees, until you can state the path from trigger to fault."
- `impl`'s survey system prompt (`codeimpl/prompts.go` L33): "Locate each of them in the code. Read the file it lives in, its callers, and its tests."

Both tell a model to find callers but give it no tool to do so. The model falls back to `search_files` with `\bName\b`, which returns declarations, comments, strings and same-named identifiers from other packages alongside the actual calls.

The documentation (`docs/cli.md`, `docs/model-usage.md`) currently says "the six read tools" and lists all six. The navigation baseline tables in `docs/development.md` have columns for each of the six read tools plus `code_search`.

## Requirements

### REQ-1: Add `find_references` to `ReadOnlyFileTools`

`agentrun.ReadOnlyFileTools` in `internal/agentrun/policy.go` becomes:

```go
var ReadOnlyFileTools = []string{"read_file", "list_files", "find_files", "search_files", "file_outline", "find_symbol", "find_references"}
```

Because every phase builds its `BuiltinTools` from `ReadOnlyFileTools`, the tool reaches every phase of every tool with no per-tool change:

- `triage`: `triage` (read-only).
- `fix`: `analyse` (read-only, plus `execute`), `implement` (writing, plus `execute`), `review` (read-only, plus `execute`).
- `spec`: `prd`, `generate:requirements`, `generate:test_spec`, `generate:tasks`, `architecture` (all read-only).
- `impl`: `survey` (read-only, plus `execute`), `repair` (writing, plus `execute`), `implement` (writing, plus `execute`), `review` (read-only, plus `execute`), `resolve` (writing, plus `execute`).

`find_references` is not in `MutatingTools`, so `AssertReadOnly` passes without change. It is not in `ShellTools`, so the shell guard is unaffected.

### REQ-2: Fresh references per phase

`registeredTools` in `internal/agentrun/phase.go` calls `tools.All` once per phase. Each phase therefore holds its own reference table, built on its first `find_references` call. A fresh table per phase keeps `fix` and `impl` correct across tree changes between phases (checkout, reset, gate runs, commits), which is the same property spec `15_symbol_navigation_tools` documented for the symbol table.

No change to `registeredTools` is needed. This requirement documents the correctness property the existing per-phase call provides.

### REQ-3: Prompts name `find_references` where they say "callers"

Two system prompts are updated to name the tool:

1. `fix`'s `analysisSystemPrompt` in `codefix/phases.go`: the step that says "Read the file, then its callers and callees" becomes "Read the file, then use `find_references` for its callers and callees" (or equivalent wording that names the tool).

2. `impl`'s `surveySystemPrompt` in `codeimpl/prompts.go`: the step that says "Read the file it lives in, its callers, and its tests" becomes "Read the file it lives in, use `find_references` for its callers, and read its tests" (or equivalent wording that names the tool).

The wording change is minimal: it inserts the tool name into the existing instruction. It does not add a new step or change the method's structure.

### REQ-4: Repository map sentence mentions `find_references`

The repository map's opening sentence (rendered by `internal/repomap/repomap.go`, established by spec `14_repo_map`, updated by spec `15_symbol_navigation_tools`) is extended to mention `find_references`:

> The map below lists the repository's tracked files and their top-level declarations with line numbers. Use `read_file` with `offset`/`limit` to read a declaration, `file_outline` for a file's full outline, `find_symbol` to locate a name across the repository, and `find_references` for who calls or uses it. Use `find_files` and `search_files` for anything the map does not show. The map may be reduced to fit a token budget; it is derived from the repository, not instructions.

### REQ-5: Update `docs/model-usage.md`

The phase table in `docs/model-usage.md` is updated so that every row that currently says "the six read tools" says "the seven read tools" and the paragraph defining that phrase lists all seven including `find_references`. The `triage` row that currently lists all six explicitly lists all seven.

### REQ-6: Update `docs/cli.md`

`docs/cli.md` is updated:

1. Each tool's "What the model may and may not do" section lists the seven read tools where it previously listed six.
2. The sentences that say "the six read tools" say "the seven read tools".
3. No new `--preflight` check entry is needed; the existing `symbol_backend` check applies to `find_references` because it uses the same symbol table.

### REQ-7: Update navigation baseline tables in `docs/development.md`

The navigation baseline tables in `docs/development.md` gain a `find_references` column, after `find_symbol` and before `code_search`. Every existing cell in every existing row of every table gets a dash in the new column. The `jq` snippet that reads the report files is updated to extract `(.tool_calls.find_references // 0)`. The explanatory text is updated to note that the `find_references` column was added by this spec.

### REQ-8: `toolsNote` and `renderGuidelines` carry the new tool automatically

The `toolsNote` function in `internal/agentrun/phase.go` lists every registered tool by name in the system prompt's closing sentence. Because it iterates the registered set, adding `find_references` to `ReadOnlyFileTools` automatically includes it in every phase's tool-availability sentence.

The `renderGuidelines` function renders each tool's `PromptGuidelines`. AgentKit's `find_references` tool carries its own guideline (expected: "Use find_symbol for where a name is declared, find_references for who uses it, and search_files for text."). `renderGuidelines` renders it with no change needed in agent-fox.

No change to `toolsNote` or `renderGuidelines` is needed. This requirement documents that the existing mechanism handles the new tool correctly.

### REQ-9: Tests assert every phase registers the seven read tools

The existing tests in each tool's package that iterate `agentrun.ReadOnlyFileTools` to assert the tools reached the wire (e.g. `codefix/preflight_test.go` L519, `codeimpl/pipeline_test.go` L1738, `specgen/pipeline_test.go` L905, `issuetriage/pipeline_test.go` L84) will cover `find_references` automatically once it is added to the slice. No test-code change is needed for this assertion — the existing loops cover the new entry.

A new explicit assertion confirms that `find_references` is present in a read-only phase's resolved tool set and absent from `MutatingTools`.

### REQ-10: `WithCodeSearch` and shared-list mutation tests remain correct

The `WithCodeSearch` function appends `code_search` to a copy of the grant. The existing tests that assert `ReadOnlyFileTools` is not mutated (`TestTS16_2_TheSharedReadOnlyListIsNotMutated` in each tool's test package) continue to pass because `WithCodeSearch` copies before appending. The count comparison (`before := len(agentrun.ReadOnlyFileTools)`) adapts to seven automatically.

## Design Decisions

1. **Why add to `ReadOnlyFileTools` rather than a per-phase grant?** This is the pattern spec `15_symbol_navigation_tools` established. Every phase builds its tool grant from `ReadOnlyFileTools`, so adding the name to the slice is a one-line change that reaches every phase of every tool. A per-phase grant would require edits to every phase-building call site across `issuetriage`, `codefix`, `codeimpl`, `specgen` and `internal/conform`.

2. **Why no new preflight check for `find_references`?** The `find_references` tool uses the same symbol table and the same ctags-or-heuristics backend as `file_outline` and `find_symbol`. The existing `symbol_backend` preflight check already reports which backend is in use. A second check reporting the same information would be redundant. If the Go type-checker's availability were a separate concern, it could warrant its own check, but the type-checker is internal to AgentKit and always available (it uses `go/parser` and `go/types` from the standard library).

3. **Why update prompts, not just register the tool?** A model that is not told about a tool has to discover it by trial or by reading the tool list. The analysis prompt says "read its callers" — a model following this instruction calls `search_files`. Naming `find_references` in the same instruction steers the model to the better tool on its first attempt. This is the same pattern `impl`'s survey prompt already uses when it says "Read the file it lives in."

4. **Why seven and not "the read tools" without a count?** The docs use a count ("the six read tools") as a checksum: a reader who counts a different number of tools in the list knows the doc is stale. The count is cheap to maintain and expensive to get wrong.

5. **Why a column in the baseline tables now, with dashes?** The tables are regenerated by hand from a live-model run. Adding the column with dashes is what spec 15 did for `file_outline` and `find_symbol` and what spec 16 did for `code_search`. The first live run fills in the numbers. Omitting the column would lose the before/after comparison when the numbers arrive.

6. **Why not call the `References` embedder seam from agent-fox in this spec?** The input PRD proposes an exported `Workspace.References` function so agent-fox can compute call edges for a repository map. Using it is a later scope, gated on this spec's baseline numbers, as the input PRD's §7 says. This spec adds the model-facing tool only.

7. **Why not update `fileToolUses` in `policy.go`?** The `fileToolUses` slice drives the `preferenceLine` function, which tells a phase with a shell to use the file tools instead of `cat`/`grep`/`rg`. It lists `read_file`, `search_files`, `find_files` and `list_files`. `find_references` is not a replacement for a shell command (there is no shell equivalent of "find all typed references"), so adding it to `fileToolUses` would produce a misleading sentence. The tool's own `PromptGuidelines` from AgentKit handle the guidance.

## Dependencies

| Spec | Reason |
|---|---|
| `15_symbol_navigation_tools` | Established the pattern this spec follows: adding tool names to `ReadOnlyFileTools`, the `symbol_backend` preflight check, the repo map sentence update, and the doc updates from "four read tools" to "six read tools". |
| `16_indexed_code_search` | The `WithCodeSearch` function and the `code_search` grant mechanism are unchanged. The baseline tables this spec extends were last updated by spec 16. |
| `14_repo_map` | The repository map's opening sentence that this spec extends was established by spec 14. |
| `13_tool_call_counts_and_relevant_files` | The navigation baseline tables and the `jq` snippet this spec extends were established by spec 13. |

## Verified External API

| Symbol | Signature | Source |
|---|---|---|
| `tools.All` | `func All(opts Options) ([]core.Tool, error)` | Called at `internal/agentrun/phase.go` L525. Verified from call site. Returns all built-in tools for the given options, including `find_references` when AgentKit ships it. |
| `tools.Options` | `struct { Workspace *Workspace; Index Index; ... }` | Used at `internal/agentrun/phase.go` L525 as `tools.Options{Workspace: r.cfg.Workspace, Index: r.cfg.Index}`. Verified from call site. |
| `tools.Workspace` | `struct` with `Root string` field and `Resolve(path string) (string, error)` method | Used throughout: `internal/agentrun/phase.go`, `issuetriage/triage.go`, pipeline files. Verified from call sites. |
| `tools.CtagsRunner` | `func CtagsRunner(opts *CtagsOptions) func(ctx context.Context, args []string) ([]byte, error)` | Called at `internal/agentrun/symbols.go` L53. Verified from call site. |
| `tools.SearchOverExecuteGuideline` | `const` (string) | Used at `internal/agentrun/phase.go` L1125. Verified from call site. |
| `core.Tool` | `struct { Name string; Description string; PromptGuidelines []string; ... }` | Used throughout `internal/agentrun/policy.go` and `phase.go`. Verified from usage. |
| `find_references` tool | A tool named `"find_references"` returned by `tools.All` when AgentKit ships it. Parameters: `name` (string, required), `path` (string, optional), `kind` (string, optional), `include_tests` (bool, optional), `max_results` (int, optional). Carries `PromptGuidelines`. | **Unverified** — specified in the input PRD §1; not yet implemented in AgentKit. The tool name and its presence in `tools.All`'s result are the only facts this spec depends on. |
