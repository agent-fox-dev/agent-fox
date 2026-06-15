# Spec Format v2 Implementation Plan

This document captures the implementation plan for migrating agent-fox from
the current all-markdown spec format to the new JSON-based format defined in
`af-spec/docs/spec-format.md` (v1.1). It is intended as input for spec
creation — each section below maps to a proposed spec.

## Progress (June 2026)

The migration took a different path than originally planned. Instead of
building schemas, parsers, and validators in-house (Specs A through E), the
project adopted the `afspec` library from af-core, which provides Pydantic
data models, schema validation, and rendering. This significantly reduced
scope — several planned specs became unnecessary.

Four agent-fox specs implemented the integration:

| agent-fox Spec | Covers from this plan | Status |
|---|---|---|
| 132: afspec Integration | Spec F (format detection, SpecFormat enum, SpecInfo.format) | Done |
| 133: v1.2 Parsing Pipeline | Spec K (parser_v12.py mapper, planner routing) | Done |
| 134: v1.2 Context Rendering | Specs K, J (context assembly, verification checklist, helpers) | Done |
| 135: v1.2 Skill and Validation | Specs H, L (lint routing, af-spec skill template) | Done |

Remaining items from this plan:
- **Specs A-E (schemas, data models)**: Superseded by `afspec` library
- **Spec B (prd.md frontmatter, lifecycle)**: Not yet implemented
- **Spec G (cross-file integrity)**: Handled by `afspec.validate()`
- **Spec I (mutation engine)**: Not yet implemented
- **Spec J (renderer)**: Partially covered by `afspec.render_individual()`
- **Spec M (legacy code removal)**: Deferred; v1 code paths remain

See [Spec Format v1.2 Architecture](architecture/06-spec-format-v12.md) for
the current state of the system.

---

## 1. Change Summary

### What changes

| Aspect | Current | New |
|---|---|---|
| File set | 5 required markdown files | 4 required (1 md + 3 json) + 1 optional md |
| `requirements.md` | Markdown with EARS prose | `requirements.json` — decomposed EARS fields, glossary map, correctness properties, execution paths, error handling |
| `design.md` | Markdown | **Eliminated** — content distributed to `requirements.json` and optional `architecture.md` |
| `test_spec.md` | Markdown | `test_spec.json` — structured test cases, computed coverage |
| `tasks.md` | Markdown checkboxes | `tasks.json` — state machine, kind enum, structured verification |
| `prd.md` | Plain markdown | Markdown with YAML frontmatter (lifecycle, intent hash, source) |
| `architecture.md` | N/A | Optional free-form markdown (absorbs design.md architectural content) |
| Validation | Regex on markdown + section presence | JSON Schema + cross-file referential integrity |
| Mutations | Direct file edits | RFC 6902 JSON Patch, atomic multi-file transactions |
| Lifecycle | Implicit (checkbox completion) | Explicit state machine: draft → active → sealed → superseded → archived |
| Permissions | None | Per-actor (operator/coordinator/archetype) write scopes |
| Rendering | Source files are human-readable | JSON → markdown renderer for human consumption |

### What stays the same

- Spec folder naming: `NN_snake_case_name`
- Spec root directory: `.agent-fox/specs/`
- EARS syntax (decomposed into fields, but same 6 patterns)
- Requirement ID format: `{spec_id}-REQ-{N}.{C}`
- Test spec ID format: `TS-{spec_id}-{N}`
- Task group concept (ordered groups with verification)
- Cross-spec dependency model (group-level granularity)
- Wiring verification as final task group
- Superseding and archiving workflow

---

## 2. Migration Strategy

**Incremental with format detection.** The system learns to read JSON files
alongside markdown. A format detector auto-selects the parser per spec.
Consumers are updated to use a format-agnostic API. New specs are created in
JSON format. Existing completed/archived specs remain in markdown (read-only).

This avoids a big-bang migration and lets each spec in this plan be
independently valuable.

### Transition phases

