"""Fixtures for DuckDB knowledge store tests.

Provides KnowledgeConfig with tmp_path, in-memory DuckDB connections,
a create_schema helper that mirrors the real schema DDL, seeded
causal graph data for Time Vision (spec 13) tests, and Fox Ball
(spec 12) fixtures for embeddings, search, oracle, and ingestion.
"""

from __future__ import annotations

from collections.abc import Generator
from dataclasses import dataclass
from dataclasses import field as dc_field
from pathlib import Path

import duckdb
import pytest
from agentfox.core.config import KnowledgeConfig

# --- Stub types replacing deleted knowledge modules --------------------------
# These stubs are retained so that test fixtures remain importable until the
# dead test files are cleaned up in task group 6 (spec 114).


@dataclass
class Fact:
    """Minimal stub for Fact dataclass (original module removed in spec 114)."""

    id: str
    content: str
    category: str = "decision"
    spec_name: str = ""
    session_id: str | None = None
    commit_sha: str | None = None
    keywords: list[str] = dc_field(default_factory=list)
    confidence: float = 0.6
    created_at: str = ""
    supersedes: str | None = None
    superseded_by: str | None = None


# -- Well-known fact UUIDs for Time Vision tests --------------------------------
# These are full UUIDs used consistently across causal/temporal/pattern tests.

FACT_AAA = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
FACT_BBB = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
FACT_CCC = "cccccccc-cccc-cccc-cccc-cccccccccccc"
FACT_DDD = "dddddddd-dddd-dddd-dddd-dddddddddddd"
FACT_EEE = "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"

# -- Schema DDL (mirrors KnowledgeDB._initialize_schema) --------------------

SCHEMA_DDL = """
CREATE TABLE IF NOT EXISTS schema_version (
    version     INTEGER PRIMARY KEY,
    applied_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    description TEXT
);

CREATE TABLE IF NOT EXISTS memory_facts (
    id            UUID PRIMARY KEY,
    content       TEXT NOT NULL,
    category      TEXT,
    spec_name     TEXT,
    session_id    TEXT,
    commit_sha    TEXT,
    confidence    DOUBLE DEFAULT 0.6,
    created_at    TIMESTAMP,
    superseded_by UUID,
    keywords      TEXT[] DEFAULT []
);

CREATE TABLE IF NOT EXISTS memory_embeddings (
    id        UUID PRIMARY KEY REFERENCES memory_facts(id),
    embedding FLOAT[384]
);

CREATE TABLE IF NOT EXISTS session_outcomes (
    id            UUID PRIMARY KEY,
    spec_name     TEXT,
    task_group    TEXT,
    node_id       TEXT,
    touched_path  TEXT,
    status        TEXT,
    input_tokens  INTEGER,
    output_tokens INTEGER,
    duration_ms   INTEGER,
    created_at    TIMESTAMP
);

CREATE TABLE IF NOT EXISTS fact_causes (
    cause_id  UUID,
    effect_id UUID,
    PRIMARY KEY (cause_id, effect_id)
);

CREATE TABLE IF NOT EXISTS tool_calls (
    id         UUID PRIMARY KEY,
    session_id TEXT,
    node_id    TEXT,
    tool_name  TEXT,
    called_at  TIMESTAMP
);

CREATE TABLE IF NOT EXISTS tool_errors (
    id        UUID PRIMARY KEY,
    session_id TEXT,
    node_id    TEXT,
    tool_name  TEXT,
    failed_at  TIMESTAMP
);

CREATE TABLE IF NOT EXISTS review_findings (
    id              UUID PRIMARY KEY,
    severity        TEXT NOT NULL,
    description     TEXT NOT NULL,
    requirement_ref TEXT,
    spec_name       TEXT NOT NULL,
    task_group      TEXT NOT NULL,
    session_id      TEXT NOT NULL,
    superseded_by   TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    category        TEXT
);

CREATE TABLE IF NOT EXISTS verification_results (
    id              UUID PRIMARY KEY,
    requirement_id  TEXT NOT NULL,
    verdict         TEXT NOT NULL,
    evidence        TEXT,
    spec_name       TEXT NOT NULL,
    task_group      TEXT NOT NULL,
    session_id      TEXT NOT NULL,
    superseded_by   TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS drift_findings (
    id              UUID PRIMARY KEY,
    severity        TEXT NOT NULL,
    description     TEXT NOT NULL,
    spec_ref        TEXT,
    artifact_ref    TEXT,
    spec_name       TEXT NOT NULL,
    task_group      TEXT NOT NULL,
    session_id      TEXT NOT NULL,
    superseded_by   TEXT,
    created_at      TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO schema_version (version, description)
    SELECT 1, 'initial schema'
    WHERE NOT EXISTS (SELECT 1 FROM schema_version WHERE version = 1);
"""


