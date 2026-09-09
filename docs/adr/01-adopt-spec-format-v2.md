# 01. Adopt spec format v2 to close the plan-completeness gap

Status: accepted
Date: 2026-09-08
Implemented: 2026-09-09 (see "Implementation notes")

## Context

Operators report that generated specs are "over-complicated and at the same
time under-specified": `requirements.json` and `tasks.json` are large, yet
implementations built from them fail because the plan has gaps. This ADR
records the investigation, the root causes found in the v1.3 format and in
the Go generation pipeline, and the decision to move to format version 2
(`specification/spec-format-v2.md`).

### Evidence from the eight archived specs

All numbers come from `.specs/archive/0[1-8]_*` (the specs used to build
this repository), computed from the JSON artifacts.

| Spec | REQ | criteria (AC+EC) | PROP | PATH | tests | task groups | subtasks | tests owned by no task | edge-case criteria owned by a task | PROP tests owned by a task |
|---|---|---|---|---|---|---|---|---|---|---|
| 01 | 28 | 55 + 53 | 10 | 6 | 124 | 12 | 60 | 63 (51 %) | 0 / 53 | 0 / 10 |
| 02 | 6 | 19 + 14 | 7 | 5 | 45 | 11 | 37 | 21 (47 %) | 0 / 14 | 0 / 7 |
| 03 | 8 | 29 + 20 | 8 | 4 | 62 | 12 | 34 | 29 (47 %) | 0 / 20 | 0 / 9 |
| 04 | 20 | 28 + 36 | 7 | 4 | 75 | 10 | 40 | 44 (59 %) | 0 / 36 | 0 / 7 |
| 05 | 10 | 35 + 28 | 7 | 3 | 73 | 13 | 35 | 7 (10 %) | 0 / 28 | 0 / 7 |
| 06 | 15 | 43 + 35 | 10 | 6 | 94 | 15 | 42 | 45 (48 %) | 0 / 35 | 0 / 10 |
| 07 | 11 | 46 + 34 | 10 | 4 | 94 | 17 | 61 | 44 (47 %) | 0 / 34 | 0 / 10 |
| 08 | 16 | 43 + 41 | 8 | 6 | 98 | 13 | 49 | 51 (52 %) | 0 / 41 | 0 / 8 |

Observations:

- **Every edge-case test and every property test in every spec is
  orphaned.** No subtask's `test_spec_refs` and no traceability entry
  references a `TS-NN-E*` or `TS-NN-P*` id. The `traceability` array
  covers exactly the `test_cases` list and nothing else. A coder working
  from the scoped render of a task group therefore never sees the edge
  cases. This is the "under-specified" half.
- **The spec is valid anyway.** `spec validate` checks that references
  resolve and that every criterion has a test, but never that every test
  is owned by a task or that every criterion is owned by an
  implementation task.
- **Most content is repetition.** Each requirement appears in a "tests"
  group (subtask details restate the test descriptions), again in a
  "standard" group (details restate the criteria), and a third time in
  `traceability`. `error_handling` entries restate `unwanted` criteria
  verbatim (`08-ERR-1` ≡ `08-REQ-2.2`). Specs run 12–17 task groups and
  65–100 KB per artifact. This is the "over-complicated" half.
- **Scope guidance is ignored.** The format allows "at most 10
  requirements" as a warning; specs have 16–28. PRDs are written as
  design documents and the generator transcribes them.
- **`external_apis` are hallucinated.** Spec 08 lists a package
  `agentspec_go_core` with import path `agentspec/core`, neither of which
  exists, with `version: "unverified — cross-check …"`.
- Spec 05 has zero `edge_case_tests` and 63 `test_cases`: the generator
  put edge cases into the other array, which validation accepts. The
  split between the two test kinds carries no information.

### Root causes in the Go pipeline (`golang/agentspec`)

