"""Tests for workspace migration — dependency declarations, import rewiring, isolation.

These tests verify the atomic import migration across the workspace.
They will fail until the full migration is complete.

TS-01-34: agentfox/pyproject.toml declares afaudit>=4.3.2
TS-01-35: No old audit module paths remain in agentfox, af
TS-01-36: agentfox/__init__.py does not re-export moved audit symbols
TS-01-37: af pyproject.toml declares afaudit dependency
TS-01-38: agentspec and spec have zero direct audit imports
TS-01-39: cleanup.py has no duckdb; duckdb_sink has retention logic
"""

from __future__ import annotations

import tomllib
from pathlib import Path

WORKSPACE_ROOT = Path(__file__).resolve().parents[3]

# Package paths
AGENTFOX_PKG = WORKSPACE_ROOT / "packages" / "agentfox"
AF_PKG = WORKSPACE_ROOT / "packages" / "af"
AGENTSPEC_PKG = WORKSPACE_ROOT / "packages" / "agentspec"
SPEC_PKG = WORKSPACE_ROOT / "packages" / "spec"
AFAUDIT_SRC = WORKSPACE_ROOT / "packages" / "afaudit" / "afaudit"


def _collect_py_files(base: Path) -> list[Path]:
    """Collect all .py files recursively under a directory."""
    if not base.is_dir():
        return []
    return list(base.rglob("*.py"))


def _read_all_py_content(base: Path) -> str:
    """Read and concatenate all .py file contents under a directory."""
    parts: list[str] = []
    for f in _collect_py_files(base):
        try:
            parts.append(f.read_text(encoding="utf-8"))
        except OSError:
            continue
    return "\n".join(parts)


class TestAgentfoxDependency:
    """TS-01-34: agentfox/pyproject.toml declares afaudit>=4.3.2.

    Requirement: 01-REQ-10.1
    """

    def test_afaudit_in_agentfox_dependencies(self) -> None:
        """agentfox must list afaudit>=4.7.2 in [project.dependencies]."""
        with open(AGENTFOX_PKG / "pyproject.toml", "rb") as f:
            toml = tomllib.load(f)
        deps = toml["project"]["dependencies"]
        matching = [d for d in deps if "afaudit" in d]
        assert len(matching) > 0, f"afaudit not found in agentfox [project.dependencies]; current deps: {deps}"
        # Verify version constraint
        dep_str = matching[0]
        assert "4.7.2" in dep_str, f"Expected version constraint with 4.7.2, got: {dep_str}"


class TestNoOldImportPaths:
    """TS-01-35: No old agentfox audit module paths in production code.

    Requirement: 01-REQ-10.2
    """

    OLD_MODULE_PATHS = [
        "from agentfox.knowledge.audit import",
        "from agentfox.knowledge.sink import",
        "from agentfox.knowledge.agent_trace import",
        "from agentfox.workspace.audit_cleanup import",
    ]

    def test_no_old_imports_in_agentfox(self) -> None:
        """No old audit module imports should remain in agentfox/ source."""
        content = _read_all_py_content(AGENTFOX_PKG)
        for old_path in self.OLD_MODULE_PATHS:
            assert old_path not in content, f"Old import path '{old_path}' still found in agentfox/"

    def test_no_old_imports_in_af(self) -> None:
        """No old audit module imports should remain in af/ source."""
        content = _read_all_py_content(AF_PKG)
        for old_path in self.OLD_MODULE_PATHS:
            assert old_path not in content, f"Old import path '{old_path}' still found in af/"


