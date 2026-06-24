"""Tests for nightshift app.py delegation pattern.

Test Spec: TS-07-32, TS-07-33, TS-07-P2
Requirements: 07-REQ-7.1, 07-REQ-7.2
"""

from __future__ import annotations

import ast
from pathlib import Path

APP_PY = Path("packages/nightshift/nightshift/app.py")


def _read_app_source() -> str:
    """Read the source of nightshift/app.py."""
    return APP_PY.read_text()


class TestAppDelegation:
    """TS-07-32: app.py delegates to agentfox.nightshift.

    Requirements: 07-REQ-7.1
    """

    def test_imports_agentfox_nightshift(self) -> None:
        """app.py imports from agentfox.nightshift or agentfox."""
        source = _read_app_source()
        assert "agentfox" in source, (
            "app.py must import from agentfox"
        )

    def test_thin_wrapper_line_count(self) -> None:
        """app.py is a thin delegation layer (< 150 lines)."""
        source = _read_app_source()
        line_count = len(source.splitlines())
        assert line_count < 150, (
            f"app.py has {line_count} lines; expected < 150 for a thin wrapper"
        )

    def test_uses_agentfox_group(self) -> None:
        """app.py uses AgentFoxGroup from agentfox.io."""
        source = _read_app_source()
        assert "AgentFoxGroup" in source, (
            "app.py must use AgentFoxGroup"
        )

    def test_uses_common_options(self) -> None:
        """app.py uses common_options from agentfox.io."""
        source = _read_app_source()
        assert "common_options" in source or "agentfox.io" in source, (
            "app.py must use common_options or agentfox.io"
        )


class TestNoDaemonLogicReimplementation:
    """TS-07-P2: No reimplemented daemon logic in app.py.

    Requirements: 07-REQ-7.1, 07-REQ-7.2
    """

    # Business logic function names from agentfox.nightshift that must NOT
    # be re-implemented in the thin wrapper.
    BANNED_FUNCTION_NAMES = {
        "run_fix_pipeline",
        "process_task",
        "harvest_findings",
        "execute_fix",
        "plan_fix",
        "apply_patch",
        "daemon_loop",
        "scan_workspace",
    }

    def test_no_reimplemented_functions(self) -> None:
        """app.py defines no functions that duplicate agentfox.nightshift logic."""
        source = _read_app_source()
        tree = ast.parse(source)
        defined_names = set()
        for node in ast.walk(tree):
            if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef)):
                defined_names.add(node.name)
        overlap = defined_names & self.BANNED_FUNCTION_NAMES
        assert not overlap, (
            f"app.py redefines business logic functions: {overlap}"
        )


class TestNoCopyPastedLogic:
    """TS-07-P2 extended: No copy-pasted business logic.

    Requirements: 07-REQ-7.1
    """

    def test_no_fix_pipeline_code(self) -> None:
        """app.py does not contain fix pipeline implementation code."""
        source = _read_app_source()
        # These are implementation details that should stay in agentfox.nightshift
        for pattern in ["subprocess.run", "Popen", "asyncio.create_subprocess"]:
            assert pattern not in source, (
                f"app.py contains {pattern!r}; business logic should stay in agentfox"
            )
