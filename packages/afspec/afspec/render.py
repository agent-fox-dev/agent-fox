"""Deterministic markdown rendering for spec artifacts.

Renders Requirements, TestSpec, and Tasks Pydantic models to markdown
strings. The output is deterministic — identical inputs always produce
byte-identical output. The rendering format matches the Go implementation
for cross-implementation compatibility.
"""

from __future__ import annotations

import json
import logging
import re
from typing import Any

from afspec.ears import render_ears_sentence
from afspec.models import (
    Requirements,
    Spec,
    SubtaskState,
    TaskGroup,
    Tasks,
    TestSpec,
)

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Helper: format JSON value for display in markdown
# ---------------------------------------------------------------------------


def _format_json_value(value: Any) -> str:
    """Format a value as a JSON string for display in markdown."""
    if value is None:
        return "null"
    return json.dumps(value, indent=2, sort_keys=True)


# ---------------------------------------------------------------------------
# render_requirements
# ---------------------------------------------------------------------------


def render_requirements(req: Requirements) -> str:
    """Render requirements to markdown.

    Produces a markdown string containing the introduction, glossary table,
    each requirement with EARS-rendered acceptance criteria and edge cases,
    correctness properties, execution paths, and error handling table.
    """
    lines: list[str] = []

    # Title
    lines.append(f"# Requirements: {req.spec_name}")
    lines.append("")

    # Introduction
    lines.append("## Introduction")
    lines.append("")
    lines.append(req.introduction)
    lines.append("")

    # Glossary
    lines.append("## Glossary")
    lines.append("")
    lines.append("| Term | Definition |")
    lines.append("|------|-----------|")
    for term in sorted(req.glossary.keys()):
        lines.append(f"| {term} | {req.glossary[term]} |")
    lines.append("")

    # Requirements
    lines.append("## Requirements")
    lines.append("")

    for r in req.requirements:
        lines.append(f"### {r.id}: {r.title}")
        lines.append("")

        # User story
        lines.append(
            f"**User Story:** As a {r.user_story.role}, I want {r.user_story.goal}, so that {r.user_story.benefit}."
        )
        lines.append("")

        # Acceptance criteria
        if r.acceptance_criteria:
            lines.append("#### Acceptance Criteria")
            lines.append("")
            for i, c in enumerate(r.acceptance_criteria, 1):
                sentence = render_ears_sentence(c)
                lines.append(f"{i}. [{c.id}] {sentence}")
            lines.append("")

        # Edge cases
        if r.edge_cases:
            lines.append("#### Edge Cases")
            lines.append("")
            for i, c in enumerate(r.edge_cases, 1):
                sentence = render_ears_sentence(c)
                lines.append(f"{i}. [{c.id}] {sentence}")
            lines.append("")

    # Correctness Properties
    lines.append("## Correctness Properties")
    lines.append("")

    for prop in req.correctness_properties:
        lines.append(f"### {prop.id}: {prop.title}")
        lines.append("")
        lines.append(f"*For any* {prop.for_any}")
        lines.append(f"*Invariant:* {prop.invariant}")
        lines.append("")
        if prop.validates:
            lines.append(f"**Validates:** {', '.join(prop.validates)}")
            lines.append("")

    # Execution Paths
    lines.append("## Execution Paths")
    lines.append("")

    for path in req.execution_paths:
        lines.append(f"### {path.id}: {path.title}")
        lines.append("")
        if path.steps:
            for i, step in enumerate(path.steps, 1):
                lines.append(f"{i}. **{step.actor}** {step.action}")
            lines.append("")

    # Error Handling
    lines.append("## Error Handling")
    lines.append("")
    lines.append("| ID | Condition | Behavior | Requirement |")
    lines.append("|----|-----------|----------|-------------|")
    for entry in req.error_handling:
        lines.append(f"| {entry.id} | {entry.condition} | {entry.behavior} | {entry.requirement_id} |")
    lines.append("")

    if req.external_apis:
        lines.append("## External APIs")
        lines.append("")
        for api in req.external_apis:
            lines.append(f"### `{api.package}` (v{api.version})")
            lines.append("")
            lines.append("| Symbol | Import Path | Signature | Notes |")
            lines.append("|--------|-------------|-----------|-------|")
            for sym in api.symbols:
                notes = sym.notes or ""
                lines.append(f"| `{sym.name}` | `{sym.import_path}` | `{sym.signature}` | {notes} |")
            lines.append("")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# render_test_spec
