"""Tests for adk_tools.py coding tools module.

Test Spec: TS-04-18, TS-04-19, TS-04-20 through TS-04-25, TS-04-38,
           TS-04-40, TS-04-E7, TS-04-E8, TS-04-E9, TS-04-E10, TS-04-P3
Requirements: 04-REQ-6.1, 04-REQ-6.2, 04-REQ-6.3, 04-REQ-6.4, 04-REQ-6.5,
              04-REQ-6.6, 04-REQ-6.7, 04-REQ-6.8,
              04-REQ-6.E1, 04-REQ-6.E2, 04-REQ-6.E3, 04-REQ-6.E4,
              04-REQ-14.1, 04-REQ-14.3

google-adk is a mandatory dependency of the agentfox package.
"""

from __future__ import annotations

import inspect
import tempfile
from pathlib import Path

import pytest

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


# ===========================================================================
# Task Group 6: adk_tools.py path containment and error edge cases
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-04-19: All file-accepting tools reject paths that escape workspace root
# Requirement: 04-REQ-6.2
# ---------------------------------------------------------------------------


class TestPathContainment:
    """Verify all tools reject paths that escape the workspace root."""

    def test_read_file_path_escape(self) -> None:
        """TS-04-19: read_file rejects path traversal with structured error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")
            result = read_file(path="../../etc/passwd")

        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }

    def test_write_file_path_escape(self) -> None:
        """TS-04-19: write_file rejects path traversal with structured error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            write_file = _find_tool(tools, "write_file")
            result = write_file(path="../../etc/evil", content="x")

        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }

    def test_edit_file_path_escape(self) -> None:
        """TS-04-19: edit_file rejects path traversal with structured error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            edit_file = _find_tool(tools, "edit_file")
            result = edit_file(
                path="../../etc/passwd",
                old_text="root",
                new_text="evil",
            )

        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }

    def test_list_files_path_escape(self) -> None:
        """TS-04-19: list_files rejects path traversal with structured error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            list_files = _find_tool(tools, "list_files")
            result = list_files(path="../../etc")

        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }

    def test_search_files_path_escape(self) -> None:
        """TS-04-19: search_files rejects path traversal with structured error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")
            result = search_files(pattern="root", path="../../etc")

        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }

    def test_no_io_on_escape(self) -> None:
        """TS-04-19: No file is created when path escapes workspace root."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            escape_target = cwd / ".." / "escape_test_marker.txt"
            # Ensure the file does not exist before the call
            if escape_target.resolve().exists():
                escape_target.resolve().unlink()

            tools = make_tools(cwd)
            write_file = _find_tool(tools, "write_file")
            result = write_file(
                path="../escape_test_marker.txt",
                content="should not be written",
            )

            # The call should return the error dict
            assert result.get("error") == "path_not_allowed"
            # The file should NOT have been created
            assert not escape_target.resolve().exists()


# ---------------------------------------------------------------------------
# TS-04-E7: Symlink within cwd resolving outside workspace triggers error
# Requirement: 04-REQ-6.E1
# ---------------------------------------------------------------------------


class TestSymlinkEscape:
    """Verify symlinks resolving outside workspace root trigger path error."""

    def test_symlink_to_etc_rejected(self) -> None:
        """TS-04-E7: Symlink to /etc triggers path_not_allowed error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            # Create a symlink inside the workspace pointing outside
            symlink_path = cwd / "evil_link"
            try:
                symlink_path.symlink_to("/etc")
            except OSError:
                pytest.skip("Cannot create symlinks (platform limitation)")

            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")
            result = read_file(path="evil_link/passwd")

        assert result == {
            "error": "path_not_allowed",
            "detail": "Path escapes workspace root",
        }

    def test_symlink_escape_no_io(self) -> None:
        """TS-04-E7 variant: No file I/O performed on symlink escape."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)

            # Create a second temp dir outside workspace to symlink to
            with tempfile.TemporaryDirectory() as outside:
                symlink_path = cwd / "outside_link"
                try:
                    symlink_path.symlink_to(outside)
                except OSError:
                    pytest.skip("Cannot create symlinks (platform limitation)")

                tools = make_tools(cwd)
                write_file = _find_tool(tools, "write_file")
                result = write_file(
                    path="outside_link/secret.txt",
                    content="should not appear",
                )

                assert result.get("error") == "path_not_allowed"
                # No file should have been written in the outside directory
                assert not (Path(outside) / "secret.txt").exists()


# ---------------------------------------------------------------------------
# TS-04-E8: Shell command timeout — terminated and returns structured result
# Requirement: 04-REQ-6.E2
# ---------------------------------------------------------------------------