1. **Dual-read** — system reads both formats; new specs created in JSON
2. **New-only** — all new spec creation uses JSON; old specs are read-only
3. **Cleanup** — markdown parsing code removed once no active specs use it

The cleanup phase happens when all active (non-archived) specs are in JSON
format. Archived specs are never re-validated, so their format is irrelevant.

---

## 3. Spec Decomposition

Thirteen specs organized in four phases. Each spec targets one cohesive
concern and stays within the 10-requirement limit.

### Phase 1: Foundation (no runtime dependencies)

#### Spec A: JSON Schema Definitions

**Scope:** Create the four JSON Schema files that define the structure of
`requirements.json`, `test_spec.json`, `tasks.json`, and `prd.md` frontmatter.
Bundle them with the agent-fox package.

**Key requirements:**
- `requirements.v1.json` — top-level structure, requirement objects with
  discriminated `oneOf` for EARS patterns, correctness properties, execution
  paths, error handling, glossary
- `test_spec.v1.json` — test cases, property tests, edge case tests, smoke
  tests, coverage object
- `tasks.v1.json` — task groups with kind enum, subtask state machine,
  dependencies, traceability, verification, test_commands
- `prd-frontmatter.v1.json` — YAML frontmatter fields (spec_id, status,
  intent_hash, etc.)
- Schema loader utility that validates a JSON file against its bundled schema
- Schema versioning: `schema_version: 1` field, forward-compatible rules

**Affected files (new):**
- `agent_fox/spec/schemas/requirements.v1.json`
- `agent_fox/spec/schemas/test_spec.v1.json`
- `agent_fox/spec/schemas/tasks.v1.json`
- `agent_fox/spec/schemas/prd_frontmatter.v1.json`
- `agent_fox/spec/schemas/__init__.py` (loader)

**Dependencies:** None — pure data definition.

**Risk:** Low. Schema design errors surface during later specs. Iterate.

---

#### Spec B: prd.md Frontmatter and Lifecycle

**Scope:** Extend `prd.md` to support YAML frontmatter with lifecycle metadata.
Implement the lifecycle state machine (draft → active → sealed → superseded →
archived). Add the Intent section and intent hash mechanism. Move Source from
body section to frontmatter field.

**Key requirements:**
- Parse YAML frontmatter from prd.md (fields: spec_id, spec_name, title,
  status, created_at, updated_at, owner, source, supersedes, tags,
  intent_hash, schema_version)
- Validate frontmatter against `prd-frontmatter.v1.json` schema
- Implement lifecycle state machine with transition enforcement
- Compute SHA-256 intent hash at draft→active transition
- Reject mutations that alter Intent section after activation
- Distinguish mutable fields (title, updated_at, owner, source, tags) from
  protected fields (status, spec_id, spec_name, created_at, supersedes,
  intent_hash) — protected fields change only through library transitions

**Affected files:**
- `agent_fox/spec/prd.py` (new — frontmatter parser, lifecycle manager)
- `agent_fox/spec/discovery.py` (read status from frontmatter)
- `agent_fox/spec/validators/_helpers.py` (frontmatter validation)
- `agent_fox/engine/issue_summary.py` (read source from frontmatter)

**Dependencies:** Spec A (schema for frontmatter).

**Risk:** Medium. Frontmatter parsing needs a YAML library. The lifecycle state
machine is new behavior — needs careful design of transition rules and error
messages. Existing prd.md files lack frontmatter; format detection must handle
both.

**Open question:** Does the lifecycle apply only to JSON-format specs, or do
we retrofit frontmatter onto existing markdown specs too? Recommendation:
JSON-format specs only. Existing markdown specs have no lifecycle — they are
either active (have uncompleted tasks) or done (all tasks checked).

---

### Phase 2: JSON Data Models (sequential — each builds on the prior)

#### Spec C: requirements.json Data Model and Parser

**Scope:** Define the in-memory data model for `requirements.json`. Implement
a parser that loads and validates the file. This is the largest single artifact
change because requirements.json absorbs content from both `requirements.md`
and `design.md` (correctness properties, execution paths, error handling).

