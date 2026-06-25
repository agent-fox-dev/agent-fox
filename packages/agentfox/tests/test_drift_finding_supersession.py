"""Tests for file-based drift finding supersession (spec 12).

Verifies that ``supersede_drift_findings_by_files`` correctly matches drift
findings by artifact_ref against a session's touched files, supersedes matching
findings, and integrates with the post-merge ingest path in fox_provider.

Test Spec: TS-12-1 through TS-12-30, TS-12-E1 through TS-12-E4,
           TS-12-P1 through TS-12-P6, TS-12-35, TS-12-36.
Requirements: 12-REQ-1 through 12-REQ-10.
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
def conn() -> duckdb.DuckDBPyConnection:
    """In-memory DuckDB with fully migrated schema."""
    c = duckdb.connect(":memory:")
    run_migrations(c)
    yield c  # type: ignore[misc]
    c.close()


# Stable deterministic UUIDs keyed by short alias for test assertions.
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
    spec_name: str = "test_spec",
    task_group: str = "0",
    artifact_ref: str | None = "src/foo.py",
    superseded_by: str | None = None,
    severity: str = "major",
    description: str = "Test drift finding",
    session_id: str = "drift_session:1",
) -> str:
    """Insert a drift finding directly into the database and return its ID.

    If *finding_id* is not a valid UUID, it is converted to a stable UUID
    via ``uuid.uuid5`` so that the DuckDB UUID column accepts it while
    keeping test assertions deterministic.
    """
    fid = finding_id or str(uuid.uuid4())
    # Convert short aliases to proper UUIDs for the UUID column.
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
# TS-12-1: Function signature and return type (12-REQ-1.1)
# ---------------------------------------------------------------------------


def test_ts12_01_signature_and_return_type(conn: duckdb.DuckDBPyConnection) -> None:
    """supersede_drift_findings_by_files accepts four params and returns int."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    _insert_finding(conn, spec_name="test_spec", artifact_ref="src/foo.py")
    result = supersede_drift_findings_by_files(conn, "test_spec", ["src/foo.py"], "test_spec:1")
    assert isinstance(result, int)
    assert result >= 0


# ---------------------------------------------------------------------------
# TS-12-2: Empty touched_files returns 0, debug log, no DB writes (12-REQ-1.2)
# ---------------------------------------------------------------------------


def test_ts12_02_empty_touched_files_short_circuit(
    conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Empty touched_files logs debug and returns 0 without DB writes."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(conn, spec_name="test_spec", artifact_ref="src/foo.py")
    with caplog.at_level(logging.DEBUG):
        result = supersede_drift_findings_by_files(conn, "test_spec", [], "test_spec:1")
    assert result == 0
    assert any(record.levelno == logging.DEBUG for record in caplog.records)
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None


# ---------------------------------------------------------------------------
# TS-12-3: Cross-task-group query (12-REQ-1.3)
# ---------------------------------------------------------------------------


def test_ts12_03_cross_task_group_supersession(conn: duckdb.DuckDBPyConnection) -> None:
    """Findings from both task group 0 and 2 are superseded when matching."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    _insert_finding(
        conn, finding_id="id-g0", spec_name="test_spec", task_group="0", artifact_ref="src/foo.py"
    )
    _insert_finding(
        conn, finding_id="id-g2", spec_name="test_spec", task_group="2", artifact_ref="src/bar.py"
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["src/foo.py", "src/bar.py"], "test_spec:3"
    )
    assert result == 2
    rows = conn.execute(
        "SELECT id, superseded_by FROM drift_findings WHERE spec_name = ?",
        ["test_spec"],
    ).fetchall()
    for row in rows:
        assert row[1] == "test_spec:3"


# ---------------------------------------------------------------------------
# TS-12-4: Null artifact_ref skip (12-REQ-1.4)
# ---------------------------------------------------------------------------



def test_ts12_04_null_artifact_ref_skipped(conn: duckdb.DuckDBPyConnection) -> None:
    """Findings with null artifact_ref are never superseded."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="id-null", spec_name="test_spec", artifact_ref=None
    )
    result = supersede_drift_findings_by_files(conn, "test_spec", ["src/foo.py"], "test_spec:1")
    assert result == 0
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None


# ---------------------------------------------------------------------------
# TS-12-5: Line-number suffix stripping (12-REQ-1.5)
# ---------------------------------------------------------------------------



