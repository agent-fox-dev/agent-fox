"""Coding tools module for the Google ADK backend.

Provides six file and shell operation tools that are registered as ADK function
tools when constructing the Agent. All file operations enforce path containment
to ensure I/O is scoped to the workspace root.

Requirements: 04-REQ-6.1 through 04-REQ-6.8, 04-REQ-6.E1 through 04-REQ-6.E4
"""

import re
import subprocess
from pathlib import Path
from typing import Any

# Default subprocess timeout in seconds (04-REQ-6.E2).
_DEFAULT_SUBPROCESS_TIMEOUT = 120.0

# ---------------------------------------------------------------------------
# Path containment helper
# ---------------------------------------------------------------------------

_PATH_NOT_ALLOWED: dict[str, str] = {
    "error": "path_not_allowed",
    "detail": "Path escapes workspace root",
}


def _check_path(cwd: Path, user_path: str) -> Path | dict[str, str]:
    """Resolve *user_path* against *cwd* and enforce workspace containment.

    Returns the resolved ``Path`` on success, or a structured error dict if
    the resolved path escapes the workspace root.

    The check follows symlinks via ``Path.resolve()`` so a symlink pointing
    outside the workspace is correctly rejected (04-REQ-6.E1).
    """
    resolved_cwd = cwd.resolve()
    resolved = (cwd / user_path).resolve()
    if not resolved.is_relative_to(resolved_cwd):
        return _PATH_NOT_ALLOWED
    return resolved


# ---------------------------------------------------------------------------
# Tool implementations
# ---------------------------------------------------------------------------


def _read_file(cwd: Path, *, path: str) -> dict:
    """Read and return the contents of a file within *cwd*.

    Args:
        path: Relative path to the file within the workspace.

    Returns:
        ``{"content": "<file contents>"}`` on success, or a structured error
        dict on failure.
    """
    check = _check_path(cwd, path)
    if isinstance(check, dict):
        return check
    try:
        return {"content": check.read_text(encoding="utf-8")}
    except OSError as exc:
        return {"error": type(exc).__name__, "detail": str(exc)}


def _write_file(cwd: Path, *, path: str, content: str) -> dict:
    """Write or create a file within *cwd* with the given content.

    Args:
        path: Relative path to the file within the workspace.
        content: The text content to write.

    Returns:
        ``{"ok": true}`` on success, or a structured error dict on failure.
    """
    check = _check_path(cwd, path)
    if isinstance(check, dict):
        return check
    try:
        # Create parent directories if needed.
        check.parent.mkdir(parents=True, exist_ok=True)
        check.write_text(content, encoding="utf-8")
        return {"ok": True}
    except OSError as exc:
        return {"error": type(exc).__name__, "detail": str(exc)}


def _edit_file(cwd: Path, *, path: str, old_text: str, new_text: str) -> dict:
    """Replace the first occurrence of *old_text* with *new_text* in a file.

    Args:
        path: Relative path to the file within the workspace.
        old_text: The text to find in the file.
        new_text: The replacement text.

    Returns:
        ``{"ok": true}`` on success, ``{"error": "text_not_found", ...}`` if
        *old_text* is not present, or a structured error dict on I/O failure.
    """
    check = _check_path(cwd, path)
    if isinstance(check, dict):
        return check
    try:
        file_content = check.read_text(encoding="utf-8")
    except OSError as exc:
        return {"error": type(exc).__name__, "detail": str(exc)}

    if old_text not in file_content:
        return {
            "error": "text_not_found",
            "detail": f"The specified old_text was not found in {path}",
        }

    # Replace only the first occurrence.
    new_content = file_content.replace(old_text, new_text, 1)
    try:
        check.write_text(new_content, encoding="utf-8")
        return {"ok": True}
    except OSError as exc:
        return {"error": type(exc).__name__, "detail": str(exc)}


def _execute(
    cwd: Path,
    timeout: float,
    *,
    command: str,
) -> dict:
    """Run a shell command in *cwd* and return stdout, stderr, and exit code.

    Args:
        command: The shell command to execute.

    Returns:
        ``{"stdout": "...", "stderr": "...", "returncode": <int>}``
    """
    try:
        proc = subprocess.run(
            command,
            shell=True,
            cwd=str(cwd),
            capture_output=True,
            text=True,
            timeout=timeout,
        )
        return {
            "stdout": proc.stdout,
            "stderr": proc.stderr,
            "returncode": proc.returncode,
        }
    except subprocess.TimeoutExpired as exc:
        stdout = ""
        if exc.stdout is not None:
            stdout = exc.stdout if isinstance(exc.stdout, str) else exc.stdout.decode("utf-8", errors="replace")
        stderr = ""
        if exc.stderr is not None:
            stderr = exc.stderr if isinstance(exc.stderr, str) else exc.stderr.decode("utf-8", errors="replace")
        return {
            "stdout": stdout,
            "stderr": stderr,
            "returncode": -1,
        }
    except OSError as exc:
        return {
            "stdout": "",
            "stderr": str(exc),
            "returncode": -1,
        }


