"""Canonical shared types for the spec layer.

Provides frozen dataclasses for task definitions (``TaskGroupDef``,
``SubtaskDef``, ``CrossSpecDep``) and validation findings (``Finding``),
along with severity constants and helper functions.

These types were previously split across ``parser.py`` and
``validators/_helpers.py``.  This module consolidates them into a single
import location used by all consumers.

Requirements: 137-REQ-1.1, 137-REQ-1.2
"""

from __future__ import annotations

from dataclasses import dataclass

# ---------------------------------------------------------------------------
# Severity constants
# ---------------------------------------------------------------------------

SEVERITY_ERROR = "error"
SEVERITY_WARNING = "warning"
SEVERITY_HINT = "hint"

# Sorting order: error < warning < hint
SEVERITY_ORDER = {SEVERITY_ERROR: 0, SEVERITY_WARNING: 1, SEVERITY_HINT: 2}


# ---------------------------------------------------------------------------
# Task-definition types (formerly in parser.py)
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class SubtaskDef:
    """A single nested subtask within a task group."""

    id: str  # e.g., "1.2"
    title: str  # subtask description text
    completed: bool  # checkbox state


@dataclass(frozen=True)
class TaskGroupDef:
    """A parsed top-level task group from tasks.md."""

    number: int  # group number (1, 2, 3, ...)
    title: str  # group title text
    optional: bool  # True if marked with *
    completed: bool  # True if checkbox is [x]
    subtasks: tuple[SubtaskDef, ...]  # nested subtasks
    body: str  # full raw text of the group
    archetype: str | None = None  # 26-REQ-5.1: from [archetype: X] tag
    kind: str | None = None  # afspec TaskGroupKind (e.g. "checkpoint")


@dataclass(frozen=True)
class CrossSpecDep:
    """A cross-spec dependency declaration from a prd.md table."""

    from_spec: str  # source spec name (the spec declaring the dependency)
    from_group: int  # source group number (0 = first group, resolved by builder)
    to_spec: str  # target spec name (the spec being depended on)
    to_group: int  # target group number (0 = last group, resolved by builder)


# ---------------------------------------------------------------------------
# Validation finding types (formerly in validators/_helpers.py)
# ---------------------------------------------------------------------------


@dataclass(frozen=True)
class Finding:
    """A single validation finding."""

    spec_name: str  # e.g., "01_core_foundation"
    file: str  # e.g., "tasks.md"
    rule: str  # e.g., "missing-file", "oversized-group"
    severity: str  # "error" | "warning" | "hint"
    message: str  # Human-readable description
    line: int | None  # Source line number, if available


# ---------------------------------------------------------------------------
# Helper functions
# ---------------------------------------------------------------------------


def sort_findings(findings: list[Finding]) -> list[Finding]:
    """Sort findings by spec_name, file, then severity (error < warning < hint)."""
    return sorted(
        findings,
        key=lambda f: (f.spec_name, f.file, SEVERITY_ORDER.get(f.severity, 99)),
    )


def compute_exit_code(findings: list[Finding]) -> int:
    """Determine exit code from findings: 1 if any errors, 0 otherwise."""
    return 1 if any(f.severity == SEVERITY_ERROR for f in findings) else 0