class TestNoReexportShims:
    """TS-01-36: agentfox/__init__.py does not re-export moved audit symbols.

    Requirement: 01-REQ-10.3
    """

    MOVED_SYMBOLS = [
        "AuditEvent",
        "AuditJsonlSink",
        "SessionSink",
        "AgentTraceSink",
        "build_postmortem",
        "emit_audit_event",
        "purge_stale_audit_files",
        "enforce_file_retention",
        "AUDIT_DIR",
    ]

    def test_no_audit_symbols_in_agentfox_init(self) -> None:
        """agentfox/__init__.py must not import or re-export any moved symbol."""
        init_path = AGENTFOX_PKG / "agentfox" / "__init__.py"
        content = init_path.read_text(encoding="utf-8")
        for sym in self.MOVED_SYMBOLS:
            assert sym not in content, (
                f"Moved audit symbol '{sym}' found in agentfox/__init__.py — "
                "shim re-exports are not allowed (01-REQ-10.3)"
            )


class TestAfNightshiftDependencies:
    """TS-01-37: af declares afaudit as a direct dependency.

    Requirement: 01-REQ-10.4
    """

    def test_af_depends_on_afaudit(self) -> None:
        """af/pyproject.toml must list afaudit in [project.dependencies]."""
        with open(AF_PKG / "pyproject.toml", "rb") as f:
            toml = tomllib.load(f)
        deps = toml["project"]["dependencies"]
        assert any("afaudit" in d for d in deps), f"afaudit not found in af [project.dependencies]: {deps}"

    def test_af_imports_from_afaudit(self) -> None:
        """af source must import symbols from afaudit (not old agentfox paths)."""
        content = _read_all_py_content(AF_PKG)
        assert "from afaudit" in content or "import afaudit" in content, (
            "af source should import from afaudit after migration"
        )


class TestAgentspecSpecNoAuditImports:
    """TS-01-38: agentspec and spec have zero direct audit imports.

    Requirement: 01-REQ-10.5
    """

    AUDIT_IMPORT_PATTERNS = [
        "afaudit",
        "agentfox.knowledge.audit",
        "agentfox.knowledge.sink",
        "agentfox.knowledge.agent_trace",
        "agentfox.workspace.audit_cleanup",
    ]

    def test_agentspec_no_audit_imports(self) -> None:
        """agentspec/ must have zero direct audit imports."""
        content = _read_all_py_content(AGENTSPEC_PKG)
        for pattern in self.AUDIT_IMPORT_PATTERNS:
            assert pattern not in content, f"Unexpected audit import '{pattern}' found in agentspec/"

    def test_spec_no_audit_imports(self) -> None:
        """spec/ must have zero direct audit imports."""
        content = _read_all_py_content(SPEC_PKG)
        for pattern in self.AUDIT_IMPORT_PATTERNS:
            assert pattern not in content, f"Unexpected audit import '{pattern}' found in spec/"


class TestDbRetentionSplit:
    """TS-01-39: cleanup.py has no duckdb; duckdb_sink has retention logic.

    Requirement: 01-REQ-10.6
    """

    def test_cleanup_no_duckdb_import(self) -> None:
        """afaudit/cleanup.py must not import or reference duckdb."""
        source = (AFAUDIT_SRC / "cleanup.py").read_text(encoding="utf-8")
        assert "duckdb" not in source, (
            "afaudit/cleanup.py must not contain any duckdb reference — "
            "DB retention logic belongs in agentfox.knowledge.duckdb_sink"
        )

    def test_cleanup_no_sql(self) -> None:
        """afaudit/cleanup.py must not contain SQL statements."""
        source = (AFAUDIT_SRC / "cleanup.py").read_text(encoding="utf-8")
        assert "DELETE FROM" not in source, (
            "afaudit/cleanup.py must not contain SQL — DB operations belong in agentfox.knowledge.duckdb_sink"
        )

    def test_duckdb_sink_has_retention_logic(self) -> None:
        """agentfox/knowledge/duckdb_sink.py must contain retention logic."""
        duckdb_sink_path = AGENTFOX_PKG / "agentfox" / "knowledge" / "duckdb_sink.py"
        source = duckdb_sink_path.read_text(encoding="utf-8")
        has_retention = "retain" in source.lower() or "retention" in source.lower()
        assert has_retention, (
            "agentfox/knowledge/duckdb_sink.py should contain DB-retention logic "
            "after the enforce_audit_retention split"
        )
