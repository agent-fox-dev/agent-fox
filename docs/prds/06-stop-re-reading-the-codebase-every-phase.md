# Stop re-reading the codebase every phase

## Intent

Every agent-fox tool reads the codebase, and every phase reads it from
scratch. A `spec` run under `--verbose` makes this easy to see: it is mostly
`list_files`, `find_files`, `search_files`, `read_file`, and then the same
again. `spec` is not the exception. The other tools have the same shape:

| Tool | Phases that read the codebase | How many per run |
|---|---|---|
| `issue` | `triage` | 1 |
| `fix` | `analyse`, `implement` | 2 |
| `spec` | `prd`, three `generate:*`, optionally `architecture` | 4–5 per scope |
| `impl` | `survey`, `implement` once per task, `repair` when needed | 2 + one per task |

[`model-usage.md`](../model-usage.md) records why each phase is a fresh
agent: a shared transcript would carry one phase's reasoning into the next.
Each phase receives its input and the prior phases' conclusions (the PRD and
prior artifacts in `spec`, the analysis in `fix`, the survey in `impl`), but
nothing about the **repository's shape**. And every phase, in every tool, gets
the same four read tools (`agentrun.ReadOnlyFileTools`), none of which can say
what a file declares or where a name is declared. So every phase starts from
`list_files .` and walks the tree again, and every turn it spends on that comes
out of the same turn and cost budget as the work. `impl` pays the most: a
spec with twelve tasks runs at least fourteen phases.

This PRD covers five changes. Three are inside agent-fox; two consume
navigation that AgentKit is adding. **All five apply to `issue`, `fix`,
`spec` and `impl`**, except where a section says otherwise and why:

- **A. Measure it.** Count every tool call, per phase, in the envelope. Today
  the counts exist only as `tool_call` events under `--verbose`. Applies to
  every phase of every tool.
- **B. Give each phase a map.** Go computes a repository map: the tracked
  tree plus the top-level declarations of each file, bounded to a token
  budget. Every phase of every tool receives it in its prompt.
- **C. Hand the PRD phase's findings forward.** `submit_prd` gains a
  `relevant_files` field. Go checks every path, and the phases after it
  start from that list. This one is `spec` only, because `submit_prd` is a
  `spec` tool; §4 says what the other tools already hand forward.
- **D. Symbol navigation tools.** `file_outline` and `find_symbol` join
  `ReadOnlyFileTools`, so every phase of every tool can call them.
- **E. Indexed search.** When adopted, one `code_search` index is built per
  run and shared by every phase of every tool.

Where each piece is implemented, here or in AgentKit, is decided in
[ADR 07](../adr/07-split-code-navigation-between-agentkit-and-agent-fox.md).
In short: A and C are agent-fox only. B's ignore-aware walk and per-file
outline, D's tools and E's index come from AgentKit. The map's selection,
budget, rendering and placement in the prompt, and which phases get which
tool, are agent-fox's.

The AgentKit side is specified in three specs in `agentkit-go`:

| AgentKit spec | Source PRD | Delivers | Used here by |
|---|---|---|---|
| `01_outline_and_walk` | PRD 04, part 1 | the `outline` package, `tools.Walk`, `tools.CtagsRunner` | B |
| `02_symbol_navigation_tools` | PRD 04, part 2 | `file_outline`, `find_symbol`, the symbol table and its freshness | D |
| `03_indexed_code_search` | PRD 05 | the `codesearch` module, `code_search`, the `tools.Index` seam | E |

None of the three is specific to `spec`. They are general tools on the
workspace, and agent-fox wires them into the phases of all four tools.

## Goals

- Every run of every tool reports, per phase, how many calls it made to each
  tool and how many bytes those calls returned. A change to navigation can
  then be judged by a number, per tool.
- No phase, in any tool, needs `list_files` to learn the repository's layout
  or where a top-level declaration lives.
- Every phase, in any tool, can ask "what does this file declare" and "where
  is this name declared" in one call instead of a `search_files` and a
  `read_file`.
- A `spec` phase after `prd` starts from the files the PRD phase found
  relevant instead of rediscovering them.
