"""Integration tests for end-to-end drift finding supersession flow (spec 12).

Verifies the full supersession lifecycle from coder session merge through
fox_provider.ingest() into the drift_findings table state, validating stale
context reduction, resolution coverage, and zero false positives.

Test Spec: TS-12-31 through TS-12-34, TS-12-SMOKE-1 through TS-12-SMOKE-4.
Requirements: 12-REQ-9.1 through 12-REQ-9.4, 12-REQ-3.1, 12-REQ-3.2,
              12-REQ-3.3.
"""

from __future__ import annotations

import logging
import uuid

import duckdb
import pytest
from agentfox.knowledge.migrations import run_migrations
from agentfox.knowledge.review_store import (
    query_active_drift_findings,
)

# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture()
def integration_conn() -> duckdb.DuckDBPyConnection:
    """In-memory DuckDB with all migrations applied for integration tests."""
    c = duckdb.connect(":memory:")
    run_migrations(c)
    yield c  # type: ignore[misc]
    c.close()


_UUID_CACHE: dict[str, str] = {}


def _stable_uuid(alias: str) -> str:
    """Return a deterministic UUID for the given alias (cached)."""
    if alias not in _UUID_CACHE:
        _UUID_CACHE[alias] = str(uuid.uuid5(uuid.NAMESPACE_DNS, alias))
    return _UUID_CACHE[alias]


def _insert_finding(
    conn: duckdb.DuckDBPyConnection,
    *,
    finding_id: str | None = None,
    spec_name: str = "nightshift_standalone_cli",
    task_group: str = "0",
    artifact_ref: str | None = None,
    superseded_by: str | None = None,
    severity: str = "major",
    description: str = "Integration test drift finding",
    session_id: str = "drift_session:1",
) -> str:
    """Insert a drift finding directly into the database and return its ID."""
    fid = finding_id or str(uuid.uuid4())
    try:
        uuid.UUID(fid)
    except ValueError:
        fid = _stable_uuid(fid)
    conn.execute(
        "INSERT INTO drift_findings "
        "(id, severity, description, spec_ref, artifact_ref, "
        "spec_name, task_group, session_id, superseded_by, created_at) "
        "VALUES (?::UUID, ?, ?, NULL, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)",
        [fid, severity, description, artifact_ref, spec_name, task_group, session_id, superseded_by],
    )
    return fid


# ---------------------------------------------------------------------------
# TS-12-31: Seed 12 drift findings (12-REQ-9.1)
# TS-12-32: Assert 10 superseded after merge (12-REQ-9.2)
# TS-12-33: Assert 2 remain active (12-REQ-9.3)
# TS-12-34: query_active_drift_findings returns only 2 (12-REQ-9.4)
# ---------------------------------------------------------------------------


