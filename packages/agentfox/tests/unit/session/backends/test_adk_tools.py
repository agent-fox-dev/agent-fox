"""Tests for adk_tools.py coding tools module.

Test Spec: TS-04-18, TS-04-20 through TS-04-25, TS-04-38
Requirements: 04-REQ-6.1, 04-REQ-6.3, 04-REQ-6.4, 04-REQ-6.5,
              04-REQ-6.6, 04-REQ-6.7, 04-REQ-6.8,
              04-REQ-14.1

All tests are guarded with pytest.importorskip('google.adk') so the suite
is skipped cleanly when the google-adk optional dependency is not installed.
"""

from __future__ import annotations

import inspect
import tempfile
from pathlib import Path

import pytest

# Skip the entire module when google-adk is not installed (04-REQ-14.1).
pytest.importorskip("google.adk")


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _find_tool(tools: list, name: str):
    """Find a tool function by __name__ in the tools list."""
    for tool in tools:
        if getattr(tool, "__name__", None) == name:
            return tool
    tool_names = [getattr(t, "__name__", repr(t)) for t in tools]
    raise LookupError(f"Tool '{name}' not found in tools list: {tool_names}")


# ===========================================================================
# Task Group 5: adk_tools.py coding tools — happy paths
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-04-18: adk_tools.py exports all six required ADK function tools
# Requirement: 04-REQ-6.1
# ---------------------------------------------------------------------------


class TestAdkToolsSignatures:
    """Verify adk_tools exports all six tools with correct signatures."""

    def test_make_tools_returns_six_tools(self) -> None:
        """TS-04-18: make_tools(cwd) returns list with all six tool names."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)

        tool_names = {getattr(t, "__name__", None) for t in tools}
        assert "read_file" in tool_names, f"read_file missing from tools: {tool_names}"
        assert "write_file" in tool_names, f"write_file missing from tools: {tool_names}"
        assert "edit_file" in tool_names, f"edit_file missing from tools: {tool_names}"
        assert "execute" in tool_names, f"execute missing from tools: {tool_names}"
        assert "list_files" in tool_names, f"list_files missing from tools: {tool_names}"
        assert "search_files" in tool_names, f"search_files missing from tools: {tool_names}"

    def test_tools_return_dict_annotation(self) -> None:
        """TS-04-18: Each tool's return annotation is dict."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)

        for tool in tools:
            sig = inspect.signature(tool)
            # Allow dict or dict[str, Any] or missing annotation
            # (implementation must annotate as dict)
            ret = sig.return_annotation
            if ret is not inspect.Parameter.empty:
                assert ret is dict or (hasattr(ret, "__origin__") and ret.__origin__ is dict), (
                    f"Tool {tool.__name__} return annotation is {ret}, expected dict"
                )


# ---------------------------------------------------------------------------
# TS-04-38: Test file has importorskip guard (meta test — structural check)
# Requirement: 04-REQ-14.1
# ---------------------------------------------------------------------------


class TestAdkToolsImportGuard:
    """Verify this test file has the importorskip guard at module level."""

    def test_importorskip_present(self) -> None:
        """TS-04-38: This file contains pytest.importorskip('google.adk')."""
        source = Path(__file__).read_text(encoding="utf-8")
        assert "importorskip" in source
        assert "google.adk" in source


# ---------------------------------------------------------------------------
# TS-04-20: read_file happy path
# Requirement: 04-REQ-6.3
# ---------------------------------------------------------------------------


class TestReadFileHappyPath:
    """Verify read_file returns file contents on the happy path."""

    def test_read_file_returns_content(self) -> None:
        """TS-04-20: read_file returns {'content': 'Hello, World!'}."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "hello.txt").write_text("Hello, World!")

            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")
            result = read_file(path="hello.txt")

        assert result == {"content": "Hello, World!"}

    def test_read_file_nested_path(self) -> None:
        """TS-04-20 variant: read_file works with nested directory paths."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            subdir = cwd / "src"
            subdir.mkdir()
            (subdir / "main.py").write_text("print('hi')")

            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")
            result = read_file(path="src/main.py")

        assert result == {"content": "print('hi')"}


# ---------------------------------------------------------------------------
# TS-04-21: write_file happy path
# Requirement: 04-REQ-6.4
# ---------------------------------------------------------------------------


class TestWriteFileHappyPath:
    """Verify write_file creates or overwrites files."""

    def test_write_file_creates_new_file(self) -> None:
        """TS-04-21: write_file creates file and returns {'ok': True}."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)

            tools = make_tools(cwd)
            write_file = _find_tool(tools, "write_file")
            result = write_file(path="output.txt", content="New content")

            assert result == {"ok": True}
            assert (cwd / "output.txt").read_text() == "New content"

    def test_write_file_overwrites_existing(self) -> None:
        """TS-04-21 variant: write_file overwrites existing file content."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "output.txt").write_text("Old content")

            tools = make_tools(cwd)
            write_file = _find_tool(tools, "write_file")
            result = write_file(path="output.txt", content="Updated content")

            assert result == {"ok": True}
            assert (cwd / "output.txt").read_text() == "Updated content"


# ---------------------------------------------------------------------------
# TS-04-22: edit_file happy path
# Requirement: 04-REQ-6.5
# ---------------------------------------------------------------------------