**Key requirements:**
- Data model: `RequirementsDoc` with fields: spec_id, spec_name,
  introduction, glossary, requirements[], correctness_properties[],
  execution_paths[], error_handling[]
- Requirement model: id, title, user_story (role/goal/benefit),
  acceptance_criteria[], edge_cases[]
- Acceptance criterion model: discriminated union on ears_pattern
  (ubiquitous, event_driven, complex_event, state_driven, unwanted, optional)
  with pattern-specific fields (trigger, condition, error_condition, state,
  feature) + common fields (id, system, action, return_contract)
- Correctness property model: id, title, for_any, invariant, validates[]
- Execution path model: id, title, steps[] (actor, action)
- Error handling model: id, condition, behavior, requirement_id
- EARS sentence rendering from decomposed fields (template per pattern)
- Schema validation via Spec A schemas
- Glossary cross-check: backtick-wrapped terms in specific fields must have
  glossary entries

**Affected files (new):**
- `agent_fox/spec/models/requirements.py` (data model)
- `agent_fox/spec/parsers/requirements_json.py` (JSON parser)

**Affected files (modified):**
- `agent_fox/spec/_patterns.py` (add JSON-aware extraction alongside regex)

**Dependencies:** Spec A (schema).

**Risk:** High. This is the most complex data model. The discriminated union
for EARS patterns needs careful implementation. The glossary cross-check
(backtick-term detection in specific fields) is a new validation concern.

---

#### Spec D: test_spec.json Data Model and Parser

**Scope:** Define the in-memory data model for `test_spec.json`. Implement
parser and validator.

**Key requirements:**
- Data model: `TestSpecDoc` with fields: spec_id, spec_name, test_cases[],
  property_tests[], edge_case_tests[], smoke_tests[], coverage{}
- Test case model: id, requirement_id, kind (unit|integration), description,
  preconditions[], input (object|null), expected (object), assertion_pseudocode
- Property test model: id, property_id, validates[], description,
  for_any_strategy, invariant_check
- Edge case test model: same structure as test cases (different ID format)
- Smoke test model: id, execution_path_id, description, trigger,
  real_components[], mockable[], expected_effects[]
- Coverage model (computed): requirements_covered[], properties_covered[],
  paths_covered[], gaps[]
- Coverage computation: auto-populate from test_cases, property_tests,
  edge_case_tests, smoke_tests; detect gaps
- Schema validation via Spec A schemas

**Affected files (new):**
- `agent_fox/spec/models/test_spec.py`
- `agent_fox/spec/parsers/test_spec_json.py`

**Dependencies:** Spec A (schema), Spec C (references requirement IDs).

**Risk:** Medium. Straightforward data model. Coverage computation is new but
well-defined.

---

#### Spec E: tasks.json Data Model and Parser

**Scope:** Define the in-memory data model for `tasks.json`. Implement parser,
validator, and subtask state machine.

**Key requirements:**
- Data model: `TasksDoc` with fields: spec_id, spec_name, test_commands{},
  dependencies[], task_groups[], traceability[]
- Task group model: id (integer), kind (tests|standard|checkpoint|
  wiring_verification), title, subtasks[], verification{}
- Subtask model: id (string, "N.N"), title, details[], test_spec_refs[],
  requirement_refs[], state (pending|queued|in_progress|done|
  pending_reevaluation|dropped), optional (boolean)
- Subtask state machine: enforce legal transitions (pending→queued,
  queued→in_progress, in_progress→done, done→pending_reevaluation, etc.)
- Verification model: id ("N.V"), checks[]
- Dependency model: depends_on_spec, from_group, to_group, relationship,
  sentinel
- Traceability model: requirement_id, test_spec_id, task_id, test_path
  (string|null)
- test_commands model: spec_tests, all_tests, linter
- Structural rules: group 1 must be kind=tests, final group must be
  kind=wiring_verification, max one wiring_verification
