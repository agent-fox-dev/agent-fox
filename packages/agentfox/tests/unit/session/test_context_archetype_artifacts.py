"""Tests for archetype-aware spec artifact filtering in assemble_context.

Verifies that each archetype receives only the spec artifact sections it
needs, reducing token waste for non-coder sessions.

Issue: #735
Requirements: NS-REQ-1 through NS-REQ-5
Test Spec: TS-NS-1 through TS-NS-5
"""

from __future__ import annotations

from pathlib import Path

import duckdb
from agentfox.session.context import (
    _resolve_artifacts,
    assemble_context,
)

# ---------------------------------------------------------------------------
# Unit tests for _resolve_artifacts
# ---------------------------------------------------------------------------


class TestResolveArtifacts:
    """Unit tests for the archetype-to-artifact resolution helper."""

    def test_coder_gets_all(self) -> None:
        result = _resolve_artifacts("coder")
        assert result == ["requirements", "test_spec", "tasks", "architecture"]

    def test_reviewer_pre_flight_gets_requirements_only(self) -> None:
        result = _resolve_artifacts("reviewer", mode="pre-flight")
        assert result == ["requirements"]

    def test_reviewer_audit_review_gets_requirements_and_test_spec(self) -> None:
        result = _resolve_artifacts("reviewer", mode="audit-review")
        assert result == ["requirements", "test_spec"]

    def test_reviewer_fix_review_gets_requirements_and_test_spec(self) -> None:
        result = _resolve_artifacts("reviewer", mode="fix-review")
        assert result == ["requirements", "test_spec"]

    def test_verifier_gets_requirements_and_tasks(self) -> None:
        result = _resolve_artifacts("verifier")
        assert result == ["requirements", "tasks"]

    def test_gate_gets_requirements_only(self) -> None:
        result = _resolve_artifacts("gate")
        assert result == ["requirements"]

    def test_unknown_archetype_gets_all(self) -> None:
        """NS-REQ-5: Unknown archetypes default to all artifacts."""
        result = _resolve_artifacts("maintainer")
        assert result == ["requirements", "test_spec", "tasks", "architecture"]

    def test_none_archetype_gets_all(self) -> None:
        """NS-REQ-5: archetype=None defaults to all artifacts."""
        result = _resolve_artifacts(None)
        assert result == ["requirements", "test_spec", "tasks", "architecture"]

    def test_reviewer_without_mode_gets_all(self) -> None:
        """Bare 'reviewer' without mode falls through to all artifacts."""
        result = _resolve_artifacts("reviewer")
        assert result == ["requirements", "test_spec", "tasks", "architecture"]

    def test_mode_ignored_for_non_modal_archetype(self) -> None:
        """Providing a mode for a non-modal archetype falls back to bare."""
        result = _resolve_artifacts("coder", mode="fix")
        # "coder:fix" is not in the mapping, falls back to "coder"
        assert result == ["requirements", "test_spec", "tasks", "architecture"]


# ---------------------------------------------------------------------------
# Integration tests for assemble_context with archetype filtering
# ---------------------------------------------------------------------------


class TestAssembleContextArchetypeFiltering:
    """TS-NS-1 through TS-NS-5: assemble_context respects archetype.

    Uses the ``tmp_spec_dir`` and ``knowledge_conn`` fixtures from the
    session conftest which create a spec with all three artifacts.
    """

    def test_reviewer_pre_flight_only_requirements(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """TS-NS-1 / NS-REQ-1: reviewer:pre-flight gets only requirements."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="reviewer",
            mode="pre-flight",
        )
        assert "## Requirements" in ctx
        # Omitted sections should have the omission note, not their content
        assert "## Test Specification" in ctx  # header is present as omission note
        assert "_(Omitted" in ctx
        # The actual test spec content should not appear
        assert "Test spec content here" not in ctx
        # The actual tasks content should not appear
        assert "Task content here" not in ctx

    def test_reviewer_audit_review_requirements_and_test_spec(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """TS-NS-2 / NS-REQ-2: reviewer:audit-review gets requirements + test_spec."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="reviewer",
            mode="audit-review",
        )
        assert "## Requirements" in ctx
        assert "## Test Specification" in ctx
        # Test spec content should be present
        assert "Test spec content here" in ctx or "TS-1-1" in ctx
        # Tasks should be omitted
        assert "Task content here" not in ctx

    def test_verifier_requirements_and_tasks(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """TS-NS-3 / NS-REQ-3: verifier gets requirements + tasks."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="verifier",
        )
        assert "## Requirements" in ctx
        assert "## Tasks" in ctx
        # Tasks content should be present
        assert "Task content here" in ctx or "1.1" in ctx
        # Test spec should be omitted
        assert "Test spec content here" not in ctx

    def test_coder_gets_all_artifacts(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """TS-NS-4 / NS-REQ-4: coder receives all three artifacts (no regression)."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="coder",
        )
        assert "## Requirements" in ctx
        assert "## Test Specification" in ctx
        assert "## Tasks" in ctx
        # All content should be present
        assert "REQ content here" in ctx
        # No omission notes
        assert "_(Omitted" not in ctx

    def test_unknown_archetype_gets_all_artifacts(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """TS-NS-5 / NS-REQ-5: unknown archetype gets all artifacts (fail-open)."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="maintainer",
        )
        assert "## Requirements" in ctx
        assert "## Test Specification" in ctx
        assert "## Tasks" in ctx
        assert "_(Omitted" not in ctx

    def test_none_archetype_gets_all_artifacts(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """TS-NS-5 / NS-REQ-5: archetype=None gets all artifacts (fail-open)."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype=None,
        )
        assert "## Requirements" in ctx
        assert "## Test Specification" in ctx
        assert "## Tasks" in ctx
        assert "_(Omitted" not in ctx

    def test_omission_note_present_for_filtered_sections(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Filtered sections include an omission indicator."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="reviewer",
            mode="pre-flight",
        )
        # test_spec, tasks, and architecture should have omission notes
        # Count omission notes — should be exactly 3
        assert ctx.count("_(Omitted") == 3