- On a fixed set of inputs per tool, navigation calls (`list_files`,
  `find_files`, `search_files`) fall by at least 40% in each of `issue`,
  `fix`, `spec` and `impl`, with no regression in that tool's own outcome
  (validation and coverage for `spec`, the gate verdicts for `fix` and
  `impl`, the filed issue's citations for `issue`). The 40% is an estimate
  to check against A's baseline, not a promise.

## Non-goals

- Sharing transcripts between phases. That design decision stands.
- Writing navigation tools in agent-fox. `file_outline` and `find_symbol`
  are AgentKit's `02_symbol_navigation_tools`, indexed search is its
  `03_indexed_code_search`. agent-fox selects them and wires them up.
- Different navigation per tool. A phase's navigation tools do not depend on
  which tool runs it; what varies is the rest of the grant (`execute`, the
  write tools), which this PRD does not change.
- Changing the spec format. `relevant_files` lives in the run and the
  envelope, never in a spec package's files, because the format belongs to the
  [`spec`](https://github.com/agent-fox-dev/spec) repository.
- A `relevant_files` hand-off in `issue`, `fix` or `impl` (§4).
- Ranking by references or call graphs (Aider's PageRank). The map ranks by
  cheap structural signals (§2). A reference graph can come later if A's
  numbers show the map is chosen badly.
- Caching across phases. Each phase declares its own terminating tool, so the
  tool block, and therefore the cached prefix, differs per phase. The map is
  cached *within* a phase like the rest of its prompt.

## Functional requirements

### 1. A: tool calls in `usage.phases[]`, for every tool

- `agentrun.Result` gains `ToolCalls map[string]int` (calls per tool name,
  blocked calls included) and `ToolResultBytes map[string]int64` (bytes of
  the model-facing text returned per tool). They are counted in the same
  place as `ToolErrors` and `Blocked` today, whether or not `--verbose` is
  set. Because every phase of every tool runs through `agentrun.Runner`,
  `issue`, `fix`, `spec` and `impl` all report them with no per-tool code.
- `toolio.PhaseInfo` gains `tool_calls` and `tool_result_bytes`, both
  `omitempty` and both classified `fact`. The addition is additive under
  [ADR 06](../adr/06-version-the-envelope-interface.md): a minor bump of
  `schema_version` and a changelog line in `docs/cli.md`.
- The `phase_end` event gains `tool_calls`, so a caller watching the stream
  sees the counts without waiting for the envelope.
- `docs/development.md` gains a **navigation baseline** procedure covering
  all four tools, run against this repository (for `fix` and `impl`, in a
  throwaway clone) with `--dry-run`:
  - `issue`: two fixed inputs (a stack trace, a prose report).
  - `fix`: two fixed inputs naming a seeded defect.
  - `spec`: three fixed inputs.
  - `impl`: one small fixed spec package of at least three tasks.

  Their per-phase `tool_calls` are recorded in one table per tool in the same
  document. The tables are regenerated by hand whenever B, C, D or E changes,
  so the effect of each change is visible in review, per tool. No live-model
  run enters `make test`.

### 2. B: the repository map, in every tool

**What it is built from.** The pipeline builds the map from the workspace
through AgentKit's `tools.Walk` and its `outline` package
(`01_outline_and_walk`). It uses the same ignore rules, hidden-entry rule and
workspace confinement as `search_files`, so a path in the map is a path the
tools accept.

**When it is built.** The map describes the tree as it is when a phase starts.

- `issue` and `spec` never change the tree, so they build it once per run,
  before the first phase. `spec` reuses it for every scope of a split.
- `fix` and `impl` change the tree: their writing phases edit files, and Go
  itself checks out a branch, runs the gate, commits and, on a parked task,
  resets. They build the map before their first phase and rebuild it before
  each later phase if the tree changed since the last build (a different
  `HEAD`, or a different set or content of dirty files). An `impl`
  implementation phase for task N therefore sees the declarations task N−1
  added.

**Contents, in order of priority:**

1. The directory tree of every non-ignored file.
2. For each source file: its top-level declarations, as the outline gives
   them (kind, name, line). Exported or public declarations come first, and
   unexported ones are included only while the budget allows.
3. Each directory's file count, for directories collapsed under budget
   pressure.

**Budget.** The default is 6 000 tokens, measured with
`afspec.EstimateTokens`. It can be set with `--repo-map-tokens <n>`, a
shared flag accepted by `issue`, `fix`, `spec` and `impl`, and `0` disables
the map. When the full map exceeds the budget, it is reduced in this order
until it fits, and the reduction is deterministic:

1. Drop unexported declarations.
2. Drop declarations from test files (the profile's test-file convention,
   e.g. `_test.go`, `test_*.py`).
3. Drop declarations from the deepest directories first.
4. Collapse the deepest directories to `dir/ (N files)`.

Directories named in the input, or containing a file named in it, are
reduced last. That is the one input-dependent signal, and it is
deterministic for a given input. What counts as "the input" is each tool's
own: the report for `issue`, the problem statement and the analysis's files
for `fix`, the idea for `spec`, and the spec package plus the current task's
files for `impl`.

**Rendering.** The map is a fenced block under `## Repository map` in the
**user** prompt of every phase. It sits after the landscape and steering
blocks, where a tool has them, and before the prior phase's conclusions
(prior artifacts, analysis, survey). It opens with one sentence: the map
lists declarations with line numbers; use `read_file` with `offset`/`limit`
to read one, and `file_outline` for a file's full outline; the map may be
reduced (and how), so use `find_symbol`, `find_files` and `search_files` for
anything it does not show. A line reads:

```
internal/agentrun/
  phase.go        type Phase L148 · type Result L190 · type Runner L215 · func (*Runner) Run L240
  policy.go       var ReadOnlyFileTools L32 · func AssertReadOnly L71 · func SelectTools L99
```

**Determinism.** Same tree, same input and same budget give a
byte-identical map. Files are sorted by path and declarations by line, with
no timestamps or sizes. A test pins this.

**Who gets it.** `agentrun.Phase` gains `RepoMap string`. Every phase of
`issue`, `fix`, `spec` and `impl` sets it, writing phases included.

**Untrusted text.** Declaration names are repository text, the same as
everything `read_file` returns. The map adds no new exposure, but its opening
sentence says that it is data derived from the repository, not instructions.
It never appears in the envelope, so
[spec 10](../../.specs/10_untrusted_text_labels/prd.md)'s labels do not
apply to it.

### 3. C: relevant files, handed forward (`spec` only)

- `submit_prd` gains an optional `relevant_files` array of
  `{ "path": string, "why": string }`, at most 30 entries, described to the
  model as *the files a later phase should read first to write requirements,
  tests and tasks for this PRD*.
- Every path is resolved with `tools.Workspace.Resolve`. It must exist, be a
  regular file, and not be ignored. A path that fails any check rejects the
  submission with a tool error naming each bad path. This is the same
  contract as `issue`'s citation check and the other `submit_prd`
  rejections in `model-usage.md`: a bad path costs one turn now rather than a
  wrong task later.
- Each generation phase and `architecture` receives a block,
  `## Files the PRD phase found relevant`, listing path and reason. It
  follows the repository map. `why` is model text, and the block introduces
  it as the previous phase's notes, not as instructions. Generation phases do not get a field to extend the list: their
  terminating tools' schemas are the spec format's, and stay so.
- On a split, each scope's PRD phase produces its own list, and only that
  scope's later phases receive it.
- The envelope's `result` gains `relevant_files` per written spec, classified
  `model`. A caller, or a later `impl` run, can use it. Nothing persists it
  inside the spec package.
- When a re-run continues from an existing PRD and the PRD phase does not
  run, there is no list. The later phases run with the map alone, and a
  `low` warning says so.

### 4. Why C stops at `spec`

The other tools already hand their first phase's findings forward, through
their own terminating tools: `fix`'s `implement` phase receives
`submit_analysis`'s result, and `impl`'s implementation phases receive
`submit_survey`'s. `issue` has one phase. Adding a second, parallel list to
those schemas would duplicate what they carry. If A's numbers show that
`fix` or `impl` re-discover files their analysis or survey already named,
the fix is in those tools' own schemas, in a later PRD.

### 5. D: `file_outline` and `find_symbol` in every tool

- `agentrun.ReadOnlyFileTools` becomes `read_file`, `list_files`,
  `find_files`, `search_files`, `file_outline`, `find_symbol`. Every phase
  builds its grant from that list, so the two tools reach `issue`'s
  `triage`, `fix`'s `analyse` and `implement`, every `spec` phase, and
  `impl`'s `survey`, `implement` and `repair`, with no per-tool change. The
  "four read tools" become the "six read tools" throughout the docs and the
  system prompts that name them.
- Neither tool writes, so the read-only invariant (`AssertReadOnly`) and the
  restricted shell guard are unaffected. Both appear in
  `FileNavigationTools()`, so `ExecuteFallbackGuideline` stays off for every
  phase, as it is today.
- `registeredTools` calls `tools.All` once per phase, so each phase holds its
  own symbol table, built on its first `find_symbol` call and bounded by
  AgentKit's defaults (50 000 files, 2 s). A fresh table per phase is what
  keeps `fix` and `impl` correct across the changes Go makes between phases
  (checkout, reset, gate runs), which AgentKit's freshness tracking does not
  see. Within a writing phase, AgentKit refreshes the table after
  `write_file`, `edit_file` and `execute` itself.
- `tools.Options.Symbols` is left at its defaults: ctags where universal-ctags
  is installed, heuristics where it is not, as `search_files` does with `rg`.
  The backend appears in each result, and `--preflight` reports whether ctags
  was found, for all four tools.

### 6. E: one `code_search` index per run, in every tool

E is conditional: it ships only if AgentKit's `03_indexed_code_search` passes
its go/no-go and A's numbers show `search_files` is still a large share of
navigation after B and D. When it ships:

- Each run of `issue`, `fix`, `spec` or `impl` creates one
  `codesearch.Index` for the workspace before its first phase, passes it as
  `tools.Options.Index` to every phase's `tools.All`, and `Close`s it when
  the run ends, on every path including failure and cancellation. One index
  per run, not per phase, because a build can take seconds; this is what
  makes it worth having in `impl`, whose phase count grows with the task
  count.
- `code_search` is added to every phase's grant alongside the six read
  tools. It is not in `ReadOnlyFileTools` itself, because it exists only when
  the index does (see below).
- AgentKit's index sees only writes made through its own tools. Go's own
  workspace changes in `fix` and `impl` (branch checkout, reset, clean, gate
  runs, commits with hooks) are invisible to it, so the pipeline calls
  `Index.Invalidate("")` after each of them, before the next phase starts.
  `issue` and `spec` never change the tree and never invalidate.
- When `codesearch.New` returns `ErrUnsupported`, or the build fails, the run
  continues with `Options.Index` nil and no `code_search`, and a `low`
  warning says so. Navigation never fails a run.

## Acceptance criteria

- An `issue`, `fix`, `spec` and `impl` `--dry-run` envelope each carries
  `tool_calls` and `tool_result_bytes` on every phase in `usage.phases[]`.
  Their sum over `read_file` equals the number of `read_file` `tool_call`
  events emitted under `--verbose`.
- The map for a fixture tree with a `.gitignore`d directory and a hidden
  directory contains neither. Every path in it resolves through
  `tools.Workspace.Resolve`.
- The map for a fixture at a deliberately small budget is under budget and
  byte-identical across two builds. Its reductions follow the §2 order,
  checked by a golden file per budget step.
- The user prompt of every phase of `issue`, `fix`, `spec` and `impl`
  contains the `## Repository map` block. A test per tool pins this.
- In an `impl` fixture with two tasks, where task 1 adds a declaration, the
  map in task 2's implementation prompt lists it. In a `spec` fixture with a
  split, the map is built once.
- `--repo-map-tokens 0` produces user prompts byte-identical to the ones
  built without this change, for all four tools. A test pins this.
- A `submit_prd` call naming a nonexistent file is rejected with that path
  in the error, and a corrected call is accepted.
- A generation phase's user prompt contains the relevant-files block exactly
  when the PRD phase supplied one.
- Every phase of all four tools registers `file_outline` and `find_symbol`;
  the read-only phases still pass `AssertReadOnly`.
- If E ships: one index is built per run whatever the phase count, it is
  closed on success, failure and cancellation, and in an `impl` fixture a
  `code_search` after Go resets the tree does not return the discarded
  content.
- `docs/development.md`'s baseline tables show the before and after counts
  for each of A, B, C and D (and E if it ships), per tool.

## Documentation

- `docs/cli.md`: `--repo-map-tokens` under the shared flags, `tool_calls`,
  `tool_result_bytes`, `relevant_files`, the six read tools (and
  `code_search` if E ships) in each tool's *What the model may and may not
  do*, ctags in each `--preflight` section, and the `schema_version`
  changelog line.
- `docs/model-usage.md`: *Reading the codebase* describes the map (and when
  `fix` and `impl` rebuild it), the symbol tools, the index if E ships, and
  the `spec` hand-off. Every row of the phase table lists the six read tools.
- `docs/development.md`: the navigation baseline procedure and the per-tool
  tables.
