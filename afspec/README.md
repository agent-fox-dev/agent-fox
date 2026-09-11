# afspec

Go library for the agent-fox specification format, version 2. Load, validate,
mutate, save and render specs with byte-for-byte round-trip fidelity.

The format itself is specified in the
[`spec`](https://github.com/agent-fox-dev/spec) repository
(`specification/spec-format-v2.md`). The JSON Schemas this library compiles and
embeds are copies of that repository's, kept in `schemas/`.

## Installation

```bash
go get github.com/agent-fox-dev/agentfox
```

## Quick start

```go
package main

import (
    "fmt"
    "log"

    "github.com/agent-fox-dev/agentfox/afspec"
)

func main() {
    spec, err := afspec.LoadSpec(".specs/01_foundation")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(spec.Title, spec.Status)

    // Schema validation plus the cross-file rules C1 to C11.
    result := spec.Validate()
    if !result.Valid {
        for _, e := range result.Errors {
            fmt.Printf("[%s] %s: %s\n", e.Check, e.Artifact, e.Message)
        }
    }

    fmt.Println(spec.RenderCombined())
}
```

## What version 2 changes

| Area | Version 1.3 | Version 2 |
|---|---|---|
| Criteria | `acceptance_criteria` + `edge_cases`, five pattern-specific fields | one `criteria` list; `pattern`, `condition`, `guard`, `action`, `contract` |
| Properties, error handling | separate sections with their own IDs | folded into criteria (`ubiquitous` + a `property` test; `unwanted` + a `contract`) |
| Tests | four arrays, four ID families, a stored `coverage` object | one `tests` list with `kind` and `verifies`; coverage computed |
| Tasks | groups → subtasks → verification, a stored `traceability` array | a flat `tasks` list; traceability computed |
| Completeness | references must resolve | every test owned by a task, every criterion owned by an implement task, the final integration task owns every smoke test |
| ID formats | thirteen | five |

## Key types

| Type | Description |
|------|-------------|
| `Spec` | PRD frontmatter fields, PRD body and the three JSON artifacts |
| `RequirementsV2Json` | Requirements artifact (generated from the JSON Schema) |
| `TestSpecV2Json` | Test specification artifact (generated from the JSON Schema) |
| `TasksV2Json` | Tasks artifact (generated from the JSON Schema) |
| `ValidationResult` | `Valid`, `Errors`, `Warnings` |
| `ValidationEntry` | One error or warning: category, check, artifact, message, entity |
| `SpecMeta` | Lightweight metadata for discovery |
| `DependencyGraph`, `DependencyEdge` | Inter-spec dependency graph from `tasks.json` |
| `CoverageReport` | Derived coverage: `Covered` and `Uncovered` ID lists |
| `TraceabilityMatrix`, `TraceLink` | Derived criterion → tests → tasks matrix |
| `PartialSpec` | The artifacts a generation run has produced so far |
| `BootstrapSpec` | Incremental builder with deferred validation via `Finalize()` |

## API overview

### I/O

| Function | Description |
|----------|-------------|
| `LoadSpec(dir) (*Spec, error)` | Load all artifacts from a spec directory |
| `(*Spec).Save(dir) error` | Atomic save with lifecycle guards |
| `(*Spec).IsScaffold() bool` | Report whether the spec has no requirements, tests or tasks yet |
| `CreateSpec(specID, specName) *Spec` | Create a draft scaffold |
| `MarshalJSON(v) ([]byte, error)` | Deterministic serialization in schema field order |

### Validation

| Function | Description |
|----------|-------------|
| `(*Spec).Validate() ValidationResult` | Schema validation, then the cross-file rules |
| `(*Spec).ValidateSchema() ValidationResult` | The four bundled JSON Schemas |
| `(*Spec).ValidateCrossFile() ValidationResult` | Rules C1 to C11 plus the never-blocking warnings |
| `ValidateCrossSpec(specs, graph) ValidationResult` | Dependencies exist, the graph is acyclic, actors are shared, glossaries agree (warning) |
| `(*Spec).ValidateStructured() map[string]any` | Structured output for CLI consumption |
| `ValidateArtifactSchema(artifact, schema, name)` | Validate one decoded artifact |

The cross-file rules, all errors:

| Rule | What it requires |
|---|---|
| C1 | `spec_id` and `spec_name` agree across the PRD, the three artifacts and the folder name |
| C2 | Every ID matches its format, carries this spec's prefix, and is unique |
| C3 | Every `test.verifies` entry resolves to a criterion or a path |
| C4 | Every criterion is verified by at least one test |
| C5 | Every path is verified by a smoke test, and every smoke test verifies a path |
| C6 | Every `task.criteria`, `task.tests` and `depends_on` entry resolves; `depends_on` forms a DAG over lower IDs |
| C7 | Every test is owned by at least one task |
| C8 | Every criterion is owned by at least one `implement` task |
| C9 | Exactly one `integration` task, last, owning every smoke test |
| C10 | Every `unwanted` criterion has a contract |
| C11 | `real_components` is present exactly when `kind` is `smoke` |

### Generation

| Function | Description |
|----------|-------------|
| `GenerationSteps` | The mandatory order: requirements, test_spec, tasks |
| `DecodeArtifact(step, content)` | Decode a model's tool input into the typed artifact |
| `ValidateGenerationStep(step, partial)` | The artifact's schema plus every rule decidable at that step |
| `FormatValidationEntries(entries) string` | Render violations as repair feedback |

### Lifecycle

| Function | Description |
|----------|-------------|
| `(*Spec).Transition(target, dir) (*Spec, error)` | Transition status, persist, return a new copy |
| `(*Spec).Supersede(specID, dir) (*Spec, error)` | Mark a sealed spec superseded with a deprecation banner |
| `ValidTransition(current, target) bool` | Whether a status transition is allowed |
| `MoveToArchive(specDir, root) error` | Transition to archived and move under `archive/` |
| `ComputeIntentHash(body) (string, error)` | SHA-256 of the `## Intent` section |

### Discovery

| Function | Description |
|----------|-------------|
| `DiscoverSpecs(root) ([]SpecMeta, error)` | Scan a root for spec directories |
| `BuildDependencyGraph(metas, root) (*DependencyGraph, error)` | Build the graph from each `tasks.json` |
| `IsSpecDirName(name) bool` / `ParseSpecDirName(name)` | The `{NN}_{snake_case_name}` pattern |
| `LoadSpecLandscape(root, includeArchive, currentSpecID)` | Metadata for landscape views |

### Rendering

| Function | Description |
|----------|-------------|
| `(*Spec).RenderCombined(opts…) string` | One concatenated Markdown document |
| `(*Spec).RenderIndividual(opts…) map[string]string` | Per-artifact Markdown |
| `(*Spec).RenderIndividualScoped(taskID, opts…)` | Scoped to one task; what a coder receives |
| `WithMaxTokens(n) RenderOption` | Cap the estimated size, truncating progressively |
| `(Criterion).RenderEARSSentence() string` | The EARS sentence |
| `(Criterion).RenderEARS() string` | The sentence plus `→ contract` when there is one |

### Derivation

| Function | Description |
|----------|-------------|
| `(*TestSpecV2Json).ComputeCoverage(req) CoverageReport` | Which criteria and paths are verified |
| `(*Spec).ComputeTraceability() TraceabilityMatrix` | criterion → tests → tasks |

Neither is stored: format v2 §7.1 and §8.5 make both derived.

### Criterion builders

| Function | Sentence |
|----------|----------|
| `UbiquitousCriterion(id, system, action)` | THE system SHALL action |
| `EventDrivenCriterion(id, condition, system, action)` | WHEN condition, THE system SHALL action |
| `ComplexEventCriterion(id, condition, guard, system, action)` | WHEN condition AND guard, THE system SHALL action |
| `StateDrivenCriterion(id, condition, system, action)` | WHILE condition, THE system SHALL action |
| `UnwantedCriterion(id, condition, system, action, contract)` | IF condition, THEN THE system SHALL action |
| `OptionalCriterion(id, condition, system, action)` | WHERE condition, THE system SHALL action |

### Tasks

| Function | Description |
|----------|-------------|
| `(*TasksV2Json).TransitionTask(id, target) (*TasksV2Json, error)` | Move one task through the state machine of §8.3.1 |
| `(*TasksV2Json).CompleteTaskStates(ids)` / `ResetTaskStates(ids)` | Set states directly, bypassing the machine |
| `(*TasksV2Json).GetTask(id) (*Task, bool)` | A copy of one task |
| `ValidTaskTransition(current, target) bool` | Whether a task transition is allowed |

### Lint

| Function | Description |
|----------|-------------|
| `RunLintSpecs(specsDir, lintAll) (LintResult, error)` | Validate every spec under a root and collect the findings; specs whose tasks are all done are skipped unless `lintAll` |
| `DiscoverLintSpecs(specsDir, filterSpec)` | The specs a lint run would look at |
| `SortFindings(findings)` / `ComputeExitCode(findings)` | Order findings by severity, and the exit status they imply |

### Migration

| Function | Description |
|----------|-------------|
| `Migrate(src, specID, specName)` | Convert version 1.3 artifacts to version 2, with a report |

The `afspec/legacy` sub-package holds the version 1.3 types and a read-only
loader (`legacy.LoadSpec`). Nothing else in the module depends on them; they
exist so that an embedder can read a spec written under the old format and
hand it to `Migrate`. No tool in this repository exposes the migration — see
[ADR 03](../docs/adr/03-rebuild-the-skills-as-tools.md) on why lifecycle and
migration stay library API.

### Error types

| Type | When returned |
|------|---------------|
| `SpecError` | Base error; every afspec error unwraps to it via `errors.As` |
| `LoadError` | Missing or malformed spec files (carries `File`) |
| `SaveError` | Disk write failures |
| `LifecycleError` | Invalid spec or task state transitions |
| `IntentError` | Missing `## Intent` section, or intent drift on an active spec |
| `BootstrapError` | `BootstrapSpec.Finalize` failures |

## Concurrency

This package provides no goroutine-safety guarantees. `Spec` instances are not
safe for concurrent use. Synchronize externally, or confine each `Spec` to one
goroutine.

## Code generation

`RequirementsV2Json`, `TestSpecV2Json`, `TasksV2Json` and
`PrdFrontmatterV2Json` are generated from the schemas in `schemas/` with
[go-jsonschema](https://github.com/atombender/go-jsonschema):

```bash
make json-gen
```

They are generated with `--only-models`. Structural checking belongs to
`Validate`, which runs the compiled schemas and reports violations with the
rule that failed; generated `UnmarshalJSON` methods would duplicate that and
would also reject the empty scaffold `CreateSpec` writes, which `Save` and
`LoadSpec` have to round-trip before the artifacts are generated.
