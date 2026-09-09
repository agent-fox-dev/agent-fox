# afspec

Standalone Go library for the agent-fox specification format (v1.3). Load,
validate, mutate, save, and render specs with byte-for-byte round-trip fidelity.

## Installation

```bash
go get github.com/agent-fox-dev/agentfox
```

## Quick Start

```go
package main

import (
    "fmt"
    "log"

    "github.com/agent-fox-dev/agentfox/afspec"
)

func main() {
    // Load a spec from disk
    spec, err := afspec.LoadSpec(".specs/01_foundation")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(spec.Title)
    fmt.Println(spec.Status)

    // Validate (schema + cross-file consistency)
    result := spec.Validate()
    if !result.Valid {
        for _, e := range result.Errors {
            fmt.Printf("[%s] %s: %s\n", e.Category, e.Artifact, e.Message)
        }
    }

    // Render all artifacts as a single Markdown document
    md := spec.RenderCombined()
    fmt.Println(md)
}
```

## Key Types

| Type | Description |
|------|-------------|
| `Spec` | Main type holding PRD frontmatter fields, PRD body, and JSON artifacts |
| `RequirementsV1Json` | Requirements artifact (generated from JSON Schema) |
| `TestSpecV1Json` | Test specification artifact (generated from JSON Schema) |
| `TasksV1Json` | Tasks artifact (generated from JSON Schema) |
| `ValidationResult` | Validation outcome with `Valid` flag, `Errors`, and `Warnings` |
| `ValidationEntry` | Single validation error or warning with category, artifact, path, etc. |
| `ValidationError` | Bootstrap validation error with `Rule` and `Message` |
| `SpecMeta` | Lightweight metadata (SpecID, SpecName, Status, Dir) for discovery |
| `DependencyGraph` | Inter-spec dependency graph built from tasks.json declarations |
| `DependencyEdge` | Directed dependency edge between two specs |
| `CoverageReport` | Test coverage result with `Covered` and `Uncovered` ID lists |
| `BootstrapSpec` | Incremental spec builder with deferred validation via `Finalize()` |

## API Overview

### I/O

| Function | Description |
|----------|-------------|
| `LoadSpec(dir) (*Spec, error)` | Load all artifacts from a spec directory |
| `(*Spec).Save(dir) error` | Atomic save with lifecycle guards (rejects sealed/superseded/archived) |
| `MarshalJSON(v) ([]byte, error)` | Deterministic JSON serialization with schema-ordered fields |

### Validation

| Function | Description |
|----------|-------------|
| `(*Spec).Validate() ValidationResult` | Full validation (schema + cross-file consistency) |
| `(*Spec).ValidateSchema() ValidationResult` | Schema-only validation against bundled JSON Schemas |
| `(*Spec).ValidateCrossFile() ValidationResult` | Cross-file checks: dangling refs, coverage gaps, ID formats |
| `ValidateCrossSpec(specs, graph) ValidationResult` | Cross-spec checks (glossary conflicts between interacting specs) |
| `(*Spec).ValidateStructured() map[string]any` | Structured validation output for CLI consumption |

### Lifecycle

| Function | Description |
|----------|-------------|
| `(*Spec).Transition(target, dir) (*Spec, error)` | Transition spec status, persist, return new copy |
| `(*Spec).Supersede(supersedingSpecID, dir) (*Spec, error)` | Mark sealed spec as superseded with deprecation banner |
| `ValidTransition(current, target) bool` | Check if a status transition is allowed |
| `MoveToArchive(specDir, root) error` | Transition to archived and move directory to archive/ |

### Discovery

| Function | Description |
|----------|-------------|
| `DiscoverSpecs(root) ([]SpecMeta, error)` | Scan root for spec directories, parse PRD metadata |
| `BuildDependencyGraph(metas, root) (*DependencyGraph, error)` | Build dependency graph from tasks.json declarations |
| `IsSpecDirName(name) bool` | Check if a name matches the NN_snake_case pattern |
| `ParseSpecDirName(name) (prefix, name, error)` | Parse numeric prefix and name from a spec directory name |
| `LoadSpecLandscape(root, includeArchive, currentSpecID) ([]SpecMeta, error)` | Collect metadata for landscape views |

