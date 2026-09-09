# afspec-go: Go Implementation of the afspec Library

## Intent

The Python `afspec` library provides the canonical implementation for loading, validating, mutating, and rendering agent-fox specification packages. Go consumers (CLIs, servers, CI tools) currently cannot work with spec packages natively — they must shell out to Python or reprocess specs from scratch.

This project creates an idiomatic Go library (`afspec`) that provides the same public API surface as the Python package, enabling Go programs to be first-class citizens in the spec-driven development workflow. Byte-for-byte round-trip fidelity with the Python implementation is a hard requirement.

## Goals

- Provide a Go package that can load, validate, mutate, save, and render spec packages with the same semantics as the Python `afspec` library.
- Reuse the auto-generated Go structs from `make json-go` as the foundation for data types.
- Produce idiomatic Go code: exported methods on structs where the struct is the primary operand, `(result, error)` return signatures, standard library patterns, Go naming conventions.
- Maintain byte-for-byte round-trip fidelity: a spec loaded and saved by the Go library must produce identical output to the Python library.
- Share the same JSON Schema files for schema validation, embedded via `//go:embed`.
- Include `BootstrapSpec` for incremental spec population with deferred cross-file validation.

## Non-goals

- Porting `agentspec` (the AI-powered spec creation library).
- Porting the `spec` CLI.
- Porting `lint.py` (not part of the public API).
- Implementing a Go `pydantic`-equivalent model layer — the auto-generated structs from JSON Schema are sufficient.

## Functional Requirements

### I/O (load, save, marshal)

- `LoadSpec(dir)` loads all artifacts (`prd.md`, `requirements.json`, `test_spec.json`, `tasks.json`, optional `architecture.md`) from a spec directory and returns a `Spec` struct. Returns a `LoadError` on missing required files or malformed content. Standalone function (constructs the struct).
- `spec.Save(dir)` writes all artifacts to disk using a two-phase strategy: all temp files are written and fsynced first (Phase 1), then all temp files are renamed to their final names (Phase 2). If any write in Phase 1 fails, all temp files are removed and no artifact file is modified. Lifecycle guards prevent saving sealed, superseded, or archived specs. Returns `SaveError` or `LifecycleError`.
- `MarshalJSON(v)` produces deterministic JSON serialization (2-space indent, sorted keys) matching Python's output byte-for-byte. Standalone function.
- PRD loading must parse YAML frontmatter (delimited by `---`) and extract the markdown body separately. Edge-case behavior (missing closing delimiter, malformed YAML) must match the Python implementation.

### Validation

- `spec.Validate()` returns a `ValidationResult` containing errors and warnings. `Valid` is true when `Errors` is empty.
- `spec.ValidateSchema()` checks each artifact against the bundled JSON Schema files (embedded via `//go:embed`) plus EARS constraints and task group structure rules.
- `spec.ValidateCrossFile()` checks cross-file consistency: dangling references, coverage gaps, glossary completeness, ID format validation.
- `ValidateCrossSpec(specs, graph)` checks cross-spec interface consistency: API symbols, glossary conflicts, contract mismatches. Standalone function (operates across multiple specs).
- `spec.ValidateStructured()` returns validation results reshaped as a categorized map for CLI consumption.
- JSON Schema validation uses `github.com/santhosh-tekuri/jsonschema`.

### Lifecycle

- `spec.Transition(target, dir)` transitions a spec's lifecycle state (`draft → active → sealed`, plus `superseded` and `archived` terminals), persists to disk, and returns the updated `Spec`. Returns `LifecycleError` for invalid transitions.
- `ValidTransition(current, target)` checks whether a state transition is allowed (pure function, no side effects). Standalone function.
- `spec.Supersede(supersedingSpecID, dir)` marks a sealed spec as superseded, prepends a deprecation banner to the PRD body, and persists.
- `MoveToArchive(specDir, root)` transitions to archived (if needed) and moves the spec directory to `{root}/archive/`. Standalone function.

### Discovery and Dependencies

- `DiscoverSpecs(root)` scans a directory for spec directories (`NN_name/` pattern) and returns lightweight `SpecMeta` entries parsed from PRD frontmatter. Standalone function.
- `BuildDependencyGraph(metas, root)` builds the inter-spec dependency graph from `tasks.json` declarations. Returns a `DependencyGraph` with `Edges()`, `Dependencies(specID)`, `Dependents(specID)`, `TopologicalSort()`, and cycle detection/reporting. Standalone function.
- `IsSpecDirName(name)` and `ParseSpecDirName(name)` validate and parse directory names matching `{NN}_{snake_case}`. Standalone functions.
- `LoadSpecLandscape(root, includeArchive, currentSpecID)` collects metadata for all specs for landscape views. Standalone function.