def test_ts12_05_line_number_stripping(conn: duckdb.DuckDBPyConnection) -> None:
    """artifact_ref 'src/foo.py:42' normalizes to 'src/foo.py' and matches."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="id-ln", spec_name="test_spec", artifact_ref="src/foo.py:42"
    )
    result = supersede_drift_findings_by_files(conn, "test_spec", ["src/foo.py"], "test_spec:1")
    assert result == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "test_spec:1"


# ---------------------------------------------------------------------------
# TS-12-6: Prefix matching for directory refs (12-REQ-1.6)
# ---------------------------------------------------------------------------



def test_ts12_06_prefix_matching(conn: duckdb.DuckDBPyConnection) -> None:
    """Trailing-slash artifact_ref uses prefix matching."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn,
        finding_id="id-dir",
        spec_name="test_spec",
        artifact_ref="packages/nightshift/",
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["packages/nightshift/src/main.py"], "test_spec:1"
    )
    assert result == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "test_spec:1"


# ---------------------------------------------------------------------------
# TS-12-7: Exact path matching (12-REQ-1.7)
# ---------------------------------------------------------------------------



def test_ts12_07_exact_matching(conn: duckdb.DuckDBPyConnection) -> None:
    """Non-trailing-slash artifact_ref uses exact matching."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="id-exact", spec_name="test_spec", artifact_ref="src/foo.py"
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["src/foo.py"], "test_spec:1"
    )
    assert result == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "test_spec:1"


# ---------------------------------------------------------------------------
# TS-12-8: Superseded_by marker and count (12-REQ-1.8)
# ---------------------------------------------------------------------------



def test_ts12_08_superseded_by_marker_and_count(conn: duckdb.DuckDBPyConnection) -> None:
    """superseded_by set to node_id for matching; non-matching left null."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    _insert_finding(conn, finding_id="id-a", spec_name="test_spec", artifact_ref="src/a.py")
    _insert_finding(conn, finding_id="id-b", spec_name="test_spec", artifact_ref="src/b.py")
    _insert_finding(conn, finding_id="id-c", spec_name="test_spec", artifact_ref="src/c.py")
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["src/a.py", "src/b.py"], "test_spec:2"
    )
    assert result == 2
    for alias in ["id-a", "id-b"]:
        row = conn.execute(
            "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
            [_stable_uuid(alias)],
        ).fetchone()
        assert row is not None
        assert row[0] == "test_spec:2"
    row_c = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
        [_stable_uuid("id-c")],
    ).fetchone()
    assert row_c is not None
    assert row_c[0] is None


# ---------------------------------------------------------------------------
# TS-12-9: Observability logging (12-REQ-1.9)
# ---------------------------------------------------------------------------



def test_ts12_09_observability_logging(
    conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Superseded finding ID and artifact_ref are logged."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="id-log", spec_name="test_spec", artifact_ref="src/foo.py"
    )
    with caplog.at_level(logging.DEBUG):
        supersede_drift_findings_by_files(conn, "test_spec", ["src/foo.py"], "test_spec:1")
    assert any(
        fid in record.message and "src/foo.py" in record.message
        for record in caplog.records
    )


# ---------------------------------------------------------------------------
# TS-12-10: Private helper query (12-REQ-2.1)
# ---------------------------------------------------------------------------



def test_ts12_10_private_helper_query(conn: duckdb.DuckDBPyConnection) -> None:
    """Private helper returns active findings across all task groups."""
    from agentfox.knowledge.review_store import (
        _query_active_drift_findings_for_spec,
    )

    _insert_finding(
        conn,
        finding_id="id-g0",
        spec_name="test_spec",
        task_group="0",
        artifact_ref="src/a.py",
    )
    _insert_finding(
        conn,
        finding_id="id-g3",
        spec_name="test_spec",
        task_group="3",
        artifact_ref="src/b.py",
    )
    _insert_finding(
        conn,
        finding_id="id-sup",
        spec_name="test_spec",
        task_group="0",
        artifact_ref="src/c.py",
        superseded_by="test_spec:1",
    )
    rows = _query_active_drift_findings_for_spec(conn, "test_spec")
    assert len(rows) == 2
    ids = [str(r[0]) for r in rows]
    assert _stable_uuid("id-g0") in ids
    assert _stable_uuid("id-g3") in ids
    assert _stable_uuid("id-sup") not in ids