def test_ts12_31_to_34_end_to_end_supersession(
    integration_conn: duckdb.DuckDBPyConnection,
) -> None:
    """End-to-end: seed 12 findings, simulate merge, verify supersession state."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    conn = integration_conn
    spec_name = "nightshift_standalone_cli"
    node_id = "nightshift_standalone_cli:3"
    touched_files = [f"src/file_{i}.py" for i in range(10)]

    # TS-12-31: Seed 12 findings (10 matching, 1 non-matching, 1 null ref)
    for i in range(10):
        _insert_finding(
            conn,
            finding_id=f"match-{i}",
            spec_name=spec_name,
            artifact_ref=f"src/file_{i}.py",
        )
    _insert_finding(
        conn,
        finding_id="nomatch-0",
        spec_name=spec_name,
        artifact_ref="src/unrelated.py",
    )
    _insert_finding(
        conn,
        finding_id="null-ref",
        spec_name=spec_name,
        artifact_ref=None,
    )

    total_count = conn.execute(
        "SELECT COUNT(*) FROM drift_findings WHERE spec_name = ?",
        [spec_name],
    ).fetchone()[0]
    assert total_count == 12

    # TS-12-32: Simulate supersession via direct function call
    result = supersede_drift_findings_by_files(conn, spec_name, touched_files, node_id)
    assert result == 10

    superseded_count = conn.execute(
        "SELECT COUNT(*) FROM drift_findings WHERE spec_name = ? AND superseded_by = ?",
        [spec_name, node_id],
    ).fetchone()[0]
    assert superseded_count == 10

    # TS-12-33: Assert 2 remain active
    active_count = conn.execute(
        "SELECT COUNT(*) FROM drift_findings WHERE spec_name = ? AND superseded_by IS NULL",
        [spec_name],
    ).fetchone()[0]
    assert active_count == 2

    active_ids = conn.execute(
        "SELECT id::VARCHAR FROM drift_findings WHERE spec_name = ? AND superseded_by IS NULL",
        [spec_name],
    ).fetchall()
    active_id_set = {r[0] for r in active_ids}
    assert _stable_uuid("nomatch-0") in active_id_set
    assert _stable_uuid("null-ref") in active_id_set

    # TS-12-34: query_active_drift_findings returns exactly 2
    results = query_active_drift_findings(conn, spec_name, include_prereview=True)
    assert len(results) == 2
    for finding in results:
        assert finding.superseded_by is None
    result_ids = {f.id for f in results}
    assert _stable_uuid("nomatch-0") in result_ids
    assert _stable_uuid("null-ref") in result_ids


# ---------------------------------------------------------------------------
# TS-12-SMOKE-1: Full coder session merge through fox_provider (12-PATH-1)
# ---------------------------------------------------------------------------


def test_ts12_smoke1_full_coder_merge_path(
    integration_conn: duckdb.DuckDBPyConnection,
) -> None:
    """Smoke: coder session merge triggers drift finding supersession."""

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    conn = integration_conn
    spec_name = "smoke_spec"
    node_id = "smoke_spec:1"

    # Seed 5 findings: 3 matching, 2 non-matching
    for i in range(3):
        _insert_finding(
            conn,
            finding_id=f"smoke-match-{i}",
            spec_name=spec_name,
            artifact_ref=f"src/file_{i}.py",
        )
    _insert_finding(
        conn,
        finding_id="smoke-nomatch",
        spec_name=spec_name,
        artifact_ref="src/unrelated.py",
    )
    _insert_finding(
        conn,
        finding_id="smoke-null",
        spec_name=spec_name,
        artifact_ref=None,
    )

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    # Simulate ingest with completed coder session
    provider.ingest(
        session_id=node_id,
        spec_name=spec_name,
        context={
            "session_status": "completed",
            "touched_files": ["src/file_0.py", "src/file_1.py", "src/file_2.py"],
            "project_root": "",
            "archetype": "coder",
        },
    )

    # Verify 4 superseded (3 file-matched + 1 pre-code null-ref), 1 active
    superseded_count = conn.execute(
        "SELECT COUNT(*) FROM drift_findings WHERE spec_name = ? AND superseded_by = ?",
        [spec_name, node_id],
    ).fetchone()[0]
    assert superseded_count == 4

    active_count = conn.execute(
        "SELECT COUNT(*) FROM drift_findings WHERE spec_name = ? AND superseded_by IS NULL",
        [spec_name],
    ).fetchone()[0]
    assert active_count == 1


# ---------------------------------------------------------------------------
# TS-12-SMOKE-2: Null touched_files short-circuit (12-PATH-2)
# ---------------------------------------------------------------------------


def test_ts12_smoke2_null_touched_files_short_circuit(
    integration_conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Smoke: null touched_files short-circuits without DB modification."""
    # Ensure the function exists and is importable (fails until implemented)
    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    conn = integration_conn
    spec_name = "smoke_spec"

    _insert_finding(
        conn,
        finding_id="smoke-active",
        spec_name=spec_name,
        artifact_ref="src/file.py",
    )

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    with caplog.at_level(logging.DEBUG):
        provider.ingest(
            session_id="smoke_spec:1",
            spec_name=spec_name,
            context={
                "session_status": "completed",
                "touched_files": None,
                "project_root": "",
                "archetype": "coder",
            },
        )

    # Verify the function was called (via debug log from supersede_drift_findings_by_files)
    drift_debug_msgs = [
        r.message for r in caplog.records if r.levelno == logging.DEBUG and "drift" in r.message.lower()
    ]
    assert len(drift_debug_msgs) > 0, (
        f"Expected a debug log from supersede_drift_findings_by_files; got: {[r.message for r in caplog.records]}"
    )

    # Finding remains active
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
        [_stable_uuid("smoke-active")],
    ).fetchone()
    assert row is not None
    assert row[0] is None

    # Confirm the function reference is valid (suppress F841 via usage)
    assert callable(supersede_drift_findings_by_files)


# ---------------------------------------------------------------------------
# TS-12-SMOKE-3: Exception swallowed in fox_provider (12-PATH-3)
# ---------------------------------------------------------------------------


def test_ts12_smoke3_exception_swallowed(
    integration_conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Smoke: exception from supersession is caught and logged as warning."""
    from unittest.mock import patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    conn = integration_conn
    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    with (
        patch("agentfox.knowledge.fox_provider.supersede_injected_findings"),
        patch(
            "agentfox.knowledge.fox_provider.supersede_drift_findings_by_files",
            side_effect=RuntimeError("DuckDB failure"),
        ),
        caplog.at_level(logging.WARNING),
    ):
        # Must not raise
        provider.ingest(
            session_id="smoke_spec:1",
            spec_name="smoke_spec",
            context={
                "session_status": "completed",
                "touched_files": ["src/foo.py"],
                "project_root": "",
                "archetype": "coder",
            },
        )

    assert any(record.levelno == logging.WARNING for record in caplog.records)


# ---------------------------------------------------------------------------
# TS-12-SMOKE-4: Reviewer/verifier sessions skip drift supersession (12-PATH-4)
# ---------------------------------------------------------------------------


def test_ts12_smoke4_reviewer_verifier_skip_supersession(
    integration_conn: duckdb.DuckDBPyConnection,
) -> None:
    """Smoke: reviewer and verifier sessions do not trigger drift supersession."""
    from unittest.mock import MagicMock, patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    conn = integration_conn
    spec_name = "smoke_spec"

    _insert_finding(
        conn,
        finding_id="smoke-persist",
        spec_name=spec_name,
        artifact_ref="src/file.py",
    )

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    mock_supersede_drift = MagicMock(return_value=0)

    with patch(
        "agentfox.knowledge.fox_provider.supersede_drift_findings_by_files",
        mock_supersede_drift,
    ):
        for archetype in ("reviewer", "verifier"):
            provider.ingest(
                session_id="smoke_spec:1",
                spec_name=spec_name,
                context={
                    "session_status": "completed",
                    "touched_files": ["src/file.py"],
                    "project_root": "",
                    "archetype": archetype,
                },
            )

    assert mock_supersede_drift.call_count == 0

    # Verify finding unchanged
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
        [_stable_uuid("smoke-persist")],
    ).fetchone()
    assert row is not None
    assert row[0] is None
