"""Tests for architecture.md archetype-aware scoping.

Verifies that architecture.md content is gated by the archetype artifact
filter: excluded from reviewer, gate sessions; included for coder,
verifier, and archetype=None (fail-open).

Issue: #749
Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5
"""

from __future__ import annotations

from pathlib import Path

import duckdb
from agentfox.session.prompt import assemble_context

# ---------------------------------------------------------------------------
# TS-NS-1: Architecture excluded from reviewer:pre-flight
# ---------------------------------------------------------------------------


class TestArchitectureExcludedReviewerPreflight:
    """TS-NS-1: Architecture content excluded from reviewer:pre-flight."""

    def test_reviewer_preflight_excludes_architecture(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """reviewer:pre-flight context must NOT contain architecture content."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="reviewer",
            mode="pre-flight",
        )
        assert "Design content here" not in ctx

    def test_reviewer_preflight_has_omission_note(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """reviewer:pre-flight context includes an omission note for architecture."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="reviewer",
            mode="pre-flight",
        )
        assert "_(Omitted" in ctx


# ---------------------------------------------------------------------------
# TS-NS-2: Architecture excluded from gate and verifier
# ---------------------------------------------------------------------------


class TestArchitectureExcludedGate:
    """TS-NS-2: Architecture content excluded from gate contexts."""

    def test_gate_excludes_architecture(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """gate context must NOT contain architecture content."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="gate",
        )
        assert "Design content here" not in ctx


class TestArchitectureExcludedVerifier:
    """TS-NS-2 (partial): Verifier does NOT receive architecture content."""

    def test_verifier_excludes_architecture(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """verifier context must NOT contain architecture content."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="verifier",
        )
        assert "Design content here" not in ctx


# ---------------------------------------------------------------------------
# TS-NS-3: Coder still receives architecture (no regression)
# ---------------------------------------------------------------------------


class TestArchitectureIncludedCoder:
    """TS-NS-3: Coder archetype receives architecture content."""

    def test_coder_includes_architecture_content(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """coder context contains architecture content."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="coder",
        )
        assert "Design content here" in ctx

    def test_coder_includes_architecture_header(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """coder context contains the ## Architecture section header."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
            archetype="coder",
        )
        assert "## Architecture" in ctx


# ---------------------------------------------------------------------------
# TS-NS-4: Fail-open — archetype=None includes architecture
# ---------------------------------------------------------------------------


class TestArchitectureFailOpen:
    """TS-NS-4: archetype=None (fail-open) includes architecture."""

    def test_none_archetype_includes_architecture(
        self,
        tmp_spec_dir: Path,
        knowledge_conn: duckdb.DuckDBPyConnection,
    ) -> None:
        """Default (no archetype) includes architecture content."""
        ctx = assemble_context(
            tmp_spec_dir,
            task_group=1,
            conn=knowledge_conn,
        )
        assert "Design content here" in ctx


# ---------------------------------------------------------------------------
# TS-NS-5: _render_spec_sections respects artifacts parameter
# ---------------------------------------------------------------------------


class TestRenderSpecSectionsArtifactFilter:
    """TS-NS-5: _render_spec_sections respects the artifacts parameter."""

    def test_architecture_excluded_when_not_in_artifacts(
        self,
        tmp_spec_dir: Path,
    ) -> None:
        """Sections list excludes architecture when artifacts=['requirements']."""
        from agentfox.session.context import _render_spec_sections

        sections = _render_spec_sections(tmp_spec_dir, artifacts=["requirements"])
        assert all("Design content here" not in s for s in sections)

    def test_architecture_included_when_in_artifacts(
        self,
        tmp_spec_dir: Path,
    ) -> None:
        """Sections list includes architecture when 'architecture' in artifacts."""
        from agentfox.session.context import _render_spec_sections

        sections = _render_spec_sections(tmp_spec_dir, artifacts=["requirements", "architecture"])
        assert any("Design content here" in s for s in sections)
