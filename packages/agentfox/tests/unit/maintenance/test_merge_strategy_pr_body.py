"""Tests for build_pr_body() pure rendering function.

Test Spec: TS-02-13 (signature and purity), TS-02-14 (af code body),
           TS-02-15 (nightshift body), TS-02-16 (keyword-only callers),
           TS-02-E9 (empty changed_files), TS-02-E10 (ambiguous params),
           TS-02-P4 (purity property)
Requirements: 02-REQ-5.1, 02-REQ-5.2, 02-REQ-5.3, 02-REQ-5.4,
              02-REQ-5.E1, 02-REQ-5.E2
"""

from __future__ import annotations

import ast
import inspect
from pathlib import Path

import pytest
from agentfox.nightshift.fix_pipeline import build_pr_body
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# TS-02-13: build_pr_body() has exact keyword-only signature and is pure
# Requirement: 02-REQ-5.1
# ---------------------------------------------------------------------------


class TestBuildPrBodySignature:
    """TS-02-13: build_pr_body() exists in fix_pipeline.py with the exact
    keyword-only signature and is a pure function with no side effects.

    Requirements: 02-REQ-5.1
    """

    def test_all_parameters_are_keyword_only(self) -> None:
        """All parameters of build_pr_body are keyword-only."""
        sig = inspect.signature(build_pr_body)
        for name, param in sig.parameters.items():
            assert param.kind == inspect.Parameter.KEYWORD_ONLY, (
                f"Parameter '{name}' is not keyword-only (kind={param.kind})"
            )

    def test_spec_name_default_is_none(self) -> None:
        """spec_name parameter defaults to None."""
        sig = inspect.signature(build_pr_body)
        assert sig.parameters["spec_name"].default is None

    def test_task_group_id_default_is_none(self) -> None:
        """task_group_id parameter defaults to None."""
        sig = inspect.signature(build_pr_body)
        assert sig.parameters["task_group_id"].default is None

    def test_task_group_title_default_is_none(self) -> None:
        """task_group_title parameter defaults to None."""
        sig = inspect.signature(build_pr_body)
        assert sig.parameters["task_group_title"].default is None

    def test_changed_files_is_required(self) -> None:
        """changed_files parameter has no default (is required)."""
        sig = inspect.signature(build_pr_body)
        assert sig.parameters["changed_files"].default is inspect.Parameter.empty

    def test_issue_number_default_is_none(self) -> None:
        """issue_number parameter defaults to None."""
        sig = inspect.signature(build_pr_body)
        assert sig.parameters["issue_number"].default is None

    def test_issue_title_default_is_none(self) -> None:
        """issue_title parameter defaults to None."""
        sig = inspect.signature(build_pr_body)
        assert sig.parameters["issue_title"].default is None

    def test_return_annotation_is_str(self) -> None:
        """Return type annotation is str."""
        sig = inspect.signature(build_pr_body)
        assert sig.return_annotation is str or sig.return_annotation == "str"

    def test_has_exactly_six_parameters(self) -> None:
        """build_pr_body has exactly six parameters."""
        sig = inspect.signature(build_pr_body)
        assert len(sig.parameters) == 6, (
            f"Expected 6 parameters, got {len(sig.parameters)}: "
            f"{list(sig.parameters.keys())}"
        )

    def test_pure_function_identical_output(self) -> None:
        """Two calls with identical arguments return identical strings."""
        result1 = build_pr_body(changed_files=["a.py"], spec_name="s")
        result2 = build_pr_body(changed_files=["a.py"], spec_name="s")
        assert result1 == result2

    def test_returns_str(self) -> None:
        """build_pr_body returns a str instance."""
        result = build_pr_body(changed_files=["a.py"], spec_name="test")
        assert isinstance(result, str)


# ---------------------------------------------------------------------------
# TS-02-14: af code session body rendering
# Requirement: 02-REQ-5.2
# ---------------------------------------------------------------------------


