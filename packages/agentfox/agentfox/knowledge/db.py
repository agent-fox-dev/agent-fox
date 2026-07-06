"""DuckDB connection management, schema initialization, VSS extension setup.

Requirements: 11-REQ-1.1, 11-REQ-1.2, 11-REQ-1.3, 11-REQ-1.E1, 11-REQ-1.E2,
              11-REQ-2.1, 11-REQ-2.2, 11-REQ-2.3, 11-REQ-7.1
"""

from __future__ import annotations

import logging
from pathlib import Path

import duckdb  # noqa: F401

from agentfox.core.config import KnowledgeConfig
from agentfox.core.errors import KnowledgeStoreError  # noqa: F401
from agentfox.knowledge.migrations import run_migrations

logger = logging.getLogger("agentfox.knowledge.db")


class KnowledgeDB:
    """Manages the DuckDB knowledge store lifecycle."""

    def __init__(self, config: KnowledgeConfig, *, read_only: bool = False) -> None:
        self._config = config
        self._read_only = read_only
        self._conn: duckdb.DuckDBPyConnection | None = None

    @property
    def connection(self) -> duckdb.DuckDBPyConnection:
        """Return the active connection, raising if closed."""
        if self._conn is None:
            raise KnowledgeStoreError("Knowledge store is not open")
        return self._conn

    def open(self) -> None:
        """Open the database, install/load VSS, run migrations.

        Creates the parent directory if it does not exist. On first
        open, installs the VSS extension and creates the full schema.
        On subsequent opens, loads VSS and applies pending migrations.

        Raises:
            KnowledgeStoreError: If the database cannot be opened or
                schema initialization fails.
        """
        try:
            self._ensure_parent_dir()
            self._conn = duckdb.connect(self._config.store_path, read_only=self._read_only)
            if not self._read_only:
                self._setup_vss()
                run_migrations(self._conn)
        except KnowledgeStoreError:
            raise
        except Exception as exc:
            raise KnowledgeStoreError(
                f"Failed to open knowledge store: {exc}",
            ) from exc

    def close(self) -> None:
        """Close the database connection, releasing file locks."""
        if self._conn is not None:
            try:
                self._conn.close()
            except Exception:
                pass
            self._conn = None

    def _ensure_parent_dir(self) -> None:
        """Create the parent directory for the database file."""
        parent = Path(self._config.store_path).parent
        parent.mkdir(parents=True, exist_ok=True)

    def _setup_vss(self) -> None:
        """Install (first time) or load the VSS extension."""
        assert self._conn is not None
        try:
            self._conn.execute("LOAD vss;")
        except Exception:
            # First time: install then load
            try:
                self._conn.execute("INSTALL vss; LOAD vss;")
            except Exception as exc:
                logger.warning("VSS extension unavailable: %s", exc)

    def __enter__(self) -> KnowledgeDB:
        self.open()
        return self

    def __exit__(self, *exc: object) -> None:
        self.close()


class ContextKnowledgeDB:
    """Cursor-based wrapper for read-only context assembly queries.

    Wraps a DuckDB cursor obtained from the primary read-write connection,
    allowing concurrent SELECT queries without opening a second database
    connection.

    DuckDB disallows opening a second connection to the same file with a
    different ``read_only`` flag than existing connections. This class
    satisfies 06-REQ-7.3 (context reads don't contend with write queries)
    without violating that constraint.
    """

    def __init__(self, cursor: duckdb.DuckDBPyConnection) -> None:
        self._cursor = cursor

    @property
    def connection(self) -> duckdb.DuckDBPyConnection:
        """Return the underlying cursor (usable for SELECT queries)."""
        return self._cursor

    def close(self) -> None:
        """Close the underlying cursor."""
        try:
            self._cursor.close()
        except Exception:
            pass


def open_knowledge_store(config: KnowledgeConfig, *, read_only: bool) -> KnowledgeDB:
    """Open the knowledge store. Raises on failure.

    ``read_only`` is required: pass ``True`` for concurrent reads,
    ``False`` for writes.

    Args:
        config: Knowledge configuration with store path.
        read_only: If True, open in read-only mode (allows concurrent
            access while another process holds a write lock).  This
            parameter has no default — callers must declare intent
            explicitly.

    Returns a KnowledgeDB instance on success.

    Raises:
        TypeError: If ``read_only`` is omitted (no default value).
        RuntimeError: If the knowledge store cannot be opened,
            with message containing "Knowledge store initialization failed"
            and the underlying error detail including the file path.

    Requirements: 38-REQ-1.1, 38-REQ-1.2, 38-REQ-1.E1,
                  06-REQ-1.1, 06-REQ-1.E1
    """
    try:
        db = KnowledgeDB(config, read_only=read_only)
        db.open()
        return db
    except Exception as exc:
        raise RuntimeError(f"Knowledge store initialization failed ({config.store_path}): {exc}") from exc