# ---------------------------------------------------------------------------
# TS-12-11: Private helper not in public API (12-REQ-2.2)
# ---------------------------------------------------------------------------



def test_ts12_11_private_helper_not_public() -> None:
    """The private helper exists but is not part of the public review_store API."""
    import agentfox.knowledge.review_store as review_store

    # The function must exist (proves it was implemented)
    assert hasattr(review_store, "_query_active_drift_findings_for_spec")
    # And it must not be in the public API
    public_api = [name for name in dir(review_store) if not name.startswith("_")]
    assert "_query_active_drift_findings_for_spec" not in public_api
    if hasattr(review_store, "__all__"):
        assert "_query_active_drift_findings_for_spec" not in review_store.__all__


# ---------------------------------------------------------------------------
# TS-12-12: result_handler/fox_provider call ordering (12-REQ-3.1)
#
# Drift report: supersede_injected_findings is called in fox_provider.py
# ingest(), not in result_handler.py. Tests target fox_provider.ingest().
# ---------------------------------------------------------------------------


def test_ts12_12_fox_provider_calls_drift_supersession_after_injected(
    conn: duckdb.DuckDBPyConnection,
) -> None:
    """fox_provider.ingest() calls supersede_drift_findings_by_files after
    supersede_injected_findings for a completed coder session."""
    from unittest.mock import patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    call_order: list[str] = []

    def mock_supersede_injected(*args: object, **kwargs: object) -> None:
        call_order.append("injected")

    def mock_supersede_drift(*args: object, **kwargs: object) -> int:
        call_order.append("drift")
        return 0

    with (
        patch(
            "agentfox.knowledge.fox_provider.supersede_injected_findings",
            side_effect=mock_supersede_injected,
        ),
        patch(
            "agentfox.knowledge.fox_provider.supersede_drift_findings_by_files",
            side_effect=mock_supersede_drift,
        ),
    ):
        provider.ingest(
            session_id="test_spec:1",
            spec_name="test_spec",
            context={
                "session_status": "completed",
                "touched_files": ["src/foo.py"],
                "project_root": "",
                "archetype": "coder",
            },
        )

    assert call_order == ["injected", "drift"]


# ---------------------------------------------------------------------------
# TS-12-13: No drift supersession for reviewer/verifier (12-REQ-3.2)
#
# Drift report: the coder-only guard must be added in fox_provider.ingest().
# ---------------------------------------------------------------------------


def test_ts12_13_no_drift_supersession_for_reviewer_verifier(
    conn: duckdb.DuckDBPyConnection,
) -> None:
    """supersede_drift_findings_by_files is NOT called for reviewer/verifier."""
    from unittest.mock import MagicMock, patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

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
                session_id="test_spec:1",
                spec_name="test_spec",
                context={
                    "session_status": "completed",
                    "touched_files": ["src/foo.py"],
                    "project_root": "",
                    "archetype": archetype,
                },
            )

    assert mock_supersede_drift.call_count == 0


# ---------------------------------------------------------------------------
# TS-12-14: Exception swallowed with warning log (12-REQ-3.3)
# ---------------------------------------------------------------------------


def test_ts12_14_exception_swallowed_with_warning(
    conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """Exception from supersede_drift_findings_by_files is caught and logged."""
    from unittest.mock import patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    with (
        patch(
            "agentfox.knowledge.fox_provider.supersede_injected_findings",
        ),
        patch(
            "agentfox.knowledge.fox_provider.supersede_drift_findings_by_files",
            side_effect=RuntimeError("DuckDB failure"),
        ),
        caplog.at_level(logging.WARNING),
    ):
        # Must not raise
        provider.ingest(
            session_id="test_spec:1",
            spec_name="test_spec",
            context={
                "session_status": "completed",
                "touched_files": ["src/foo.py"],
                "project_root": "",
                "archetype": "coder",
            },
        )

    assert any(record.levelno == logging.WARNING for record in caplog.records)


# ---------------------------------------------------------------------------
# TS-12-15: Exact match hit (12-REQ-4.1)
# ---------------------------------------------------------------------------



def test_ts12_15_exact_match_hit(conn: duckdb.DuckDBPyConnection) -> None:
    """Finding with exact matching artifact_ref is superseded."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="id-exact", spec_name="test_spec", artifact_ref="src/foo.py"
    )
    result = supersede_drift_findings_by_files(conn, "test_spec", ["src/foo.py"], "test_spec:1")
    assert result == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "test_spec:1"


# ---------------------------------------------------------------------------
# TS-12-16: Exact match miss (12-REQ-4.2)
# ---------------------------------------------------------------------------



def test_ts12_16_exact_match_miss(conn: duckdb.DuckDBPyConnection) -> None:
    """Finding whose artifact_ref does not match remains active."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn,
        finding_id="id-no-match",
        spec_name="test_spec",
        artifact_ref="src/foo.py",
    )
    result = supersede_drift_findings_by_files(conn, "test_spec", ["src/bar.py"], "test_spec:1")
    assert result == 0
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None


