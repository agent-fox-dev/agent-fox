"""Tests for prior fix attempt context retrieval (spec 128).

Test Spec: TS-128-1 through TS-128-9, TS-128-E1 through TS-128-E3,
           TS-128-P1 through TS-128-P5, TS-128-SMOKE-1
Requirements: 128-REQ-1.1, 128-REQ-1.2, 128-REQ-1.3,
              128-REQ-1.E1, 128-REQ-1.E2,
              128-REQ-2.1, 128-REQ-2.2, 128-REQ-2.3,
              128-REQ-3.1, 128-REQ-3.2,
              128-REQ-4.1, 128-REQ-4.2
"""

from __future__ import annotations

import uuid
from collections.abc import Generator
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import duckdb
import pytest
from agentfox.nightshift.prior_attempts import (
    PriorAttempt,
    format_prior_attempts,
    query_prior_attempts,
)
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def db_conn() -> Generator[duckdb.DuckDBPyConnection, None, None]:
    """In-memory DuckDB with session_outcomes table (all migration columns).

    Uses the full production schema via SCHEMA_DDL + migrations so that
    columns like run_id, archetype, model, and error_message are present.
    """
    from agentfox.knowledge.migrations import apply_pending_migrations

    from tests.unit.knowledge.conftest import SCHEMA_DDL

    conn = duckdb.connect(":memory:")
    conn.execute(SCHEMA_DDL)
    apply_pending_migrations(conn)
    yield conn
    conn.close()


def _insert_session(
    conn: duckdb.DuckDBPyConnection,
    *,
    spec_name: str = "fix-issue-42",
    run_id: str = "run_A",
    archetype: str = "coder",
    status: str = "failed",
    error_message: str | None = None,
    model: str | None = "claude-sonnet-4-5-20250514",
    created_at: str = "2026-05-28 10:00:00",
) -> None:
    """Insert a single session_outcomes row for testing."""
    session_id = str(uuid.uuid4())
    conn.execute(
        """
        INSERT INTO session_outcomes
            (id, spec_name, task_group, node_id, touched_path,
             status, input_tokens, output_tokens, duration_ms,
             created_at, run_id, attempt, cost, model, archetype,
             commit_sha, error_message, is_transport_error)
        VALUES (?, ?, '0', ?, '', ?, 100, 50, 1000, ?, ?, 1, 0.01, ?, ?, '', ?, FALSE)
        """,
        [
            session_id,
            spec_name,
            f"{spec_name}:0:{archetype}",
            status,
            created_at,
            run_id,
            model,
            archetype,
            error_message,
        ],
    )


# ---------------------------------------------------------------------------
# TS-128-1: Query returns prior coder sessions
# Requirement: 128-REQ-1.1
# ---------------------------------------------------------------------------


class TestQueryReturnsPriorSessions:
    """Verify query returns coder sessions excluding current run."""

    def test_query_returns_prior_sessions(self, db_conn: duckdb.DuckDBPyConnection) -> None:
        """TS-128-1: query returns prior coder sessions, excludes current run."""
        # Insert 2 prior runs + 1 current run
        _insert_session(db_conn, run_id="run_A", created_at="2026-05-25 10:00:00")
        _insert_session(db_conn, run_id="run_B", created_at="2026-05-26 10:00:00")
        _insert_session(db_conn, run_id="run_current", created_at="2026-05-28 10:00:00")

        result = query_prior_attempts(db_conn, "fix-issue-42", "run_current")

        assert len(result) == 2
        assert all(r.run_id != "run_current" for r in result)


# ---------------------------------------------------------------------------
# TS-128-2: Query groups by run, returns last session per run
# Requirement: 128-REQ-1.2
# ---------------------------------------------------------------------------