class TestBuildPrBodyAfCode:
    """TS-02-14: build_pr_body() renders the correct Markdown body for an
    af code session with spec_name, task_group_id, task_group_title, and
    changed_files.

    Requirements: 02-REQ-5.2
    """

    def test_contains_summary_section(self) -> None:
        """Rendered body contains '## Summary' section."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py", "config_gen.py"],
        )
        assert "## Summary" in body

    def test_summary_contains_spec_name(self) -> None:
        """Summary section contains the spec name."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py"],
        )
        assert "merge_strategy" in body

    def test_contains_task_group_section(self) -> None:
        """Rendered body contains '## Task Group' section."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py"],
        )
        assert "## Task Group" in body

    def test_task_group_contains_id_and_title(self) -> None:
        """Task Group section contains 'task_group_id: task_group_title'."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py"],
        )
        assert "task-001: Config and validation" in body

    def test_contains_changed_files_section(self) -> None:
        """Rendered body contains '## Changed Files' section."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py", "config_gen.py"],
        )
        assert "## Changed Files" in body

    def test_changed_files_lists_each_file(self) -> None:
        """Changed Files section lists each file as a bullet."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py", "config_gen.py"],
        )
        assert "- config.py" in body
        assert "- config_gen.py" in body

    def test_no_fixes_line_for_af_code(self) -> None:
        """af code session body does NOT contain 'Fixes #'."""
        body = build_pr_body(
            spec_name="merge_strategy",
            task_group_id="task-001",
            task_group_title="Config and validation",
            changed_files=["config.py"],
        )
        assert "Fixes #" not in body


# ---------------------------------------------------------------------------
# TS-02-15: nightshift fix session body rendering
# Requirement: 02-REQ-5.3
# ---------------------------------------------------------------------------


class TestBuildPrBodyNightshift:
    """TS-02-15: build_pr_body() renders the correct Markdown body for a
    nightshift fix session with issue_number, issue_title, and changed_files,
    omitting the Task Group section.

    Requirements: 02-REQ-5.3
    """

    def test_contains_summary_section(self) -> None:
        """Rendered body contains '## Summary'."""
        body = build_pr_body(
            issue_number=42,
            issue_title="Login fails on empty password",
            changed_files=["auth/login.py", "tests/test_login.py"],
        )
        assert "## Summary" in body

    def test_summary_contains_fix_reference(self) -> None:
        """Summary contains 'Fix #42: Login fails on empty password'."""
        body = build_pr_body(
            issue_number=42,
            issue_title="Login fails on empty password",
            changed_files=["auth/login.py"],
        )
        assert "Fix #42: Login fails on empty password" in body

    def test_no_task_group_section(self) -> None:
        """Nightshift body does NOT contain '## Task Group'."""
        body = build_pr_body(
            issue_number=42,
            issue_title="Login fails on empty password",
            changed_files=["auth/login.py"],
        )
        assert "## Task Group" not in body

    def test_contains_changed_files_section(self) -> None:
        """Rendered body contains '## Changed Files' with files listed."""
        body = build_pr_body(
            issue_number=42,
            issue_title="Login fails on empty password",
            changed_files=["auth/login.py", "tests/test_login.py"],
        )
        assert "## Changed Files" in body
        assert "- auth/login.py" in body
        assert "- tests/test_login.py" in body

    def test_ends_with_fixes_reference(self) -> None:
        """Nightshift body contains 'Fixes #42'."""
        body = build_pr_body(
            issue_number=42,
            issue_title="Login fails on empty password",
            changed_files=["auth/login.py"],
        )
        assert "Fixes #42" in body


# ---------------------------------------------------------------------------
# TS-02-16: all callers of build_pr_body() use keyword arguments only
# Requirement: 02-REQ-5.4
# ---------------------------------------------------------------------------


