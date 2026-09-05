"""Audit result types produced by the reviewer's audit-review mode.

``parse_audit_output`` (``session/review_parser.py``) builds these records and
``session/auditor_output.py`` renders and persists them.

Requirements: 113-REQ-4.1
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class AuditEntry:
    """A single TS entry audit result.

    Supports two construction patterns:
    - Original: AuditEntry(ts_entry="TS-1", test_functions=[], verdict="PASS")
    - Audit finding: AuditEntry(severity="critical", description="...")
      where ts_entry/test_functions/verdict default to empty/blank values.
    """

    ts_entry: str = ""
    test_functions: list[str] = field(default_factory=list)
    verdict: str = ""  # PASS | WEAK | MISSING | MISALIGNED
    notes: str | None = None
    # 113-REQ-4.1: Additional fields for audit finding persistence
    severity: str = ""
    description: str = ""


@dataclass(frozen=True)
class AuditResult:
    """Aggregated audit result for a spec."""

    entries: list[AuditEntry]
    overall_verdict: str  # PASS | FAIL
    summary: str