class TestQueryGroupsByRun:
    """Verify only the last coder session per run is returned."""

    def test_query_groups_by_run(self, db_conn: duckdb.DuckDBPyConnection) -> None:
        """TS-128-2: when a run has multiple coder sessions (retries), only the last one is returned."""
        # 3 coder sessions for run_A with increasing timestamps
        _insert_session(db_conn, run_id="run_A", created_at="2026-05-25 10:00:00", status="failed")
        _insert_session(db_conn, run_id="run_A", created_at="2026-05-25 11:00:00", status="failed")
        _insert_session(
            db_conn,
            run_id="run_A",
            created_at="2026-05-25 12:00:00",
            status="completed",
            error_message=None,
        )

        result = query_prior_attempts(db_conn, "fix-issue-42", "run_current")

        assert len(result) == 1
        assert result[0].run_id == "run_A"
        # The created_at should correspond to attempt 3 (12:00:00)
        assert "12:00:00" in result[0].created_at
        # All run_ids must be distinct
        run_ids = [r.run_id for r in result]
        assert len(set(run_ids)) == len(run_ids)


# ---------------------------------------------------------------------------
# TS-128-3: Query respects max_results limit
# Requirement: 128-REQ-1.2
# ---------------------------------------------------------------------------


class TestQueryMaxResults:
    """Verify query returns at most max_results entries."""

    def test_query_respects_max_results(self, db_conn: duckdb.DuckDBPyConnection) -> None:
        """TS-128-3: with 5 prior runs, max_results=3 returns exactly 3."""
        for i in range(5):
            _insert_session(
                db_conn,
                run_id=f"run_{i}",
                created_at=f"2026-05-{20 + i:02d} 10:00:00",
            )

        result = query_prior_attempts(db_conn, "fix-issue-42", "run_current", max_results=3)

        assert len(result) == 3


# ---------------------------------------------------------------------------
# TS-128-4: PriorAttempt dataclass fields
# Requirement: 128-REQ-1.3
# ---------------------------------------------------------------------------


class TestPriorAttemptFields:
    """Verify PriorAttempt has the correct fields."""

    def test_prior_attempt_fields(self) -> None:
        """TS-128-4: PriorAttempt dataclass fields are accessible and typed correctly."""
        pa = PriorAttempt(
            run_id="r1",
            created_at="2026-05-28T10:00:00",
            status="failed",
            error_message="boom",
            model="claude-sonnet",
        )
        assert pa.run_id == "r1"
        assert pa.created_at == "2026-05-28T10:00:00"
        assert pa.status == "failed"
        assert pa.error_message == "boom"
        assert pa.model == "claude-sonnet"

    def test_prior_attempt_none_fields(self) -> None:
        """PriorAttempt allows None for error_message and model."""
        pa = PriorAttempt(
            run_id="r2",
            created_at="2026-05-28T10:00:00",
            status="completed",
            error_message=None,
            model=None,
        )
        assert pa.error_message is None
        assert pa.model is None

    def test_prior_attempt_is_frozen(self) -> None:
        """PriorAttempt is a frozen dataclass (immutable)."""
        pa = PriorAttempt(
            run_id="r1",
            created_at="2026-05-28T10:00:00",
            status="failed",
            error_message="boom",
            model="claude-sonnet",
        )
        with pytest.raises(AttributeError):
            pa.run_id = "r2"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-128-5: Format produces markdown block
# Requirement: 128-REQ-2.1, 128-REQ-2.2
# ---------------------------------------------------------------------------


class TestFormatMarkdown:
    """Verify format output contains heading and numbered entries."""

    def test_format_produces_markdown_block(self) -> None:
        """TS-128-5: format output starts with heading and has numbered entries."""
        attempts = [
            PriorAttempt(
                run_id="run_A",
                created_at="2026-05-28T10:00:00",
                status="failed",
                error_message="merge conflict in parser.py",
                model="claude-sonnet-4-5-20250514",
            ),
            PriorAttempt(
                run_id="run_B",
                created_at="2026-05-25T10:00:00",
                status="completed",
                error_message=None,
                model="claude-sonnet-4-5-20250514",
            ),
        ]

        result = format_prior_attempts(attempts)

        assert "## Prior Fix Attempts" in result
        assert "1." in result
        assert "2." in result
        assert "failed" in result
        # Each entry includes date, status, and model
        assert "2026-05-28" in result
        assert "claude-sonnet-4-5-20250514" in result


# ---------------------------------------------------------------------------
# TS-128-6: Format truncates long error messages
# Requirement: 128-REQ-2.2
# ---------------------------------------------------------------------------