1. **Downstream generators do not see the requirements.**
   `renderRequirementsArtifact` (prompts.go) passes only requirement ids,
   titles and acceptance-criterion ids to the test and task generators.
   Edge-case ids, property ids, path ids, criterion text and contracts
   are omitted (introduced by issue #54, "summarize prior artifacts").
   The test generator has to re-derive edge cases from the PRD and guess
   their ids.
2. **Tasks are generated without the test spec.** `GenerateArtifacts`
   runs `test_spec` and `tasks` concurrently after `requirements` (issue
   #57). The task generator never sees a single test id; it guesses
   `TS-NN-1…` for acceptance criteria and cannot know the `E`/`P`/`SMOKE`
   ids at all. This alone explains the 100 % orphan rate.
3. **Inline validation is shallow.** `validateArtifactContent` checks
   three top-level keys, id formats and EARS field sets. Full schema
   validation and cross-file rules run only after all artifacts are
   written, and their failures are downgraded to "warnings" in
   `GenerateResult` (the CLI then exits 1 but leaves the files).
   `session.go` claims schema validation happened inline; it did not.
4. **Prompt examples contradict the schema.** The few-shot examples in
   `generation_user_*.md` use `"schema_version": "1.0"` (schema: integer
   `1`), a string `user_story` (schema: object), a `coverage` object
   inside each test case, verification subtasks inside `subtasks` with a
   `checks` field, and omit `kind`, `state` and `optional`.
5. **The renderer hides what matters.** `return_contract` is never
   rendered. Edge-case and smoke tests render as id and description only
   (`render.go`), while `test_cases` render in full. The CLI `spec render`
   prints raw JSON rather than the Markdown renderer's output.
6. **Validation is stringly typed.** The wiring-verification check greps
   subtask titles for the words `stub` or `dead`; the glossary check
   flags every backticked identifier. Both generate repair churn without
   improving the plan.

### Root causes in the format (v1.3)

- Thirteen id families, four test arrays, two criterion arrays, a stored
  `coverage` object and a stored `traceability` array: every pair of
  these is a place for drift, and the tooling in both languages only ever
  wired one of the four test kinds into the plan.
- Task groups of kind `tests` followed by groups of kind `standard`
  force each requirement to be described twice and double the plan size
  without adding information; TDD is a property of how a task is
  executed, not a separate task.
- `correctness_properties` and `error_handling` duplicate criteria.
- Validation proves referential integrity, not plan completeness.

## Decision

Adopt spec format version 2 as specified in
`specification/spec-format-v2.md` with schemas
`specification/schemas/*.v2.json` and the worked example in
`testdata/v2_example/`. In one sentence: one list of criteria, one list of
tests, one list of tasks, every test owned by a task, every criterion owned
by a task, nothing derived is stored.

Key changes:

| Area | v1.3 | v2 |
|---|---|---|
| Criteria | `acceptance_criteria` + `edge_cases`, 5 pattern-specific fields | one `criteria` list; `pattern`, `condition`, `guard`, `action`, `contract` |
| Properties, error handling | separate sections with own ids | folded into criteria (`ubiquitous` + `property` test; `unwanted` + `contract`) |
| Tests | 4 arrays, 4 id families, stored `coverage` | one `tests` list with `kind` and `verifies[]`; coverage computed |
| Tasks | groups → subtasks → verification, `tests/standard/checkpoint/wiring_verification`, stored `traceability` | flat `tasks` with `criteria`, `tests`, `steps`, `touches`, `depends_on`, `done_when`; kinds `implement`/`integration`; traceability computed |
| Completeness rules | references must resolve | C7 every test owned by a task, C8 every criterion owned by an implement task, C9 integration task owns all smoke tests |
| Word-match checks | `stub|dead` regex, glossary backticks as errors | removed / warning only |
| Ids | 13 formats | 5 formats |
| Generation | parallel, id-only context, shallow inline validation | sequential, full context, inline schema + cross-file validation with repair (§12) |

Verification of the v2 design done in this session: the four v2 schemas
compile with `santhosh-tekuri/jsonschema/v6`; `testdata/v2_example`
validates against them and passes rules C1–C11; six negative cases (a
`ubiquitous` criterion with a condition, an `unwanted` criterion without a
contract, an `event_driven` criterion with a guard, a unit test with
`real_components`, an implement task without criteria, a leaked v1
`traceability` field) are rejected by the schemas; removing a test from
its task is rejected by C7. The scratch program used for this check is
not part of the repository; the rule implementation belongs in
`validate.go` (Phase 2 below).

## Consequences

Positive: a valid v2 spec is a complete plan by construction; artifacts
shrink by roughly half (no duplicated task groups, no restated error
handling, no stored traceability or coverage); the validator, renderer,
mutators and prompt templates lose the code that handled four test kinds
and two criterion kinds; the generation pipeline can no longer emit
orphaned work.

Negative: v1 specs in `.specs/archive` and any downstream consumer that
reads `task_groups`/`subtasks` (scoped rendering by group) must migrate.
The subtask state machine loses `queued` and `pending_reevaluation`; a
runtime that used them keeps that state in its own store.

The Python packages are not migrated; they are being phased out.

## Go implementation plan

The change touches the three Go packages. The phases are ordered so that
each leaves `make check` green. Estimated size: the library rewrite is
dominated by `validate.go` (2.4 k lines) and its tests (8 k lines), most of
which delete.

### Phase 0 — Groundwork (this ADR)

- `specification/spec-format-v2.md`, `specification/schemas/*.v2.json`,
  `testdata/v2_example/`. Done.

### Phase 1 — Types and I/O (`golang/`)

1. `Makefile` `json-gen`: generate `*.v2.go` from the v2 schemas
   (go-jsonschema, package `afspec`). Keep `*.v1.go` until Phase 5.
   Because `RequirementsV1Json` and the v2 type would collide on nested
   names (`Criterion`, `Requirement`, …), generate v2 into a sub-package
   `golang/v2` or use go-jsonschema's `--struct-name-from-title` with a
   `V2` suffix; the sub-package is cleaner and lets Phase 5 delete `v1`
   wholesale.
2. `afspec.go` (`Spec`, `LoadSpec`, `Save`): dispatch on
   `schema_version` read from `prd.md` frontmatter. A `Spec` holds either
   v1 or v2 artifacts. `Save` stops calling `ComputeCoverageStruct` for
   v2 (no coverage field).
3. `prd.go`: frontmatter fields `owner`, `source`, `supersedes`, `tags`
   become optional; render omits them when empty for v2.
4. `marshal.go`: the schema-ordered field writer needs the v2 field
   order (`init()` registers orders per type).
5. `schemas.go`: embed `schemas/*.v2.json` as well; `getCompiledSchemas`
   compiles both sets.
6. `nextid.go`, `mutate.go`, `ears.go`, `subtask.go`: v2 counterparts —
   `NextCriterionID`, `NextTestID`, `NextTaskID`; `AddCriterion`,
   `AddTest`, `AddTask`; criterion builders taking `(id, condition,
   guard, system, action, contract)`; task state machine with the four
   states of §8.3.1. `RenderEARSSentence` renders `→ contract`.

### Phase 2 — Validation (`golang/validate.go`)

Replace the v1 rule list with rules C1–C11 of §10.2 for v2 specs:

- Keep: schema validation, id format/prefix/duplicate checks (C2),
  spec_id consistency and folder name (C1), `unwanted` needs contract
  (C10), vague-language and scope warnings.
- Replace: coverage gap (C4/C5 on `verifies`), dangling refs (C3/C6).
- Add: C7 (every test owned), C8 (every criterion owned by an
  `implement` task, expanding requirement ids), C9 (single last
  `integration` task owning all smoke tests), C11, DAG check on
  `depends_on`.
- Delete: `validateEarsConstraints` (the schema conditionals cover it),
  `cross_file_8` traceability dedup, the `wiring_verification` regex
  checks, glossary backtick errors (downgrade to the §10.3 warning),
  `checkMissingSubtaskRefs` (C7/C8 make empty refs impossible),
  `ValidateRequirementsMap/TestSpecMap/TasksMap` (Phase 4 validates the
  full artifact instead).
- `ValidateCrossSpec`: `dependencies[].spec` replaces `depends_on_spec`;
  drop `cross_spec_1`/`cross_spec_4` signature and contract comparisons
  (they compared free text and never fired usefully); keep unknown
  dependency, glossary conflict (warning), actor-reference check.
- `coverage.go`: `ComputeCoverage` returns the derived matrix
  (criterion → tests → tasks) for `spec validate --trace`; nothing is
  written to disk.
- Tests: rewrite `validate_test.go` around the eleven rules with one
  positive fixture (`testdata/v2_example`) and one negative fixture per
  rule.

### Phase 3 — Rendering (`golang/render.go`, `render_budget.go`)

- `RenderIndividualScoped(taskID)` scopes by task using `task.criteria`
  and `task.tests` only; delete `render_inference.go` (traceability and
  text-based inference are unnecessary once C7/C8 hold).
- Render every test with all fields regardless of `kind`; render
  `contract` under each criterion; render `steps`, `touches`,
  `done_when` and the implicit definition of done under each task.
- Budget levels stay (drop architecture, then slim tests), but the slim
  test render must keep `then` and `verifies`.
- `cmd/spec/render.go`: emit the Markdown renderer's output instead of
  the raw JSON files (`--json` keeps the envelope with Markdown inside).

### Phase 4 — Generation pipeline (`golang/agentspec`)

1. `agent.go` `GenerateArtifacts`: sequential `requirements → tests →
   tasks`. Remove the errgroup. The prior-artifact block for `tests` is
   the full Markdown render of requirements (every criterion sentence,
   contract and path); for `tasks` it is the full render of requirements
   plus a table of every test (`id`, `kind`, `verifies`, `title`).
2. `validateArtifactContent`: run the compiled v2 schema on the tool
   input, then the cross-file rules decidable at that step (C2–C5 after
   tests, C6–C9 after tasks) using the prior artifacts. Keep the
   conversation-continuation repair loop; raise `maxRepairs` to 3. A
   spec that is still invalid after repair is a failed generation:
   `session.Generate` does not write it and does not return it as
   warnings.
3. `tools.go`: `ArtifactTool` builds the tool schema from the v2 schema;
   drop the `$defs` inlining special cases that no longer occur.
4. Templates: rewrite `generation_system.md` (five id formats, v2
   top-level shapes), `generation_user_requirements.md` (pattern
   selection table, edge-case checklist of §6.5.5, contract rule, no
   backtick rule), `generation_user_test_spec.md` (one list, `verifies`,
   given/when/then, contract-assertion rule), `generation_user_tasks.md`
   (group by requirement, tests written inside the task, integration
   task checklist of §8.4, `touches`, `depends_on`). Every example must
   validate against the v2 schema; add a test that loads each template's
   fenced JSON and validates it, replacing the string-matching assertions
   in `generation_prompts_content_test.go`.
5. `assessment_system.md`: add the scope check of §12.4 (does the PRD
   fit in ten requirements; if not, ask the operator to split).
6. `prompts.go`: delete `renderRequirementsArtifact` and siblings; use
   the library renderer.
7. `session.go`: remove the misleading "schema validated inline" comment
   path; `Generate` returns errors, not warnings, for validation
   failures.

### Phase 5 — CLI and migration (`golang/cmd/spec`)

- `new.go`: scaffold v2 artifacts (an empty `tests` list and a two-task
  skeleton are schema-invalid, so scaffold in bootstrap mode and mark
  the spec incomplete until `generate` runs; `validate` reports
  incompleteness rather than schema errors).
- `spec migrate <spec>`: deterministic v1 → v2 conversion following
  §13, attaching orphaned edge-case and property tests to the task that
  owns the parent requirement, merging each `tests` group into the
  `standard` group that owns the same requirements, and converting the
  `wiring_verification` group into the `integration` task.
- `spec validate --trace`: print the derived traceability matrix.
- `lint.go`, `status.go`, `list.go`: "fully implemented" means all tasks
  `done` or `dropped`.
- Delete the v1 types, schemas and code paths once `.specs/archive` has
  been migrated (or leave read-only v1 loading for the archive if the
  operator prefers not to rewrite history).

### Phase 6 — Documentation

- `README.md`, `docs/cli.md`, `golang/README.md`, `docs/development.md`
  (schema workflow now lists the v2 files), `AGENTS.md`/`CLAUDE.md`
  artifact descriptions.
- Retire `specification/spec-format.md` (v1.3) to
  `specification/archive/`.

### Order of value

If only part of this is done, do Phase 4 items 1 and 2 first: sequential
generation with full context and inline completeness validation remove the
orphaned-test problem even on v1, and are a two-file change. The format
change is what makes that validation cheap and the artifacts small.

## Implementation notes

Phases 1 to 6 are implemented in this repository, which is also where the Go
code now lives: it moved here from `agent-fox-dev/spec` in the same session.
Where the implementation diverged from the plan above, this is what it did
instead and why.

**One v2 library, not two side by side.** The plan generated the v2 types into
a `v2` sub-package so that v1 and v2 could coexist until Phase 5. This
repository is new and hosts the new format only, so `afspec` is v2 throughout
and the v1 types moved to `afspec/legacy` as a read-only package. Nothing but
`Migrate` depends on it, and the v1 schemas are no longer bundled.

**Generated models only.** The types are generated with go-jsonschema's
`--only-models`. The generated `UnmarshalJSON` methods duplicated what
`Validate` already does through the compiled schemas, with worse messages, and
they rejected the empty scaffold `spec new` has to write and read back before
`spec generate` fills it in.

**Two bugs surfaced on the way, both pre-existing.** The deterministic
marshaller used `json.Marshal` for strings, which escapes `<`, `>` and `&`, so
no spec containing them could round-trip byte-for-byte — the worked example in
`testdata/v2_example` contains `<message>` and hit it immediately. And the
schema validator unmarshalled each artifact into an `any` that already held the
struct pointer, so every artifact failed with "invalid jsonType". The v1 test
fixtures contained no angle brackets and the second bug was introduced by the
same change that revealed it.

**Named rules alongside schema conditionals.** Two rules are expressed as
conditionals in the schemas: an `unwanted` criterion needs a contract (C10) and
`real_components` appears exactly on smoke tests (C11). Their schema messages
are "missing property" and "'not' failed", which tell a repairing model
nothing, so C10 and C11 also run when schema validation fails and contribute a
message that names the rule.

**Scaffolds report incompleteness, and it is not valid.** `spec validate` on a
freshly created spec emits a single `completeness` error saying to run `spec
generate`, and exits non-zero. The spec has no plan yet; saying so is more
useful than either a wall of `minItems` violations or a green tick.

**Two checks removed as unreachable, not merely unhelpful.** The CLI's
cross-spec duplicate-requirement-ID check cannot fire on valid specs: C1 ties
`spec_id` to the folder prefix and C2 requires every requirement ID to carry
it, so two valid specs cannot share one. Cross-spec glossary conflicts became
warnings per §10.4 and are now surfaced with `severity: "warning"` rather than
dropped.

**`spec render` gained two flags.** `--task N` exposes the scoped render of
§11.1 — the render a coder actually receives — and `--max-tokens N` applies the
progressive budget. Both existed in the library and had no way to be reached
from the CLI.

The Python packages were not migrated, as this ADR said. They stayed in
`agent-fox-dev/spec` and implement format version 1.