# ---------------------------------------------------------------------------


def render_test_spec(ts: TestSpec) -> str:
    """Render test spec to markdown.

    Produces a markdown string containing test cases, property tests,
    edge case tests, smoke tests, and a coverage summary.
    """
    lines: list[str] = []

    # Title
    lines.append(f"# Test Specification: {ts.spec_name}")
    lines.append("")

    # Test Cases
    lines.append("## Test Cases")
    lines.append("")

    for tc in ts.test_cases:
        lines.append(f"### {tc.id}: {tc.description}")
        lines.append("")
        lines.append(f"**Requirement:** {tc.requirement_id}")
        lines.append(f"**Type:** {tc.kind}")
        lines.append("")
        if tc.preconditions:
            lines.append("**Preconditions:**")
            lines.append("")
            for pre in tc.preconditions:
                lines.append(f"- {pre}")
            lines.append("")
        if tc.input is not None:
            lines.append(f"**Input:** `{_format_json_value(tc.input)}`")
            lines.append("")
        if tc.expected is not None:
            lines.append(f"**Expected:** `{_format_json_value(tc.expected)}`")
            lines.append("")
        if tc.assertion_pseudocode:
            lines.append("**Assertion pseudocode:**")
            lines.append("")
            lines.append("```")
            lines.append(tc.assertion_pseudocode)
            lines.append("```")
            lines.append("")

    # Property Tests
    lines.append("## Property Tests")
    lines.append("")

    for pt in ts.property_tests:
        lines.append(f"### {pt.id}: {pt.description}")
        lines.append("")
        lines.append(f"**Property:** {pt.property_id}")
        lines.append("")
        if pt.validates:
            lines.append(f"**Validates:** {', '.join(pt.validates)}")
            lines.append("")
        if pt.for_any_strategy:
            lines.append(f"**For any:** {pt.for_any_strategy}")
            lines.append("")
        if pt.invariant_check:
            lines.append(f"**Invariant check:** {pt.invariant_check}")
            lines.append("")

    # Edge Case Tests
    lines.append("## Edge Case Tests")
    lines.append("")

    for et in ts.edge_case_tests:
        lines.append(f"### {et.id}: {et.description}")
        lines.append("")
        lines.append(f"**Requirement:** {et.requirement_id}")
        lines.append(f"**Type:** {et.kind}")
        lines.append("")
        if et.preconditions:
            lines.append("**Preconditions:**")
            lines.append("")
            for pre in et.preconditions:
                lines.append(f"- {pre}")
            lines.append("")
        if et.input is not None:
            lines.append(f"**Input:** `{_format_json_value(et.input)}`")
            lines.append("")
        if et.expected is not None:
            lines.append(f"**Expected:** `{_format_json_value(et.expected)}`")
            lines.append("")
        if et.assertion_pseudocode:
            lines.append("**Assertion pseudocode:**")
            lines.append("")
            lines.append("```")
            lines.append(et.assertion_pseudocode)
            lines.append("```")
            lines.append("")

    # Smoke Tests
    lines.append("## Smoke Tests")
    lines.append("")

    for st in ts.smoke_tests:
        lines.append(f"### {st.id}: {st.description}")
        lines.append("")
        lines.append(f"**Execution Path:** {st.execution_path_id}")
        lines.append("")
        if st.trigger:
            lines.append(f"**Trigger:** `{st.trigger}`")
            lines.append("")
        if st.real_components:
            lines.append(f"**Real components:** {', '.join(st.real_components)}")
            lines.append("")
        if st.mockable:
            lines.append(f"**Mockable:** {', '.join(st.mockable)}")
            lines.append("")
        if st.expected_effects:
            lines.append("**Expected effects:**")
            lines.append("")
            for effect in st.expected_effects:
                lines.append(f"- {effect}")
            lines.append("")

    # Coverage
    lines.append("## Coverage")
    lines.append("")
    if ts.coverage.requirements_covered:
        lines.append(f"**Requirements covered:** {', '.join(ts.coverage.requirements_covered)}")
        lines.append("")
    if ts.coverage.properties_covered:
        lines.append(f"**Properties covered:** {', '.join(ts.coverage.properties_covered)}")
        lines.append("")
    if ts.coverage.paths_covered:
        lines.append(f"**Paths covered:** {', '.join(ts.coverage.paths_covered)}")
        lines.append("")
    if ts.coverage.gaps:
        lines.append(f"**Gaps:** {', '.join(ts.coverage.gaps)}")
        lines.append("")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Checkbox mapping for subtask states
