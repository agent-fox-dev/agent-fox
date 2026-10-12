---
spec_id: "16"
spec_name: "indexed_code_search"
title: "Indexed code search: one code_search index per run, shared by every phase"
status: "active"
created_at: "2026-10-02T18:34:22.479438Z"
updated_at: "2026-10-02T18:34:22.479438Z"
intent_hash: "b39144b09dad68cd5ada961cda1d0c1062eea2983f8bd6ea510a33aa3322ceed"
schema_version: 2
source: "docs/prds/06-stop-re-reading-the-codebase-every-phase.md"
---
## Intent

After the repository map (spec `14_repo_map`) and symbol navigation tools (spec `15_symbol_navigation_tools`) ship, `search_files` may still account for a large share of navigation calls. This spec conditionally adds AgentKit's `code_search` tool — backed by a single `codesearch.Index` built once per run and shared by every phase — to every phase of `issue`, `fix`, `spec` and `impl`, so that ranked, indexed search is available alongside the six read tools when the baseline numbers justify it.

## Goals

- When the feature ships, every phase of every tool (`issue`, `fix`, `spec`, `impl`) can call `code_search` alongside the six read tools, with no per-tool code change beyond the pipeline's index lifecycle.
- Exactly one `codesearch.Index` is built per run, regardless of how many phases the run has, and it is `Close`d on every exit path including failure and cancellation.
- In `fix` and `impl`, the index is invalidated after every Go-initiated tree change (branch checkout, reset, clean, gate runs, commits with hooks) so that the next phase's `code_search` results reflect the current tree.
- When the index cannot be built (unsupported platform, build failure), the run continues without `code_search` and emits a `low` warning. Navigation never fails a run.
- On the navigation baseline inputs from spec `13_tool_call_counts_and_relevant_files`, `search_files` calls fall measurably in each tool after `code_search` is available, with no regression in that tool's own outcome.

## Non-goals

- Writing `code_search` or the `codesearch` module in agent-fox. They are AgentKit's `03_indexed_code_search` spec. agent-fox imports the module, builds the index and wires the tool into phases.
- Changing `ReadOnlyFileTools`. `code_search` is not added to `ReadOnlyFileTools` because it exists only when an index is present. It is appended to each phase's `BuiltinTools` by the pipeline.
- Changing the repository map, the symbol tools, the tool-call counters or the `relevant_files` hand-off. Those are delivered by specs `14_repo_map`, `15_symbol_navigation_tools` and `13_tool_call_counts_and_relevant_files`.
- Changing the envelope schema. No new envelope fields are added by this spec.
- Per-run index persistence or caching across runs. The index is ephemeral: built at run start, closed at run end.
- Rebuilding the index mid-run. The index is built once; `fix` and `impl` invalidate stale entries rather than rebuilding.
- Shipping unconditionally. This spec ships only after AgentKit's `03_indexed_code_search` passes its go/no-go and the baseline numbers from spec `13_tool_call_counts_and_relevant_files` show `search_files` is still a large share of navigation after the map and symbol tools.

## Background

Every phase of every tool is a fresh agent (`agentrun.Runner.Run` in `internal/agentrun/phase.go`). After specs 14 and 15, each phase has a repository map in its prompt and six read tools (`read_file`, `list_files`, `find_files`, `search_files`, `file_outline`, `find_symbol`). `search_files` delegates to `rg` where available and falls back to a native Go implementation. It is a linear scan: every call walks the tree. For large repositories or phases that search repeatedly, an indexed search that ranks results by relevance can reduce both the number of calls and the bytes returned.

ADR 07 (`docs/adr/07-split-code-navigation-between-agentkit-and-agent-fox.md`) decides the split: AgentKit provides the `codesearch` module as a **separate Go module** (`github.com/agentfox/agentkit-go/codesearch`) with its own `go.mod`, following the same pattern as `difftest/`. It plugs into `tools.Options` through an interface the root module defines (`tools.Index`). agent-fox opts in by importing the module, building one index per run, and adding `code_search` to every phase's tool grant.

The `registeredTools` function in `internal/agentrun/phase.go` L437–451 calls `tools.All(tools.Options{Workspace: r.cfg.Workspace})` once per phase. When `tools.Options.Index` is set, `tools.All` includes `code_search` in its returned tool set. `SelectTools` then picks it up if the phase's `BuiltinTools` names it.

