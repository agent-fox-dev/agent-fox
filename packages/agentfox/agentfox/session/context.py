"""Context assembly: spec documents, findings, memory facts, steering.

Gathers spec documents, review/drift/verification findings from DuckDB,
memory facts, and steering directives into session context for coding
agents.

Requirements: 03-REQ-4.1 through 03-REQ-4.E1, 03-REQ-5.1, 03-REQ-5.2,
              27-REQ-5.1, 27-REQ-5.2, 27-REQ-5.3, 27-REQ-5.E1, 27-REQ-5.E2,
              27-REQ-10.1, 27-REQ-10.2, 32-REQ-8.1, 32-REQ-8.2,
              42-REQ-1.1, 42-REQ-1.2, 42-REQ-4.1, 42-REQ-4.2
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass
from pathlib import Path
from typing import Any

import duckdb

from agentfox.core.prompt_safety import sanitize_prompt_content
from agentfox.session.steering import load_steering

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class PriorFinding:
    """A finding from a prior task group, tagged by type.

    Requirements: 42-REQ-4.1, 42-REQ-4.2
    """

    type: str  # "review" | "drift" | "verification"
    group: str  # task_group value
    severity: str  # severity level or verdict
    description: str  # description text or evidence
    created_at: str  # ISO timestamp string for sorting


# ---------------------------------------------------------------------------
# Findings rendering
# ---------------------------------------------------------------------------

# Archetype-produced files — only present after the corresponding archetype
# (Reviewer / Verifier) has run.  Included silently when they exist on disk,
# skipped silently when they don't.
_ARCHETYPE_SPEC_FILES: list[tuple[str, str]] = [
    ("review.md", "## Reviewer Findings"),
    ("verification.md", "## Verification Report"),
]

# v1.2 artifact-to-header mapping for JSON-based specs rendered via afspec.
# Requirements: 134-REQ-1.1, 134-REQ-2.1
_SECTION_HEADERS: dict[str, str] = {
    "requirements": "## Requirements",
    "test_spec": "## Test Specification",
    "tasks": "## Tasks",
}


def render_inmemory_spec_sections(spec: Any) -> list[str]:
    """Render an in-memory afspec Spec to per-artifact markdown sections.

    Accepts a single ``Spec`` argument and returns a list of markdown
    section strings by delegating to ``afspec.render_individual(spec)``.

    Performs no file system reads or writes.  Exceptions from
    ``render_individual`` propagate to the caller as-is.

    Requirements: 01-REQ-6.1, 01-REQ-6.2, 01-REQ-6.E1
    """
    import afspec

    rendered = afspec.render_individual(spec)

    sections: list[str] = []
    for key, header in _SECTION_HEADERS.items():
        content = rendered.get(key, "")
        if content:
            sections.append(f"{header}\n\n{content}")

    return sections


def _render_spec_sections(spec_dir: Path) -> list[str]:
    """Load a v1.2 spec and render per-artifact markdown sections.

    Returns a list of rendered section strings.  Raises ``afspec.LoadError``
    on malformed specs (caller handles fallback).

    Delegates rendering to ``render_inmemory_spec_sections`` after loading
    the Spec from disk, eliminating duplicated rendering logic.

    Requirements: 134-REQ-2.1, 134-REQ-2.2, 134-REQ-2.3, 134-REQ-2.E1,
                  01-REQ-6.3
    """
    import afspec

    spec = afspec.load_spec(spec_dir)
    sections = render_inmemory_spec_sections(spec)

    # architecture.md is a plain markdown file in v1.2
    arch_path = spec_dir / "architecture.md"
    if arch_path.is_file():
        arch_content = arch_path.read_text(encoding="utf-8")
        safe = sanitize_prompt_content(arch_content, label="spec")
        sections.append(f"## Architecture\n\n{safe}")

    return sections


def _render_severity_findings(
    findings: list,
    title: str,
    format_finding: Callable[..., str],
    *,
    show_empty_groups: bool = False,
) -> str:
    """Render findings grouped by severity as a markdown section.

    Args:
        findings: List of finding objects with a ``severity`` attribute.
        title: Markdown heading for the section (e.g. "## Reviewer Findings").
        format_finding: Callable that formats a single finding as a string.
        show_empty_groups: If True, render "(none)" for severity levels
            with no findings.
    """
    severity_groups = {
        "critical": "### Critical Findings",
        "major": "### Major Findings",
        "minor": "### Minor Findings",
        "observation": "### Observations",
    }

    lines = [title, ""]
    counts: dict[str, int] = {"critical": 0, "major": 0, "minor": 0, "observation": 0}

    for sev, header in severity_groups.items():
        sev_findings = [f for f in findings if f.severity == sev]
        counts[sev] = len(sev_findings)
        if sev_findings:
            lines.append(header)
            for f in sev_findings:
                lines.append(format_finding(f))
            lines.append("")
        elif show_empty_groups:
            lines.append(header)
            lines.append("(none)")
            lines.append("")

    lines.append(
        f"Summary: {counts['critical']} critical, {counts['major']} major, "
        f"{counts['minor']} minor, {counts['observation']} observations."
    )

    return "\n".join(lines)


def render_drift_context(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
) -> str | None:
    """Render active drift findings as a markdown section.

    Returns None if no findings exist (32-REQ-8.E1).

    Requirements: 32-REQ-8.1, 32-REQ-8.2
    """
    from agentfox.knowledge.review_store import (
        query_active_drift_findings,
    )

    findings = query_active_drift_findings(conn, spec_name)
    if not findings:
        return None

    def _format(f):
        desc = sanitize_prompt_content(f.description, label="drift-finding")
        refs = []
        if f.spec_ref:
            refs.append(f"spec: {f.spec_ref}")
        if f.artifact_ref:
            refs.append(f"artifact: {f.artifact_ref}")
        if refs:
            desc += f" ({', '.join(refs)})"
        return f"- {desc}"

    return _render_severity_findings(findings, "## Drift Report", _format)


def render_review_context(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
) -> str | None:
    """Render active findings as a markdown section.

    Returns None if no findings exist (27-REQ-5.E2).

    Requirements: 27-REQ-5.1, 27-REQ-5.3
    """
    from agentfox.knowledge.review_store import (
        query_active_findings,
    )

    findings = query_active_findings(conn, spec_name)
    if not findings:
        return None

    def _format_review(f):
        sanitized = sanitize_prompt_content(f.description, label="review-finding")
        return f"- [severity: {f.severity}] {sanitized}"

    return _render_severity_findings(
        findings,
        "## Reviewer Findings",
        _format_review,
        show_empty_groups=True,
    )


def render_verification_context(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
) -> str | None:
    """Render active verdicts as a markdown section.

    Returns None — the verification_results table has been removed and
    verdicts are no longer persisted.  Retained as a no-op stub so
    callers do not need updating.
    """
    return None


def render_retry_history(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
    task_group: str,
) -> str | None:
    """Render unresolved injected findings as retry history for the reviewer.

    Returns a markdown section listing findings that were injected into a
    prior coder session for this (spec, task_group) but remain active.
    Returns None when no unresolved injections exist.
    """
    from agentfox.knowledge.review_store import query_unresolved_injections

    unresolved = query_unresolved_injections(conn, spec_name, task_group)
    if not unresolved:
        return None

    lines = [
        "## Retry History",
        "",
        "The following findings were injected into the coder's prior session but remain unresolved.",
        "Consider downgrading to WEAK if the issue appears beyond the coder's capability.",
        "",
    ]
    for desc, severity in unresolved:
        sanitized = sanitize_prompt_content(desc, label="retry-history")
        lines.append(f"- [{severity}] {sanitized}")
    return "\n".join(lines)


def _migrate_legacy_files(
    conn: duckdb.DuckDBPyConnection,
    spec_dir: Path,
    spec_name: str,
) -> None:
    """Migrate legacy review.md files to DB records.

    Idempotent: for each legacy file, the function queries existing active
    records (``superseded_by IS NULL``) before attempting migration.  If
    any active record already exists for the spec, the migration for that
    file type is skipped entirely.  Running this function multiple times
    with the same ``(conn, spec_dir, spec_name)`` arguments produces no
    duplicate records and raises no errors.

    On parse failure, logs a warning and skips (27-REQ-10.E1).

    Requirements: 27-REQ-10.1, 27-REQ-10.2, 27-REQ-10.E1, 06-REQ-5.3
    """
    from agentfox.knowledge.review_store import (
        insert_findings,
        query_active_findings,
    )
    from agentfox.session.review_parser import (
        parse_legacy_review_md,
    )

    # Table-driven legacy migration: (filename, query_fn, parse_fn, insert_fn, label)
    _migrations: list[tuple[str, Any, Any, Any, str]] = [
        (
            "review.md",
            query_active_findings,
            parse_legacy_review_md,
            insert_findings,
            "findings",
        ),
    ]
    for filename, query_fn, parse_fn, insert_fn, label in _migrations:
        path = spec_dir / filename
        if not path.exists() or query_fn(conn, spec_name):
            continue
        try:
            content = path.read_text(encoding="utf-8")
            records = parse_fn(content, spec_name, "legacy", "legacy-migration")
            if records:
                insert_fn(conn, records)
                logger.info("Migrated %d %s from %s", len(records), label, path)
        except Exception:
            logger.warning(
                "Failed to migrate legacy %s file %s, skipping",
                label,
                path,
                exc_info=True,
            )


# ---------------------------------------------------------------------------
# Context assembly
# ---------------------------------------------------------------------------


def assemble_context(
    spec_dir: Path,
    task_group: int,
    memory_facts: list[str] | None = None,
    *,
    conn: duckdb.DuckDBPyConnection,
    project_root: Path | None = None,
    archetype: str | None = None,
) -> str:
    """Assemble task-specific context for a coding session.

    conn must be a read-only connection; write operations
    (_migrate_legacy_files) is performed
    at orchestrator startup, not here.  See 06-REQ-5.1, 06-REQ-6.1,
    06-REQ-7.1, 06-REQ-7.2.

    Renders spec documents via afspec (v1.2 JSON format).

    Renders review/verification/drift sections from DuckDB
    (27-REQ-5.1, 27-REQ-5.2, 38-REQ-4.1, 38-REQ-4.2).
    DB errors propagate — no file-based fallback (38-REQ-3.E1).

    Appends relevant memory facts (if provided).

    When project_root is provided, includes steering directives from
    .agent-fox/steering.md after spec files and before memory facts
    (64-REQ-2.1, 64-REQ-2.2).

    When archetype is ``"verifier"``, appends a structured verification
    checklist (task completion audit + requirement-to-test coverage).

    Returns a formatted string with section headers.

    Logs a warning for any missing spec file but does not raise.
    """
    sections: list[str] = []

    # Derive spec_name from directory name
    spec_name = spec_dir.name

    # DB-backed rendering — errors propagate (38-REQ-3.E1, 38-REQ-4.2)
    db_rendered_files: set[str] = set()

    # DB-backed rendering (27-REQ-5.1, 27-REQ-5.2, 38-REQ-4.3)
    review_md = render_review_context(conn, spec_name)
    if review_md is not None:
        sections.append(review_md)
        db_rendered_files.add("review.md")

    verification_md = render_verification_context(conn, spec_name)
    if verification_md is not None:
        sections.append(verification_md)
        db_rendered_files.add("verification.md")

    # Render drift-review report (32-REQ-8.1)
    drift_md = render_drift_context(conn, spec_name)
    if drift_md is not None:
        sections.append(drift_md)

    # 03-REQ-4.1, 134-REQ-1.1: Read spec documents via afspec (v1.2 JSON)
    file_sections: list[str] = []

    try:
        raw_sections = _render_spec_sections(spec_dir)
        # Sanitize rendered spec sections for safe prompt inclusion
        file_sections = [sanitize_prompt_content(s, label="spec") for s in raw_sections]
    except Exception:
        logger.warning(
            "Failed to load spec in %s",
            spec_dir,
            exc_info=True,
        )

    # Include archetype-produced files (review.md, verification.md) only
    # when they exist on disk and weren't already rendered from the DB.
    for filename, header in _ARCHETYPE_SPEC_FILES:
        if filename in db_rendered_files:
            continue
        filepath = spec_dir / filename
        if not filepath.exists():
            continue
        content = filepath.read_text(encoding="utf-8")
        safe_content = sanitize_prompt_content(content, label="spec")
        file_sections.append(f"{header}\n\n{safe_content}")

    # Insert file sections before DB-rendered sections
    sections = file_sections + sections

    # 64-REQ-2.1, 64-REQ-2.2: Include steering directives after spec files,
    # before memory facts.
    if project_root is not None:
        steering_content = load_steering(project_root, spec_root=spec_dir.parent)
        if steering_content:
            sections.append(f"## Steering Directives\n\n{steering_content}")

    # 03-REQ-4.2: Include memory facts (sanitize stored facts against injection)
    if memory_facts:
        facts_text = "\n".join(f"- {sanitize_prompt_content(fact, label='memory-fact')}" for fact in memory_facts)
        sections.append(f"## Memory Facts\n\n{facts_text}")

    # 39-REQ-6.1, 39-REQ-6.2: Include prior group findings
    if task_group > 1:
        try:
            prior_findings = get_prior_group_findings(
                conn,
                spec_name,
                task_group=task_group,
            )
            if prior_findings:
                prior_section = render_prior_group_findings(prior_findings)
                sections.append(prior_section)
        except Exception:
            logger.debug(
                "Failed to fetch prior group findings for %s group %d",
                spec_name,
                task_group,
            )

    # Retry history for the reviewer archetype
    if archetype == "reviewer":
        try:
            retry_md = render_retry_history(conn, spec_name, str(task_group))
            if retry_md is not None:
                sections.append(retry_md)
        except Exception:
            logger.debug(
                "Failed to render retry history for %s group %d",
                spec_name,
                task_group,
            )

    # Verification checklist for the verifier archetype
    if archetype == "verifier":
        try:
            from agentfox.spec.verification_checklist import (
                build_verification_checklist,
                render_checklist_markdown,
            )

            tests_dir = project_root / "tests" if project_root is not None else None
            checklist = build_verification_checklist(spec_dir, tests_dir=tests_dir)
            checklist_md = render_checklist_markdown(checklist)
            sections.append(checklist_md)
        except Exception:
            logger.warning(
                "Failed to build verification checklist for %s",
                spec_dir.name,
                exc_info=True,
            )

    # 03-REQ-4.3: Return formatted string with section headers
    return "\n\n---\n\n".join(sections)


# ---------------------------------------------------------------------------
# Prior group findings
# ---------------------------------------------------------------------------


def get_prior_group_findings(
    conn: duckdb.DuckDBPyConnection,
    spec_name: str,
    *,
    task_group: int,
) -> list[PriorFinding]:
    """Query active findings from all three tables for prior task groups.

    Returns PriorFinding objects from groups 1 through task_group-1 for the
    given spec, excluding superseded findings, sorted by created_at ascending.

    Queries review_findings, drift_findings, and verification_results tables.
    If any table does not exist (pre-migration database), that table's results
    are silently skipped.

    Requirements: 42-REQ-4.1, 42-REQ-4.E1
    """
    if task_group <= 1:
        return []

    # Build list of prior group identifiers (as strings, matching DB format)
    prior_groups = [str(g) for g in range(1, task_group)]
    placeholders = ", ".join("?" for _ in prior_groups)

    findings: list[PriorFinding] = []

    def _query_findings_table(
        table: str,
        finding_type: str,
        columns: str = "CAST(id AS VARCHAR), severity, description, task_group, CAST(created_at AS VARCHAR)",
        *,
        row_mapper: Callable[[tuple], PriorFinding] | None = None,
    ) -> None:
        """Query a findings table and append results to the findings list."""
        try:
            rows = conn.execute(
                f"SELECT {columns} FROM {table} "
                f"WHERE spec_name = ? AND task_group IN ({placeholders}) "
                f"AND superseded_by IS NULL",
                [spec_name, *prior_groups],
            ).fetchall()
            for row in rows:
                if row_mapper is not None:
                    findings.append(row_mapper(row))
                else:
                    findings.append(
                        PriorFinding(
                            type=finding_type,
                            group=str(row[3]),
                            severity=str(row[1]),
                            description=str(row[2]),
                            created_at=str(row[4]) if row[4] is not None else "",
                        )
                    )
        except Exception:
            logger.debug(
                "Failed to query %s for prior groups (table may not exist)",
                table,
            )

    _query_findings_table("review_findings", "review")
    _query_findings_table("drift_findings", "drift")

    return findings


def render_prior_group_findings(findings: list[PriorFinding]) -> str:
    """Render prior group findings as a markdown section.

    Findings are rendered under a "Prior Group Findings" header with each
    finding prefixed by its group number and type label. Findings are
    sorted by created_at ascending.

    Returns empty string when findings list is empty (causes section omission).

    Requirements: 42-REQ-4.2, 42-REQ-4.3, 42-REQ-4.E2
    """
    if not findings:
        return ""

    # Sort by created_at ascending (42-REQ-4.3)
    sorted_findings = sorted(findings, key=lambda f: f.created_at)

    lines = ["## Prior Group Findings", ""]

    for f in sorted_findings:
        group_label = f"[group {f.group}]"
        type_label = f"[{f.type}]"
        sev_label = f"[{f.severity}]"
        safe_desc = sanitize_prompt_content(f.description, label="prior-finding")
        lines.append(f"- {group_label} {type_label} {sev_label} {safe_desc}")

    return "\n".join(lines)