class TestFormatTruncation:
    """Verify error messages longer than 500 chars are truncated."""

    def test_format_truncates_long_error_messages(self) -> None:
        """TS-128-6: error message of 1000 chars is truncated to ~503 chars."""
        long_error = "x" * 1000
        attempt = PriorAttempt(
            run_id="run_A",
            created_at="2026-05-28T10:00:00",
            status="failed",
            error_message=long_error,
            model="claude-sonnet-4-5-20250514",
        )

        result = format_prior_attempts([attempt])

        assert "..." in result
        # The full 1000-char error must NOT appear in the output
        assert long_error not in result


# ---------------------------------------------------------------------------
# TS-128-7: Context injected into task prompt
# Requirement: 128-REQ-3.1
# ---------------------------------------------------------------------------


class TestContextInjectedIntoPrompt:
    """Verify prior_context appears in the task prompt before issue description."""

    def test_context_injected_into_task_prompt(self) -> None:
        """TS-128-7: when prior_context is non-empty, it appears before the issue title."""
        from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
        from agentfox.nightshift.spec_builder import InMemorySpec

        config = MagicMock()
        pipeline = FixPipeline(config=config, platform=MagicMock())

        spec = InMemorySpec(
            issue_number=42,
            title="Fix the bug",
            task_prompt="Fix the issue: Fix the bug\n\nIssue #42\n\nBug details here.",
            system_context="Bug details here.",
            branch_name="fix/42-fix-the-bug",
        )
        triage = TriageResult()

        ctx = "## Prior Fix Attempts\n\n1. **2026-05-28** (failed): merge conflict"

        _, task_prompt = pipeline._build_coder_prompt(spec, triage, prior_context=ctx)

        prior_idx = task_prompt.index("Prior Fix Attempts")
        issue_idx = task_prompt.index("Fix the issue")
        assert prior_idx < issue_idx


# ---------------------------------------------------------------------------
# TS-128-8: Empty context leaves prompt unchanged
# Requirement: 128-REQ-3.2
# ---------------------------------------------------------------------------


class TestEmptyContextUnchanged:
    """Verify empty prior_context produces the same task prompt as before."""

    def test_empty_context_leaves_prompt_unchanged(self) -> None:
        """TS-128-8: when prior_context is empty string, prompt has no Prior Fix Attempts."""
        from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
        from agentfox.nightshift.spec_builder import InMemorySpec

        config = MagicMock()
        pipeline = FixPipeline(config=config, platform=MagicMock())

        spec = InMemorySpec(
            issue_number=42,
            title="Fix the bug",
            task_prompt="Fix the issue: Fix the bug\n\nIssue #42\n\nBug details here.",
            system_context="Bug details here.",
            branch_name="fix/42-fix-the-bug",
        )
        triage = TriageResult()

        _, task_prompt = pipeline._build_coder_prompt(spec, triage, prior_context="")

        assert "Prior Fix Attempts" not in task_prompt


# ---------------------------------------------------------------------------
# TS-128-9: Pipeline wires query into process_issue
# Requirements: 128-REQ-4.1, 128-REQ-4.2
# ---------------------------------------------------------------------------


