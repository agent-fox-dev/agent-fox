# 07. Split code navigation between AgentKit and agent-fox

**Status:** proposed
**Date:** 2026-10-01
**Supersedes:** nothing
**Related:** [PRD 06](../prds/06-stop-re-reading-the-codebase-every-phase.md),
AgentKit PRDs [04](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/04-add-symbol-navigation-tools.md)
and [05](https://github.com/agent-fox-dev/agentkit-go/blob/main/docs/prd/05-add-an-indexed-code-search-module.md)

## Context

A `spec` run under `--verbose` is mostly `list_files`, `find_files`,
`search_files` and `read_file`. Each of its four or five phases is a fresh
agent with the four read tools and nothing that knows the repository's
shape, so each phase walks the tree again. Three proposals address it:

| Item | What | PRD |
|---|---|---|
| A | Count tool calls per phase in the envelope | agent-fox PRD 06 |
| B | A repository map, computed once per run, in every phase's prompt | agent-fox PRD 06 |
| C | The PRD phase hands its relevant files to the phases after it | agent-fox PRD 06 |
| D | `file_outline` and `find_symbol` tools | AgentKit PRD 04 |
| E | An indexed, ranked `code_search` tool backed by zoekt | AgentKit PRD 05 |

The two repositories already divide work in a consistent way.
AgentKit owns the built-in tools (`tools.All`), the workspace confinement
(`tools.Workspace`), the ignore engine (unexported, in `tools/ignore.go`)
and the ripgrep-with-native-fallback pattern in `search_files`. Its root
module requires nothing outside the standard library, and
`internal/policy/deps_test.go` enforces that, cgo-freedom included.
agent-fox decides which tools a phase gets (`agentrun.ReadOnlyFileTools`),
writes every prompt, and owns the envelope.

Each item could be put on either side. The cost of the wrong side is
specific:

- In agent-fox, a repository map or an outline needs an ignore-aware walk.
  The only one is unexported in AgentKit, so agent-fox would write a second
  `.gitignore` engine. Two engines that disagree would show the model a file
  in the map that `search_files` then says does not exist.
- In AgentKit, anything that names a phase, a spec artifact or the envelope
  pulls agent-fox's vocabulary into a general SDK, and AgentKit's other
  embedders (`examples/cleaner`, `examples/issued`) would pay for it.
- In AgentKit's root module, zoekt would break REQ-GO-11 for every
  embedder, including those that never search.

## Decision

**Mechanism goes in AgentKit. Policy, prompts and the envelope stay in
agent-fox.** Concretely:

| Item | AgentKit | agent-fox |
|---|---|---|
| A, tool-call counts | nothing | `agentrun.Result.ToolCalls`, `toolio.PhaseInfo.tool_calls` |
| B, repository map | an exported ignore-aware walk and the `outline` package, which turns a file into its declarations (PRD 04 §3) | which phases get a map, its token budget, ranking and truncation, the prompt block |
| C, relevant files | nothing | the `relevant_files` field of `submit_prd`, its validation, the prompt block in later phases |
| D, symbol tools | `file_outline` and `find_symbol` in `tools`, standard library only, with universal-ctags as an optional accelerator (the `rg` pattern) | two names added to `ReadOnlyFileTools` |
| E, indexed search | a **separate Go module**, `github.com/agentfox/agentkit-go/codesearch`, with its own `go.mod` as `difftest/` has. It plugs into `tools.Options` through an interface the root defines. | opts in by importing the module and adding `code_search` to read-only phases, only if A's numbers justify it (PRD 05 §2) |

The import direction follows ADR 01 in AgentKit. `outline` sits below the
root and imports only the standard library and `core`. `tools` imports
`outline`. `codesearch` imports `tools` and `outline`, and nothing in the
root module imports `codesearch`.

### Order

1. **agent-fox: A and C.** They need nothing from AgentKit. A ships first
   because it produces the baseline every later step is judged against.
2. **AgentKit: PRD 04 part 1.** The exported walk and the `outline`
   package, with no new tools yet.
3. **agent-fox: B.** The repository map, built from step 2.
4. **AgentKit: PRD 04 part 2.** The `file_outline` and `find_symbol` tools.
   agent-fox adds them to `ReadOnlyFileTools`.
5. **AgentKit: PRD 05.** Only if the go/no-go in its §2 passes on A's
   numbers.

Each step leaves `make check` green in both repositories on its own.
agent-fox builds against AgentKit through `replace => ../agentkit-go`, so
step 3 needs step 2 merged in AgentKit first.

## Consequences

- One ignore engine and one path resolver answer every navigation question.
  A path in the map, an outline result and a `search_files` hit agree by
  construction.
- AgentKit's other embedders get the outline and symbol tools for free and
  pay nothing for zoekt unless they import `codesearch`.
- agent-fox's go.mod gains zoekt's dependency graph if step 5 happens. That
  is acceptable: agent-fox has no standard-library-only rule, and the
  dependency only arrives when the measurements call for it.
- Changes to the prompt wording stay changes to one repository. The repository map's
  rendering is agent-fox's, so tuning it never needs an AgentKit release.
- Two repositories must move in order. The cost is one coordination step
  (2 before 3), which the order above makes explicit.

## Alternatives considered

- **Everything in agent-fox.** Fastest to start. It duplicates the ignore
  engine and keeps the tools from every other AgentKit embedder. Rejected
  for the duplication alone.
- **Everything in AgentKit, including the repository map's prompt and the
  PRD hand-off.** The hand-off is a field on `submit_prd`, an agent-fox
  tool. The map's budget and placement are prompt policy, and AgentKit has
  no notion of phases. Rejected.
- **zoekt inside `tools`.** Breaks REQ-GO-11 and adds zoekt's dependency
  graph to every embedder. `difftest/` already established the separate-module answer
  to the same problem. Rejected.