# ---------------------------------------------------------------------------

_CHECKBOX_MAP: dict[SubtaskState, str] = {
    SubtaskState.PENDING: "[ ]",
    SubtaskState.QUEUED: "[~]",
    SubtaskState.IN_PROGRESS: "[-]",
    SubtaskState.DONE: "[x]",
    SubtaskState.PENDING_REEVALUATION: "[?]",
    # DROPPED subtasks are omitted from output entirely
}


# ---------------------------------------------------------------------------
# render_tasks
# ---------------------------------------------------------------------------


def render_tasks(t: Tasks) -> str:
    """Render tasks to markdown.

    Produces a markdown string containing test commands, dependencies table,
    task groups with checkbox-formatted subtasks, and a traceability table.
    Dropped subtasks are omitted from output. Optional subtasks append '*'
    after the checkbox.
    """
    lines: list[str] = []

    # Title
    lines.append(f"# Implementation Plan: {t.spec_name}")
    lines.append("")

    # Test Commands
    lines.append("## Test Commands")
    lines.append("")
    lines.append(f"- Spec tests: `{t.test_commands.spec_tests}`")
    lines.append(f"- All tests: `{t.test_commands.all_tests}`")
    lines.append(f"- Linter: `{t.test_commands.linter}`")
    lines.append("")

    # Dependencies
    if t.dependencies:
        lines.append("## Dependencies")
        lines.append("")
        lines.append("| Depends On | From Group | To Group | Relationship |")
        lines.append("|------------|-----------|----------|--------------|")
        for dep in t.dependencies:
            lines.append(f"| {dep.depends_on_spec} | {dep.from_group} | {dep.to_group} | {dep.relationship} |")
        lines.append("")

    # Tasks (task groups with subtasks)
    lines.append("## Tasks")
    lines.append("")

    for group in t.task_groups:
        # Determine group checkbox: done if all non-dropped subtasks are done
        non_dropped = [s for s in group.subtasks if s.state != SubtaskState.DROPPED]
        all_done = len(non_dropped) > 0 and all(s.state == SubtaskState.DONE for s in non_dropped)
        group_checkbox = "[x]" if all_done else "[ ]"
        lines.append(f"- {group_checkbox} {group.id}. {group.title}")

        # Subtasks
        for subtask in group.subtasks:
            # Skip dropped subtasks
            if subtask.state == SubtaskState.DROPPED:
                continue

            checkbox = _CHECKBOX_MAP[subtask.state]
            # Optional subtasks append '*' after the checkbox
            opt_marker = "*" if subtask.optional else ""
            lines.append(f"  - {checkbox}{opt_marker} {subtask.id} {subtask.title}")

            # Details
            for detail in subtask.details:
                lines.append(f"    - {detail}")

            # Test spec refs
            if subtask.test_spec_refs:
                refs = ", ".join(subtask.test_spec_refs)
                lines.append(f"    - _Test Spec: {refs}_")

            # Requirement refs
            if subtask.requirement_refs:
                refs = ", ".join(subtask.requirement_refs)
                lines.append(f"    - _Requirements: {refs}_")

        # Verification subtask
        if group.verification.id:
            # Determine verification checkbox: done if all subtasks done
            ver_checkbox = "[x]" if all_done else "[ ]"
            lines.append(f"  - {ver_checkbox} {group.verification.id} Verify task group {group.id}")
            for check in group.verification.checks:
                lines.append(f"    - {check}")

        lines.append("")

    # Traceability
    lines.append("## Traceability")
    lines.append("")
    lines.append("| Requirement | Test Spec Entry | Task | Test Path |")
    lines.append("|-------------|-----------------|------|-----------|")
    for entry in t.traceability:
        test_path = entry.test_path if entry.test_path is not None else "null"
        lines.append(f"| {entry.requirement_id} | {entry.test_spec_id} | {entry.task_id} | {test_path} |")
    lines.append("")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# render_combined
# ---------------------------------------------------------------------------


