"""Formatting, scoring, and keyword-extraction helpers for knowledge retrieval.

Pure functions that transform knowledge-store records into prompt-ready
strings.  No database access — all data arrives via function arguments.

Extracted from ``fox_provider.py`` to separate retrieval logic (database
queries) from presentation logic (scoring, formatting, keyword extraction).

Requirements: 116-REQ-6.1, 120-REQ-3.1, 120-REQ-3.2,
              120-REQ-3.E1, 120-REQ-3.E2
"""

from __future__ import annotations

from typing import Any

# Severity ordering for sorting — lower value = higher priority.
_SEVERITY_RANK: dict[str, int] = {"critical": 0, "major": 1, "minor": 2, "observation": 3}


def _extract_keywords(task_description: str) -> frozenset[str]:
    """Extract lowercase words from *task_description* for relevance scoring.

    Returns an empty frozenset when *task_description* is blank, which
    causes ``_score_relevance`` to return 0 for every item and preserves
    the existing severity/description sort order (AC-3).
    """
    return frozenset(word.lower() for word in task_description.split() if word)


def _score_relevance(text: str, keywords: frozenset[str]) -> int:
    """Count how many *keywords* appear as substrings in *text* (case-insensitive).

    Returns 0 when *keywords* is empty so that an absent or blank
    ``task_description`` has no effect on ordering (AC-3).
    """
    if not keywords:
        return 0
    text_lower = text.lower()
    return sum(1 for kw in keywords if kw in text_lower)


def generate_archetype_summary(
    archetype: str,
    findings: list[Any] | None = None,
    verdicts: list[Any] | None = None,
) -> str:
    """Generate a summary string for reviewer or verifier sessions.

    For reviewer: counts findings by severity and includes descriptions of
    up to 3 top-severity findings.
    For verifier: counts pass/fail verdicts and lists the requirement IDs
    of all FAIL verdicts.

    Returns a non-empty string even when the input lists are empty
    (120-REQ-3.E1, 120-REQ-3.E2).

    Requirements: 120-REQ-3.1, 120-REQ-3.2, 120-REQ-3.E1, 120-REQ-3.E2
    """
    if archetype == "reviewer":
        if not findings:
            return "Reviewer session completed with no findings."
        severity_counts: dict[str, int] = {}
        for f in findings:
            sev = getattr(f, "severity", "unknown")
            severity_counts[sev] = severity_counts.get(sev, 0) + 1
        # Build count string ordered by severity rank
        count_parts: list[str] = []
        for sev in ["critical", "major", "minor", "observation"]:
            if sev in severity_counts:
                count_parts.append(f"{severity_counts[sev]} {sev}")
        count_str = ", ".join(count_parts) if count_parts else "0 findings"
        # Include up to 3 top-severity finding descriptions
        sorted_findings = sorted(
            findings,
            key=lambda f: _SEVERITY_RANK.get(getattr(f, "severity", ""), 99),
        )
        top_descriptions = [getattr(f, "description", "") for f in sorted_findings[:3]]
        desc_str = "; ".join(top_descriptions)
        return f"Reviewer session completed with {count_str}. Top findings: {desc_str}"

    if archetype == "verifier":
        if not verdicts:
            return "Verifier session completed with no verdicts."
        pass_count = sum(1 for v in verdicts if getattr(v, "verdict", "") == "PASS")
        fail_count = sum(1 for v in verdicts if getattr(v, "verdict", "") == "FAIL")
        fail_req_ids = [getattr(v, "requirement_id", "") for v in verdicts if getattr(v, "verdict", "") == "FAIL"]
        parts = [f"Verifier session completed with {pass_count} pass, {fail_count} fail."]
        if fail_req_ids:
            parts.append(f"Failed requirements: {', '.join(fail_req_ids)}")
        return " ".join(parts)

    return f"{archetype} session completed."


def format_finding_parts(finding: Any) -> str:
    """Format a finding's severity, category, and description into a text fragment.

    Returns a string like ``[critical] correctness: description text``.
    Used by the various ``_query_*`` methods to build prompt-ready lines.
    """
    parts = [f"[{finding.severity}]"]
    if finding.category:
        parts.append(f"{finding.category}:")
    parts.append(finding.description)
    return " ".join(parts)


def format_verdict_parts(verdict: Any) -> str:
    """Format a verdict's requirement ID and evidence into a text fragment.

    Returns a string like ``[FAIL] REQ-1.1 evidence text``.
    """
    parts = [f"[FAIL] {verdict.requirement_id}"]
    if verdict.evidence:
        parts.append(verdict.evidence)
    return " ".join(parts)


def sort_findings(
    findings: list[Any],
    keywords: frozenset[str],
) -> list[Any]:
    """Sort findings by severity, then relevance score, then description.

    Uses ``_SEVERITY_RANK`` for severity ordering (critical first) and
    ``_score_relevance`` for keyword-based relevance scoring within each
    severity tier.
    """
    return sorted(
        findings,
        key=lambda f: (
            _SEVERITY_RANK.get(f.severity, 99),
            -_score_relevance(f"{f.category or ''} {f.description}", keywords),
            f.description,
        ),
    )


def sort_verdicts(
    verdicts: list[Any],
    keywords: frozenset[str],
) -> list[Any]:
    """Sort verdicts by relevance score, then requirement ID.

    Higher keyword overlap ranks first; requirement ID is the stable
    alphabetical tiebreaker.
    """
    return sorted(
        verdicts,
        key=lambda v: (
            -_score_relevance(f"{v.requirement_id} {v.evidence or ''}", keywords),
            v.requirement_id,
        ),
    )