# ---------------------------------------------------------------------------
# TS-12-17: Prefix match hit (12-REQ-5.1)
# ---------------------------------------------------------------------------



def test_ts12_17_prefix_match_hit(conn: duckdb.DuckDBPyConnection) -> None:
    """Trailing-slash artifact_ref superseded when touched file matches prefix."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn,
        finding_id="id-dir",
        spec_name="test_spec",
        artifact_ref="packages/nightshift/",
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["packages/nightshift/cli.py"], "test_spec:1"
    )
    assert result == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "test_spec:1"


# ---------------------------------------------------------------------------
# TS-12-18: Prefix match miss (12-REQ-5.2)
# ---------------------------------------------------------------------------



def test_ts12_18_prefix_match_miss(conn: duckdb.DuckDBPyConnection) -> None:
    """Trailing-slash artifact_ref remains active when no file matches prefix."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn,
        finding_id="id-dir-miss",
        spec_name="test_spec",
        artifact_ref="packages/nightshift/",
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["packages/other/cli.py"], "test_spec:1"
    )
    assert result == 0
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None


# ---------------------------------------------------------------------------
# TS-12-19: Supersession marker string equality (12-REQ-6.1)
# ---------------------------------------------------------------------------



def test_ts12_19_supersession_marker_exact_string(conn: duckdb.DuckDBPyConnection) -> None:
    """superseded_by is set to exactly the node_id string."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    _insert_finding(conn, finding_id="id-a", spec_name="my_spec", artifact_ref="src/a.py")
    _insert_finding(conn, finding_id="id-b", spec_name="my_spec", artifact_ref="src/b.py")
    supersede_drift_findings_by_files(
        conn, "my_spec", ["src/a.py", "src/b.py"], "my_spec:3"
    )
    for alias in ["id-a", "id-b"]:
        row = conn.execute(
            "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
            [_stable_uuid(alias)],
        ).fetchone()
        assert row is not None
        assert row[0] == "my_spec:3"


# ---------------------------------------------------------------------------
# TS-12-20: Null artifact_ref persists after function call (12-REQ-7.1)
# ---------------------------------------------------------------------------



def test_ts12_20_null_artifact_ref_persists(conn: duckdb.DuckDBPyConnection) -> None:
    """Findings with null artifact_ref remain active."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="id-null", spec_name="test_spec", artifact_ref=None
    )
    supersede_drift_findings_by_files(conn, "test_spec", ["src/any.py"], "test_spec:1")
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None


# ---------------------------------------------------------------------------
# TS-12-21: Non-matching artifact_ref persists (12-REQ-7.2)
# ---------------------------------------------------------------------------



def test_ts12_21_non_matching_persists(conn: duckdb.DuckDBPyConnection) -> None:
    """Findings whose artifact_ref doesn't match remain active."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn,
        finding_id="id-unrel",
        spec_name="test_spec",
        artifact_ref="src/unrelated.py",
    )
    supersede_drift_findings_by_files(
        conn, "test_spec", ["src/different.py"], "test_spec:1"
    )
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None


# ---------------------------------------------------------------------------
# TS-12-22: query_active_drift_findings excludes superseded (12-REQ-7.3)
# ---------------------------------------------------------------------------


def test_ts12_22_query_excludes_superseded(conn: duckdb.DuckDBPyConnection) -> None:
    """query_active_drift_findings returns only active findings."""
    active_id = _insert_finding(
        conn,
        finding_id="id-active",
        spec_name="test_spec",
        task_group="0",
        artifact_ref="src/a.py",
    )
    sup_id = _insert_finding(
        conn,
        finding_id="id-sup",
        spec_name="test_spec",
        task_group="0",
        artifact_ref="src/b.py",
        superseded_by="test_spec:1",
    )
    results = query_active_drift_findings(conn, "test_spec", include_prereview=True)
    ids = [r.id for r in results]
    assert active_id in ids
    assert sup_id not in ids


# ---------------------------------------------------------------------------
# TS-12-23 through TS-12-30: REQ-8 unit test matrix
# ---------------------------------------------------------------------------



def test_ts12_23_req8_exact_match(conn: duckdb.DuckDBPyConnection) -> None:
    """REQ-8.1: Exact path match supersedes finding."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(conn, finding_id="f1", spec_name="s", artifact_ref="src/foo.py")
    count = supersede_drift_findings_by_files(conn, "s", ["src/foo.py"], "spec:1")
    assert count == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "spec:1"