def create_schema(conn: duckdb.DuckDBPyConnection) -> None:
    """Create the full knowledge store schema in an existing connection.

    This helper executes the same DDL that KnowledgeDB._initialize_schema
    uses, allowing tests to set up schema without going through the full
    KnowledgeDB.open() path.
    """
    conn.execute(SCHEMA_DDL)


# Production schema with DOUBLE confidence (matches KnowledgeDB._initialize_schema)
# SCHEMA_DDL already uses DOUBLE confidence and includes keywords column,
# so SCHEMA_DDL_V2 is now identical. Retained for backwards compatibility.
SCHEMA_DDL_V2 = SCHEMA_DDL


def create_schema_v2(conn: duckdb.DuckDBPyConnection) -> None:
    """Create schema with DOUBLE confidence column (matches production).

    Now identical to ``create_schema`` since SCHEMA_DDL was updated to
    use DOUBLE confidence and include the keywords column.
    """
    conn.execute(SCHEMA_DDL_V2)


# -- Fixtures ----------------------------------------------------------------


@pytest.fixture
def knowledge_config(tmp_path: Path) -> KnowledgeConfig:
    """KnowledgeConfig with store_path pointing to a temp directory."""
    db_path = tmp_path / "knowledge.duckdb"
    return KnowledgeConfig(store_path=str(db_path))


@pytest.fixture
def in_memory_conn() -> Generator[duckdb.DuckDBPyConnection, None, None]:
    """An in-memory DuckDB connection for isolated unit tests."""
    conn = duckdb.connect(":memory:")
    yield conn  # type: ignore[misc]
    try:
        conn.close()
    except Exception:
        pass


@pytest.fixture
def schema_conn(in_memory_conn: duckdb.DuckDBPyConnection) -> duckdb.DuckDBPyConnection:
    """An in-memory DuckDB connection with the full schema created."""
    create_schema(in_memory_conn)
    return in_memory_conn


@pytest.fixture
def knowledge_conn() -> Generator[duckdb.DuckDBPyConnection, None, None]:
    """In-memory DuckDB with the full production schema (v9+).

    Creates the base schema (including ``memory_facts.keywords``) and
    applies all registered migrations through the latest version. Use
    this fixture for tests that need the complete knowledge store schema,
    including entity_graph tables and the keywords column required for
    fingerprint-based deduplication.

    Requirements: 101-REQ-4.E3, 101-REQ-5.6, 101-REQ-6.6
    """
    from agentfox.knowledge.migrations import run_migrations

    conn = duckdb.connect(":memory:")
    run_migrations(conn)
    yield conn  # type: ignore[misc]
    try:
        conn.close()
    except Exception:
        pass


# -- Seed data helpers for Time Vision (spec 13) --------------------------------


def seed_facts(conn: duckdb.DuckDBPyConnection) -> None:
    """Insert well-known facts into memory_facts for causal graph tests."""
    conn.execute(
        """
        INSERT INTO memory_facts (id, content, spec_name, session_id,
                                  commit_sha, category, confidence, created_at)
        VALUES
            (?, 'User.email changed to nullable', '07_oauth', '07/3',
             'a1b2c3d', 'decision', 0.9, '2025-11-03 14:22:00'),
            (?, 'test_user_model.py assertions failed', '09_user_tests', '09/1',
             'e4f5g6h', 'gotcha', 0.9, '2025-11-17 09:15:00'),
            (?, 'Added migration for nullable email', '12_auth_fix', '12/2',
             'i7j8k9l', 'pattern', 0.9, '2025-11-18 11:30:00'),
            (?, 'Isolated root fact with no links', '05_setup', '05/1',
             NULL, 'convention', 0.6, '2025-10-01 08:00:00'),
            (?, 'Auth module refactored', '17_auth_v2', '17/1',
             'm0n1o2p', 'decision', 0.9, '2025-12-01 10:00:00')
        """,
        [FACT_AAA, FACT_BBB, FACT_CCC, FACT_DDD, FACT_EEE],
    )