The `agentrun.Config` struct carries the `Workspace` that every phase shares. The index, like the workspace, is a per-run resource that every phase uses. Adding it to `Config` lets `registeredTools` pass it through to `tools.Options` without any per-tool change.

The four pipelines handle tree changes differently:

- `issue` and `spec` never change the tree. No invalidation needed.
- `fix` changes the tree between its two phases: `git.CreateBranch` after analyse, then the implement phase writes files. The index must be invalidated after the branch creation.
- `impl` changes the tree extensively: branch creation, each task's writes, `revertSpecDir`, `discard` (reset + clean), gate runs, commits, and optionally repair. The index must be invalidated after each of these operations, before the next phase starts.

## Requirements

### REQ-1: `agentrun.Config` gains an `Index` field

`agentrun.Config` gains:

```go
// Index is the code-search index shared by every phase of this run.
// When non-nil, registeredTools passes it as tools.Options.Index, and
// code_search appears in tools.All's result. Nil means no indexed search.
Index tools.Index
```

`registeredTools` passes it through:

```go
built, err := tools.All(tools.Options{Workspace: r.cfg.Workspace, Index: r.cfg.Index})
```

This is the only change to `internal/agentrun/`. Every phase that names `"code_search"` in its `BuiltinTools` gets the tool; every phase that does not, does not.

### REQ-2: `code_search` in every phase's tool grant

Each tool's pipeline appends `"code_search"` to every phase's `BuiltinTools` when the `Config.Index` is non-nil. The append is conditional: when `Index` is nil, `BuiltinTools` is unchanged and `tools.All` does not return `code_search`, so `SelectTools` finds nothing to pick.

The condition is checked once per pipeline, not per phase. Each pipeline receives the index from its `Options` (see REQ-3) and decides at the top of its `Run` function whether to append the name.

`code_search` is not added to `ReadOnlyFileTools` because it is not universally available: a run without an index must not ask `SelectTools` for a tool that does not exist. The six read tools remain the unconditional set.

### REQ-3: Each tool's `Options` gains an `Index` field

`issuetriage.Options`, `codefix.Options`, `specgen.Options` and `codeimpl.Options` each gain:

```go
// Index is the code-search index for this run. Nil means no indexed search.
Index tools.Index
```

The `cmd/` entry points (`cmd/issue/main.go`, `cmd/fix/main.go`, `cmd/spec/main.go`, `cmd/impl/main.go`) build the index before calling `Run` and pass it through `Options`. They also pass it to `agentrun.Config` when building the `Runner`.

### REQ-4: Index lifecycle in `cmd/` entry points

Each `cmd/` entry point's `Exec` function:

1. Calls `codesearch.New(ctx, ws)` to build the index, where `ws` is the resolved `*tools.Workspace`.
2. If `codesearch.New` returns `codesearch.ErrUnsupported` or any other error, sets `Index` to nil, emits a `low` warning with code `code_search_unavailable`, and continues. Navigation never fails a run.
3. If the index was built, defers `index.Close()` so it is closed on every exit path, including failure, cancellation and panic.
4. Passes the index to both `agentrun.Config.Index` and the tool's `Options.Index`.

The index is built once, before the `Runner` is constructed. It is not rebuilt mid-run.

### REQ-5: Invalidation in `fix`

`codefix.Run` calls `Index.Invalidate("")` (invalidate the entire index) after each Go-initiated tree change, before the next phase starts:

1. After `git.CreateBranch` (between analyse and implement).

The implement phase is the last phase, so no invalidation is needed after it. The `Invalidate("")` call is a no-op when `Index` is nil, so the call site does not need a nil check — but the pipeline guards it with `if o.Index != nil` for clarity.

### REQ-6: Invalidation in `impl`

`codeimpl`'s pipeline calls `Index.Invalidate("")` after each Go-initiated tree change, before the next phase starts. The invalidation points are:

1. After branch creation or checkout (`git.CreateBranch` or `git.Checkout`), before the survey phase.
2. After `revertSpecDir` and `dropScratchFiles` in `runTask`, before the gate run.
3. After `discard` (reset + clean) when a task attempt fails and is retried.
4. After `git.CommitAll` when a task lands, before the next task's implementation phase.
5. After `runRepair`'s own tree changes (its commit), before the first task.
6. After `repairAfterTask`'s tree changes, before the task's commit.

Each call is guarded with `if o.Index != nil`. `issue` and `spec` never change the tree and never call `Invalidate`.

### REQ-7: Warning code for unavailable index