class TestPipelineWiring:
    """Verify process_issue calls query_prior_attempts with conn and run_id."""

    @pytest.mark.asyncio
    async def test_pipeline_wires_query_into_process_issue(self) -> None:
        """TS-128-9: process_issue calls query_prior_attempts with correct args."""
        import json

        from agentfox.nightshift.fix_pipeline import FixPipeline
        from agentfox.platform.protocol import IssueResult

        config = MagicMock()
        config.routing.retries_before_escalation = 1
        config.orchestrator.max_retries = 3
        mock_platform = AsyncMock()
        mock_conn = MagicMock()

        pipeline = FixPipeline(config=config, platform=mock_platform, conn=mock_conn)
        pipeline._setup_workspace = AsyncMock(  # type: ignore[method-assign]
            return_value=MagicMock(
                path=Path("/tmp/mock-worktree"),
                branch="fix/42-test-branch",
                spec_name="fix-issue-42",
                task_group=0,
            )
        )
        pipeline._cleanup_workspace = AsyncMock()  # type: ignore[method-assign]

        triage_response = json.dumps(
            {
                "summary": "s",
                "affected_files": [],
                "acceptance_criteria": [
                    {"id": "AC-1", "description": "d", "preconditions": "p", "expected": "e", "assertion": "a"},
                ],
            }
        )
        review_response = json.dumps(
            {
                "verdicts": [{"criterion_id": "AC-1", "verdict": "PASS", "evidence": "ok"}],
                "overall_verdict": "PASS",
                "summary": "ok",
            }
        )

        async def mock_run_session(archetype: str, workspace: object = None, **kwargs: object) -> MagicMock:
            outcome = MagicMock(
                input_tokens=10,
                output_tokens=5,
                cache_read_input_tokens=0,
                cache_creation_input_tokens=0,
            )
            if archetype == "maintainer":
                outcome.response = triage_response
            elif archetype == "reviewer":
                outcome.response = review_response
            else:
                outcome.response = ""
            return outcome

        pipeline._run_session = mock_run_session  # type: ignore[assignment]

        issue = IssueResult(
            number=42,
            title="Fix something",
            html_url="https://github.com/test/repo/issues/42",
        )

        with (
            patch(
                "agentfox.nightshift.fix_pipeline.query_prior_attempts",
                create=True,
                return_value=[],
            ) as mock_query,
            patch(
                "agentfox.nightshift.fix_pipeline.format_prior_attempts",
                create=True,
                return_value="",
            ),
            patch.object(pipeline, "_harvest_and_push", AsyncMock(return_value="merged")),
        ):
            await pipeline.process_issue(issue, issue_body="Something is broken.")

        # Verify query_prior_attempts was called
        assert mock_query.called, "query_prior_attempts was not called by process_issue"
        # Check arguments (support both positional and keyword calling conventions)
        call_args = mock_query.call_args
        # The function should receive: conn, spec_name, current_run_id
        # Extract spec_name and current_run_id from either positional or keyword args
        if call_args.args and len(call_args.args) >= 3:
            # Called with positional args
            assert call_args.args[1] == "fix-issue-42"
            assert call_args.args[2] == pipeline._run_id
        else:
            # Called with keyword args
            assert call_args.kwargs.get("spec_name") == "fix-issue-42" or call_args.args[1] == "fix-issue-42"
            assert call_args.kwargs.get("current_run_id") == pipeline._run_id or call_args.args[2] == pipeline._run_id


# ===========================================================================
# Edge Case Tests
# ===========================================================================

# ---------------------------------------------------------------------------
# TS-128-E1: No prior sessions exist
# Requirement: 128-REQ-1.E1
# ---------------------------------------------------------------------------


class TestNoPriorSessions:
    """Verify query returns empty list when no prior sessions exist."""

    def test_no_prior_sessions(self, db_conn: duckdb.DuckDBPyConnection) -> None:
        """TS-128-E1: empty session_outcomes returns empty list."""
        result = query_prior_attempts(db_conn, "fix-issue-99", "run_current")
        assert result == []


# ---------------------------------------------------------------------------
# TS-128-E2: Database query failure
# Requirement: 128-REQ-1.E2
# ---------------------------------------------------------------------------


