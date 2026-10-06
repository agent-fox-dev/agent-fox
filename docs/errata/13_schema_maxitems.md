# Erratum: spec 13 names a `MaxItemsN` method that `agentkit-go` does not have

Recorded because `.specs/13_tool_call_counts_and_relevant_files` reads as
unmet in 13-REQ-4.2 ([#164](https://github.com/agent-fox-dev/agent-fox/issues/164)).
The requirement is met. What differs is the call the spec says to make.

## 13-REQ-4.2: the limit is set on the struct field, not by a chained call

**Spec:** `schema.Array(...).MaxItemsN(30)`, with `MaxItemsN` listed under the
spec's external APIs as `func (s *Schema) MaxItemsN(n int) *Schema`. The same
entry marks it unverified: it was inferred from the `MinItemsN` call in
`codefix/phases.go`.

**Is:** `agentkit-go/schema` provides `MinItemsN` and no `MaxItemsN`. The
`Schema` struct has a public `MaxItems *int` field, which the JSON rendering
emits as `maxItems` (`schema/impl.go`, next to `minItems`). So
`relevantFilesArraySchema` (`specgen/prd.go`) builds the array and sets the
field:

```go
maxItems := 30
s := schema.Array(...)
s.MaxItems = &maxItems
```

The serialized `submit_prd` schema carries `"maxItems":30` on the
`relevant_files` property, so the model sees the limit before it calls, which
is what the criterion asks for. `TestTS_13_15_SchemaMaxItems30`
(`specgen/prd_relevantfiles_test.go`) reads that property out of the rendered
schema and fails when the limit is missing or is not 30.

`MaxItemsN` is also named in the spec's `prd.md` (external APIs table) and in
the step for the `relevant_files` task in `tasks.json`. The spec files are left
as written; this erratum is the correction.