def test_ts12_24_req8_exact_non_match(conn: duckdb.DuckDBPyConnection) -> None:
    """REQ-8.2: Exact path non-match leaves finding active."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(conn, finding_id="f2", spec_name="s", artifact_ref="src/foo.py")
    count = supersede_drift_findings_by_files(conn, "s", ["src/bar.py"], "spec:1")
    assert count == 0
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None



def test_ts12_25_req8_directory_prefix(conn: duckdb.DuckDBPyConnection) -> None:
    """REQ-8.3: Directory prefix match supersedes finding."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="f3", spec_name="s", artifact_ref="packages/nightshift/"
    )
    count = supersede_drift_findings_by_files(
        conn, "s", ["packages/nightshift/utils.py"], "spec:1"
    )
    assert count == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "spec:1"



def test_ts12_26_req8_line_number_strip(conn: duckdb.DuckDBPyConnection) -> None:
    """REQ-8.4: Line number suffix is stripped before matching."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn, finding_id="f4", spec_name="s", artifact_ref="src/foo.py:42"
    )
    count = supersede_drift_findings_by_files(conn, "s", ["src/foo.py"], "spec:1")
    assert count == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "spec:1"



def test_ts12_27_req8_null_ref(conn: duckdb.DuckDBPyConnection) -> None:
    """REQ-8.5: Null artifact_ref is never superseded."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(conn, finding_id="f5", spec_name="s", artifact_ref=None)
    count = supersede_drift_findings_by_files(conn, "s", ["src/foo.py"], "spec:1")
    assert count == 0
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None



def test_ts12_28_req8_empty_list(
    conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """REQ-8.6: Empty touched_files returns 0 with debug log, no DB writes."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(conn, finding_id="f6", spec_name="s", artifact_ref="src/foo.py")
    with caplog.at_level(logging.DEBUG):
        count = supersede_drift_findings_by_files(conn, "s", [], "spec:1")
    assert count == 0
    assert any(record.levelno == logging.DEBUG for record in caplog.records)
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None



def test_ts12_29_req8_null_list(
    conn: duckdb.DuckDBPyConnection,
    caplog: pytest.LogCaptureFixture,
) -> None:
    """REQ-8.7: None touched_files treated identically to empty list."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(conn, finding_id="f7", spec_name="s", artifact_ref="src/foo.py")
    with caplog.at_level(logging.DEBUG):
        count = supersede_drift_findings_by_files(conn, "s", None, "spec:1")
    assert count == 0
    assert any(record.levelno == logging.DEBUG for record in caplog.records)
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] is None



def test_ts12_30_req8_multiple_matches(conn: duckdb.DuckDBPyConnection) -> None:
    """REQ-8.8: Multiple findings matching same file all superseded."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fids = []
    for i in range(3):
        fids.append(
            _insert_finding(
                conn, finding_id=f"f8-{i}", spec_name="s", artifact_ref="src/foo.py"
            )
        )
    count = supersede_drift_findings_by_files(conn, "s", ["src/foo.py"], "spec:1")
    assert count == 3
    for fid in fids:
        row = conn.execute(
            "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
        ).fetchone()
        assert row is not None
        assert row[0] == "spec:1"


# ---------------------------------------------------------------------------
# TS-12-E1: All null artifact_ref findings (12-REQ-1.E1)
# ---------------------------------------------------------------------------



def test_ts12_e1_all_null_artifact_refs(conn: duckdb.DuckDBPyConnection) -> None:
    """All findings with null artifact_ref: returns 0, no DB writes."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    for i in range(3):
        _insert_finding(
            conn, finding_id=f"null-{i}", spec_name="test_spec", artifact_ref=None
        )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["src/foo.py", "src/bar.py"], "test_spec:1"
    )
    assert result == 0
    rows = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE spec_name = ?",
        ["test_spec"],
    ).fetchall()
    assert all(row[0] is None for row in rows)


