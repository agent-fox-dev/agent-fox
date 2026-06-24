"""Unit tests for the agentfox.io package public API.

Verifies that the package re-exports exactly the twelve curated public
symbols specified by Spec 03, that internal symbols are not exposed,
and that the package structure contains the required seven files.

Spec 04 later extended the package with additional symbols
(format_table, ProgressDisplay) and files (group.py, progress.py).
These tests validate the original Spec 03 contract while acknowledging
documented extensions.  See docs/errata/03_io_package_extended_by_spec_04.md.

Test Spec: TS-03-1, TS-03-2, TS-03-3, TS-03-E1
Requirements: 03-REQ-1.1, 03-REQ-1.2, 03-REQ-1.3, 03-REQ-1.E1
"""

from __future__ import annotations

import os

import pytest

# The twelve curated public symbols specified by Spec 03 (03-REQ-1.1).
SPEC_03_PUBLIC_SYMBOLS = [
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

# Additional symbols added by Spec 04, documented in errata.
SPEC_04_EXTRA_SYMBOLS = [
    "ProgressDisplay",
    "format_table",
]


class TestPublicAPI:
    """TS-03-1: Verify all twelve Spec 03 public symbols are importable from agentfox.io."""

    def test_all_twelve_spec03_symbols_importable(self) -> None:
        """03-REQ-1.1: All twelve Spec 03 symbols are importable from agentfox.io."""
        import agentfox.io

        for sym in SPEC_03_PUBLIC_SYMBOLS:
            assert hasattr(agentfox.io, sym), f"{sym} not found in agentfox.io"

    def test_exactly_twelve_spec03_symbols(self) -> None:
        """03-REQ-1.1: Validate the original Spec 03 contract of exactly twelve symbols.

        The package may contain additional symbols added by later specs
        (documented in errata), but the original twelve must all be present
        and any extras must be from the known Spec 04 extension set.
        """
        import agentfox.io

        actual_public = set(agentfox.io.__all__)
        spec_03_expected = set(SPEC_03_PUBLIC_SYMBOLS)
        spec_04_known = set(SPEC_04_EXTRA_SYMBOLS)

        # All twelve original symbols must be present.
        missing = spec_03_expected - actual_public
        assert missing == set(), f"Missing Spec 03 symbols: {missing}"

        # Any extra symbols must be from the documented Spec 04 set.
        extras = actual_public - spec_03_expected
        undocumented = extras - spec_04_known
        assert undocumented == set(), (
            f"Undocumented extra symbols beyond Spec 03 twelve and Spec 04 extensions: {undocumented}"
        )

    def test_handle_cli_errors_not_in_public_api(self) -> None:
        """03-REQ-1.1: handle_cli_errors is not among the public symbols."""
        import agentfox.io

        assert "handle_cli_errors" not in agentfox.io.__all__


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
    """TS-03-3: Verify the agentfox/io/ directory contains the seven Spec 03 required files."""

    def test_exactly_seven_spec03_files_exist(self) -> None:
        """03-REQ-1.3: All seven Spec 03 files exist in agentfox/io/.

        Spec 04 later added group.py and progress.py; any extra .py files
        beyond the original seven must be from the documented extension set.
        """
        import agentfox.io

        io_dir = os.path.dirname(agentfox.io.__file__)
        files = set(os.listdir(io_dir))

        # The seven files specified by Spec 03.
        spec_03_files = {
            "__init__.py",
            "output.py",
            "json.py",
            "spinner.py",
            "errors.py",
            "cli.py",
            "help.py",
        }

        # Known additions by Spec 04.
        spec_04_extra_files = {
            "group.py",
            "progress.py",
        }

        # All seven original files must be present.
        missing = spec_03_files - files
        assert missing == set(), f"Missing Spec 03 files: {missing}"

        # Any extra .py files must be from the documented Spec 04 set.
        all_py_files = {f for f in files if f.endswith(".py")}
        extras = all_py_files - spec_03_files
        undocumented = extras - spec_04_extra_files
        assert undocumented == set(), (
            f"Undocumented extra .py files beyond Spec 03 seven and Spec 04 extensions: {undocumented}"
        )


class TestSubmoduleInternalSymbol:
    """TS-03-E1: Importing submodule-internal symbol from agentfox.io raises ImportError."""

    def test_handle_cli_errors_raises_import_error(self) -> None:
        """03-REQ-1.E1: ImportError raised for unlisted symbol from agentfox.io."""
        with pytest.raises(ImportError):
            from agentfox.io import handle_cli_errors  # noqa: F401