def _list_files(cwd: Path, *, path: str) -> dict:
    """List the contents of a directory within *cwd*.

    Args:
        path: Relative path to the directory within the workspace.

    Returns:
        ``{"entries": ["<name>", ...]}`` on success, or a structured error
        dict on failure.
    """
    check = _check_path(cwd, path)
    if isinstance(check, dict):
        return check
    try:
        entries = sorted(entry.name for entry in check.iterdir())
        return {"entries": entries}
    except OSError as exc:
        return {"error": type(exc).__name__, "detail": str(exc)}


def _search_files(cwd: Path, *, pattern: str, path: str) -> dict:
    """Search for *pattern* within files under *path* inside *cwd*.

    Args:
        pattern: A grep-style search pattern (plain string or regex).
        path: Relative path to the directory to search within.

    Returns:
        ``{"matches": [{"file": "...", "line": <int>, "text": "..."}]}``
        on success, or a structured error dict on failure.
    """
    check = _check_path(cwd, path)
    if isinstance(check, dict):
        return check

    if not check.exists():
        return {"error": "FileNotFoundError", "detail": f"Path does not exist: {path}"}

    resolved_cwd = cwd.resolve()
    matches: list[dict[str, Any]] = []

    try:
        compiled = re.compile(pattern)
    except re.error:
        # Fall back to plain-string search if the pattern is not valid regex.
        compiled = re.compile(re.escape(pattern))

    try:
        search_root = check
        if search_root.is_file():
            files = [search_root]
        else:
            files = sorted(f for f in search_root.rglob("*") if f.is_file())

        for file_path in files:
            # Skip binary files by attempting to read as text.
            try:
                content = file_path.read_text(encoding="utf-8")
            except (UnicodeDecodeError, OSError):
                continue

            for line_no, line in enumerate(content.splitlines(), start=1):
                if compiled.search(line):
                    # Report file path relative to cwd.
                    try:
                        rel_path = str(file_path.relative_to(resolved_cwd))
                    except ValueError:
                        rel_path = str(file_path)
                    matches.append(
                        {
                            "file": rel_path,
                            "line": line_no,
                            "text": line,
                        }
                    )
    except OSError as exc:
        return {"error": type(exc).__name__, "detail": str(exc)}

    return {"matches": matches}


# ---------------------------------------------------------------------------
# Public factory
# ---------------------------------------------------------------------------


def make_tools(
    cwd: Path,
    subprocess_timeout: float = _DEFAULT_SUBPROCESS_TIMEOUT,
) -> list:
    """Construct the six coding tools with *cwd* bound via closure.

    Args:
        cwd: The workspace root directory. All file operations are scoped
            to this directory via path containment checks.
        subprocess_timeout: Maximum time in seconds for the ``execute`` tool
            subprocess to run before being terminated.

    Returns:
        A list of six callable tool functions: ``read_file``, ``write_file``,
        ``edit_file``, ``execute``, ``list_files``, ``search_files``.
    """

    def read_file(*, path: str) -> dict:
        """Read and return the contents of a file within the workspace.

        Args:
            path: Relative path to the file within the workspace.

        Returns:
            A dict with 'content' key on success, or an error dict.
        """
        return _read_file(cwd, path=path)

    def write_file(*, path: str, content: str) -> dict:
        """Write or create a file within the workspace.

        Args:
            path: Relative path to the file within the workspace.
            content: The text content to write.

        Returns:
            A dict with 'ok' key on success, or an error dict.
        """
        return _write_file(cwd, path=path, content=content)

    def edit_file(*, path: str, old_text: str, new_text: str) -> dict:
        """Replace the first occurrence of old_text with new_text in a file.

        Args:
            path: Relative path to the file within the workspace.
            old_text: The text to find in the file.
            new_text: The replacement text.

        Returns:
            A dict with 'ok' key on success, or an error dict.
        """
        return _edit_file(cwd, path=path, old_text=old_text, new_text=new_text)

    def execute(*, command: str) -> dict:
        """Run a shell command in the workspace directory.

        Args:
            command: The shell command to execute.

        Returns:
            A dict with 'stdout', 'stderr', and 'returncode' keys.
        """
        return _execute(cwd, subprocess_timeout, command=command)

    def list_files(*, path: str) -> dict:
        """List the contents of a directory within the workspace.

        Args:
            path: Relative path to the directory within the workspace.

        Returns:
            A dict with 'entries' key on success, or an error dict.
        """
        return _list_files(cwd, path=path)

    def search_files(*, pattern: str, path: str) -> dict:
        """Search for a pattern within files under a directory in the workspace.

        Args:
            pattern: A grep-style search pattern.
            path: Relative path to the directory to search within.

        Returns:
            A dict with 'matches' key on success, or an error dict.
        """
        return _search_files(cwd, pattern=pattern, path=path)

    return [read_file, write_file, edit_file, execute, list_files, search_files]