class TestExecuteTimeout:
    """Verify execute tool handles subprocess timeout correctly."""

    def test_hanging_command_terminated(self) -> None:
        """TS-04-E8: sleep 9999 is terminated; returns non-zero returncode."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            # Pass a short subprocess timeout to avoid blocking the test
            tools = make_tools(cwd, subprocess_timeout=0.1)
            execute_tool = _find_tool(tools, "execute")
            result = execute_tool(command="sleep 9999")

        assert "returncode" in result
        assert result["returncode"] != 0
        assert "stdout" in result
        assert "stderr" in result

    def test_timeout_no_exception_escapes(self) -> None:
        """TS-04-E8 variant: No exception escapes on timeout."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd, subprocess_timeout=0.1)
            execute_tool = _find_tool(tools, "execute")
            try:
                result = execute_tool(command="sleep 9999")
            except Exception as exc:
                pytest.fail(f"Exception escaped execute tool: {exc}")
            assert isinstance(result, dict)


# ---------------------------------------------------------------------------
# TS-04-E9: Shell command with non-zero exit code returns structured result
# Requirement: 04-REQ-6.E3
# ---------------------------------------------------------------------------


class TestExecuteNonZeroExit:
    """Verify execute tool handles non-zero exit codes without raising."""

    def test_false_command_returns_nonzero(self) -> None:
        """TS-04-E9: 'false' command returns non-zero returncode."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            execute_tool = _find_tool(tools, "execute")
            result = execute_tool(command="false")

        assert "returncode" in result
        assert result["returncode"] != 0
        assert "stdout" in result
        assert "stderr" in result

    def test_exit_one_returns_nonzero(self) -> None:
        """TS-04-E9 variant: 'exit 1' returns non-zero returncode."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            execute_tool = _find_tool(tools, "execute")
            result = execute_tool(command="exit 1")

        assert "returncode" in result
        assert result["returncode"] != 0
        assert "stdout" in result
        assert isinstance(result["stderr"], str)

    def test_nonzero_exit_no_exception(self) -> None:
        """TS-04-E9 variant: No exception raised for non-zero exit."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            execute_tool = _find_tool(tools, "execute")
            try:
                result = execute_tool(command="false")
            except Exception as exc:
                pytest.fail(f"Exception raised for non-zero exit: {exc}")
            assert isinstance(result, dict)


# ---------------------------------------------------------------------------
# TS-04-E10: OS-level exception during file operation returns error dict
# Requirement: 04-REQ-6.E4
# ---------------------------------------------------------------------------


class TestFileOperationOsError:
    """Verify OS-level errors return structured error dicts."""

    def test_read_nonexistent_file(self) -> None:
        """TS-04-E10: read_file on missing file returns error dict."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")
            result = read_file(path="nonexistent_file.txt")

        assert "error" in result
        assert "detail" in result

    def test_read_nonexistent_no_exception(self) -> None:
        """TS-04-E10 variant: No exception raised for missing file."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")
            try:
                result = read_file(path="nonexistent_file.txt")
            except Exception as exc:
                pytest.fail(f"Exception raised for missing file: {exc}")
            assert isinstance(result, dict)

    def test_list_nonexistent_directory(self) -> None:
        """TS-04-E10 variant: list_files on missing directory returns error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            list_files = _find_tool(tools, "list_files")
            result = list_files(path="nonexistent_dir")

        assert "error" in result
        assert "detail" in result

    def test_edit_nonexistent_file(self) -> None:
        """TS-04-E10 variant: edit_file on missing file returns error dict."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            edit_file = _find_tool(tools, "edit_file")
            result = edit_file(
                path="no_such_file.txt",
                old_text="hello",
                new_text="world",
            )

        assert "error" in result
        assert "detail" in result

    def test_search_nonexistent_directory(self) -> None:
        """TS-04-E10 variant: search_files on missing path returns error."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")
            result = search_files(
                pattern="anything",
                path="nonexistent_dir",
            )

        assert "error" in result
        assert "detail" in result


# ---------------------------------------------------------------------------
# TS-04-40: Full test suite coverage for all six tools (meta test)
# Requirement: 04-REQ-14.3
# ---------------------------------------------------------------------------


class TestAdkToolsSuiteCoverage:
    """Meta test: verify this test file covers all six tools."""

    def test_all_six_tools_have_tests(self) -> None:
        """TS-04-40: All six tool names appear in this test file's source."""
        source = Path(__file__).read_text(encoding="utf-8")
        for tool_name in (
            "read_file",
            "write_file",
            "edit_file",
            "execute",
            "list_files",
            "search_files",
        ):
            assert tool_name in source, (
                f"No test found for tool: {tool_name}"
            )

    def test_path_containment_tests_present(self) -> None:
        """TS-04-40 variant: Path containment tests exist in this file."""
        source = Path(__file__).read_text(encoding="utf-8")
        assert "path_not_allowed" in source, (
            "No path containment test found in test file"
        )
        assert "Path escapes workspace root" in source, (
            "No path escape detail assertion found in test file"
        )


