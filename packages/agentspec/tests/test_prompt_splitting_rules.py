"""Tests for splitting heuristic rules in generation_user_tasks.md.

Covers TS-08-1, TS-08-2, TS-08-4, TS-08-5, TS-08-7, TS-08-8,
TS-08-9, TS-08-10, TS-08-11, TS-08-E3, TS-08-E4.
"""

from __future__ import annotations

import re

from agentspec.prompt_loader import load_prompt

# Resolve the prompt content once — all tests share the same file.
_PROMPT_NAME = "generation_user_tasks"


def _load_prompt_content() -> str:
    """Load the generation_user_tasks.md prompt template content."""
    return load_prompt(_PROMPT_NAME)


def _find_rule_section(content: str) -> str:
    """Extract the splitting-rules section from the prompt content.

    Returns the full content if no clearly delineated section is found,
    so that assertions still run against the entire document.
    """
    # Look for a heading that signals the splitting rules section.
    # Accept variations like "Splitting Rules", "Task Group Splitting Rules", etc.
    pattern = re.compile(
        r"(^#{1,4}\s+.*(?:split|splitting).*$)",
        re.IGNORECASE | re.MULTILINE,
    )
    match = pattern.search(content)
    if match:
        # Return everything from the heading to the end-of-file
        # (or the next same-level heading, but full remainder is safe).
        return content[match.start() :]
    return content


# ===================================================================
# TS-08-1: test_spec_refs ceiling rule (15) present in prompt
# Requirement: 08-REQ-1.1
# ===================================================================


class TestTestSpecRefsCeiling:
    """Verify the 15 test_spec_refs ceiling rule exists in the prompt."""

    def test_contains_15_threshold(self) -> None:
        """TS-08-1: The prompt contains an explicit '15' threshold
        for test_spec_refs and mentions splitting."""
        content = _load_prompt_content()
        assert "15" in content, (
            "generation_user_tasks.md must contain the number '15' as the test_spec_refs ceiling threshold"
        )

    def test_test_spec_refs_and_split_in_same_section(self) -> None:
        """TS-08-1: 'test_spec_refs' and 'split' appear together
        in the same rule block."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()
        assert "test_spec_refs" in lower, "The splitting rules section must reference 'test_spec_refs'"
        assert "split" in lower, "The splitting rules section must mention 'split'"

    def test_15_and_test_spec_refs_in_proximity(self) -> None:
        """TS-08-1: The '15' threshold and 'test_spec_refs' appear in
        the same paragraph or rule block, confirming they are linked."""
        content = _load_prompt_content()
        # Find all paragraphs (blocks separated by blank lines)
        paragraphs = re.split(r"\n\s*\n", content)
        found = any("15" in para and "test_spec_refs" in para.lower() for para in paragraphs)
        assert found, "'15' and 'test_spec_refs' must appear in the same paragraph to form a coherent ceiling rule"


# ===================================================================
# TS-08-2: 15-refs ceiling applies to ALL kind values
# Requirement: 08-REQ-1.2
# ===================================================================


class TestTestSpecRefsCeilingUniversalKind:
    """Verify the 15 test_spec_refs rule is not restricted to kind: tests."""

    def test_rule_not_scoped_to_tests_only(self) -> None:
        """TS-08-2: The test_spec_refs ceiling rule does not restrict
        itself to only kind: tests groups."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        # The rule must either say "all" kinds or mention standard/checkpoint
        # explicitly — it must NOT be scoped only to kind: tests.
        has_all = "all" in lower
        has_standard = "standard" in lower
        has_checkpoint = "checkpoint" in lower
        has_explicit_kinds = has_standard and has_checkpoint

        assert has_all or has_explicit_kinds, (
            "The 15 test_spec_refs ceiling rule must apply to all task group "
            "kinds (tests, standard, checkpoint), not just kind: tests"
        )


# ===================================================================
# TS-08-4: subtask count ceiling rule (6) present in prompt
# Requirement: 08-REQ-2.1
# ===================================================================