class TestEditFileHappyPath:
    """Verify edit_file replaces first occurrence of old_text with new_text."""

    def test_edit_file_replaces_text(self) -> None:
        """TS-04-22: edit_file replaces 'World' with 'Python'."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "greet.txt").write_text("Hello World")

            tools = make_tools(cwd)
            edit_file = _find_tool(tools, "edit_file")
            result = edit_file(
                path="greet.txt",
                old_text="World",
                new_text="Python",
            )

            assert result == {"ok": True}
            assert (cwd / "greet.txt").read_text() == "Hello Python"

    def test_edit_file_old_text_not_found(self) -> None:
        """TS-04-22: edit_file returns error when old_text not present."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "greet.txt").write_text("Hello Python")

            tools = make_tools(cwd)
            edit_file = _find_tool(tools, "edit_file")
            result = edit_file(
                path="greet.txt",
                old_text="NOTHERE",
                new_text="x",
            )

            assert result.get("error") == "text_not_found"

    def test_edit_file_replaces_first_occurrence_only(self) -> None:
        """TS-04-22 variant: only the first occurrence is replaced."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "multi.txt").write_text("foo bar foo baz")

            tools = make_tools(cwd)
            edit_file = _find_tool(tools, "edit_file")
            result = edit_file(
                path="multi.txt",
                old_text="foo",
                new_text="qux",
            )

            assert result == {"ok": True}
            assert (cwd / "multi.txt").read_text() == "qux bar foo baz"


# ---------------------------------------------------------------------------
# TS-04-23: execute happy path
# Requirement: 04-REQ-6.6
# ---------------------------------------------------------------------------


class TestExecuteHappyPath:
    """Verify execute runs a shell command and returns stdout/stderr/returncode."""

    def test_execute_echo_command(self) -> None:
        """TS-04-23: execute('echo hello') returns stdout with 'hello'."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)

            tools = make_tools(cwd)
            execute_tool = _find_tool(tools, "execute")
            result = execute_tool(command="echo hello")

        assert result["returncode"] == 0
        assert "hello" in result["stdout"]
        assert isinstance(result["stderr"], str)

    def test_execute_runs_in_cwd(self) -> None:
        """TS-04-23 variant: execute runs commands in the workspace cwd."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            # Create a marker file so we can verify cwd
            (cwd / "marker.txt").write_text("present")

            tools = make_tools(cwd)
            execute_tool = _find_tool(tools, "execute")
            result = execute_tool(command="cat marker.txt")

        assert result["returncode"] == 0
        assert "present" in result["stdout"]


# ---------------------------------------------------------------------------
# TS-04-24: list_files happy path
# Requirement: 04-REQ-6.7
# ---------------------------------------------------------------------------


class TestListFilesHappyPath:
    """Verify list_files returns directory entries."""

    def test_list_files_returns_entries(self) -> None:
        """TS-04-24: list_files('.') returns {'entries': ['a.py', 'b.py']}."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "a.py").write_text("")
            (cwd / "b.py").write_text("")

            tools = make_tools(cwd)
            list_files = _find_tool(tools, "list_files")
            result = list_files(path=".")

        assert "entries" in result
        assert set(result["entries"]) == {"a.py", "b.py"}

    def test_list_files_subdirectory(self) -> None:
        """TS-04-24 variant: list_files works with subdirectories."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            subdir = cwd / "src"
            subdir.mkdir()
            (subdir / "app.py").write_text("")
            (subdir / "util.py").write_text("")

            tools = make_tools(cwd)
            list_files = _find_tool(tools, "list_files")
            result = list_files(path="src")

        assert "entries" in result
        assert set(result["entries"]) == {"app.py", "util.py"}


# ---------------------------------------------------------------------------
# TS-04-25: search_files happy path
# Requirement: 04-REQ-6.8
# ---------------------------------------------------------------------------


class TestSearchFilesHappyPath:
    """Verify search_files returns matching lines with file names."""

    def test_search_files_finds_pattern(self) -> None:
        """TS-04-25: search_files finds 'def foo' in code.py."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "code.py").write_text("def foo():\n    pass\n")

            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")
            result = search_files(pattern="def foo", path=".")

        assert "matches" in result
        assert len(result["matches"]) >= 1
        first_match = result["matches"][0]
        assert "code.py" in first_match["file"]
        assert "def foo" in first_match["text"]

    def test_search_files_returns_line_number(self) -> None:
        """TS-04-25 variant: search_files returns correct line numbers."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "code.py").write_text("# header\ndef bar():\n    pass\n")

            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")
            result = search_files(pattern="def bar", path=".")

        assert "matches" in result
        assert len(result["matches"]) >= 1
        match = result["matches"][0]
        assert match["line"] == 2
        assert "def bar" in match["text"]

    def test_search_files_no_matches(self) -> None:
        """TS-04-25 variant: search_files returns empty matches for no hits."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "code.py").write_text("def foo():\n    pass\n")

            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")
            result = search_files(pattern="nonexistent_pattern", path=".")

        assert "matches" in result
        assert len(result["matches"]) == 0

    def test_search_files_multiple_files(self) -> None:
        """TS-04-25 variant: search_files finds matches across multiple files."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            (cwd / "a.py").write_text("import os\n")
            (cwd / "b.py").write_text("import sys\nimport os\n")

            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")
            result = search_files(pattern="import os", path=".")

        assert "matches" in result
        assert len(result["matches"]) >= 2
        matched_files = {m["file"] for m in result["matches"]}
        # Both files should contain matches (paths may be relative or absolute)
        assert any("a.py" in f for f in matched_files)
        assert any("b.py" in f for f in matched_files)