A new `WarnCode` constant is added to `internal/toolio/warncode.go`:

```go
WarnCodeSearchUnavailable WarnCode = "code_search_unavailable"
```

Its stage in `warnStages` is `"preflight"`, because the index is built during the preflight-equivalent setup before any model phase. It is added to `DeclaredWarnCodes()`.

### REQ-8: `AssertReadOnly` is unaffected

`code_search` is not in `MutatingTools`. It is a read-only tool. `AssertReadOnly` checks against `MutatingTools`, not against `ReadOnlyFileTools`, so adding `code_search` to a phase's grant does not affect the read-only invariant. No change to `AssertReadOnly` is needed.

### REQ-9: `--preflight` reports index availability

Each tool's `RunPreflight` function adds a `PreflightCheck` entry reporting whether the code-search index was built:

- `check`: `"code_search_index"`
- `ok`: always `true` (the fallback is `search_files`; the check is informational, not refusing)
- `detail`: `"built"` when the index was created, `"unavailable: <reason>"` when it was not.

When `Index` is nil (because `codesearch.New` failed or returned `ErrUnsupported`), the detail says why. The check is omitted entirely when the `codesearch` module is not imported (i.e. the feature has not shipped).

### REQ-10: `go.mod` gains the `codesearch` module

`go.mod` adds a `require` for `github.com/agentfox/agentkit-go/codesearch`. Because `go.mod` already has a `replace` directive for `github.com/agentfox/agentkit-go => ../agentkit-go`, a corresponding `replace` for the `codesearch` sub-module is added:

```
replace github.com/agentfox/agentkit-go/codesearch => ../agentkit-go/codesearch
```

This brings zoekt's dependency graph into agent-fox's `go.sum`. That is acceptable per ADR 07: agent-fox has no standard-library-only rule, and the dependency only arrives when the measurements call for it.

### REQ-11: Documentation updates

1. **`docs/cli.md`**: Each tool's "What the model may and may not do" section lists `code_search` alongside the six read tools, with a note that it is available when the index is built. Each tool's `--preflight` example gains the `code_search_index` check entry.
2. **`docs/model-usage.md`**: The phase table lists `code_search` for every phase, with a note that it is conditional on the index. The "Reading the codebase" section describes the indexed search: what it does, that it is built once per run, and that `fix` and `impl` invalidate it after tree changes.
3. **`docs/development.md`**: The navigation baseline tables are regenerated to show the effect of `code_search` on `search_files` call counts, per tool.

### REQ-12: Test that `code_search` reaches every phase when the index is present

A test in `internal/agentrun/` constructs a `Config` with a non-nil `Index` (a test double that satisfies `tools.Index`) and a `Phase` whose `BuiltinTools` includes `"code_search"`. It asserts that the resolved tool set includes `code_search` and that `AssertReadOnly` still passes for a read-only phase.

A second test constructs a `Config` with a nil `Index` and a `Phase` whose `BuiltinTools` includes `"code_search"`. It asserts that `code_search` is absent from the resolved tool set (because `tools.All` did not return it).

### REQ-13: Test that the index is closed on every exit path

An integration-level test per tool (or a shared helper) verifies that the index's `Close` method is called exactly once, on success, on failure and on context cancellation. The test uses a test double that records `Close` calls.

### REQ-14: Test that invalidation happens after tree changes in `impl`

A test using `codeimpl`'s scripted brain verifies that after a task lands (commit), the index's `Invalidate` method was called before the next task's implementation phase starts. The test uses a test double that records `Invalidate` calls with timestamps or sequence numbers.

## Design Decisions

1. **Why conditional on baseline numbers?** The index adds zoekt's dependency graph to `go.mod` and a build step to every run. If the map and symbol tools already reduce `search_files` calls to a small fraction, the index's cost exceeds its benefit. The baseline tables from spec 13 are the decision input.

2. **Why one index per run, not per phase?** Building an index can take seconds. An `impl` run with twelve tasks runs at least fourteen phases. Building fourteen indexes would add minutes of overhead. One index, invalidated when the tree changes, amortises the build cost across every phase.

3. **Why `Invalidate("")` rather than rebuilding?** `Invalidate("")` marks the entire index as stale, and the next `code_search` call rebuilds the affected parts lazily. This is cheaper than a full rebuild and is the API AgentKit's `03_indexed_code_search` provides for this purpose.