def seed_causal_links(conn: duckdb.DuckDBPyConnection) -> None:
    """Insert causal links: aaa -> bbb -> ccc, aaa -> eee."""
    conn.execute(
        """
        INSERT INTO fact_causes (cause_id, effect_id) VALUES
            (?, ?),
            (?, ?),
            (?, ?)
        """,
        [FACT_AAA, FACT_BBB, FACT_BBB, FACT_CCC, FACT_AAA, FACT_EEE],
    )


FACT_S20 = "20202020-2020-2020-2020-202020202020"
FACT_S21 = "21212121-2121-2121-2121-212121212121"


def seed_session_outcomes(conn: duckdb.DuckDBPyConnection) -> None:
    """Insert session outcomes for pattern detection tests.

    Also inserts additional facts + causal links for the second
    occurrence pair (20/1 -> 21/1) so pattern detection can find
    co-occurrences validated against the causal graph.
    """
    conn.execute(
        """
        INSERT INTO session_outcomes (id, spec_name, task_group, node_id,
                                      touched_path, status, created_at)
        VALUES
            ('11111111-1111-1111-1111-111111111111', '07_oauth', '3', '07/3',
             'src/auth/user.py', 'completed', '2025-11-03 14:00:00'),
            ('22222222-2222-2222-2222-222222222222', '09_user_tests', '1', '09/1',
             'tests/test_user_model.py', 'failed', '2025-11-03 15:00:00'),
            ('33333333-3333-3333-3333-333333333333', '14_billing', '2', '14/2',
             'src/auth/session.py', 'completed', '2025-12-10 10:00:00'),
            ('44444444-4444-4444-4444-444444444444', '15_payments', '1', '15/1',
             'tests/test_payments.py', 'failed', '2025-12-10 11:00:00'),
            ('55555555-5555-5555-5555-555555555555', '20_auth_v3', '1', '20/1',
             'src/auth/user.py', 'completed', '2026-01-05 09:00:00'),
            ('66666666-6666-6666-6666-666666666666', '21_user_tests_v2', '1', '21/1',
             'tests/test_user_model.py', 'failed', '2026-01-05 10:00:00')
        """,
    )
    # Add facts and causal link for the second occurrence (20/1 -> 21/1)
    # so pattern detection can validate against the causal graph.
    conn.execute(
        """
        INSERT INTO memory_facts (id, content, spec_name, session_id,
                                  category, confidence, created_at)
        VALUES
            (?, 'Auth refactored again', '20_auth_v3', '20/1',
             'decision', 0.9, '2026-01-05 09:00:00'),
            (?, 'User model tests broke again', '21_user_tests_v2', '21/1',
             'gotcha', 0.9, '2026-01-05 10:00:00')
        """,
        [FACT_S20, FACT_S21],
    )
    conn.execute(
        "INSERT INTO fact_causes (cause_id, effect_id) VALUES (?, ?)",
        [FACT_S20, FACT_S21],
    )


def create_empty_db() -> duckdb.DuckDBPyConnection:
    """Create an in-memory DuckDB with schema but no seeded data."""
    conn = duckdb.connect(":memory:")
    create_schema(conn)
    return conn


# -- Fox Ball (spec 12) fixtures and helpers ----------------------------------

# Additional well-known UUIDs for Fox Ball tests
FACT_FFF = "ffffffff-ffff-ffff-ffff-ffffffffffff"
FACT_111 = "11111111-aaaa-bbbb-cccc-111111111111"
FACT_222 = "22222222-aaaa-bbbb-cccc-222222222222"