def _requirement_matches_refs(r: Any, ref_ids: set[str]) -> bool:
    """Check if a requirement or any of its criteria match the ref set.

    Subtask ``requirement_refs`` reference acceptance criteria IDs
    (e.g. ``01-REQ-1.1``), not requirement IDs (``01-REQ-1``).  This
    helper checks the requirement ID and all of its criteria/edge-case
    IDs against *ref_ids*.
    """
    if r.id in ref_ids:
        return True
    for c in r.acceptance_criteria:
        if c.id in ref_ids:
            return True
    for c in r.edge_cases:
        if c.id in ref_ids:
            return True
    return False


def render_requirements_scoped(
    req: Requirements,
    requirement_refs: set[str],
    execution_path_ids: set[str] | None = None,
) -> str:
    """Render requirements filtered to a subset of requirement refs.

    *requirement_refs* may contain requirement IDs (``01-REQ-1``) or
    acceptance criteria / edge-case IDs (``01-REQ-1.1``).  A requirement
    is included when it or any of its criteria match.

    Includes a Spec Overview listing all requirement IDs and titles for
    orientation, followed by full content only for matching requirements.
    Correctness properties and error handling entries are filtered to
    those linked to the in-scope requirements.

    When *execution_path_ids* is provided (not ``None``), execution paths
    are filtered to those whose IDs are in the set; omitted paths are
    replaced with a one-line summary.  When ``None``, all paths are
    rendered (backward compatibility).

    External APIs are filtered to those whose package name appears in the
    acceptance criteria text of in-scope requirements.
    """
    lines: list[str] = []

    lines.append(f"# Requirements: {req.spec_name}")
    lines.append("")

    filtered = [r for r in req.requirements if _requirement_matches_refs(r, requirement_refs)]
    filtered_ids: set[str] = set()
    for r in filtered:
        filtered_ids.add(r.id)
        for c in r.acceptance_criteria:
            filtered_ids.add(c.id)
        for c in r.edge_cases:
            filtered_ids.add(c.id)

    included_ids = [r.id for r in req.requirements if r.id in filtered_ids]
    total_count = len(req.requirements)
    lines.append("## Spec Overview")
    lines.append("")
    lines.append(
        f"This group covers {len(included_ids)} of {total_count} requirements: "
        f"{', '.join(included_ids)}."
    )
    lines.append("")

    lines.append("## Introduction")
    lines.append("")
    lines.append(req.introduction)
    lines.append("")

    lines.append("## Glossary")
    lines.append("")
    lines.append("| Term | Definition |")
    lines.append("|------|-----------|")
    for term in sorted(req.glossary.keys()):
        lines.append(f"| {term} | {req.glossary[term]} |")
    lines.append("")

    lines.append("## Requirements")
    lines.append("")
    for r in filtered:
        lines.append(f"### {r.id}: {r.title}")
        lines.append("")
        lines.append(
            f"**User Story:** As a {r.user_story.role}, I want {r.user_story.goal}, so that {r.user_story.benefit}."
        )
        lines.append("")
        if r.acceptance_criteria:
            lines.append("#### Acceptance Criteria")
            lines.append("")
            for i, c in enumerate(r.acceptance_criteria, 1):
                sentence = render_ears_sentence(c)
                lines.append(f"{i}. [{c.id}] {sentence}")
            lines.append("")
        if r.edge_cases:
            lines.append("#### Edge Cases")
            lines.append("")
            for i, c in enumerate(r.edge_cases, 1):
                sentence = render_ears_sentence(c)
                lines.append(f"{i}. [{c.id}] {sentence}")
            lines.append("")

    # Include design context sections — filter to scope where linkage exists
    lines.append("## Correctness Properties")
    lines.append("")
    filtered_props = [
        prop for prop in req.correctness_properties if not prop.validates or set(prop.validates) & filtered_ids
    ]
    for prop in filtered_props:
        lines.append(f"### {prop.id}: {prop.title}")
        lines.append("")
        lines.append(f"*For any* {prop.for_any}")
        lines.append(f"*Invariant:* {prop.invariant}")
        lines.append("")
        if prop.validates:
            lines.append(f"**Validates:** {', '.join(prop.validates)}")
            lines.append("")
    omitted_props = len(req.correctness_properties) - len(filtered_props)
    if omitted_props > 0:
        lines.append(f"({omitted_props} additional correctness properties omitted — see full spec for details)")
        lines.append("")

    lines.append("## Execution Paths")
    lines.append("")
    if execution_path_ids is not None:
        in_scope_paths = [p for p in req.execution_paths if p.id in execution_path_ids]
        omitted_path_count = len(req.execution_paths) - len(in_scope_paths)
        for path in in_scope_paths:
            lines.append(f"### {path.id}: {path.title}")
            lines.append("")
            if path.steps:
                for i, step in enumerate(path.steps, 1):
                    lines.append(f"{i}. **{step.actor}** {step.action}")
                lines.append("")
        if omitted_path_count > 0:
            lines.append(
                f"({omitted_path_count} execution paths defined (see full spec for details))"
            )
            lines.append("")
    else:
        for path in req.execution_paths:
            lines.append(f"### {path.id}: {path.title}")
            lines.append("")
            if path.steps:
                for i, step in enumerate(path.steps, 1):
                    lines.append(f"{i}. **{step.actor}** {step.action}")
                lines.append("")

    lines.append("## Error Handling")
    lines.append("")
    lines.append("| ID | Condition | Behavior | Requirement |")
    lines.append("|----|-----------|----------|-------------|")
    filtered_errors = [
        entry for entry in req.error_handling if not entry.requirement_id or entry.requirement_id in filtered_ids
    ]
    for entry in filtered_errors:
        lines.append(f"| {entry.id} | {entry.condition} | {entry.behavior} | {entry.requirement_id} |")
    lines.append("")
    omitted_errors = len(req.error_handling) - len(filtered_errors)
    if omitted_errors > 0:
        lines.append(f"({omitted_errors} additional error handling entries omitted — see full spec for details)")
        lines.append("")

    if req.external_apis:
        # Filter external APIs to those whose package name appears in
        # the acceptance criteria text of in-scope requirements.
        _ac_text_parts: list[str] = []
        for r in filtered:
            for c in r.acceptance_criteria:
                _ac_text_parts.append(render_ears_sentence(c))
            for c in r.edge_cases:
                _ac_text_parts.append(render_ears_sentence(c))
        _combined_ac_text = " ".join(_ac_text_parts)

        matched_apis = [api for api in req.external_apis if api.package in _combined_ac_text]
        omitted_api_count = len(req.external_apis) - len(matched_apis)

        lines.append("## External APIs")
        lines.append("")
        for api in matched_apis:
            lines.append(f"### `{api.package}` (v{api.version})")
            lines.append("")
            lines.append("| Symbol | Import Path | Signature | Notes |")
            lines.append("|--------|-------------|-----------|-------|")
            for sym in api.symbols:
                notes = sym.notes or ""
                lines.append(f"| `{sym.name}` | `{sym.import_path}` | `{sym.signature}` | {notes} |")
            lines.append("")
        if omitted_api_count > 0:
            lines.append(
                f"({omitted_api_count} external API packages omitted — see full spec for details)"
            )
            lines.append("")

    return "\n".join(lines)