class TestSubtaskCountCeiling:
    """Verify the 6-subtask ceiling rule exists in the prompt."""

    def test_contains_6_subtask_threshold(self) -> None:
        """TS-08-4: The prompt mentions '6' in connection with subtask
        count and splitting."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        assert "6" in section, "generation_user_tasks.md must contain '6' as the subtask count ceiling"
        assert "subtask" in lower, "The rules section must mention 'subtask'"
        assert "split" in lower, "The rules section must mention 'split'"

    def test_verification_exclusion_mentioned(self) -> None:
        """TS-08-4: The prompt indicates that the verification subtask
        is excluded from the 6-subtask count."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        assert "verification" in lower or "verif" in lower, (
            "The subtask ceiling rule must mention the verification subtask exclusion"
        )

    def test_6_and_subtask_in_proximity(self) -> None:
        """TS-08-4: '6' and 'subtask' appear in the same paragraph
        or rule, confirming they form a coherent ceiling rule."""
        content = _load_prompt_content()
        paragraphs = re.split(r"\n\s*\n", content)
        found = any("6" in para and "subtask" in para.lower() for para in paragraphs)
        assert found, (
            "'6' and 'subtask' must appear in the same paragraph to form a coherent subtask count ceiling rule"
        )


# ===================================================================
# TS-08-5: 6-subtask ceiling applies to ALL kind values
# Requirement: 08-REQ-2.2
# ===================================================================