4. **Why not add `code_search` to `ReadOnlyFileTools`?** `ReadOnlyFileTools` is the unconditional set every phase gets. `code_search` exists only when an index is present. Adding it to `ReadOnlyFileTools` would cause `SelectTools` to look for a tool that `tools.All` did not return when the index is nil, silently dropping it. Appending it conditionally to each phase's `BuiltinTools` makes the presence explicit.

5. **Why add `Index` to `agentrun.Config` rather than to each tool's `Options`?** The index is passed to `tools.All` inside `registeredTools`, which is on `Runner`. `Runner` is built from `Config`. Putting the index on `Config` lets `registeredTools` pass it through without any per-tool change to the phase-building code. Each tool's `Options` also carries it for the pipeline's invalidation calls, which happen outside the runner.

6. **Why a `replace` directive for the `codesearch` sub-module?** The existing `replace` for `agentkit-go` does not cover sub-modules. Go modules require an explicit `replace` for each module path. The `codesearch` module has its own `go.mod` (like `difftest/`), so it needs its own `replace`.

7. **Why `"preflight"` as the warning stage?** The index is built during the setup phase before any model call, which is the preflight-equivalent stage. This is consistent with other setup warnings like `WarnNoVerifyCommand` and `WarnDraftPackage`.

8. **Why not a `--code-search` flag to enable/disable?** The feature is already conditional on the index being buildable. A flag would add a second condition with no clear benefit: if the index builds, the tool is useful; if it does not, the tool is absent. A future flag could be added if operators need to disable it for cost or latency reasons, but that is not needed now.

9. **Invalidation points in `impl` are conservative.** Every Go-initiated tree change gets an invalidation call, even when the next phase might not use `code_search`. A false positive (invalidating when nothing changed) is a no-op; a false negative (not invalidating when something changed) returns stale results. Conservative is correct.

10. **`code_search` is not in `MutatingTools`.** It reads the index; it does not write to the workspace. `AssertReadOnly` is unaffected.

## Dependencies

| Spec | Reason |
|---|---|
| `13_tool_call_counts_and_relevant_files` | The navigation baseline tables from spec 13 are the go/no-go input for this spec. The tool-call counters measure `code_search`'s effect on `search_files` calls. |
| `14_repo_map` | The repository map reduces navigation calls. This spec ships only if `search_files` is still a large share after the map, so the map's effect is a prerequisite measurement. |
| `15_symbol_navigation_tools` | The symbol tools reduce navigation calls. This spec ships only if `search_files` is still a large share after the symbol tools, so their effect is a prerequisite measurement. The six read tools that spec 15 establishes are what `code_search` joins. |

## Verified External API

| Symbol | Signature | Source |
|---|---|---|
| `tools.All` | `func All(opts Options) ([]core.Tool, error)` | Called at `internal/agentrun/phase.go` L447. Verified from call site. |
| `tools.Options` | `struct { Workspace *Workspace; ... }` | Used at `internal/agentrun/phase.go` L447. The `Index` field is referenced in ADR 07 and the input PRD as part of AgentKit's `03_indexed_code_search` spec. **Unverified** — the `Index` field's type is inferred from ADR 07: `tools.Index`, an interface the root module defines. |
| `tools.Index` | An interface defined in AgentKit's root module, with at least `Invalidate(path string) error` and `Close() error`. | Referenced in ADR 07 and the input PRD. **Unverified** — not yet implemented; signature inferred from the PRD's description of `Index.Invalidate("")` and `Close`. |
| `codesearch.New` | `func New(ctx context.Context, ws *tools.Workspace) (tools.Index, error)` | Referenced in the input PRD as part of AgentKit's `03_indexed_code_search` spec. **Unverified** — not yet implemented; signature inferred from the PRD's usage pattern. |
| `codesearch.ErrUnsupported` | `var ErrUnsupported error` | Referenced in the input PRD. **Unverified** — not yet implemented; inferred from the PRD's error-handling description. |
| `tools.Workspace` | `struct` with `Root string` and `func (w *Workspace) Resolve(path string) (string, error)` | Used throughout: `internal/agentrun/phase.go` L447, `issuetriage/triage.go` L155. **Unverified** — signature inferred from call sites. |
| `agentrun.Config` | `struct` in `internal/agentrun/phase.go` L128–162. Verified. |
| `agentrun.ReadOnlyFileTools` | `var ReadOnlyFileTools = []string{...}` in `internal/agentrun/policy.go` L33. Verified. |