- Schema validation via Spec A schemas

**Affected files (new):**
- `agent_fox/spec/models/tasks.py`
- `agent_fox/spec/parsers/tasks_json.py`

**Affected files (modified):**
- `agent_fox/spec/parser.py` (existing `parse_tasks()` kept for markdown;
  new `parse_tasks_json()` added)

**Dependencies:** Spec A (schema), Spec C and D (traceability references).

**Risk:** Medium. The subtask state machine is the most novel part. State
transition validation is new runtime behavior. The existing `parser.py`
currently returns `TaskGroupDef` — the new JSON parser must produce the same
(or compatible) data structure so downstream consumers work unchanged.

**Critical design decision:** The new JSON parser should return the existing
`TaskGroupDef` / `SubtaskDef` / `CrossSpecDep` types (or a compatible
superset) so that graph/builder.py, graph/planner.py, engine/hot_load.py,
engine/preflight.py, and verification_checklist.py continue to work without
modification until they are explicitly migrated (Spec J).

---

### Phase 3: Integration Layer

#### Spec F: Format Detection and Unified Parser API

**Scope:** Create a format-detection layer that auto-selects the correct
parser (markdown or JSON) per spec directory. Expose a unified API so
consumers don't need to know which format a spec uses.

**Key requirements:**
- Format detection: check for `requirements.json` (JSON format) vs.
  `requirements.md` (markdown format) — presence of `.json` files is the
  discriminator
- Unified `load_spec()` function that returns format-agnostic data structures
  regardless of source format
- Unified `parse_tasks()` that delegates to markdown or JSON parser
- Unified `parse_cross_deps()` that reads from prd.md (markdown) or
  tasks.json (JSON)
- Update `discover_specs()` to report detected format
- Update `EXPECTED_FILES` to be format-dependent (or introduce
  `expected_files(format)` function)

**Affected files:**
- `agent_fox/spec/discovery.py` (format detection, SpecInfo gains format field)
- `agent_fox/spec/parser.py` (unified dispatch)
- `agent_fox/spec/validators/_helpers.py` (format-aware EXPECTED_FILES)

**Dependencies:** Specs C, D, E (JSON parsers exist).

**Risk:** Medium. The key risk is ensuring the unified API is truly
format-agnostic. The markdown parser returns `TaskGroupDef` with a
`completed` boolean and checkbox-based subtasks; the JSON parser has a richer
state machine (pending/queued/in_progress/done). The API must map the richer
model onto the simpler one for backward compatibility, or consumers must be
updated simultaneously.

**Recommendation:** The unified API returns the richer model. Add a
`completed` property to the new subtask model that returns
`self.state == "done"`. The existing `TaskGroupDef.completed` becomes a
computed property: `all(s.completed for s in self.subtasks)`. This preserves
backward compatibility.

---

#### Spec G: Cross-File Integrity Validation

**Scope:** Implement the cross-file referential integrity checks defined in
spec-format.md §10.2. These run after per-file schema validation and verify
consistency across the four required JSON artifacts.

**Key requirements (the 8 rules):**
1. Every `requirement_id` in test_spec.json, tasks.json traceability, and
   error_handling must exist in requirements.json
2. Every requirement and edge case in requirements.json must be covered by a
   test case in test_spec.json
3. Every correctness property must be referenced by a property test
4. Every execution path must be referenced by a smoke test
5. Every `test_spec_id` in tasks.json must exist in test_spec.json
6. Glossary cross-check: backtick terms in requirement fields must be in
   glossary
7. spec_id and spec_name must be consistent across all four files
8. No duplicate (requirement_id, test_spec_id) pairs in traceability

**Affected files (new):**
- `agent_fox/spec/validators/integrity.py`

**Affected files (modified):**
- `agent_fox/spec/validators/runner.py` (add integrity checks for JSON specs)

**Dependencies:** Specs C, D, E, F (need all parsers and format detection).

**Risk:** Low-medium. The rules are well-defined. Implementation is
straightforward set-comparison logic. The main risk is performance on large
specs (but specs are capped at 10 requirements, so data volumes are small).