def render_test_spec_scoped(ts: TestSpec, test_spec_ids: set[str]) -> str:
    """Render test spec filtered to a subset of test spec IDs.

    Only includes test cases, property tests, edge case tests, and
    smoke tests whose IDs are in *test_spec_ids*. Coverage section
    is included in full.
    """
    lines: list[str] = []

    lines.append(f"# Test Specification: {ts.spec_name}")
    lines.append("")

    lines.append("## Test Cases")
    lines.append("")
    filtered_tc = [tc for tc in ts.test_cases if tc.id in test_spec_ids]
    for tc in filtered_tc:
        lines.append(f"### {tc.id}: {tc.description}")
        lines.append("")
        lines.append(f"**Requirement:** {tc.requirement_id}")
        lines.append(f"**Type:** {tc.kind}")
        lines.append("")
        if tc.preconditions:
            lines.append("**Preconditions:**")
            lines.append("")
            for pre in tc.preconditions:
                lines.append(f"- {pre}")
            lines.append("")
        if tc.input is not None:
            lines.append(f"**Input:** `{_format_json_value(tc.input)}`")
            lines.append("")
        if tc.expected is not None:
            lines.append(f"**Expected:** `{_format_json_value(tc.expected)}`")
            lines.append("")
        if tc.assertion_pseudocode:
            lines.append("**Assertion pseudocode:**")
            lines.append("")
            lines.append("```")
            lines.append(tc.assertion_pseudocode)
            lines.append("```")
            lines.append("")

    lines.append("## Property Tests")
    lines.append("")
    filtered_pt = [pt for pt in ts.property_tests if pt.id in test_spec_ids]
    for pt in filtered_pt:
        lines.append(f"### {pt.id}: {pt.description}")
        lines.append("")
        lines.append(f"**Property:** {pt.property_id}")
        lines.append("")
        if pt.validates:
            lines.append(f"**Validates:** {', '.join(pt.validates)}")
            lines.append("")
        if pt.for_any_strategy:
            lines.append(f"**For any:** {pt.for_any_strategy}")
            lines.append("")
        if pt.invariant_check:
            lines.append(f"**Invariant check:** {pt.invariant_check}")
            lines.append("")

    lines.append("## Edge Case Tests")
    lines.append("")
    filtered_et = [et for et in ts.edge_case_tests if et.id in test_spec_ids]
    for et in filtered_et:
        lines.append(f"### {et.id}: {et.description}")
        lines.append("")
        lines.append(f"**Requirement:** {et.requirement_id}")
        lines.append(f"**Type:** {et.kind}")
        lines.append("")
        if et.preconditions:
            lines.append("**Preconditions:**")
            lines.append("")
            for pre in et.preconditions:
                lines.append(f"- {pre}")
            lines.append("")
        if et.input is not None:
            lines.append(f"**Input:** `{_format_json_value(et.input)}`")
            lines.append("")
        if et.expected is not None:
            lines.append(f"**Expected:** `{_format_json_value(et.expected)}`")
            lines.append("")
        if et.assertion_pseudocode:
            lines.append("**Assertion pseudocode:**")
            lines.append("")
            lines.append("```")
            lines.append(et.assertion_pseudocode)
            lines.append("```")
            lines.append("")

    lines.append("## Smoke Tests")
    lines.append("")
    filtered_st = [st for st in ts.smoke_tests if st.id in test_spec_ids]
    for st in filtered_st:
        lines.append(f"### {st.id}: {st.description}")
        lines.append("")
        lines.append(f"**Execution Path:** {st.execution_path_id}")
        lines.append("")
        if st.trigger:
            lines.append(f"**Trigger:** `{st.trigger}`")
            lines.append("")
        if st.real_components:
            lines.append(f"**Real components:** {', '.join(st.real_components)}")
            lines.append("")
        if st.mockable:
            lines.append(f"**Mockable:** {', '.join(st.mockable)}")
            lines.append("")
        if st.expected_effects:
            lines.append("**Expected effects:**")
            lines.append("")
            for effect in st.expected_effects:
                lines.append(f"- {effect}")
            lines.append("")

    lines.append("## Coverage")
    lines.append("")
    lines.append("_(Full coverage summary omitted in scoped view — see traceability table.)_")
    lines.append("")

    return "\n".join(lines)