def insert_fact_with_embedding(
    conn: duckdb.DuckDBPyConnection,
    fact_id: str,
    content: str,
    embedding: list[float],
    *,
    category: str = "decision",
    spec_name: str = "test_spec",
    session_id: str | None = "test/1",
    commit_sha: str | None = "abc123",
    superseded_by: str | None = None,
) -> None:
    """Insert a fact with its embedding into the test database."""
    conn.execute(
        """
        INSERT INTO memory_facts (id, content, category, spec_name,
                                  session_id, commit_sha, confidence,
                                  created_at, superseded_by)
        VALUES (?, ?, ?, ?, ?, ?, 0.9, CURRENT_TIMESTAMP, ?)
        """,
        [fact_id, content, category, spec_name, session_id, commit_sha, superseded_by],
    )
    conn.execute(
        "INSERT INTO memory_embeddings (id, embedding) VALUES (?, ?::FLOAT[384])",
        [fact_id, embedding],
    )


def insert_fact_without_embedding(
    conn: duckdb.DuckDBPyConnection,
    fact_id: str,
    content: str,
    *,
    category: str = "decision",
    spec_name: str = "test_spec",
) -> None:
    """Insert a fact without an embedding into the test database."""
    conn.execute(
        """
        INSERT INTO memory_facts (id, content, category, spec_name,
                                  confidence, created_at)
        VALUES (?, ?, ?, ?, 0.9, CURRENT_TIMESTAMP)
        """,
        [fact_id, content, category, spec_name],
    )


# -- Fixtures merged from tests/unit/memory/conftest.py ----------------------


def make_fact(
    *,
    id: str = "test-uuid-1",
    content: str = "Test fact content.",
    category: str = "pattern",
    spec_name: str = "01_core_foundation",
    keywords: list[str] | None = None,
    confidence: float = 0.9,
    created_at: str = "2026-03-01T10:00:00+00:00",
    supersedes: str | None = None,
) -> Fact:
    """Create a Fact with sensible defaults for testing."""
    return Fact(
        id=id,
        content=content,
        category=category,
        spec_name=spec_name,
        keywords=keywords if keywords is not None else ["test"],
        confidence=confidence,
        created_at=created_at,
        supersedes=supersedes,
    )


@pytest.fixture
def sample_fact() -> Fact:
    """A single sample fact with default values."""
    return make_fact()


# -- Mock LLM responses for extraction tests --------------------------------

VALID_LLM_RESPONSE = """[
  {
    "content": "The pytest-asyncio plugin requires mode='auto' in pyproject.toml.",
    "category": "gotcha",
    "confidence": "high",
    "keywords": ["pytest", "asyncio", "configuration"]
  },
  {
    "content": "Using tmp_path fixture provides reliable filesystem isolation.",
    "category": "pattern",
    "confidence": "medium",
    "keywords": ["pytest", "tmp_path", "testing"]
  }
]"""

EMPTY_LLM_RESPONSE = "[]"

INVALID_JSON_LLM_RESPONSE = "not valid json {{"

UNKNOWN_CATEGORY_LLM_RESPONSE = """[
  {
    "content": "Some learning about testing.",
    "category": "unknown_cat",
    "confidence": "high",
    "keywords": ["testing"]
  }
]"""

# -- Markdown-fenced LLM responses ------------------------------------------

FENCED_JSON_LLM_RESPONSE = """```json
[
  {
    "content": "Always pin dependency versions in requirements.txt.",
    "category": "convention",
    "confidence": "high",
    "keywords": ["dependencies", "pinning"]
  }
]
```"""

FENCED_NO_LANG_LLM_RESPONSE = """```
[
  {
    "content": "Use structured logging for production services.",
    "category": "pattern",
    "confidence": "medium",
    "keywords": ["logging", "structured"]
  }
]
```"""

PROSE_WRAPPED_JSON_LLM_RESPONSE = """Here are the learnings I extracted:

[
  {
    "content": "Mock external APIs at the HTTP boundary.",
    "category": "pattern",
    "confidence": "high",
    "keywords": ["mocking", "api", "testing"]
  }
]

I hope this helps!"""
