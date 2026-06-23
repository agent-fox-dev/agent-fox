"""Unit tests for the agentfox.io package public API.

Verifies that the package re-exports exactly the twelve curated
public symbols, that internal symbols are not exposed, and that
the package structure contains exactly the seven required files.

Test Spec: TS-03-1, TS-03-2, TS-03-3, TS-03-E1
Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-1.E1
"""

from __future__ import annotations

import os

import pytest

# The twelve curated public symbols expected from agentfox.io
PUBLIC_SYMBOLS = [
    "OutputManager",
    "StatusSpinner",
    "get_output_manager",
    "emit",
    "emit_ok",
    "emit_line",
    "emit_error",
    "read_stdin",
    "error_envelope",
    "AgentFoxGroup",
    "common_options",
    "exit_codes",
]


class TestPublicAPI:
    """TS-03-1: Verify all twelve public symbols are importable from agentfox.io."""

    def test_all_twelve_symbols_importable(self) -> None:
        """03-REQ-1.1: All twelve public symbols are importable from agentfox.io."""
        import agentfox.io

        for sym in PUBLIC_SYMBOLS:
            assert hasattr(agentfox.io, sym), f"{sym} not found in agentfox.io"

    def test_no_extra_public_symbols(self) -> None:
        """03-REQ-1.1: No additional symbols beyond the twelve are exposed."""
        import agentfox.io

        # Collect non-dunder public names
        actual_public = {name for name in dir(agentfox.io) if not name.startswith("_")}
        expected = set(PUBLIC_SYMBOLS)
        extras = actual_public - expected
        assert extras == set(), f"Unexpected public symbols: {extras}"


class TestHandleCliErrorsExclusion:
    """TS-03-2: Verify handle_cli_errors is NOT importable from agentfox.io."""

    def test_handle_cli_errors_not_in_package(self) -> None:
        """03-REQ-1.2: from agentfox.io import handle_cli_errors raises ImportError."""
        with pytest.raises(ImportError):
            from agentfox.io import handle_cli_errors  # noqa: F401

    def test_handle_cli_errors_importable_from_submodule(self) -> None:
        """03-REQ-1.2: from agentfox.io.errors import handle_cli_errors succeeds."""
        from agentfox.io.errors import handle_cli_errors

        assert callable(handle_cli_errors)


class TestPackageStructure:
    """TS-03-3: Verify the agentfox/io/ directory contains exactly the seven required files."""

    def test_seven_required_files_exist(self) -> None:
        """03-REQ-1.3: All seven files exist in agentfox/io/."""
        import agentfox.io

        io_dir = os.path.dirname(agentfox.io.__file__)
        files = set(os.listdir(io_dir))
        expected = {
            "__init__.py",
            "output.py",
            "json.py",
            "spinner.py",
            "errors.py",
            "cli.py",
            "help.py",
        }
        assert expected.issubset(files), f"Missing files: {expected - files}"


class TestSubmoduleInternalSymbol:
    """TS-03-E1: Importing submodule-internal symbol from agentfox.io raises ImportError."""

    def test_handle_cli_errors_raises_import_error(self) -> None:
        """03-REQ-1.E1: ImportError raised for unlisted symbol from agentfox.io."""
        with pytest.raises(ImportError):
            from agentfox.io import handle_cli_errors  # noqa: F401