def render_tasks_scoped(t: Tasks, target_group: int) -> str:
    """Render tasks with the target group in full detail and others as summaries.

    Test Commands and Dependencies sections are always included.
    The target group shows full subtask detail. Other groups show
    only group checkbox and title as one-line summaries.
    Traceability section is included in full.
    """
    lines: list[str] = []

    lines.append(f"# Implementation Plan: {t.spec_name}")
    lines.append("")

    lines.append("## Test Commands")
    lines.append("")
    lines.append(f"- Spec tests: `{t.test_commands.spec_tests}`")
    lines.append(f"- All tests: `{t.test_commands.all_tests}`")
    lines.append(f"- Linter: `{t.test_commands.linter}`")
    lines.append("")

    if t.dependencies:
        lines.append("## Dependencies")
        lines.append("")
        lines.append("| Depends On | From Group | To Group | Relationship |")
        lines.append("|------------|-----------|----------|--------------|")
        for dep in t.dependencies:
            lines.append(f"| {dep.depends_on_spec} | {dep.from_group} | {dep.to_group} | {dep.relationship} |")
        lines.append("")

    lines.append("## Tasks")
    lines.append("")

    for group in t.task_groups:
        non_dropped = [s for s in group.subtasks if s.state != SubtaskState.DROPPED]
        all_done = len(non_dropped) > 0 and all(s.state == SubtaskState.DONE for s in non_dropped)
        group_checkbox = "[x]" if all_done else "[ ]"

        if group.id == target_group:
            # Full detail for target group
            lines.append(f"- {group_checkbox} {group.id}. {group.title}")
            for subtask in group.subtasks:
                if subtask.state == SubtaskState.DROPPED:
                    continue
                checkbox = _CHECKBOX_MAP[subtask.state]
                opt_marker = "*" if subtask.optional else ""
                lines.append(f"  - {checkbox}{opt_marker} {subtask.id} {subtask.title}")
                for detail in subtask.details:
                    lines.append(f"    - {detail}")
                if subtask.test_spec_refs:
                    refs = ", ".join(subtask.test_spec_refs)
                    lines.append(f"    - _Test Spec: {refs}_")
                if subtask.requirement_refs:
                    refs = ", ".join(subtask.requirement_refs)
                    lines.append(f"    - _Requirements: {refs}_")
            if group.verification.id:
                ver_checkbox = "[x]" if all_done else "[ ]"
                lines.append(f"  - {ver_checkbox} {group.verification.id} Verify task group {group.id}")
                for check in group.verification.checks:
                    lines.append(f"    - {check}")
        else:
            # One-line summary with completion count for other groups
            done_count = sum(1 for s in non_dropped if s.state == SubtaskState.DONE)
            total_count = len(non_dropped)
            lines.append(f"- {group_checkbox} {group.id}. {group.title} ({done_count}/{total_count} subtasks done)")

        lines.append("")

    target_prefix = f"{target_group}."
    group_entries = [e for e in t.traceability if e.task_id.startswith(target_prefix)]
    omitted = len(t.traceability) - len(group_entries)

    lines.append("## Traceability")
    lines.append("")
    lines.append("| Requirement | Test Spec Entry | Task | Test Path |")
    lines.append("|-------------|-----------------|------|-----------|")
    for entry in group_entries:
        test_path = entry.test_path if entry.test_path is not None else "null"
        lines.append(f"| {entry.requirement_id} | {entry.test_spec_id} | {entry.task_id} | {test_path} |")
    if omitted:
        lines.append(f"\n_({omitted} traceability rows for other groups omitted.)_")
    lines.append("")

    return "\n".join(lines)