class TestBuildPrBodyCallerKeywordOnly:
    """TS-02-16: All callers of build_pr_body() in session_lifecycle.py and
    fix_pipeline.py use keyword arguments exclusively.

    Requirements: 02-REQ-5.4

    Note: This test uses AST inspection. When no call sites exist yet
    (before the merge_strategy pipeline integration), the test verifies
    that the constraint holds vacuously. Once call sites are added in
    later task groups, this test catches any positional argument usage.
    """

    @staticmethod
    def _find_build_pr_body_calls(filepath: Path) -> list[ast.Call]:
        """Parse a file and return all AST Call nodes for build_pr_body."""
        if not filepath.exists():
            return []
        source = filepath.read_text(encoding="utf-8")
        tree = ast.parse(source, filename=str(filepath))
        calls: list[ast.Call] = []
        for node in ast.walk(tree):
            if not isinstance(node, ast.Call):
                continue
            # Match direct call: build_pr_body(...)
            if isinstance(node.func, ast.Name) and node.func.id == "build_pr_body":
                calls.append(node)
            # Match attribute call: module.build_pr_body(...)
            elif isinstance(node.func, ast.Attribute) and node.func.attr == "build_pr_body":
                calls.append(node)
        return calls

    def test_session_lifecycle_uses_keyword_only(self) -> None:
        """All build_pr_body calls in session_lifecycle.py use keyword args."""
        filepath = (
            Path(__file__).resolve().parents[3]
            / "agentfox"
            / "engine"
            / "session_lifecycle.py"
        )
        calls = self._find_build_pr_body_calls(filepath)
        for call in calls:
            assert len(call.args) == 0, (
                f"Positional args found in session_lifecycle.py call to "
                f"build_pr_body at line {call.lineno}"
            )

    def test_fix_pipeline_uses_keyword_only(self) -> None:
        """All build_pr_body calls in fix_pipeline.py use keyword args."""
        filepath = (
            Path(__file__).resolve().parents[3]
            / "agentfox"
            / "nightshift"
            / "fix_pipeline.py"
        )
        calls = self._find_build_pr_body_calls(filepath)
        for call in calls:
            assert len(call.args) == 0, (
                f"Positional args found in fix_pipeline.py call to "
                f"build_pr_body at line {call.lineno}"
            )


# ---------------------------------------------------------------------------
# TS-02-E9: empty changed_files
# Requirement: 02-REQ-5.E1
# ---------------------------------------------------------------------------


class TestBuildPrBodyEmptyChangedFiles:
    """TS-02-E9: build_pr_body() renders an empty Changed Files section
    (with header but no bullets) when changed_files is an empty list.

    Requirements: 02-REQ-5.E1
    """

    def test_empty_changed_files_has_section_header(self) -> None:
        """'## Changed Files' header is present even with empty list."""
        body = build_pr_body(
            spec_name="my_spec",
            task_group_id="g1",
            task_group_title="Group 1",
            changed_files=[],
        )
        assert "## Changed Files" in body

    def test_empty_changed_files_no_bullets(self) -> None:
        """No bullet points in Changed Files section when list is empty."""
        body = build_pr_body(
            spec_name="my_spec",
            task_group_id="g1",
            task_group_title="Group 1",
            changed_files=[],
        )
        # Extract the Changed Files section text
        parts = body.split("## Changed Files")
        assert len(parts) >= 2, "## Changed Files section not found"
        # Get content after the header, up to the next section or end
        section_content = parts[1].split("##")[0] if "##" in parts[1] else parts[1]
        assert "- " not in section_content

    def test_empty_changed_files_no_error(self) -> None:
        """No exception is raised when changed_files is empty."""
        # Should not raise
        result = build_pr_body(
            spec_name="my_spec",
            task_group_id="g1",
            task_group_title="Group 1",
            changed_files=[],
        )
        assert isinstance(result, str)


# ---------------------------------------------------------------------------
# TS-02-E10: ambiguous parameter combinations
# Requirement: 02-REQ-5.E2
# ---------------------------------------------------------------------------