**Note:** Several of these checks overlap with existing markdown validators
(untraced-requirement, untraced-test-spec, etc.). The new integrity validator
replaces those checks for JSON specs. The existing validators continue to
work for markdown specs.

---

#### Spec H: Validator Migration

**Scope:** Update the validation system to handle JSON-format specs. Route
JSON specs to schema validation + cross-file integrity (Specs A, G) instead
of the current markdown section/regex validators.

**Key requirements:**
- `validate_specs()` dispatches to format-appropriate validators based on
  format detection (Spec F)
- For JSON specs: run JSON Schema validation → cross-file integrity →
  structural rules (first group tests, last group wiring, etc.)
- For markdown specs: keep existing validators unchanged
- Update `Finding` data model if needed (e.g., file field changes from
  "requirements.md" to "requirements.json")
- Update fixer system for JSON specs: fixes become JSON mutations instead of
  markdown text edits
- Update or retire validators that are now schema-enforced for JSON specs:
  missing-file, missing-ears-keyword, inconsistent-req-id-format,
  non-bracket-req-id-format, missing-acceptance-criteria, missing-section,
  missing-correctness-properties, missing-error-table,
  missing-definition-of-done, etc.
- Preserve existing validators for markdown-format specs (backward compat)
- Retire AI validators that become redundant (vague-criterion detection is
  harder to automate on structured fields, but implementation-leak detection
  may still apply)

**Affected files:**
- `agent_fox/spec/validators/runner.py` (format dispatch)
- `agent_fox/spec/validators/files.py` (format-aware file check)
- `agent_fox/spec/validators/requirements.py` (bypass for JSON)
- `agent_fox/spec/validators/tasks.py` (bypass for JSON)
- `agent_fox/spec/validators/traceability.py` (bypass for JSON)
- `agent_fox/spec/validators/schema.py` (bypass for JSON)
- `agent_fox/spec/fixers/runner.py` (format dispatch)
- All fixer modules (JSON-aware versions)

**Dependencies:** Specs A, F, G.

**Risk:** Medium. Large surface area — many validator modules touched. But the
changes are mostly "if JSON format, skip this check" (the schema and
integrity validators handle it).

---

### Phase 4: Operations (can run in parallel with Phase 3)

#### Spec I: Mutation Engine

**Scope:** Implement the mutation contract: RFC 6902 JSON Patch for all JSON
artifact mutations, atomic multi-file patches, and per-actor permission
enforcement.

**Key requirements:**
- JSON Patch (RFC 6902) apply function: supports add, remove, replace, move,
  copy, test operations
- Pre-apply validation: validate patch against target file's JSON Schema
- Post-apply validation: run cross-file integrity after patch application
- Atomic multi-file patch: apply patches to multiple files; if any validation
  fails, roll back all changes
- Per-actor permissions: operator can write prd.md mutable fields +
  architecture.md + requirements.json + tasks.json planning; coordinator can
  write all JSON files; archetype can only update own subtask state
- Transaction API: `begin() → apply_patch(file, patch) → commit()/rollback()`
- Intent hash check: reject mutations that alter prd.md Intent section when
  status is not draft

**Affected files (new):**
- `agent_fox/spec/mutations.py` (patch engine)
- `agent_fox/spec/permissions.py` (actor permission model)
- `agent_fox/spec/transaction.py` (atomic multi-file commit)

**Dependencies:** Spec A (schema validation), Spec G (cross-file integrity).

**Risk:** High. This is architecturally novel for agent-fox. RFC 6902 JSON
Patch needs a library or custom implementation. Atomic multi-file transactions
need careful rollback semantics. Per-actor permissions need an actor identity
mechanism.

**Open question:** Is the mutation engine used by the af-spec skill (which
generates specs via LLM)? Or is it only used by the programmatic API? If the
skill generates complete JSON files in one shot, the mutation engine is mainly
for incremental updates (adding a requirement, changing task state). The
initial spec creation flow may bypass the mutation engine and write files
directly (with post-write validation).