_REQ_ID_RE = re.compile(r"\b(\w+-REQ-\d+(?:\.\d+|\.E\d+)?)\b")
_TS_ID_RE = re.compile(r"\b(TS-\w+-(?:\d+|P\d+|E\d+|SMOKE-\d+))\b")


def _infer_refs_from_traceability(
    spec: Spec,
    target_group: int,
) -> tuple[set[str], set[str]]:
    """Infer requirement and test spec refs from the traceability table.

    Looks up traceability entries whose ``task_id`` belongs to the target
    group (prefix ``"{target_group}."``) and collects the associated
    ``requirement_id`` and ``test_spec_id`` values.
    """
    prefix = f"{target_group}."
    requirement_ids: set[str] = set()
    test_spec_ids: set[str] = set()
    for entry in spec.tasks.traceability:
        if entry.task_id.startswith(prefix):
            if entry.requirement_id:
                requirement_ids.add(entry.requirement_id)
            if entry.test_spec_id:
                test_spec_ids.add(entry.test_spec_id)
    return requirement_ids, test_spec_ids


def _infer_refs_from_subtask_text(
    spec: Spec,
    group: TaskGroup,
) -> tuple[set[str], set[str]]:
    """Infer refs by scanning subtask title and details for ID patterns.

    Matches found IDs against the actual requirement and test spec IDs
    present in the spec to avoid false positives.
    """
    all_req_ids: set[str] = {r.id for r in spec.requirements.requirements}
    for r in spec.requirements.requirements:
        for c in r.acceptance_criteria:
            all_req_ids.add(c.id)
        for c in r.edge_cases:
            all_req_ids.add(c.id)

    all_ts_ids: set[str] = set()
    for tc in spec.test_spec.test_cases:
        all_ts_ids.add(tc.id)
    for pt in spec.test_spec.property_tests:
        all_ts_ids.add(pt.id)
    for et in spec.test_spec.edge_case_tests:
        all_ts_ids.add(et.id)
    for st in spec.test_spec.smoke_tests:
        all_ts_ids.add(st.id)

    requirement_ids: set[str] = set()
    test_spec_ids: set[str] = set()

    for subtask in group.subtasks:
        text_parts = [subtask.title] + subtask.details
        for text in text_parts:
            for match in _REQ_ID_RE.finditer(text):
                candidate = match.group(1)
                if candidate in all_req_ids:
                    requirement_ids.add(candidate)
            for match in _TS_ID_RE.finditer(text):
                candidate = match.group(1)
                if candidate in all_ts_ids:
                    test_spec_ids.add(candidate)

    return requirement_ids, test_spec_ids