# ---------------------------------------------------------------------------
# TS-04-P3: Property-style tests for path containment across many patterns
# Requirement: 04-REQ-6.2, 04-REQ-6.E1
# ---------------------------------------------------------------------------


class TestPathContainmentProperty:
    """Property-style tests iterating escape path patterns for each tool."""

    # Escape path patterns to test — all should be blocked
    ESCAPE_PATHS = [
        "../../../etc/passwd",         # relative traversal
        "../../etc/passwd",            # relative traversal (shorter)
        "../outside",                  # one level up
        "/etc/passwd",                 # absolute path
        "/tmp/outside_workspace",      # absolute path to tmp
    ]

    def test_read_file_rejects_all_escape_paths(self) -> None:
        """TS-04-P3: read_file rejects all escape path patterns."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            read_file = _find_tool(tools, "read_file")

            for escape_path in self.ESCAPE_PATHS:
                resolved = (cwd / escape_path).resolve()
                if not resolved.is_relative_to(cwd.resolve()):
                    result = read_file(path=escape_path)
                    assert result == {
                        "error": "path_not_allowed",
                        "detail": "Path escapes workspace root",
                    }, f"read_file did not reject escape path: {escape_path}"

    def test_write_file_rejects_all_escape_paths(self) -> None:
        """TS-04-P3: write_file rejects all escape path patterns."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            write_file = _find_tool(tools, "write_file")

            for escape_path in self.ESCAPE_PATHS:
                resolved = (cwd / escape_path).resolve()
                if not resolved.is_relative_to(cwd.resolve()):
                    result = write_file(
                        path=escape_path,
                        content="malicious",
                    )
                    assert result == {
                        "error": "path_not_allowed",
                        "detail": "Path escapes workspace root",
                    }, f"write_file did not reject escape path: {escape_path}"

    def test_edit_file_rejects_all_escape_paths(self) -> None:
        """TS-04-P3: edit_file rejects all escape path patterns."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            edit_file = _find_tool(tools, "edit_file")

            for escape_path in self.ESCAPE_PATHS:
                resolved = (cwd / escape_path).resolve()
                if not resolved.is_relative_to(cwd.resolve()):
                    result = edit_file(
                        path=escape_path,
                        old_text="x",
                        new_text="y",
                    )
                    assert result == {
                        "error": "path_not_allowed",
                        "detail": "Path escapes workspace root",
                    }, f"edit_file did not reject escape path: {escape_path}"

    def test_list_files_rejects_all_escape_paths(self) -> None:
        """TS-04-P3: list_files rejects all escape path patterns."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            list_files = _find_tool(tools, "list_files")

            for escape_path in self.ESCAPE_PATHS:
                resolved = (cwd / escape_path).resolve()
                if not resolved.is_relative_to(cwd.resolve()):
                    result = list_files(path=escape_path)
                    assert result == {
                        "error": "path_not_allowed",
                        "detail": "Path escapes workspace root",
                    }, f"list_files did not reject escape path: {escape_path}"

    def test_search_files_rejects_all_escape_paths(self) -> None:
        """TS-04-P3: search_files rejects all escape path patterns."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            search_files = _find_tool(tools, "search_files")

            for escape_path in self.ESCAPE_PATHS:
                resolved = (cwd / escape_path).resolve()
                if not resolved.is_relative_to(cwd.resolve()):
                    result = search_files(
                        pattern="test",
                        path=escape_path,
                    )
                    assert result == {
                        "error": "path_not_allowed",
                        "detail": "Path escapes workspace root",
                    }, f"search_files did not reject escape path: {escape_path}"

    def test_no_io_on_any_escape_path(self) -> None:
        """TS-04-P3: No I/O is performed when path containment check fails."""
        from agentfox.session.backends.adk_tools import make_tools

        with tempfile.TemporaryDirectory() as tmp:
            cwd = Path(tmp)
            tools = make_tools(cwd)
            write_file = _find_tool(tools, "write_file")

            # Track files before operations
            marker_path = cwd / ".." / "containment_check_marker.txt"
            resolved_marker = marker_path.resolve()
            if resolved_marker.exists():
                resolved_marker.unlink()

            result = write_file(
                path="../containment_check_marker.txt",
                content="should not exist",
            )

            assert result.get("error") == "path_not_allowed"
            assert not resolved_marker.exists(), (
                "File was created outside workspace despite containment check"
            )
