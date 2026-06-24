"""Tests for open_knowledge_store required read_only parameter.

Verifies that open_knowledge_store requires an explicit read_only
keyword argument, that KnowledgeDB retains its default, and that
all production callers pass read_only.

Test Spec: TS-06-1, TS-06-2, TS-06-3, TS-06-E1, TS-06-E8
Requirements: 06-REQ-1.1, 06-REQ-1.2, 06-REQ-1.3, 06-REQ-1.E1
"""

from __future__ import annotations

import ast
import inspect
from pathlib import Path

import pytest
from agentfox.core.config import KnowledgeConfig
from agentfox.knowledge.db import KnowledgeDB, open_knowledge_store

# -----------------------------------------------------------------------
# TS-06-1: open_knowledge_store requires read_only (no default)
# -----------------------------------------------------------------------


class TestOpenKnowledgeStoreRequiresReadOnly:
    """TS-06-1: calling open_knowledge_store() without read_only raises TypeError."""

    def test_calling_without_read_only_raises_type_error(self) -> None:
        """open_knowledge_store() with no arguments must raise TypeError
        immediately, before any file I/O occurs."""
        with pytest.raises(TypeError):
            open_knowledge_store()  # type: ignore[call-arg]

    def test_calling_with_config_but_no_read_only_raises_type_error(self, tmp_path: Path) -> None:
        """open_knowledge_store(config) without read_only keyword must
        raise TypeError before any file I/O."""
        config = KnowledgeConfig(store_path=str(tmp_path / "test.duckdb"))
        with pytest.raises(TypeError):
            open_knowledge_store(config)  # type: ignore[call-arg]

    def test_read_only_signature_has_no_default(self) -> None:
        """The read_only parameter on open_knowledge_store must have no
        default value in the function signature."""
        sig = inspect.signature(open_knowledge_store)
        param = sig.parameters["read_only"]
        assert param.default is inspect.Parameter.empty, (
            "read_only must have no default value (should be a required kwarg)"
        )


# -----------------------------------------------------------------------
# TS-06-E1 / TS-06-E8: TypeError message references read_only
# -----------------------------------------------------------------------


class TestTypeErrorMessage:
    """TS-06-E1 / TS-06-E8: TypeError message must reference read_only."""

    def test_type_error_message_contains_read_only(self, tmp_path: Path) -> None:
        """The TypeError raised when read_only is omitted must mention
        'read_only' in its message to guide the developer."""
        config = KnowledgeConfig(store_path=str(tmp_path / "test.duckdb"))
        with pytest.raises(TypeError, match="read_only"):
            open_knowledge_store(config)  # type: ignore[call-arg]


# -----------------------------------------------------------------------
# TS-06-2: KnowledgeDB retains its existing read_only default
# -----------------------------------------------------------------------


class TestKnowledgeDBRetainsDefault:
    """TS-06-2: KnowledgeDB can be instantiated without passing read_only."""

    def test_knowledge_db_instantiates_without_read_only(self, tmp_path: Path) -> None:
        """KnowledgeDB(config) without read_only must NOT raise TypeError.
        The class retains its default value for internal/test use."""
        config = KnowledgeConfig(store_path=str(tmp_path / "test.duckdb"))
        db = KnowledgeDB(config)
        assert db is not None

    def test_knowledge_db_read_only_has_default(self) -> None:
        """KnowledgeDB.__init__ read_only parameter must have a default."""
        sig = inspect.signature(KnowledgeDB.__init__)
        param = sig.parameters["read_only"]
        assert param.default is not inspect.Parameter.empty, (
            "KnowledgeDB should retain its read_only default for test/internal use"
        )


# -----------------------------------------------------------------------
# TS-06-3: AST scan of all production callers
# -----------------------------------------------------------------------

# Production modules that should call open_knowledge_store with explicit read_only
_PRODUCTION_MODULES = [
    "packages/af/af/code.py",
    "packages/af/af/plan.py",
    "packages/af/af/standup.py",
    "packages/af/af/findings.py",
    "packages/af/af/nightshift.py",
    "packages/af/af/reset.py",
    "packages/agentfox/agentfox/engine/run.py",
    "packages/agentfox/agentfox/fix/analyzer.py",
    "packages/agentfox/agentfox/session/context.py",
    "packages/agentfox/agentfox/graph/planner.py",
]

# Modules that open DuckDB connections and must route through
# open_knowledge_store rather than calling duckdb.connect directly.
_MODULES_REQUIRING_FACTORY = [
    "packages/af/af/code.py",
    "packages/af/af/plan.py",
    "packages/af/af/standup.py",
    "packages/af/af/findings.py",
    "packages/af/af/reset.py",
    "packages/af/af/nightshift.py",
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


class TestAllProductionCallersPassReadOnly:
    """TS-06-3: every production caller passes explicit read_only keyword."""

    def test_ast_scan_production_modules(self) -> None:
        """AST-walk all production modules and assert every call to
        open_knowledge_store includes read_only as an explicit keyword
        argument."""
        project_root = _find_project_root()
        violations: list[str] = []

        for module_path_str in _PRODUCTION_MODULES:
            module_path = project_root / module_path_str
            if not module_path.exists():
                # Module not present — skip (some modules may use
                # KnowledgeDB directly instead of open_knowledge_store)
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
