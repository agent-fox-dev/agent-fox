---
spec_id: '02'
spec_name: nightshift_prompt_alignment
title: Nightshift Prompt Alignment
status: draft
created_at: '2026-06-22T12:55:18.273601+00:00'
updated_at: '2026-06-22T12:55:18.273601+00:00'
owner: ''
source: interactive
schema_version: 1
---
# Night-Shift Prompt Alignment with Code Path

## Intent

Update the night-shift fix pipeline's prompt building to use
afspec-rendered context instead of ad-hoc criteria text, so the
night-shift coder receives the same structured prompt format as the
`code` command coder.

## Goals

1. Update `_build_coder_prompt()` in `fix_pipeline.py` to construct an
   in-memory afspec `Spec` from triage output and render it as the
   system prompt context.
2. Update `_build_reviewer_prompt()` to use the same afspec-rendered
   context so the reviewer verifies against structured requirements.
3. Replace the ad-hoc `_render_criteria_context()` with
   `render_inmemory_spec_sections()` from spec 01.
4. Ensure the task prompt references the same "tasks.md subtask list"
   phrasing used by the code-path `build_task_prompt()`.

## Non-Goals

- Changing the triage agent, its prompts, or its output format.
- Changing `assemble_context()` or the code-path flow.
- Modifying `InMemorySpec` dataclass fields.
- Adding disk persistence for night-shift specs.
- Changing the reviewer's parse format (the reviewer's JSON verdict
  report format stays the same).

## Background

After spec 01 provides `build_afspec_from_triage()` and
`render_inmemory_spec_sections()`, this spec wires them into the
fix pipeline's prompt building.

Currently in `fix_pipeline.py`:
- `_build_coder_prompt()` calls `_render_criteria_context(triage)` to
  produce flat markdown, then appends it to `spec.system_context` and
  passes the combined string to `build_system_prompt(context=...)`.
- `_build_reviewer_prompt()` does the same for the reviewer.
- `_render_criteria_context()` produces an ad-hoc `## Acceptance Criteria
  from Triage` section with flat field listings.

After this spec:
- `_build_coder_prompt()` calls `build_afspec_from_triage()` to get an
  afspec `Spec`, then `render_inmemory_spec_sections()` to get
  `## Requirements`, `## Test Specification`, `## Tasks` sections.
  These replace the ad-hoc criteria context.
- `_build_reviewer_prompt()` uses the same afspec-rendered sections.
- `_render_criteria_context()` and `_render_criteria_section()` are
  removed (dead code after this change).

## Tech Stack

- Python 3.12
- afspec library (in-memory construction from spec 01)
- Existing types: `TriageResult`, `InMemorySpec` from
  `agentfox.nightshift.fix_pipeline` and
  `agentfox.nightshift.spec_builder`

## Detailed Requirements

### Coder prompt changes

`_build_coder_prompt()` must:
1. Call `build_afspec_from_triage(spec, triage)` to construct an
   in-memory `Spec`.
2. Call `render_inmemory_spec_sections(afspec_spec)` to get markdown
   sections.
3. Join the issue body context with the rendered sections (issue body
   first, then spec sections — same order as the code path).
4. Pass the combined context to `build_system_prompt()`.
5. Update the task prompt to reference the subtask list format used by
   the code path: "Refer to the tasks subtask list in the context
   above."
6. Preserve existing behavior for `review_feedback` injection (reviewer
   feedback still appended to the task prompt on retry).
7. Preserve existing behavior for `prior_context` injection (prior
   attempt context still prepended to the task prompt).

### Reviewer prompt changes

`_build_reviewer_prompt()` must:
1. Use the same afspec-rendered context as the coder.
2. Preserve the existing task prompt structure: the reviewer is told
   to "verify each acceptance criterion" — this still applies since
   the criteria are now rendered as formal requirements.
3. Preserve the fallback for empty triage: when triage has no criteria,
   the reviewer still gets "No acceptance criteria were produced by
   triage. Verify the fix based on the issue description above."

### Dead code removal

After the integration:
- `_render_criteria_context()` in `fix_pipeline.py` is no longer called.
  Remove it.
- `_render_criteria_section()` in `fix_pipeline.py` is only used by
  `_render_criteria_context()` and `_format_triage_comment()`. Keep it
  only if `_format_triage_comment()` still needs it. If
  `_format_triage_comment()` can use afspec rendering instead, remove
  `_render_criteria_section()` too.

### Triage comment format

`_format_triage_comment()` posts a human-readable triage report as a
GitHub issue comment. This uses `_render_criteria_section()` with
`bold=True`. The triage comment is a GitHub-facing artifact, not a
coder prompt — it should keep its current human-friendly format. Do NOT
change `_format_triage_comment()` to use afspec rendering.

This means `_render_criteria_section()` is still needed and must be
preserved.

### Review comment and verdict format

`_format_review_comment()` and `FixReviewResult` are unchanged. The
reviewer's output format (JSON verdict report with per-criterion
verdicts) stays the same. The reviewer now verifies against afspec
requirements instead of ad-hoc criteria, but the verdict structure
(`criterion_id`, `verdict`, `evidence`) is unaffected.

### Error handling

- If `build_afspec_from_triage()` raises (malformed triage data), fall
  back to the current ad-hoc rendering via `_render_criteria_context()`.
  Log a warning but don't block the fix pipeline.
  
  NOTE: This requires keeping `_render_criteria_context()` as a private
  fallback rather than removing it. Mark it with a comment indicating
  it is a fallback only.

## Dependencies

| Spec | From Group | To Group | Relationship |
|------|-----------|----------|--------------|
| 01_nightshift_afspec_models | 1 | 1 | Uses build_afspec_from_triage() and render_inmemory_spec_sections() |

## Design Decisions

1. **Keep _render_criteria_section() for triage comments:** The GitHub
   triage comment is a human-facing artifact that benefits from the
   current bold-formatted layout. Replacing it with afspec rendering
   would produce a less readable comment. The rendering code is small
   and its purpose is distinct from prompt building.

2. **Fallback on afspec construction failure:** The fix pipeline must
   be resilient — a mapping failure in the new code path should not
   prevent the coder from running. Falling back to the ad-hoc format
   preserves existing behavior while logging for investigation.

3. **Task prompt alignment:** Using similar phrasing to the code-path
   task prompt ("Refer to the tasks subtask list in the context above")
   ensures consistency. The coder doesn't need to know whether it's
   running in code or night-shift mode — the context structure is the
   same.

4. **Don't change reviewer verdict format:** The reviewer's JSON output
   uses `criterion_id` which currently matches `AcceptanceCriterion.id`.
   After the change, the requirements have different IDs (e.g.
   "NS-REQ-1" instead of "AC-1"). The reviewer will naturally reference
   whichever IDs appear in the context. The verdict parser
   (`parse_fix_review_output`) does not validate criterion IDs against
   a known set — it accepts whatever the reviewer produces.

## Source

Source: Input provided by user via interactive prompt (based on prior
analysis of night-shift vs code command architecture)