### Rendering

| Function | Description |
|----------|-------------|
| `(*Spec).RenderCombined() string` | Single concatenated Markdown document |
| `(*Spec).RenderIndividual() map[string]string` | Per-artifact Markdown, keyed by artifact name |
| `(*Spec).RenderIndividualScoped(targetGroup) map[string]string` | Scoped to a task group's refs, others summarized |
| `(*RequirementsV1Json).Render() string` | Requirements artifact as Markdown |
| `(*TestSpecV1Json).Render() string` | Test spec artifact as Markdown |
| `(*TasksV1Json).Render() string` | Tasks artifact as Markdown |
| `(Criterion).RenderEARSSentence() string` | Render criterion as an EARS natural-language sentence |

### EARS Criterion Builders

| Function | Description |
|----------|-------------|
| `UbiquitousCriterion(id, system, action)` | "THE system SHALL action" |
| `EventDrivenCriterion(id, trigger, system, action)` | "WHEN trigger, THE system SHALL action" |
| `ComplexEventCriterion(id, trigger, condition, system, action)` | "WHEN trigger IF condition, THE system SHALL action" |
| `StateDrivenCriterion(id, state, system, action)` | "WHILE state, THE system SHALL action" |
| `UnwantedCriterion(id, errorCondition, system, action)` | "IF errorCondition, THEN THE system SHALL action" |
| `OptionalCriterion(id, feature, system, action)` | "WHERE feature, THE system SHALL action" |

### Subtask Mutation

| Function | Description |
|----------|-------------|
| `(*TasksV1Json).TransitionSubtask(subtaskID, target) (*TasksV1Json, error)` | Transition subtask state via state machine, return new copy |
| `(*TasksV1Json).CompleteSubtaskStates(groupIDs) (*TasksV1Json, error)` | Set all subtasks in groups to done (bypasses state machine) |
| `(*TasksV1Json).ResetSubtaskStates(groupIDs) (*TasksV1Json, error)` | Reset all subtasks in groups to pending (bypasses state machine) |

### Other

| Function | Description |
|----------|-------------|
| `CreateSpec(specID, specName) *Spec` | Create a new draft spec with empty artifacts |
| `NewBootstrapSpec(specID, specName) *BootstrapSpec` | Create incremental builder for deferred validation |
| `(*BootstrapSpec).Finalize() (*Spec, []ValidationError)` | Validate completeness and assemble final Spec |
| `ComputeIntentHash(body) (string, error)` | SHA-256 hash of the `## Intent` section in a PRD body |
| `(*TestSpecV1Json).ComputeCoverage(req) CoverageReport` | Compute test coverage against requirements |
| `Schemas() map[string][]byte` | Bundled JSON Schema files (embedded at compile time) |
| `(*DependencyGraph).TopologicalSort() ([]string, error)` | Topological ordering of specs (Kahn's algorithm) |
| `(*DependencyGraph).Dependencies(specID) []DependencyEdge` | Edges where specID is the dependent |
| `(*DependencyGraph).Dependents(specID) []DependencyEdge` | Edges where other specs depend on specID |

### Error Types

| Type | When returned |
|------|---------------|
| `SpecError` | Base error; all afspec errors unwrap to this via `errors.As` |
| `LoadError` | Missing or malformed spec files (includes `File` field) |
| `SaveError` | Disk write failures |
| `LifecycleError` | Invalid spec or subtask state transitions |
| `IntentError` | Missing `## Intent` section in PRD body |
| `BootstrapError` | BootstrapSpec.Finalize failures |

## Concurrency

This package provides no goroutine-safety guarantees. `Spec` instances are not
safe for concurrent use by multiple goroutines. Callers must synchronize access
externally (e.g. with `sync.Mutex` or by confining each Spec to a single
goroutine).

## Code Generation

Types like `RequirementsV1Json`, `TestSpecV1Json`, `TasksV1Json`, and
`PrdFrontmatterV1Json` are generated from JSON Schemas in
`afspec/schemas/` via
[go-jsonschema](https://github.com/atombender/go-jsonschema). After changing
any schema, regenerate the Go types:

```bash
make json-gen
```