**Recommendation:** Phase this internally. Start with validation-on-write
(validate after every file save) and per-actor permissions. Add JSON Patch
and atomic transactions as a follow-up if incremental mutations are needed.

---

#### Spec J: JSON-to-Markdown Renderer

**Scope:** Implement deterministic rendering of JSON artifacts to markdown for
human consumption. The rendered markdown is a derived view — never the source
of truth.

**Key requirements:**
- Per-file rendering: requirements.json → markdown, test_spec.json →
  markdown, tasks.json → markdown
- Combined rendering: prd.md (as-is) + architecture.md (as-is, if present) +
  rendered requirements + rendered test_spec + rendered tasks
- EARS sentence rendering from decomposed fields using pattern templates
  (WHEN {trigger}, THE {system} SHALL {action})
- Deterministic output: same JSON in → same markdown out, byte-for-byte
- Render tasks.json with checkbox syntax for human-readable task tracking
- CLI command: `agent-fox render-spec <spec_name>` → writes rendered markdown
  to stdout or file

**Affected files (new):**
- `agent_fox/spec/renderer.py`
- `agent_fox/cli/render_spec.py` (CLI command)

**Affected files (modified):**
- `agent_fox/cli/app.py` (register render-spec command)
- `agent_fox/session/context.py` (use renderer for context assembly instead
  of raw file reads)

**Dependencies:** Specs C, D, E (data models to render from).

**Risk:** Low-medium. Rendering is deterministic transformation — well-suited
to property testing (round-trip: render → parse → render should be stable).
The main complexity is formatting — getting markdown output that reads well.

---

### Phase 5: Consumer Migration

#### Spec K: Graph and Engine Consumer Migration

**Scope:** Update all graph construction and engine modules to work with
JSON-format specs via the unified parser API (Spec F).

**Key requirements:**
- `graph/builder.py`: use unified `parse_tasks()` which returns
  `TaskGroupDef` regardless of format
- `graph/planner.py`: use unified `parse_cross_deps()` which reads from
  prd.md (markdown) or tasks.json (JSON)
- `graph/spec_helpers.py`: `is_test_writing_group()` checks kind field for
  JSON specs, title pattern for markdown; `count_ts_entries()` reads JSON
  array length for JSON specs
- `graph/injection.py`: no changes needed if unified API works
- `engine/hot_load.py`: use format-aware EXPECTED_FILES; update completeness
  check; update lint gate for JSON specs
- `engine/preflight.py`: use unified task completion check
- `engine/blocking.py`: no changes needed (reads from DB, not spec files)
- `session/context.py`: update `_CORE_SPEC_FILES` to use renderer for JSON
  specs; include architecture.md when present
- `spec/verification_checklist.py`: update requirement ID extraction for JSON
  (direct field access) and task checkbox auditing (read state field)

**Affected files:** All files listed above.

**Dependencies:** Spec F (unified parser API), Spec J (renderer for context
assembly).

**Risk:** Medium. Wide surface area but each change is straightforward:
replace direct file reads with unified API calls. The risk is missing a
call site — thorough grep and test coverage required.

---

#### Spec L: Skill and Documentation Migration

**Scope:** Update the af-spec skill to generate JSON-format specs. Update all
other skills and documentation for the new format.

**Key requirements:**
- `af-spec/SKILL.md`: complete rewrite — Steps 3-6 now output JSON files
  instead of markdown; Step 4 (design.md) is eliminated; correctness
  properties, execution paths, error handling move to Step 3 (requirements);
  architecture.md is optional output
- `af-spec-audit/SKILL.md`: update for JSON format, new validation rules
- `af-reverse-engineer/SKILL.md`: output JSON specs
- `af-fix/SKILL.md`: update spec generation for JSON
- `CLAUDE.md`: update artifact list (5→4+1), file references
- `AGENTS.md`: update spec file descriptions
- `docs/architecture/01-spec-authoring.md`: major rewrite
- `agent_fox/_templates/agents_md.md`: update template
- `agent_fox/_templates/ai_validation/`: update prompt templates for JSON
- `agent_fox/fix/spec_gen.py`: generate JSON instead of markdown
- `agent_fox/nightshift/spec_builder.py`: update for JSON format