### Subtask Mutation

- `tasks.TransitionSubtask(subtaskID, target)` transitions a single subtask's state via the state machine (hardcoded valid transitions as defined in spec-format.md). Method on `Tasks`.
- `tasks.CompleteSubtaskStates(groupIDs)` marks all subtasks in specified groups as `done` (bypasses state machine). Method on `Tasks`.
- `tasks.ResetSubtaskStates(groupIDs)` resets all subtasks in specified groups to `pending`. Method on `Tasks`.

### Rendering

- `spec.RenderCombined()` renders all artifacts as a single Markdown document. Method on `Spec`.
- `spec.RenderIndividual()` renders each artifact separately, returning a `map[string]string`. Method on `Spec`.
- `spec.RenderIndividualScoped(targetGroup)` renders each artifact scoped to a target task group's refs. Method on `Spec`.
- `req.Render()` renders requirements as Markdown. Method on `Requirements`.
- `ts.Render()` renders test spec as Markdown. Method on `TestSpec`.
- `t.Render()` renders tasks as Markdown with checkbox-formatted subtasks. Method on `Tasks`.
- `criterion.RenderEARSSentence()` renders a single EARS criterion as a natural-language sentence. Method on `Criterion`.

### EARS Criterion Builders

Standalone convenience constructors:

- `UbiquitousCriterion(id, system, action)`.
- `EventDrivenCriterion(id, trigger, system, action)`.
- `ComplexEventCriterion(id, trigger, condition, system, action)`.
- `StateDrivenCriterion(id, state, system, action)`.
- `UnwantedCriterion(id, errorCondition, system, action)`.
- `OptionalCriterion(id, feature, system, action)`.

### Bootstrap

- `BootstrapSpec` struct with `NewBootstrapSpec(specID, specName)` constructor. Supports incremental population of spec artifacts with deferred cross-file validation via `Finalize()`. Method on `BootstrapSpec`.

### Other

- `Schemas()` returns the bundled JSON Schema files as a `map[string][]byte` (embedded via `//go:embed`). Standalone function.
- `ComputeIntentHash(body)` computes SHA-256 of the normalized `## Intent` section from a PRD body. Returns `IntentError` if section is missing. Standalone function.
- `ts.ComputeCoverage(req)` computes test coverage by scanning test entries against requirements, properties, and paths. Method on `TestSpec`.
- `CreateSpec(specID, specName)` creates a new Spec with initialized sub-artifacts in draft state. Standalone function.

### Error Types

- `SpecError` — base error type (Go error wrapping conventions).
- `LoadError`, `SaveError`, `LifecycleError`, `IntentError`, `BootstrapError` — specific error types wrapping `SpecError`.

## Technical Boundaries

- Go 1.26+ (per existing `go.mod`).
- Module path: `github.com/agent-fox-dev/spec`.
- Package name: `afspec` — all public functions and methods in project root, internal helpers in `internal/` subpackages.
- JSON Schema files copied to `schemas/` in the project root as the source of truth, embedded via `//go:embed`.
- JSON Schema validation: `github.com/santhosh-tekuri/jsonschema`.
- YAML parsing: `github.com/goccy/go-yaml` (already a dependency).
- Atomic file writes via write-to-temp-then-rename.

## Dependencies

- **JSON Schema files** — copied from `packages/afspec/afspec/schemas/` to `schemas/` in the project root. These are the source of truth for both Python and Go.
- **Auto-generated Go structs** — produced by `make json-go`, used as the data model foundation.
- **Python test fixtures** — golden-file tests using fixture specs from the Python test suite to verify byte-for-byte fidelity.

## Design Decisions

- **BootstrapSpec included** — needed by downstream consumers for incremental spec building.
- **lint.py excluded** — not part of the public API in Python.
- **Schema embedding** — `//go:embed` bundles schemas into the binary, avoiding filesystem dependencies at runtime.
- **Atomic saves** — write-to-temp-then-rename pattern for safe concurrent access.
- **Subtask state machine** — same hardcoded transition rules as defined in `spec-format.md`, not configurable.
- **Cycle detection** — `DependencyGraph.TopologicalSort()` detects and reports cycles in addition to matching Python behavior.
- **PRD parsing edge cases** — match Python behavior exactly for malformed frontmatter.
- **Methods over functions** — any function whose primary operand is a struct is a method on that struct. Standalone functions are reserved for constructors, discovery, multi-entity operations, and pure utilities.