# ---------------------------------------------------------------------------
# TS-12-E2: Multiple distinct findings share same artifact_ref (12-REQ-1.E2)
# ---------------------------------------------------------------------------



def test_ts12_e2_multiple_findings_same_ref(conn: duckdb.DuckDBPyConnection) -> None:
    """Multiple findings with same artifact_ref all superseded; count correct."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    shared_fids = []
    for i in range(3):
        shared_fids.append(
            _insert_finding(
                conn,
                finding_id=f"shared-{i}",
                spec_name="test_spec",
                artifact_ref="src/shared.py",
            )
        )
    other_fid = _insert_finding(
        conn,
        finding_id="other",
        spec_name="test_spec",
        artifact_ref="src/other.py",
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["src/shared.py"], "test_spec:2"
    )
    assert result == 3
    for fid in shared_fids:
        row = conn.execute(
            "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
            [fid],
        ).fetchone()
        assert row is not None
        assert row[0] == "test_spec:2"
    other_row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [other_fid]
    ).fetchone()
    assert other_row is not None
    assert other_row[0] is None


# ---------------------------------------------------------------------------
# TS-12-E3: SessionRecord with null touched_files (12-REQ-3.E1)
#
# Drift report: uses fox_provider.ingest(), field is files_touched on
# SessionRecord but ingest() receives context dict with 'touched_files'.
# ---------------------------------------------------------------------------


def test_ts12_e3_null_touched_files_passthrough(
    conn: duckdb.DuckDBPyConnection,
) -> None:
    """Null touched_files passed through to supersede function, returns 0."""
    from unittest.mock import patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    db = KnowledgeDB.__new__(KnowledgeDB)
    db._conn = conn
    provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

    recorded_args: list[object] = []

    def mock_fn(conn: object, spec_name: str, touched_files: object, node_id: str) -> int:
        recorded_args.append(touched_files)
        return 0

    with (
        patch("agentfox.knowledge.fox_provider.supersede_injected_findings"),
        patch(
            "agentfox.knowledge.fox_provider.supersede_drift_findings_by_files",
            side_effect=mock_fn,
        ),
    ):
        provider.ingest(
            session_id="test_spec:1",
            spec_name="test_spec",
            context={
                "session_status": "completed",
                "touched_files": None,
                "project_root": "",
                "archetype": "coder",
            },
        )

    assert len(recorded_args) == 1
    assert recorded_args[0] is None


# ---------------------------------------------------------------------------
# TS-12-E4: Line-number suffix stripped (12-REQ-4.E1)
# ---------------------------------------------------------------------------



def test_ts12_e4_line_number_suffix_stripped(conn: duckdb.DuckDBPyConnection) -> None:
    """artifact_ref with ':42' suffix matches touched file without suffix."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    fid = _insert_finding(
        conn,
        finding_id="id-lnum",
        spec_name="test_spec",
        artifact_ref="src/foo.py:42",
    )
    result = supersede_drift_findings_by_files(
        conn, "test_spec", ["src/foo.py"], "test_spec:1"
    )
    assert result == 1
    row = conn.execute(
        "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?", [fid]
    ).fetchone()
    assert row is not None
    assert row[0] == "test_spec:1"


# ---------------------------------------------------------------------------
# TS-12-P1 through TS-12-P6: Property-based tests
# ---------------------------------------------------------------------------

hypothesis = pytest.importorskip("hypothesis")

given = hypothesis.given  # noqa: E402
settings = hypothesis.settings  # noqa: E402
st = hypothesis.strategies  # noqa: E402

# Strategy: file paths (simple alphanumeric segments with slashes)
_path_segment = st.text(
    alphabet=st.sampled_from("abcdefghijklmnopqrstuvwxyz_0123456789"),
    min_size=1,
    max_size=12,
)
_file_path = st.builds(
    lambda parts, ext: "/".join(parts) + ext,
    st.lists(_path_segment, min_size=1, max_size=4),
    st.sampled_from([".py", ".ts", ".rs", ".go", ".md"]),
)
_dir_path = st.builds(
    lambda parts: "/".join(parts) + "/",
    st.lists(_path_segment, min_size=1, max_size=4),
)
_artifact_ref = st.one_of(_file_path, _dir_path)
_node_id = st.builds(
    lambda name, group: f"{name}:{group}",
    st.text(
        alphabet=st.sampled_from("abcdefghijklmnopqrstuvwxyz_"),
        min_size=1,
        max_size=10,
    ),
    st.integers(min_value=1, max_value=99).map(str),
)


