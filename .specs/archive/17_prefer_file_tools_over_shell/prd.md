---
spec_id: "17"
spec_name: "prefer_file_tools_over_shell"
title: "Steer models to the built-in file tools instead of reading through the shell"
status: "active"
created_at: "2026-10-06T10:42:47.537934Z"
updated_at: "2026-10-06T10:42:47.537934Z"
intent_hash: "1f1018380cec216e942345891c7c29a82b0a610f0cd1f81ef0f06ade2624f639"
schema_version: 2
source: "https://github.com/agent-fox-dev/agent-fox/issues/176"
---
## Intent

Every agent phase that has both the built-in file tools and a shell must tell the model, in its system prompt, to read, search and list files with the built-in tools and to keep the shell for git and the build/test toolchain. The tool guidance that AgentKit and agent-fox already attach to each tool should reach the model, and should not be lost because agent-fox supplies its own system prompt.

## Goals

- In a phase whose registered tools include `search_files` and `execute`, the system prompt contains the search-over-execute guidance exactly once. A unit test checks this.
- Every `PromptGuidelines` entry of every registered tool (built-in and phase-specific `submit_*` tools) appears in that phase's system prompt, with exact duplicates removed. A unit test checks this.
- In every phase that has a shell and at least one of `read_file`, `search_files`, `find_files`, `list_files`, the system prompt has one explicit line saying which built-in tool to use for reading, searching, finding and listing, and that the shell is for git (and, in writing phases, building and testing) only.
- The `execute` tool description in those phases tells the model not to use `cat`/`head`/`tail`/`grep`/`rg` for reading or searching files, and names the built-in tools to use.
- The same registered tool set always produces a byte-identical tools note, so prompts stay deterministic.

## Non-goals

- Changing AgentKit's `prompt.Build` so that a custom system prompt no longer suppresses its guidelines block. That is a deliberate library behaviour, and the fix belongs in agent-fox.
- Removing `cat`, `head`, `tail`, `grep`, `rg` or `wc` from `ReadOnlyPrograms`, or making the guard refuse them with a redirect message the way it handles `find -exec`. The issue makes this conditional on logs still showing shell reads after the prompt fix. If they do, it becomes a follow-up spec.
- Changing the repository-map `Intro` text in `internal/repomap` (golden-tested under spec 14). The new tools-note line covers phases with and without a map.
- Measuring the before/after run logs. The issue asks for a manual sample-run comparison. It is a check on the change, not behaviour this spec can encode.
- Any change to tool allowlists, guard rules, operators or path confinement.

## Background

`internal/agentrun/phase.go` `newAgent` sets `core.AgentConfig.SystemPrompt: p.System` and then appends `toolsNote(registered)`. According to the issue, AgentKit's `prompt.Build` emits `BaseInstructions` and the per-tool guidelines block only when the custom prompt is empty. agent-fox always sets one, so no `PromptGuidelines` entry reaches the model. This includes:

- AgentKit's built-in guidance for the file tools, including the search-over-execute line ("Prefer search_files over execute+grep: it respects .gitignore and returns structured matches.").
- agent-fox's own guidelines on its terminating tools, for example `submitAnalysisTool` ("Report the diagnosis by calling submit_analysis; do not write it as prose.") and `submitImplementationTool`'s `criteriaGuideline` in `codefix/phases.go`, and the guidelines in `codeimpl/phases.go`, `specgen/prd.go`, `specgen/artifacts.go`, `issuetriage/triage.go` and `internal/conform/review.go`.

`SelectTools` in `internal/agentrun/policy.go` already drops guidelines that mention `execute` when the phase has no shell (tested by `TestSelectToolsDropsExecuteGuidelinesWithoutAShell`). Because nothing renders the guidelines, that filter currently does nothing.

`toolsNote` today only lists the sorted tool names. When there is no shell, it adds "There is no shell: read with the file tools…". It says nothing about preferences when a shell is present. `describeForPhase` writes the allowlist into the `execute` description ("The only programs allowed are: git, ls, cat, head, tail, wc, rg, grep, …"). For a model, that reads as an invitation to use `cat` and `grep`.