def render_individual_scoped(spec: Spec, target_group: int) -> dict[str, str]:
    """Render each spec artifact scoped to a target task group.

    Collects ``requirement_refs`` and ``test_spec_refs`` from the target
    group's subtasks and filters requirements/test specs accordingly.
    When subtask refs are empty, attempts inference from the traceability
    table and subtask text before falling back to unscoped rendering.
    """
    group = None
    for tg in spec.tasks.task_groups:
        if tg.id == target_group:
            group = tg
            break

    if group is None:
        return render_individual(spec)

    requirement_ids: set[str] = set()
    test_spec_ids: set[str] = set()
    for subtask in group.subtasks:
        requirement_ids.update(subtask.requirement_refs)
        test_spec_ids.update(subtask.test_spec_refs)

    if not requirement_ids and not test_spec_ids:
        inferred_req, inferred_ts = _infer_refs_from_traceability(spec, target_group)
        if not inferred_req and not inferred_ts:
            inferred_req, inferred_ts = _infer_refs_from_subtask_text(spec, group)
        if inferred_req or inferred_ts:
            logger.info(
                "Inferred %d requirement_refs and %d test_spec_refs for group %d "
                "(subtasks had no refs)",
                len(inferred_req), len(inferred_ts), target_group,
            )
            requirement_ids = inferred_req
            test_spec_ids = inferred_ts
        else:
            result = render_individual(spec)
            result["tasks"] = render_tasks_scoped(spec.tasks, target_group)
            return result

    # Compute execution_path_ids from in-scope smoke tests
    execution_path_ids: set[str] = set()
    for st in spec.test_spec.smoke_tests:
        if st.id in test_spec_ids and st.execution_path_id:
            execution_path_ids.add(st.execution_path_id)

    result: dict[str, str] = {}
    result["prd"] = spec.prd.body
    if spec.architecture is not None:
        result["architecture"] = spec.architecture

    if requirement_ids:
        result["requirements"] = render_requirements_scoped(
            spec.requirements, requirement_ids, execution_path_ids=execution_path_ids
        )
    else:
        result["requirements"] = render_requirements(spec.requirements)

    if test_spec_ids:
        result["test_spec"] = render_test_spec_scoped(spec.test_spec, test_spec_ids)
    else:
        result["test_spec"] = render_test_spec(spec.test_spec)

    result["tasks"] = render_tasks_scoped(spec.tasks, target_group)

    return result


def render_individual(spec: Spec) -> dict[str, str]:
    """Render each spec artifact to its own markdown string.

    Returns a dict mapping artifact name to rendered markdown.
    The PRD body is included as-is; requirements, test_spec, and tasks
    are rendered via their respective renderers. Architecture is included
    when present.
    """
    result: dict[str, str] = {}
    result["prd"] = spec.prd.body
    if spec.architecture is not None:
        result["architecture"] = spec.architecture
    result["requirements"] = render_requirements(spec.requirements)
    result["test_spec"] = render_test_spec(spec.test_spec)
    result["tasks"] = render_tasks(spec.tasks)
    return result


def render_combined(spec: Spec) -> str:
    """Render all spec artifacts to a single markdown document.

    Produces the PRD body (as-is) followed by rendered requirements,
    test spec, and tasks (in that order), separated by horizontal rules.
    """
    parts: list[str] = []

    # PRD body (as-is)
    parts.append(spec.prd.body.rstrip())

    # Separator
    parts.append("")
    parts.append("---")
    parts.append("")

    # Architecture (optional)
    if spec.architecture is not None:
        parts.append(spec.architecture.rstrip())
        parts.append("")
        parts.append("---")
        parts.append("")

    # Rendered requirements
    parts.append(render_requirements(spec.requirements).rstrip())

    # Separator
    parts.append("")
    parts.append("---")
    parts.append("")

    # Rendered test spec
    parts.append(render_test_spec(spec.test_spec).rstrip())

    # Separator
    parts.append("")
    parts.append("---")
    parts.append("")

    # Rendered tasks
    parts.append(render_tasks(spec.tasks).rstrip())
    parts.append("")

    return "\n".join(parts)
