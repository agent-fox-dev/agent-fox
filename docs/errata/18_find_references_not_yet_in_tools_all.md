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
