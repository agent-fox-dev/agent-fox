---
spec_id: 08
spec_name: spec_generation_improvement
title: Spec Generation Improvement
status: draft
created_at: '2026-06-25T08:24:02.956312+00:00'
updated_at: '2026-06-25T08:24:02.956312+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Task Group Size Limits

## Intent

Prevent oversized task groups from inflating session costs and degrading
first-attempt quality by adding splitting guidance to the spec generation
prompt and validation warnings to afspec.

## Background

Each task group maps 1:1 to a coder session. When a single group covers too
many test spec entries or subtasks, the resulting session is expensive, slow,
and produces low-quality output that requires multiple retry cycles.

Evidence from spec 07 (`nightshift_standalone_cli`) group 1:
- 5 subtasks covering 58 test spec entries
- First attempt: $11.55, 21 critical/major audit findings
- Required 3 coder→audit cycles to converge ($20.83 total, 76 minutes)
- Subtask 1.5 alone covered 25+ test spec entries

This is not isolated — across all existing specs, test groups average 38 test
spec refs (range: 25–65), well above a manageable session scope.

## Goals

1. Update the spec generation prompt (`generation_user_tasks.md`) with explicit
   rules that instruct the LLM to split oversized task groups during generation.
2. Add validation warnings in `afspec/validation.py` that flag groups exceeding
   size thresholds.
3. Apply to all task group kinds (tests, standard, checkpoint), not just tests.

## Non-Goals

- Algorithmically splitting groups post-generation (no rewriting of tasks.json
  after the LLM produces it).
- Changing per-session budget caps, retry limits, or the execution model.
- Modifying the task group state machine or node dispatch logic.

## Technical Specification

### Splitting Heuristics (Prompt Guidance)

Update `packages/agentspec/agentspec/_templates/prompts/generation_user_tasks.md`
to include:

1. **Test spec ref ceiling**: If a task group would reference more than 15 test
   spec entries (summed across all its subtasks' `test_spec_refs`), split it
   into smaller groups that each stay at or below 15.
2. **Subtask ceiling**: If a group would exceed 6 subtasks (excluding the
   verification subtask), split it.
3. **Complexity weighting**: Subtasks that involve complex logic (multiple file
   changes, cross-module coordination, or intricate assertion patterns) carry
   more weight. A group with 4 complex subtasks should be split even if it is
   under the numeric thresholds.
4. **Grouping strategy — by requirement**: When splitting, group subtasks by the
   requirement they trace to. Each resulting group covers a distinct set of
   requirements, producing cohesive groups where all subtasks relate to the same
   functional area.
5. **Test group kind preservation**: When a `kind: "tests"` group is split, all
   resulting groups retain `kind: "tests"`. The first split group remains
   group 1; subsequent groups get sequential IDs (2, 3, …).
6. **ID renumbering**: Non-test groups shift their IDs to follow the last test
   group. Subtask IDs within each group use the standard `{group_id}.{N}` format.

### Validation Warnings (afspec)

Add validation checks to `packages/afspec/afspec/validation.py`. These produce
**warnings**, not errors — they do not block `spec validate` from returning
`valid: true`.

1. **Oversized test spec refs**: Flag any task group where the total count of
   `test_spec_refs` across all subtasks exceeds 15.
2. **Too many subtasks**: Flag any task group with more than 6 subtasks
   (excluding the verification subtask).
3. **Single-subtask overload**: Flag any subtask that references more than 8
   test spec entries.

### Validation Output Model

Add a `ValidationWarning` model alongside the existing `ValidationError` to
distinguish warnings from errors. Update `validate()` to return both errors and
warnings. The CLI (`spec validate`) should display warnings but still report
`valid: true` when only warnings are present.

### Impact on Existing Rules

- The rule "first group must be `kind: tests`" remains unchanged. Multiple
  consecutive test groups are already allowed — the rule only checks
  `groups[0].kind == "tests"`.
- The existing "target 3–6 subtasks per group" guideline becomes a harder
  boundary in the prompt, backed by a validation warning.

## Tech Stack

- Python 3 (packages: `afspec`, `agentspec`)
- Pydantic models
- Prompt engineering (Markdown templates)
- pytest for testing

## Source

Source: https://github.com/agent-fox-dev/agent-fox/issues/625

## Clarifications

1. **Scope**: Splitting heuristics apply to all task group kinds, not just tests.
2. **Threshold metric**: Considers test spec ref count, subtask count, AND task
   complexity (complex subtasks count heavier).
3. **Implementation**: Both prompt guidance changes and validation warnings.
4. **Multiple test groups**: Split test groups all retain `kind: "tests"`.
5. **Grouping strategy**: Split by requirement for cohesive functional grouping.


