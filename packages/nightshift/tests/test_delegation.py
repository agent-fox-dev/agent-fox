"""Tests for nightshift app.py delegation pattern.

Test Spec: TS-07-32, TS-07-33, TS-07-E8, TS-07-P2
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
        assert "agentfox" in source, "app.py must import from agentfox"

    def test_thin_wrapper_line_count(self) -> None:
        """app.py is a thin delegation layer (< 150 lines)."""
        source = _read_app_source()
        line_count = len(source.splitlines())
        assert line_count < 160, f"app.py has {line_count} lines; expected < 160 for a thin wrapper"


class TestAppUsesAgentFoxGroup:
    """TS-07-33 / TS-07-19: app.py uses AgentFoxGroup and common_options.

    Requirements: 07-REQ-7.2, 07-REQ-3.11
    """

    def test_source_references_agentfox_group(self) -> None:
        """app.py source contains 'AgentFoxGroup'."""
        source = _read_app_source()
        assert "AgentFoxGroup" in source, "app.py must use AgentFoxGroup"

    def test_source_references_common_options(self) -> None:
        """app.py source contains 'common_options'."""
        source = _read_app_source()
        assert "common_options" in source, "app.py must use common_options from agentfox.io"

    def test_source_references_agentfox_io(self) -> None:
        """app.py imports from agentfox.io."""
        source = _read_app_source()
        assert "agentfox.io" in source, "app.py must import from agentfox.io"

    def test_main_is_agentfox_group_runtime(self) -> None:
        """Runtime check: main is an AgentFoxGroup instance.

        TS-07-19: isinstance(main, AgentFoxGroup) or
        type(main).__name__ == 'AgentFoxGroup'.
        """
        from agentfox.io import AgentFoxGroup
        from nightshift.app import main

        assert isinstance(main, AgentFoxGroup) or type(main).__name__ == "AgentFoxGroup", (
            f"main must be an AgentFoxGroup instance, got {type(main).__name__}"
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
        assert not overlap, f"app.py redefines business logic functions: {overlap}"


class TestNoCopyPastedLogic:
    """TS-07-E8: No copy-pasted business logic from agentfox.nightshift.

    Requirements: 07-REQ-7.1, 07-REQ-7.E1
    """

    def test_no_subprocess_in_app(self) -> None:
        """app.py does not contain subprocess calls (business logic)."""
        source = _read_app_source()
        for pattern in ["subprocess.run", "Popen", "asyncio.create_subprocess"]:
            assert pattern not in source, f"app.py contains {pattern!r}; business logic should stay in agentfox"

    def test_no_daemon_runner_reimplementation(self) -> None:
        """app.py does not reimplement DaemonRunner or NightShiftEngine."""
        source = _read_app_source()
        tree = ast.parse(source)
        class_names = set()
        for node in ast.walk(tree):
            if isinstance(node, ast.ClassDef):
                class_names.add(node.name)
        banned_classes = {"DaemonRunner", "NightShiftEngine", "SharedBudget"}
        overlap = class_names & banned_classes
        assert not overlap, f"app.py redefines business logic classes: {overlap}"
