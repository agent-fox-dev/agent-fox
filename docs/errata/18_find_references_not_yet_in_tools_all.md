# find_references not yet in tools.All

**Date:** 2026-10-07
**Spec:** 18_find_references_wiring

## Situation

`find_references` is added to `agentrun.ReadOnlyFileTools`
(`internal/agentrun/policy.go:37`) so that every phase of every tool will
register it once AgentKit ships the tool. However, the local `agentkit-go`
checkout does not yet include a `find_references` tool — `tools.All` does not
return it. `SelectTools` silently omits names not in `tools.All`'s output, so
`find_references` does not appear in the model's declared tools today.

## Effect on tests

Tests TS-18-3, TS-18-4, TS-18-17, TS-18-18, TS-18-19, TS-18-20 and TS-18-22
assert that every `ReadOnlyFileTools` entry returned by `tools.All` reaches the
wire (using `t.Errorf`), and that `ReadOnlyFileTools` itself contains
`find_references`. Because `tools.All` does not return `find_references`, the
wire assertion for that specific tool is skipped with a `t.Logf`. The tests
will automatically strengthen to a full wire assertion once AgentKit ships the
tool — no agent-fox code change will be needed.

## Effect on docs

`docs/model-usage.md` describes `find_references` as "AgentKit's too, available
when AgentKit ships it" (line ~189) to avoid implying the tool is already
shipped.

## Delivered behaviour

```go
// internal/agentrun/policy.go:37
var ReadOnlyFileTools = []string{
    "read_file", "list_files", "find_files", "search_files",
    "file_outline", "find_symbol", "find_references",
}
```

The tool name is wired in. Every phase that builds its `BuiltinTools` from
`ReadOnlyFileTools` will register `find_references` the moment `tools.All`
returns it, with no further agent-fox change.

## Test of delivered behaviour

`TestTS15_1_ReadOnlyFileToolsHasTheSevenReadTools` in
`internal/agentrun/policy_test.go` asserts the seven-element slice in order.
`TestTS18_3_ReadOnlyPhaseResolvesReadOnlyFileTools` asserts every available
tool reaches the wire and that `ReadOnlyFileTools` contains `find_references`.

## Requirements this leaves unmet

- **18-REQ-1.3** says the resolved tool set of every phase includes
  `find_references`. It does not, and cannot from agent-fox alone:
  `SelectTools` (`internal/agentrun/policy.go`) keeps only names `tools.All`
  returns. 18-REQ-1.1, 1.2 and 1.4 are met.
- **18-REQ-5** says `toolsNote` and AgentKit's guideline rendering carry the
  new tool into every phase's system prompt. The mechanism does (TS-18-8 and
  TS-18-9 prove it with a hand-built tool set), but with no `find_references`
  in the registered set there is nothing for it to carry.

Both close with no agent-fox change once AgentKit's `tools.All` returns the
tool.

## The prompts name a tool the model does not have

`fix`'s analysis prompt, `impl`'s survey prompt and the repository map's
opening sentence tell the model to use `find_references` (18-REQ-3, 18-REQ-4).
The same system prompt ends with `toolsNote`'s "Your tools are exactly: … Calling
any other tool is an error.", which does not list it. Until AgentKit ships the
tool, a model that follows the prompt makes one failed call and falls back to
`search_files`. Spec 18 should not reach `main` before AgentKit ships
`find_references`.

## The smoke tests TS-18-22, TS-18-23 and TS-18-24

Each test's contract includes "the model's declared tools include
`find_references`". That assertion is skipped while `tools.All` lacks the tool,
for the reason above, and starts to hold automatically when it does not.

TS-18-23 and TS-18-24 also say `AssertReadOnly` passes for the resolved tool
set. It cannot: `fix`'s `analyse` and `impl`'s `survey` carry `execute`, which
is in `MutatingTools`, held to a read-only program allowlist by the guard.
Production asserts the invariant only for phases with no shell
(`internal/agentrun/phase.go`). The tests call the real `AssertReadOnly` on
everything the model was given except that guarded `execute`, assert
`execute` is present, and assert a numbered step of the system prompt sends
the model to `find_references` for callers.

## The docs

`docs/cli.md` and `docs/model-usage.md` say "the seven read tools", as 18-REQ-6
and 18-REQ-7 ask, and every paragraph that names `find_references` says the
model gets it only once AgentKit ships it.
`TestFindReferencesDocsMatchWhatTheModelSees` in
`docs_find_references_test.go` holds that paragraph by paragraph against
`tools.All`, and fails in the other direction once AgentKit ships the tool, so
the caveat cannot outlive the gap.

## A test file outside the spec's tasks

`codefix/pipeline_test.go` is not among the files spec 18's tasks list, but
`TestTS16_3_IndexReachesTheAnalysePhase` there iterates `ReadOnlyFileTools`
against the analyse phase's wire and fails once `find_references` is in the
slice and not in `tools.All`. The change skips names `tools.All` does not
return, the same adjustment every other wire test in the spec received; without
it `make test` fails. It belongs to this change, not a pull request of its own.