class TestBuildPrBodyAmbiguousParams:
    """TS-02-E10: build_pr_body() does not raise an unhandled exception
    when both spec_name and issue_number are provided simultaneously.

    Requirements: 02-REQ-5.E2
    """

    def test_both_spec_and_issue_does_not_crash(self) -> None:
        """Providing both spec_name and issue_number must not crash."""
        result = build_pr_body(
            spec_name="my_spec",
            issue_number=42,
            issue_title="Some issue",
            changed_files=["a.py"],
        )
        assert isinstance(result, str)

    def test_both_spec_and_issue_with_task_group(self) -> None:
        """Providing all fields (spec + task group + issue) must not crash."""
        result = build_pr_body(
            spec_name="my_spec",
            task_group_id="g1",
            task_group_title="Group 1",
            issue_number=42,
            issue_title="Some issue",
            changed_files=["a.py", "b.py"],
        )
        assert isinstance(result, str)


# ---------------------------------------------------------------------------
# TS-02-P4: Property test — build_pr_body is a pure function
# Requirement: 02-REQ-5.1, 02-REQ-5.2, 02-REQ-5.3
# Property: 02-PROP-4
# ---------------------------------------------------------------------------


class TestBuildPrBodyPurity:
    """TS-02-P4: build_pr_body() is a pure function: calling it with the
    same arguments always returns the identical string and produces no
    observable side effects.

    Property: 02-PROP-4
    Validates: 02-REQ-5.1, 02-REQ-5.2, 02-REQ-5.3
    """

    @given(
        spec_name=st.one_of(st.none(), st.text(min_size=1, max_size=30)),
        task_group_id=st.one_of(st.none(), st.text(min_size=1, max_size=20)),
        task_group_title=st.one_of(st.none(), st.text(min_size=1, max_size=50)),
        issue_number=st.one_of(st.none(), st.integers(min_value=1, max_value=99999)),
        issue_title=st.one_of(st.none(), st.text(min_size=1, max_size=100)),
    )
    @settings(max_examples=50)
    def test_property_deterministic_output(
        self,
        spec_name: str | None,
        task_group_id: str | None,
        task_group_title: str | None,
        issue_number: int | None,
        issue_title: str | None,
    ) -> None:
        """Same arguments always produce the same output string."""
        changed_files = ["file1.py", "file2.py"]
        result1 = build_pr_body(
            spec_name=spec_name,
            task_group_id=task_group_id,
            task_group_title=task_group_title,
            changed_files=changed_files,
            issue_number=issue_number,
            issue_title=issue_title,
        )
        result2 = build_pr_body(
            spec_name=spec_name,
            task_group_id=task_group_id,
            task_group_title=task_group_title,
            changed_files=changed_files,
            issue_number=issue_number,
            issue_title=issue_title,
        )
        assert isinstance(result1, str)
        assert result1 == result2

    @given(
        changed_files=st.lists(
            st.text(min_size=1, max_size=50).filter(lambda s: "\n" not in s),
            min_size=0,
            max_size=10,
        ),
    )
    @settings(max_examples=30)
    def test_property_always_returns_str(
        self,
        changed_files: list[str],
    ) -> None:
        """build_pr_body always returns a str regardless of changed_files."""
        result = build_pr_body(
            spec_name="test_spec",
            changed_files=changed_files,
        )
        assert isinstance(result, str)

    @pytest.mark.parametrize(
        "kwargs",
        [
            {"spec_name": "s", "changed_files": ["a.py"]},
            {"issue_number": 1, "issue_title": "t", "changed_files": ["b.py"]},
            {"changed_files": []},
            {
                "spec_name": "x",
                "task_group_id": "g",
                "task_group_title": "T",
                "changed_files": ["c.py"],
            },
        ],
    )
    def test_property_idempotent_across_call_combos(
        self,
        kwargs: dict,
    ) -> None:
        """Multiple calls with the same kwargs produce identical output."""
        r1 = build_pr_body(**kwargs)
        r2 = build_pr_body(**kwargs)
        assert r1 == r2
        assert isinstance(r1, str)
