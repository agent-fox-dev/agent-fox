"""Tests for af standup read-only, af findings read-write, and make check.

Verifies that af standup uses open_knowledge_store(read_only=True),
af findings uses open_knowledge_store(read_only=False), and that all
production call sites pass explicit read_only arguments.

Test Spec: TS-06-19, TS-06-20, TS-06-21, TS-06-22, TS-06-23
Requirements: 06-REQ-8.1, 06-REQ-8.2, 06-REQ-9.1, 06-REQ-10.1, 06-REQ-10.2
"""

from __future__ import annotations

import ast
import subprocess
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest
from af.app import main
from agentfox.maintenance.pid import PidStatus
from click.testing import CliRunner


@pytest.fixture(autouse=True)
def _no_daemon(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(
        "agentfox.maintenance.pid.check_pid_file",
        lambda _path: (PidStatus.ABSENT, None),
    )


# -----------------------------------------------------------------------
# TS-06-19: af standup calls open_knowledge_store with read_only=True
# -----------------------------------------------------------------------


class TestStandupReadOnly:
    """TS-06-19: af standup must call open_knowledge_store with read_only=True."""

    def test_standup_uses_open_knowledge_store_read_only(self, cli_runner: CliRunner) -> None:
        """af standup must call open_knowledge_store(read_only=True).
        This test mocks open_knowledge_store and verifies it is invoked
        with read_only=True, per 06-REQ-8.1."""
        mock_db = MagicMock()
        mock_db.connection = MagicMock()

        mock_report = MagicMock()
        mock_report.cost_by_spec = {}
        mock_report.cost_by_archetype = {}

        mock_db_path = MagicMock()
        mock_db_path.exists.return_value = True

        with (
            patch("af.standup.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.standup.DEFAULT_DB_PATH", new=mock_db_path),
            patch("af.standup.generate_standup", return_value=mock_report),
        ):
            cli_runner.invoke(main, ["standup"])

        # Verify open_knowledge_store was called with read_only=True
        mock_oks.assert_called_once()
        call_kwargs = mock_oks.call_args
        assert call_kwargs.kwargs.get("read_only") is True, (
            "af standup must call open_knowledge_store with read_only=True"
        )


# -----------------------------------------------------------------------
# TS-06-21: af findings calls open_knowledge_store with read_only=False
# -----------------------------------------------------------------------


class TestFindingsReadWrite:
    """TS-06-21: af findings must call open_knowledge_store with read_only=False."""

    def test_findings_without_dismiss_uses_read_write(self, cli_runner: CliRunner) -> None:
        """af findings (without --dismiss) must call open_knowledge_store
        with read_only=False because the dismiss functionality requires
        write access (06-REQ-9.1)."""
        mock_db = MagicMock()
        mock_conn = MagicMock()
        mock_db.connection = mock_conn

        mock_db_path = MagicMock()
        mock_db_path.exists.return_value = True

        with (
            patch("af.findings.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.findings.DEFAULT_DB_PATH", new=mock_db_path),
            patch("agentfox.reporting.findings.query_findings", return_value=[]),
        ):
            cli_runner.invoke(main, ["insights"])

        # Verify open_knowledge_store was called with read_only=False
        mock_oks.assert_called_once()
        call_kwargs = mock_oks.call_args
        assert call_kwargs.kwargs.get("read_only") is False, (
            "af findings must call open_knowledge_store with read_only=False"
        )

    def test_findings_with_dismiss_uses_read_write(self, cli_runner: CliRunner) -> None:
        """af findings --dismiss must call open_knowledge_store with
        read_only=False to perform UPDATE (06-REQ-9.1)."""
        mock_db = MagicMock()
        mock_conn = MagicMock()
        mock_db.connection = mock_conn

        mock_db_path = MagicMock()
        mock_db_path.exists.return_value = True

        with (
            patch("af.findings.open_knowledge_store", return_value=mock_db) as mock_oks,
            patch("af.findings.DEFAULT_DB_PATH", new=mock_db_path),
            patch("agentfox.knowledge.review_store.dismiss_finding_by_id", return_value="dismissed"),
        ):
            cli_runner.invoke(main, ["insights", "--dismiss", "some-id", "stale finding"])

        mock_oks.assert_called_once()
        call_kwargs = mock_oks.call_args
        assert call_kwargs.kwargs.get("read_only") is False, (
            "af findings --dismiss must call open_knowledge_store with read_only=False"
        )


# -----------------------------------------------------------------------
# TS-06-22: AST scan for open_knowledge_store calls with explicit read_only
# -----------------------------------------------------------------------

# Production modules that must use open_knowledge_store with explicit read_only
_PRODUCTION_MODULES = [
    "packages/af/af/code.py",
    "packages/af/af/plan.py",
    "packages/af/af/standup.py",
    "packages/af/af/findings.py",
    "packages/af/af/reset.py",
    "packages/agentfox/agentfox/engine/run.py",
    "packages/agentfox/agentfox/fix/analyzer.py",
    "packages/agentfox/agentfox/session/context.py",
    "packages/agentfox/agentfox/graph/planner.py",
    "packages/nightshift/nightshift/_startup.py",
]

# Production modules that open DuckDB connections and therefore must
# route through open_knowledge_store rather than calling duckdb.connect
# directly.  Modules listed here are checked for stray duckdb.connect()
# calls that bypass the factory.
_MODULES_REQUIRING_FACTORY = [
    "packages/af/af/code.py",
    "packages/af/af/plan.py",
    "packages/af/af/standup.py",
    "packages/af/af/findings.py",
    "packages/af/af/reset.py",
    "packages/nightshift/nightshift/_startup.py",
    "packages/agentfox/agentfox/fix/analyzer.py",
]


def _find_project_root() -> Path:
    """Walk up from this file to find the project root containing 'packages/'."""
    current = Path(__file__).resolve()
    for parent in current.parents:
        if (parent / "packages").is_dir():
            return parent
    raise RuntimeError("Could not find project root with 'packages/' directory")


def _get_open_knowledge_store_calls(source: str) -> list[ast.Call]:
    """AST-walk source code and return all calls to open_knowledge_store."""
    tree = ast.parse(source)
    calls: list[ast.Call] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        if isinstance(node.func, ast.Name) and node.func.id == "open_knowledge_store":
            calls.append(node)
        elif isinstance(node.func, ast.Attribute) and node.func.attr == "open_knowledge_store":
            calls.append(node)
    return calls


def _get_duckdb_connect_calls(source: str) -> list[ast.Call]:
    """AST-walk source code and return all calls to duckdb.connect."""
    tree = ast.parse(source)
    calls: list[ast.Call] = []
    for node in ast.walk(tree):
        if not isinstance(node, ast.Call):
            continue
        if (
            isinstance(node.func, ast.Attribute)
            and isinstance(node.func.value, ast.Name)
            and node.func.value.id == "duckdb"
            and node.func.attr == "connect"
        ):
            calls.append(node)
    return calls


class TestAllCallSitesHaveReadOnly:
    """TS-06-22: every production call to open_knowledge_store has read_only kwarg."""

    def test_ast_scan_all_production_modules(self) -> None:
        """AST-walk all production modules and assert every call to
        open_knowledge_store includes read_only as an explicit keyword
        argument. This deduplicates TS-06-3 at module scope."""
        project_root = _find_project_root()
        violations: list[str] = []

        for module_path_str in _PRODUCTION_MODULES:
            module_path = project_root / module_path_str
            if not module_path.exists():
                continue

            source = module_path.read_text(encoding="utf-8")
            calls = _get_open_knowledge_store_calls(source)

            for call in calls:
                kwarg_names = {kw.arg for kw in call.keywords if kw.arg is not None}
                if "read_only" not in kwarg_names:
                    violations.append(
                        f"{module_path_str}:{call.lineno} — open_knowledge_store() missing read_only keyword"
                    )

        assert not violations, "Production call sites missing read_only keyword argument:\n" + "\n".join(
            f"  - {v}" for v in violations
        )

    def test_no_direct_duckdb_connect_in_production_modules(self) -> None:
        """Production modules that open DuckDB connections must use
        open_knowledge_store — not duckdb.connect() directly.
        This ensures the factory-function convention is enforced
        (06-REQ-10.1)."""
        project_root = _find_project_root()
        violations: list[str] = []

        for module_path_str in _MODULES_REQUIRING_FACTORY:
            module_path = project_root / module_path_str
            if not module_path.exists():
                continue

            source = module_path.read_text(encoding="utf-8")
            calls = _get_duckdb_connect_calls(source)

            for call in calls:
                violations.append(
                    f"{module_path_str}:{call.lineno} — uses duckdb.connect() directly; "
                    "must use open_knowledge_store() instead"
                )

        assert not violations, (
            "Production modules bypass open_knowledge_store with direct duckdb.connect():\n"
            + "\n".join(f"  - {v}" for v in violations)
        )


# -----------------------------------------------------------------------
# TS-06-20 / TS-06-23: make check exits with status 0
# -----------------------------------------------------------------------


class TestMakeCheckPasses:
    """TS-06-20 / TS-06-23: make check must exit 0 after all changes."""

    @pytest.mark.skip(reason="TS-06-20/TS-06-23: integration test — run manually, not inside make check (recursive)")
    def test_make_check_exits_zero(self) -> None:
        """Run make check from the project root and assert it exits with
        status 0. This is an integration test that validates the entire
        test suite passes."""
        project_root = _find_project_root()
        result = subprocess.run(
            ["make", "check"],
            cwd=project_root,
            capture_output=True,
            text=True,
            timeout=600,
        )
        assert result.returncode == 0, (
            f"make check failed with exit code {result.returncode}:\n"
            f"stdout: {result.stdout[-1000:]}\n"
            f"stderr: {result.stderr[-1000:]}"
        )