def _normalize_ref(ref: str) -> str:
    """Replicate expected normalization for property tests."""
    import re

    normalized = ref.strip()
    normalized = re.sub(r"(:\d+)+$", "", normalized)
    return normalized



@settings(max_examples=500, deadline=None)
@given(
    artifact_refs=st.lists(_artifact_ref, min_size=1, max_size=5),
    touched_files=st.lists(_file_path, min_size=0, max_size=5),
    node_id=_node_id,
)
def test_ts12_p1_zero_false_positive(
    artifact_refs: list[str],
    touched_files: list[str],
    node_id: str,
) -> None:
    """PROP-1: Finding superseded only if touched_files matches artifact_ref."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    try:
        for i, ref in enumerate(artifact_refs):
            _insert_finding(
                conn,
                finding_id=f"prop1-{i}",
                spec_name="s",
                artifact_ref=ref,
            )
        supersede_drift_findings_by_files(conn, "s", touched_files, node_id)
        touched_set = set(touched_files)
        for i, ref in enumerate(artifact_refs):
            row = conn.execute(
                "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
                [_stable_uuid(f"prop1-{i}")],
            ).fetchone()
            normalized = _normalize_ref(ref)
            if normalized.endswith("/"):
                should_match = any(f.startswith(normalized) for f in touched_files)
            else:
                should_match = normalized in touched_set
            if row[0] is not None:
                assert should_match, (
                    f"False positive: {ref!r} superseded but no touched file matches"
                )
            else:
                assert not should_match, (
                    f"False negative: {ref!r} not superseded but touched files match"
                )
    finally:
        conn.close()



@settings(max_examples=200, deadline=None)
@given(
    touched_files=st.lists(_file_path, min_size=1, max_size=5),
    node_id=_node_id,
)
def test_ts12_p2_null_ref_never_superseded(
    touched_files: list[str],
    node_id: str,
) -> None:
    """PROP-2: Null artifact_ref findings never superseded."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    try:
        fid = _insert_finding(conn, finding_id="null-prop", spec_name="s", artifact_ref=None)
        supersede_drift_findings_by_files(conn, "s", touched_files, node_id)
        row = conn.execute(
            "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
            [fid],
        ).fetchone()
        assert row[0] is None
    finally:
        conn.close()