class TestQueryFailure:
    """Verify query catches exceptions and returns empty list."""

    def test_query_failure_returns_empty_list(self) -> None:
        """TS-128-E2: broken connection returns empty list, no exception raised."""
        broken_conn = MagicMock()
        broken_conn.execute.side_effect = Exception("connection error")

        result = query_prior_attempts(broken_conn, "fix-issue-42", "run_current")

        assert result == []

    def test_query_failure_logs_warning(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-128-E2: broken connection logs a warning."""
        import logging

        broken_conn = MagicMock()
        broken_conn.execute.side_effect = Exception("connection error")

        with caplog.at_level(logging.WARNING):
            query_prior_attempts(broken_conn, "fix-issue-42", "run_current")

        assert any("connection error" in r.getMessage() or "prior" in r.getMessage().lower() for r in caplog.records)


# ---------------------------------------------------------------------------
# TS-128-E3: Format with empty list
# Requirement: 128-REQ-2.3
# ---------------------------------------------------------------------------


class TestFormatEmpty:
    """Verify format returns empty string for empty input."""

    def test_format_empty_list(self) -> None:
        """TS-128-E3: format_prior_attempts([]) returns empty string."""
        assert format_prior_attempts([]) == ""


# ===========================================================================
# Property Tests
# ===========================================================================

# Strategy: generate random session data for property tests

_run_id_st = st.text(
    alphabet=st.sampled_from("abcdefghijklmnopqrstuvwxyz0123456789_"),
    min_size=3,
    max_size=20,
)


def _insert_sessions_for_property(
    conn: duckdb.DuckDBPyConnection,
    sessions: list[tuple[str, str, str]],
    spec_name: str = "fix-issue-42",
) -> None:
    """Insert sessions from a list of (run_id, archetype, created_at) tuples."""
    for run_id, archetype, created_at in sessions:
        _insert_session(
            conn,
            spec_name=spec_name,
            run_id=run_id,
            archetype=archetype,
            created_at=created_at,
        )


# ---------------------------------------------------------------------------
# TS-128-P1: Current run always excluded
# Properties: Property 1 from design.md
# Requirements: 128-REQ-1.1, 128-REQ-4.2
# ---------------------------------------------------------------------------


class TestPropertyCurrentRunExcluded:
    """For any set of sessions, the current run never appears in results."""

    @given(
        n_prior=st.integers(min_value=0, max_value=5),
        n_current=st.integers(min_value=1, max_value=3),
    )
    @settings(max_examples=20)
    def test_current_run_always_excluded(self, n_prior: int, n_current: int) -> None:
        """TS-128-P1: current_run_id never appears in results."""
        from agentfox.knowledge.migrations import apply_pending_migrations

        from tests.unit.knowledge.conftest import SCHEMA_DDL

        conn = duckdb.connect(":memory:")
        conn.execute(SCHEMA_DDL)
        apply_pending_migrations(conn)

        current_run_id = "run_current"

        # Insert prior run sessions
        for i in range(n_prior):
            _insert_session(
                conn,
                run_id=f"run_prior_{i}",
                created_at=f"2026-05-{10 + i:02d} 10:00:00",
            )

        # Insert current run sessions
        for i in range(n_current):
            _insert_session(
                conn,
                run_id=current_run_id,
                created_at=f"2026-05-{20 + i:02d} 10:00:00",
            )

        result = query_prior_attempts(conn, "fix-issue-42", current_run_id)

        assert all(r.run_id != current_run_id for r in result)
        conn.close()


# ---------------------------------------------------------------------------
# TS-128-P2: One entry per run
# Properties: Property 2 from design.md
# Requirements: 128-REQ-1.2
# ---------------------------------------------------------------------------


class TestPropertyOneEntryPerRun:
    """All run_ids in the result are distinct."""

    @given(
        n_runs=st.integers(min_value=1, max_value=5),
        sessions_per_run=st.integers(min_value=1, max_value=4),
    )
    @settings(max_examples=20)
    def test_one_entry_per_run(self, n_runs: int, sessions_per_run: int) -> None:
        """TS-128-P2: all run_ids in result are distinct."""
        from agentfox.knowledge.migrations import apply_pending_migrations

        from tests.unit.knowledge.conftest import SCHEMA_DDL

        conn = duckdb.connect(":memory:")
        conn.execute(SCHEMA_DDL)
        apply_pending_migrations(conn)

        for run_idx in range(n_runs):
            for session_idx in range(sessions_per_run):
                _insert_session(
                    conn,
                    run_id=f"run_{run_idx}",
                    created_at=f"2026-05-{10 + run_idx:02d} {10 + session_idx:02d}:00:00",
                )

        result = query_prior_attempts(conn, "fix-issue-42", "other_run")

        run_ids = [r.run_id for r in result]
        assert len(set(run_ids)) == len(run_ids)
        conn.close()


# ---------------------------------------------------------------------------
# TS-128-P3: Result bounded by max_results
# Properties: Property 3 from design.md
# Requirements: 128-REQ-1.2
# ---------------------------------------------------------------------------


class TestPropertyResultBounded:
    """Result length never exceeds max_results."""

    @given(
        n_runs=st.integers(min_value=1, max_value=10),
        max_results=st.integers(min_value=1, max_value=10),
    )
    @settings(max_examples=30)
    def test_result_bounded_by_max_results(self, n_runs: int, max_results: int) -> None:
        """TS-128-P3: len(result) <= max_results for any input."""
        from agentfox.knowledge.migrations import apply_pending_migrations

        from tests.unit.knowledge.conftest import SCHEMA_DDL

        conn = duckdb.connect(":memory:")
        conn.execute(SCHEMA_DDL)
        apply_pending_migrations(conn)

        for i in range(n_runs):
            _insert_session(
                conn,
                run_id=f"run_{i}",
                created_at=f"2026-05-{10 + i:02d} 10:00:00",
            )

        result = query_prior_attempts(conn, "fix-issue-42", "other_run", max_results=max_results)

        assert len(result) <= max_results
        conn.close()


# ---------------------------------------------------------------------------
# TS-128-P4: Empty in, empty out
# Properties: Property 4 from design.md
# Requirements: 128-REQ-2.3, 128-REQ-3.2
# ---------------------------------------------------------------------------


class TestPropertyEmptyInEmptyOut:
    """format_prior_attempts([]) always returns ""."""

    def test_empty_in_empty_out(self) -> None:
        """TS-128-P4: unconditionally, format_prior_attempts([]) == ''."""
        assert format_prior_attempts([]) == ""


# ---------------------------------------------------------------------------
# TS-128-P5: Fail-open on query error
# Properties: Property 5 from design.md
# Requirements: 128-REQ-1.E2
# ---------------------------------------------------------------------------


class TestPropertyFailOpen:
    """Any exception in the query returns empty list."""

    @pytest.mark.parametrize(
        "exc_type",
        [RuntimeError, IOError, ValueError, duckdb.CatalogException],
    )
    def test_fail_open_on_query_error(self, exc_type: type) -> None:
        """TS-128-P5: any exception type returns [] and does not raise."""
        mock_conn = MagicMock()
        mock_conn.execute.side_effect = exc_type("test error")

        result = query_prior_attempts(mock_conn, "fix-issue-1", "run_current")

        assert result == []


# ===========================================================================
# Integration Smoke Tests
# ===========================================================================

# ---------------------------------------------------------------------------
# TS-128-SMOKE-1: Full pipeline with prior attempts in prompt
# Execution Path: Path 1 from design.md
# ---------------------------------------------------------------------------


class TestSmokeFullPipelineWithPriorAttempts:
    """End-to-end test that prior attempt context appears in the coder's task prompt."""

    def test_full_pipeline_prior_attempts_in_prompt(self, db_conn: duckdb.DuckDBPyConnection) -> None:
        """TS-128-SMOKE-1: real DuckDB + real pipeline code produces enriched prompt."""
        from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
        from agentfox.nightshift.spec_builder import InMemorySpec

        # Insert a prior session into the real DuckDB
        _insert_session(
            db_conn,
            spec_name="fix-issue-42",
            run_id="old_run",
            archetype="coder",
            status="failed",
            error_message="merge conflict in parser.py",
            model="claude-sonnet-4-5-20250514",
            created_at="2026-05-25 14:00:00",
        )

        # Query using real function (not mocked)
        prior = query_prior_attempts(db_conn, "fix-issue-42", "new_run")
        ctx = format_prior_attempts(prior)

        # Build prompt using real pipeline code
        config = MagicMock()
        pipeline = FixPipeline(config=config, platform=MagicMock())

        spec = InMemorySpec(
            issue_number=42,
            title="Fix the bug",
            task_prompt="Fix the issue: Fix the bug\n\nIssue #42\n\nBug details here.",
            system_context="Bug details here.",
            branch_name="fix/42-fix-the-bug",
        )
        triage = TriageResult()

        _, task_prompt = pipeline._build_coder_prompt(spec, triage, prior_context=ctx)

        assert "Prior Fix Attempts" in task_prompt
        assert "merge conflict" in task_prompt