Affected phases are those with `ReadOnlyFileTools` plus `execute`: codefix `analyse` and `implement`, codeimpl `survey`, `repair`, `implement` and `resolve`, and the conformance `review` in `internal/conform/review.go`. The guideline rendering applies to every phase that goes through `agentrun.Runner`, including triage and spec, which have no shell.

## Requirements

1. **Guidelines are rendered into the tools note.** `toolsNote` appends a guidelines section after the "Your tools are exactly: …" sentence whenever at least one registered tool has a non-empty `PromptGuidelines` entry. The section has a fixed heading line (`Tool guidelines:`) followed by one `- ` bullet per guideline. Tools are visited in the same sorted-name order as the tool list, and each tool's guidelines keep their declared order. A guideline that is empty or whitespace-only is skipped. A guideline whose trimmed text exactly matches one already emitted is skipped. When no registered tool has a guideline, there is no section.

2. **Search-over-execute guidance.** When the registered set contains `search_files` and any tool in `ShellTools`, the search-over-execute guideline (AgentKit's `tools.SearchOverExecuteGuideline` text) appears in the guidelines section exactly once. This holds whether `search_files` already carries it in its own `PromptGuidelines` or not. When either tool is absent, it does not appear.

3. **Explicit preference line.** When the registered set contains a shell tool and at least one of `read_file`, `search_files`, `find_files`, `list_files`, the note includes one sentence that names only the registered tools among those four, in the form "Read files with `read_file`, search with `search_files`, find files with `find_files` and list directories with `list_files`." The sentence is followed by a clause limiting the shell, named by its registered name (e.g. `execute`). In a writing phase (the set contains `write_file` or `edit_file`), the clause is "use `execute` only for git and for building, formatting and testing". Otherwise it is "use `execute` only for git". The line comes before the guidelines section.

4. **Phases without a shell are unchanged in substance.** When no shell tool is registered, the existing "There is no shell…" sentence stays. The preference line of requirement 3 and the guideline of requirement 2 are not emitted. Guidelines that mention `execute` remain filtered out by `SelectTools`, so they do not reach the rendered section.

5. **`execute` description discourages shell reads.** In `SelectTools`, when the selected names include `execute` and at least one of `read_file` or `search_files`, the phase-specific `execute` description (read-only and writing variants) gains a sentence: do not use `cat`, `head`, `tail`, `grep` or `rg` to read or search repository files; use `read_file` and `search_files`, which respect `.gitignore` and return structured results. The sentence names only the built-in tools that were selected. The allowlist text is unchanged, so the description still matches what the guard enforces.

6. **Determinism.** For the same registered tool set, `toolsNote` returns byte-identical output on every call. No map-iteration order, time or randomness affects it.

7. **Scope of effect.** The change is made once, in `internal/agentrun`, and applies to every phase run through `agentrun.Runner`. No pipeline package (`codefix`, `codeimpl`, `specgen`, `issuetriage`, `internal/conform`) needs its own prompt text changed for these requirements to hold.

8. **Tests.** Unit tests in `internal/agentrun` cover:
   - a set with `search_files` and `execute` renders the search-over-execute line once, and every registered tool's guidelines appear;
   - the preference line in a read-only shell set and in a writing shell set;
   - no preference line and no execute-mentioning guideline in a set without a shell;
   - exact-duplicate guidelines appear once;
   - empty guidelines produce no section;
   - the `execute` description gains the discouraging sentence only when `read_file` or `search_files` is selected.

   One runner-level test confirms that the system prompt reaching the faux provider for such a phase contains the guidelines. Existing tests (`TestToolsNoteListsTheToolsAndSaysWhenThereIsNoShell`, `TestTS15_6_ToolsNoteListsTheSymbolTools`, `TestSelectToolsTellsThePhaseItsRules`) keep passing.

9. **Documentation.** `docs/cli.md` (section "What the model may and may not do") and `docs/model-usage.md` describe what the system prompt now tells the model: the rendered tool guidelines, the preference for the built-in file tools, and that the shell is for git and the build/test toolchain. `make check` passes.

## Design Decisions

1. **Fix in agent-fox, not AgentKit.** The guidelines are rendered by `toolsNote`. `prompt.Build`'s "custom replaces built-in sections" behaviour is deliberate, and the issue flags changing it as a separate decision. agent-fox already owns the tail of its system prompt.
2. **Render the guidelines of all registered tools, custom `submit_*` tools included.** Those guidelines were written to be shown and are dropped today by the same mechanism. Restricting rendering to built-ins would leave the same bug in place for them.
3. **Sorted tool order and exact-text de-duplication.** This matches the existing sorted tool list and keeps the note deterministic for prompt caching. It also prevents the search-over-execute line appearing twice when AgentKit both attaches it to `search_files` and exposes it as a constant.
4. **The search-over-execute line is added by condition (search_files + shell), not taken on trust from `search_files`' guidelines.** I could not read AgentKit's source to confirm whether the line is a static `search_files` guideline or only added by `prompt.Build`'s conditional logic. Adding it conditionally and de-duplicating works in both cases.
5. **Writing vs read-only is derived from the registered set (`write_file`/`edit_file` present).** This keeps `toolsNote`'s signature taking only the registered tools, and it matches what the model can actually do.
6. **Defer allowlist removal and guard refusals (issue option 3).** The issue makes it conditional on logs after the prompt fix. Removing `grep`/`tail` would also break legitimate writing-phase uses like `go test ./... | tail` and `git log | grep`, and would change the documented allowlist tables.
7. **Add a discouraging sentence to the `execute` description instead of hiding `cat`/`grep` from it.** The description must keep naming exactly what the guard allows (the policy.go comment says the text "cannot drift" from the allowlist). An added sentence steers the model without breaking that invariant.
8. **Leave `internal/repomap` `Intro` alone.** It only appears with a map and is golden-tested under spec 14. The tools-note line reaches every phase regardless.
9. **No size cap on the guidelines section.** Guidelines are single sentences. The largest dynamic one, `criteriaGuideline`, is already bounded by `maxCriteria` in `codefix/criteria.go`.

## Dependencies

| Spec | Reason |
|---|---|
| `15_symbol_navigation_tools` | Modifies `toolsNote`, whose naming of the symbol tools is covered by 15-REQ-1.5 (`TestTS15_6_ToolsNoteListsTheSymbolTools`); that behaviour must be preserved. |
| `14_repo_map` | Its `Intro` text is the other place that names the file tools; this spec deliberately leaves it unchanged. |

## Verified External API

agentkit-go is pulled in through `replace github.com/agentfox/agentkit-go => ../agentkit-go`, which is outside the repository, so its source could not be read. Signatures below come from call sites in this repository unless marked otherwise.

- `core.Tool` — struct with fields `Name string`, `Description string`, `PromptGuidelines []string`, `InputSchema`, `Execute func(context.Context, json.RawMessage) core.ToolResult` (seen in `codefix/phases.go`, `internal/agentrun/policy.go`).
- `core.AgentConfig.SystemPrompt string` — set in `internal/agentrun/phase.go` `newAgent`.
- `tools.All(tools.Options{Workspace: *tools.Workspace}) ([]core.Tool, error)` — called in `registeredTools`.
- `tools.SearchOverExecuteGuideline` — **unverified**. Assumed to be an exported `string` constant in `github.com/agentfox/agentkit-go/tools` with the text "Prefer search_files over execute+grep: it respects .gitignore and returns structured matches." (per the issue). If it is not exported, a local constant with the same text is used.
- `prompt.Build` and its `Custom` / guidelines-block behaviour — **unverified**, taken from the issue's quotation of agentkit-go `prompt/prompt.go`. This spec does not call it.