class TestSubtaskCountCeilingUniversalKind:
    """Verify the 6-subtask ceiling is not restricted to kind: tests."""

    def test_rule_not_scoped_to_tests_only(self) -> None:
        """TS-08-5: The 6-subtask ceiling rule is universally applicable
        across all group kinds."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        has_all = "all" in lower
        has_standard = "standard" in lower
        has_checkpoint = "checkpoint" in lower
        has_explicit_kinds = has_standard and has_checkpoint

        assert has_all or has_explicit_kinds, (
            "The 6-subtask ceiling rule must apply to all task group kinds "
            "(tests, standard, checkpoint), not just kind: tests"
        )


# ===================================================================
# TS-08-7: complexity weighting definition and 4+ trigger
# Requirement: 08-REQ-3.1
# ===================================================================


class TestComplexityWeighting:
    """Verify complexity weighting rule definition in the prompt."""

    def test_complexity_defined(self) -> None:
        """TS-08-7: The prompt defines complexity as involving multiple
        file changes, cross-module coordination, or intricate assertion
        patterns."""
        content = _load_prompt_content()
        lower = content.lower()

        assert "complex" in lower, "The prompt must define 'complexity weighting'"
        has_multi_file = "multiple file" in lower
        has_cross_module = "cross-module" in lower or "cross module" in lower

        assert has_multi_file or has_cross_module, (
            "The complexity definition must reference 'multiple file changes' or 'cross-module coordination'"
        )

    def test_4_complex_subtasks_triggers_split(self) -> None:
        """TS-08-7: The prompt states that 4 or more complex subtasks
        in a group triggers splitting."""
        content = _load_prompt_content()

        # '4' must appear in a complexity-related rule context
        paragraphs = re.split(r"\n\s*\n", content)
        found = any("4" in para and "complex" in para.lower() and "split" in para.lower() for para in paragraphs)
        assert found, "The prompt must state that 4 or more complex subtasks triggers splitting"


# ===================================================================
# TS-08-8: complexity splitting applies independently of numeric thresholds
# Requirement: 08-REQ-3.2
# ===================================================================


class TestComplexityIndependent:
    """Verify complexity splitting applies regardless of numeric thresholds."""

    def test_regardless_of_numeric_thresholds(self) -> None:
        """TS-08-8: The prompt explicitly states that complexity-based
        splitting applies even if numeric thresholds are not exceeded."""
        content = _load_prompt_content()
        lower = content.lower()

        # The complexity rule should contain language like 'even if',
        # 'regardless of', or 'independent' indicating it applies
        # beyond numeric thresholds
        has_even_if = "even if" in lower or "even when" in lower
        has_regardless = "regardless" in lower
        has_independent = "independent" in lower

        assert has_even_if or has_regardless or has_independent, (
            "The complexity rule must state it applies 'even if' or 'regardless of' numeric thresholds"
        )

    def test_complexity_and_4_independent_of_numeric_rules(self) -> None:
        """TS-08-8: '4' and 'complex' and 'split' appear together in a
        rule that is stated independently of the numeric threshold rules."""
        content = _load_prompt_content()
        paragraphs = re.split(r"\n\s*\n", content)

        # Find the paragraph with the complexity rule
        complexity_paras = [
            para for para in paragraphs if "complex" in para.lower() and "4" in para and "split" in para.lower()
        ]
        assert len(complexity_paras) >= 1, "Must have a paragraph that links '4', 'complex', and 'split'"

        # At least one such paragraph should mention independence from
        # numeric thresholds
        has_independence = any(
            "even if" in para.lower() or "regardless" in para.lower() or "independent" in para.lower()
            for para in complexity_paras
        )
        assert has_independence, (
            "The complexity-based splitting rule must explicitly state independence from numeric threshold rules"
        )


# ===================================================================
# TS-08-9: group subtasks by requirement when splitting
# Requirement: 08-REQ-4.1
# ===================================================================


class TestGroupByRequirement:
    """Verify splitting strategy groups subtasks by requirement."""

    def test_requirement_based_grouping(self) -> None:
        """TS-08-9: The prompt instructs grouping subtasks by requirement
        when splitting, producing groups covering distinct requirements."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        assert "requirement" in lower, "The splitting strategy must reference 'requirement'"

    def test_distinct_requirement_coverage(self) -> None:
        """TS-08-9: Each resulting group should cover a distinct set of
        requirements."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        # Must mention distinct coverage or equivalent language
        has_distinct = "distinct" in lower
        has_separate = "separate" in lower
        has_cohesive = "cohesive" in lower

        assert has_distinct or has_separate or has_cohesive, (
            "The splitting strategy must indicate each group covers 'distinct' or 'separate' requirements"
        )

    def test_requirement_and_group_in_context(self) -> None:
        """TS-08-9: 'requirement' and 'group' appear together in the
        context of how to split."""
        content = _load_prompt_content()
        paragraphs = re.split(r"\n\s*\n", content)
        found = any("requirement" in para.lower() and "group" in para.lower() for para in paragraphs)
        assert found, "'requirement' and 'group' must appear in the same paragraph within the splitting strategy"


# ===================================================================
# TS-08-10: kind: tests preservation and sequential IDs when splitting
# Requirement: 08-REQ-4.2
# ===================================================================


class TestKindTestsPreservation:
    """Verify kind: tests groups retain kind after splitting."""

    def test_kind_tests_retained(self) -> None:
        """TS-08-10: The prompt instructs that when splitting a kind: tests
        group, all resulting groups retain kind: tests."""
        content = _load_prompt_content()
        lower = content.lower()

        # Must mention kind: tests in splitting instructions
        has_kind_tests = "kind: tests" in lower or "kind:tests" in lower
        assert has_kind_tests, "The splitting instructions must reference 'kind: tests'"

    def test_kind_preservation_language(self) -> None:
        """TS-08-10: The prompt uses language about retaining or preserving
        the kind value during splitting."""
        content = _load_prompt_content()
        lower = content.lower()

        has_retain = "retain" in lower
        has_preserve = "preserve" in lower
        has_keep = "keep" in lower

        assert has_retain or has_preserve or has_keep, (
            "The prompt must mention 'retain', 'preserve', or 'keep' regarding kind value during splitting"
        )

    def test_sequential_id_assignment(self) -> None:
        """TS-08-10: The prompt mentions sequential group ID assignment
        starting from 1 for the first split group."""
        content = _load_prompt_content()
        lower = content.lower()

        has_sequential = "sequential" in lower
        has_2_3 = "2, 3" in content or "2,3" in content

        assert has_sequential or has_2_3, (
            "The prompt must mention sequential ID assignment (e.g. '2, 3, ...' or 'sequential')"
        )


# ===================================================================
# TS-08-11: non-test groups shift IDs; subtask IDs use {group_id}.{N}
# Requirement: 08-REQ-4.3
# ===================================================================


class TestIdRenumbering:
    """Verify ID renumbering rules for non-test groups after splitting."""

    def test_subtask_id_format(self) -> None:
        """TS-08-11: The prompt specifies the {group_id}.{N} subtask ID format."""
        content = _load_prompt_content()

        # Must contain the format notation or a concrete example
        has_format = "{group_id}.{N}" in content or "{group_id}.{n}" in content.lower()
        has_example = re.search(r"\{group_id\}\.\{N\}", content, re.IGNORECASE)
        # Also accept if it mentions the pattern in another way
        has_dot_format = "group_id" in content.lower() and ".{" in content

        assert has_format or has_example or has_dot_format, (
            "The prompt must specify the '{group_id}.{N}' subtask ID format or equivalent notation"
        )

    def test_non_test_groups_shift_ids(self) -> None:
        """TS-08-11: The prompt describes non-test groups shifting their IDs
        to follow the last test group."""
        content = _load_prompt_content()
        lower = content.lower()

        # Must mention non-test groups in context of ID shifting
        has_non_test = "non-test" in lower or "non test" in lower
        has_shift = "shift" in lower or "follow" in lower or "after" in lower

        assert has_non_test, "The prompt must reference 'non-test' groups in the ID renumbering rules"
        assert has_shift, (
            "The prompt must describe ID shifting/ordering for non-test groups (e.g. 'shift', 'follow', 'after')"
        )


# ===================================================================
# TS-08-E3: complexity threshold is 4, not 3
# Requirement: 08-REQ-3.E1
# ===================================================================


class TestComplexityThresholdBoundary:
    """Verify the complexity threshold is exactly 4, not 3."""

    def test_threshold_is_4_not_3(self) -> None:
        """TS-08-E3: The complexity threshold stated in the prompt is
        '4 or more' (not '3 or more')."""
        content = _load_prompt_content()

        # Find paragraphs that discuss complexity and splitting
        paragraphs = re.split(r"\n\s*\n", content)
        complexity_paras = [para for para in paragraphs if "complex" in para.lower() and "split" in para.lower()]

        assert len(complexity_paras) >= 1, "Must have at least one paragraph discussing complexity and splitting"

        # The threshold in those paragraphs should mention 4, not 3
        for para in complexity_paras:
            assert "4" in para, "The complexity threshold must be '4' (not '3')"


# ===================================================================
# TS-08-E4: single-requirement edge case — prompt allows subdivision
# Requirement: 08-REQ-4.E1
# ===================================================================


class TestSingleRequirementSubdivision:
    """Verify the prompt handles the single-requirement edge case."""

    def test_no_never_split_language(self) -> None:
        """TS-08-E4: The prompt does not say 'never split' a single
        requirement."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        assert "never split" not in lower, (
            "The prompt must not say 'never split' for single-requirement groups; subdivision must be allowed"
        )

    def test_further_subdivision_instruction(self) -> None:
        """TS-08-E4: The prompt contains language about further subdivision
        when all subtasks trace to a single requirement."""
        content = _load_prompt_content()
        section = _find_rule_section(content)
        lower = section.lower()

        has_subdivide = "subdivide" in lower
        has_further_split = "further split" in lower
        has_further = "further" in lower and "split" in lower

        assert has_subdivide or has_further_split or has_further, (
            "The prompt must contain instruction for further subdivision when subtasks trace to a single requirement"
        )