@settings(max_examples=100, deadline=None)
@given(
    artifact_refs=st.lists(_artifact_ref, min_size=1, max_size=3),
    use_none=st.booleans(),
)
def test_ts12_p3_empty_touched_no_side_effects(
    artifact_refs: list[str],
    use_none: bool,
) -> None:
    """PROP-3: Empty/None touched_files returns 0, no DB modifications."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    try:
        for i, ref in enumerate(artifact_refs):
            _insert_finding(
                conn,
                finding_id=f"prop3-{i}",
                spec_name="s",
                artifact_ref=ref,
            )
        snapshot_before = conn.execute(
            "SELECT id, superseded_by FROM drift_findings ORDER BY id"
        ).fetchall()
        touched = None if use_none else []
        result = supersede_drift_findings_by_files(conn, "s", touched, "spec:1")
        assert result == 0
        snapshot_after = conn.execute(
            "SELECT id, superseded_by FROM drift_findings ORDER BY id"
        ).fetchall()
        assert snapshot_before == snapshot_after
    finally:
        conn.close()


@settings(max_examples=100, deadline=None)
@given(
    node_id=_node_id,
)
def test_ts12_p4_superseded_excluded_from_query(
    node_id: str,
) -> None:
    """PROP-4: Superseded findings excluded from query_active_drift_findings."""
    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    try:
        _insert_finding(
            conn,
            finding_id="active",
            spec_name="s",
            task_group="0",
            artifact_ref="src/a.py",
        )
        _insert_finding(
            conn,
            finding_id="sup",
            spec_name="s",
            task_group="0",
            artifact_ref="src/b.py",
            superseded_by=node_id,
        )
        results = query_active_drift_findings(conn, "s", include_prereview=True)
        for f in results:
            assert f.superseded_by is None
    finally:
        conn.close()



@settings(max_examples=100, deadline=None)
@given(
    node_id=_node_id,
    artifact_ref=_file_path,
)
def test_ts12_p5_marker_consistency(
    node_id: str,
    artifact_ref: str,
) -> None:
    """PROP-5: superseded_by equals node_id exactly for superseded findings."""
    from agentfox.knowledge.review_store import supersede_drift_findings_by_files

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    try:
        fid = _insert_finding(
            conn, finding_id="marker-test", spec_name="s", artifact_ref=artifact_ref
        )
        supersede_drift_findings_by_files(conn, "s", [artifact_ref], node_id)
        row = conn.execute(
            "SELECT superseded_by FROM drift_findings WHERE id::VARCHAR = ?",
            [fid],
        ).fetchone()
        if row[0] is not None:
            assert row[0] == node_id
    finally:
        conn.close()


@settings(max_examples=50, deadline=None)
@given(
    exc_type=st.sampled_from([Exception, RuntimeError, IOError]),
)
def test_ts12_p6_session_outcome_isolation(
    exc_type: type,
) -> None:
    """PROP-6: Exception in supersession does not affect session outcome."""
    from unittest.mock import patch

    from agentfox.core.config import KnowledgeProviderConfig
    from agentfox.knowledge.db import KnowledgeDB
    from agentfox.knowledge.fox_provider import FoxKnowledgeProvider

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    try:
        db = KnowledgeDB.__new__(KnowledgeDB)
        db._conn = conn
        provider = FoxKnowledgeProvider(db, KnowledgeProviderConfig())

        with (
            patch("agentfox.knowledge.fox_provider.supersede_injected_findings"),
            patch(
                "agentfox.knowledge.fox_provider.supersede_drift_findings_by_files",
                side_effect=exc_type("test failure"),
            ),
        ):
            # Must not raise — session outcome isolation
            provider.ingest(
                session_id="test_spec:1",
                spec_name="test_spec",
                context={
                    "session_status": "completed",
                    "touched_files": ["src/foo.py"],
                    "project_root": "",
                    "archetype": "coder",
                },
            )
    finally:
        conn.close()


# ---------------------------------------------------------------------------
# TS-12-35: Architecture documentation check (12-REQ-10.1)
# ---------------------------------------------------------------------------


def test_ts12_35_architecture_md_updated() -> None:
    """docs/architecture.md documents file-based drift finding supersession."""
    from pathlib import Path

    docs_path = Path(__file__).resolve().parents[3] / "docs" / "architecture.md"
    assert docs_path.exists(), f"docs/architecture.md not found at {docs_path}"
    content = docs_path.read_text()
    # Must mention artifact_ref or file-based supersession (not vacuously true)
    assert "artifact_ref" in content or "file-based supersession" in content.lower()
    # Must reference sections 5.4 and 10.3 per spec pseudocode
    assert "5.4" in content, "Section 5.4 reference missing"
    assert "10.3" in content, "Section 10.3 reference missing"


# ---------------------------------------------------------------------------
# TS-12-36: Knowledge system architecture doc check (12-REQ-10.2)
# ---------------------------------------------------------------------------


def test_ts12_36_knowledge_system_architecture_updated() -> None:
    """05-knowledge-system-architecture.md documents matching rules."""
    from pathlib import Path

    docs_path = (
        Path(__file__).resolve().parents[3]
        / "docs"
        / "architecture"
        / "05-knowledge-system-architecture.md"
    )
    assert docs_path.exists(), f"05-knowledge-system-architecture.md not found at {docs_path}"
    content = docs_path.read_text()
    # Must reference artifact_ref matching rules (not vacuously true)
    assert "artifact_ref" in content, "artifact_ref not documented"
    # Must reference prefix matching in the context of drift supersession
    assert "prefix match" in content.lower() or "prefix-match" in content.lower(), (
        "Prefix matching rule not documented"
    )
    # Must document null artifact_ref fallback behaviour
    assert "null" in content.lower() and "artifact_ref" in content, (
        "Null artifact_ref fallback not documented"
    )
    # Must reference sections 4.1, 8, and 9 per spec pseudocode
    assert "4.1" in content, "Section 4.1 reference missing"
    assert any(s in content for s in ["## 8", "### 8", "Section 8"]), (
        "Section 8 reference missing"
    )
