"""Predictive file conflict detection for parallel task dispatch.

Extracts predicted file modification sets from spec documents (tasks.json,
architecture.md) and detects overlapping files between task groups to prevent
merge conflicts when dispatching tasks in parallel.

Requirements: 39-REQ-9.1, 39-REQ-9.2, 39-REQ-9.3, 39-REQ-9.E1
"""

from __future__ import annotations

import logging
import re
from dataclasses import dataclass
from pathlib import Path

logger = logging.getLogger(__name__)


@dataclass
class FileImpact:
    """Predicted file modifications for a task node.

    Attributes:
        node_id: The task node identifier.
        predicted_files: Set of file paths predicted to be modified.
    """

    node_id: str
    predicted_files: set[str]


# Regex to match backtick-quoted file paths (e.g. `routing/duration.py`)
_BACKTICK_FILE_RE = re.compile(r"`([a-zA-Z0-9_/\-]+\.\w+)`")


def extract_file_impacts(
    spec_dir: Path,
    task_group: int,
) -> set[str]:
    """Extract predicted file modifications from spec documents.

    Parses tasks.json for the specified task group and scans its body
    and subtask titles for backtick-quoted file paths.  Also scans
    architecture.md for additional file references.

    Args:
        spec_dir: Path to the spec directory containing tasks.json and/or
            architecture.md.
        task_group: The task group number to extract impacts for.

    Returns:
        Set of predicted file paths. Returns empty set if no file
        references are found (39-REQ-9.E1).

    Requirements: 39-REQ-9.1, 39-REQ-9.E1
    """
    files: set[str] = set()

    # Extract from tasks.json via the spec parser
    try:
        from agentfox.spec.parser import parse_tasks  # noqa: PLC0415

        groups = parse_tasks(spec_dir)
        for group in groups:
            if group.number == task_group:
                # Scan the group body for file paths
                if group.body:
                    files.update(_extract_file_paths(group.body))
                # Scan subtask titles for file paths
                for subtask in group.subtasks:
                    files.update(_extract_file_paths(subtask.title))
                break
    except Exception:
        logger.debug(
            "Failed to parse tasks.json in %s for file impact extraction",
            spec_dir,
            exc_info=True,
        )

    # Extract from architecture.md
    design_md = spec_dir / "architecture.md"
    if design_md.exists():
        content = design_md.read_text()
        # For architecture.md, scan the entire document since file references
        # may not be grouped by task group
        files.update(_extract_file_paths(content))

    return files


def _extract_file_paths(text: str) -> set[str]:
    """Extract file paths from text using regex patterns.

    Matches backtick-quoted paths like `routing/duration.py` and
    filters out obvious non-file-path matches.
    """
    files: set[str] = set()

    for match in _BACKTICK_FILE_RE.finditer(text):
        path = match.group(1)
        if _is_likely_file_path(path):
            files.add(path)

    return files


def _is_likely_file_path(path: str) -> bool:
    """Heuristic check if a string looks like a file path."""
    # Must contain a directory separator or be a dotted filename
    if "/" not in path and "." not in path:
        return False
    # Filter out common non-file patterns
    ext = path.rsplit(".", 1)[-1] if "." in path else ""
    valid_extensions = {
        "py",
        "js",
        "ts",
        "tsx",
        "jsx",
        "rs",
        "go",
        "java",
        "md",
        "toml",
        "yaml",
        "yml",
        "json",
        "sql",
        "sh",
        "css",
        "html",
        "xml",
        "txt",
        "cfg",
        "ini",
    }
    return ext.lower() in valid_extensions


def detect_conflicts(
    impacts: list[FileImpact],
) -> list[tuple[str, str, set[str]]]:
    """Find pairs of nodes with overlapping file predictions.

    The conflict relation is symmetric: each pair is reported once
    with the lower node_id first.

    Args:
        impacts: List of FileImpact objects to check for conflicts.

    Returns:
        List of (node_a, node_b, overlapping_files) tuples, where
        node_a < node_b alphabetically and overlapping_files is the
        set of shared file paths.

    Requirements: 39-REQ-9.2
    """
    conflicts: list[tuple[str, str, set[str]]] = []

    for i in range(len(impacts)):
        for j in range(i + 1, len(impacts)):
            overlap = impacts[i].predicted_files & impacts[j].predicted_files
            if overlap:
                a, b = impacts[i].node_id, impacts[j].node_id
                # Ensure consistent ordering (lower first)
                if a > b:
                    a, b = b, a
                conflicts.append((a, b, overlap))

    return conflicts


def filter_conflicts_from_dispatch(
    ready: list[str],
    impacts: list[FileImpact],
) -> list[str]:
    """Filter conflicting tasks from a dispatch list.

    When two ready tasks have overlapping predicted files, only the
    first one (alphabetically) is dispatched. The other is deferred.

    Args:
        ready: List of ready task node_ids.
        impacts: List of FileImpact objects for all tasks.

    Returns:
        Filtered list of node_ids safe to dispatch in parallel.

    Requirements: 39-REQ-9.3
    """
    if not impacts:
        return list(ready)

    # Build impact lookup
    impact_map = {imp.node_id: imp.predicted_files for imp in impacts}

    # Track which nodes are excluded due to conflicts
    excluded: set[str] = set()
    dispatched: list[str] = []

    # Process ready tasks in order
    for node_id in ready:
        if node_id in excluded:
            continue

        node_files = impact_map.get(node_id, set())
        if not node_files:
            # No predicted files = non-conflicting (39-REQ-9.E1)
            dispatched.append(node_id)
            continue

        # Check against already-dispatched tasks
        has_conflict = False
        for dispatched_id in dispatched:
            dispatched_files = impact_map.get(dispatched_id, set())
            if node_files & dispatched_files:
                has_conflict = True
                break

        if not has_conflict:
            dispatched.append(node_id)
        else:
            excluded.add(node_id)

    return dispatched