**Dependencies:** All prior specs (this is the user-facing layer).

**Risk:** Medium. The af-spec skill rewrite is substantial — it's a 1000+ line
prompt that must produce valid JSON. LLM-generated JSON needs careful prompt
engineering to get right (schema adherence, ID consistency, cross-references).

---

### Phase 6: Cleanup

#### Spec M: Markdown Parser Retirement

**Scope:** Once all active specs use JSON format, remove the markdown parsing
code paths. This is a cleanup spec — it removes dead code and simplifies the
system.

**Key requirements:**
- Remove markdown-specific parsers: regex-based task parsing, section
  detection, checkbox parsing, EARS keyword scanning
- Remove markdown-specific validators: missing-ears-keyword,
  inconsistent-req-id-format, non-bracket-req-id-format,
  missing-section (for design.md, requirements.md, etc.)
- Remove markdown-specific fixers
- Remove format detection dispatch (only JSON path remains)
- Simplify `EXPECTED_FILES` to JSON-only list
- Remove `_SECTION_SCHEMAS` dict
- Clean up `_patterns.py` (remove markdown-only patterns)
- Remove `design.md` from all file lists and validators

**Affected files:** All spec/ modules, validators, fixers, patterns.

**Dependencies:** All active specs migrated to JSON (operational prerequisite,
not a code dependency).

**Risk:** Low. Pure deletion. But must verify no active spec still uses
markdown before executing.

**Note:** This spec should not be created until the migration is complete.
It exists in this plan to show the end state.

---

## 4. Dependency Graph

```
Phase 1 (Foundation)
  A: JSON Schemas ─────────────────────┐
  B: prd.md Frontmatter ───────────────┤
                                       │
Phase 2 (Data Models)                  │
  C: requirements.json ← A ───────────┤
  D: test_spec.json ← A, C ───────────┤
  E: tasks.json ← A, C, D ────────────┤
                                       │
Phase 3 (Integration)                  │
  F: Format Detection ← C, D, E ──────┤
  G: Cross-File Integrity ← C, D, E, F┤
  H: Validator Migration ← A, F, G ───┤
                                       │
Phase 4 (Operations) — parallel w/ P3  │
  I: Mutation Engine ← A, G ──────────┤
  J: Renderer ← C, D, E ─────────────┤
                                       │
Phase 5 (Consumers)                    │
  K: Graph/Engine Migration ← F, J ───┤
  L: Skill/Docs Migration ← all ──────┤
                                       │
Phase 6 (Cleanup)                      │
  M: Markdown Retirement ← all ───────┘
```

Critical path: **A → C → D → E → F → G → H → K → L**

Parallelizable:
- B runs in parallel with C, D, E (prd.md is independent of JSON artifacts)
- I runs in parallel with F, G, H (mutation engine is independent of validation)
- J runs in parallel with F, G, H (renderer needs only data models)

---

## 5. Effort Estimates

| Spec | Size | Estimate | Notes |
|---|---|---|---|
| A: JSON Schemas | Medium | 1 spec session | Schema design is the hard part |
| B: prd.md Frontmatter | Medium | 1 spec session | YAML parsing + lifecycle state machine |
| C: requirements.json | Large | 1-2 spec sessions | Biggest data model, EARS decomposition |
| D: test_spec.json | Medium | 1 spec session | Straightforward after C |
| E: tasks.json | Medium | 1 spec session | State machine is the novelty |
| F: Format Detection | Small | 1 spec session | Thin dispatch layer |
| G: Cross-File Integrity | Medium | 1 spec session | 8 well-defined rules |
| H: Validator Migration | Medium | 1 spec session | Wide but shallow changes |
| I: Mutation Engine | Large | 1-2 spec sessions | Architecturally novel |
| J: Renderer | Medium | 1 spec session | Deterministic transforms |
| K: Graph/Engine Migration | Medium | 1 spec session | Many files, each simple |
| L: Skill/Docs Migration | Large | 1-2 spec sessions | af-spec skill rewrite |
| M: Markdown Retirement | Small | 1 spec session | Pure deletion |

Total: ~13-16 spec sessions.

---

## 6. Open Questions

### Q1: Where do JSON Schema files live?

**Option A:** In agent-fox at `agent_fox/spec/schemas/` — bundled with the
package, validated at runtime.

**Option B:** In af-spec as the canonical definition, copied into agent-fox
at build time.

**Recommendation:** Option A. agent-fox is the implementation; it owns the
schemas. af-spec's `spec-format.md` is the human-readable specification.

### Q2: JSON Patch library

**Option A:** Use an existing library (e.g., `jsonpatch` on PyPI).

**Option B:** Implement a minimal subset (add, remove, replace, test).

**Recommendation:** Option A if the dependency is lightweight. Option B if
dependency minimization is a project goal.

### Q3: YAML frontmatter library for prd.md

The system needs to parse YAML frontmatter from markdown. Options:
`python-frontmatter`, manual `---` delimiter split + `pyyaml`.

**Recommendation:** Manual split + pyyaml (already likely a dependency or
easy to add). Avoids a new dependency for a simple parse.

### Q4: Mutation engine scope for initial implementation

The full mutation contract (JSON Patch, atomic transactions, per-actor
permissions) is substantial. For the initial implementation:

**Option A:** Full implementation per spec-format.md §11.

**Option B:** Validation-on-write only (validate after file save, no patch
abstraction). Add patch/transaction support later.

**Recommendation:** Option B for initial delivery. The af-spec skill writes
complete files, not incremental patches. Validation-on-write catches errors.
The full mutation engine can be added when programmatic spec editing is needed.

### Q5: Architecture.md generation

When should the af-spec skill generate `architecture.md`?

**Option A:** Always generate it (moving content from former design.md).

**Option B:** Generate only when the spec has significant architectural
content that doesn't fit in requirements.json.

**Recommendation:** Option A for now. The LLM generates architectural context
naturally. Making it optional risks losing useful content. The file can always
be deleted later.

### Q6: Existing spec migration

Do existing completed specs in `.agent-fox/specs/` need conversion?

**Recommendation:** No. Existing specs are either archived or fully
implemented. The format detection layer (Spec F) handles reading them. New
specs use JSON. Migration tooling is only needed if existing active specs
need to be continued under the new format.

---

## 7. Risk Register

| Risk | Impact | Likelihood | Mitigation |
|---|---|---|---|
| JSON Schema design errors discovered late | Rework in downstream specs | Medium | Validate schemas against example specs early (Spec A tests) |
| LLM struggles to generate valid JSON specs | Skill unusable | Medium | Extensive prompt engineering in Spec L; schema validation catches errors |
| Unified parser API breaks backward compat | Graph/engine failures | Medium | Property: TaskGroupDef from JSON parser == TaskGroupDef from markdown parser for equivalent specs |
| Mutation engine over-engineering | Wasted effort | Low | Start with validation-on-write (Q4 Option B) |
| Test fixture explosion | Slow tests | Low | Share fixtures between markdown and JSON test suites |
| Performance regression from JSON Schema validation | Slow lint-specs | Low | Schema validation is sub-millisecond per file |

---

## 8. Success Criteria

1. `agent-fox lint-specs` validates JSON-format specs correctly
2. `af-spec` skill generates valid JSON-format specs from PRDs
3. Graph builder constructs correct task graphs from JSON specs
4. Engine hot-loads JSON specs, runs preflight, blocks on findings
5. Existing markdown specs continue to work unchanged
6. `agent-fox render-spec` produces human-readable markdown from JSON
7. All existing tests pass throughout the migration
