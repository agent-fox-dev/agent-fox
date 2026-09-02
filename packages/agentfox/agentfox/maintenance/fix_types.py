"""Data types for triage/review workflow and PR body builder.

These types were extracted from fix_pipeline.py during nightshift removal.
They remain in use by session/review_parser.py and engine/session_lifecycle.py.
"""

from __future__ import annotations

from dataclasses import dataclass, field


@dataclass(frozen=True)
class AcceptanceCriterion:
    """A single acceptance criterion from the triage agent."""

    id: str
    description: str
    preconditions: str
    expected: str
    assertion: str


@dataclass(frozen=True)
class AssessedComplexity:
    """Complexity assessment embedded in triage output.

    Frozen dataclass with tier, variant, confidence, and rationale fields.

    Requirement: 15-REQ-11.1
    """

    tier: str
    variant: str | None
    confidence: float
    rationale: str


@dataclass(frozen=True)
class TriageResult:
    """Parsed triage output."""

    summary: str = ""
    affected_files: list[str] = field(default_factory=list)
    criteria: list[AcceptanceCriterion] = field(default_factory=list)
    issue_body: str = ""
    assessed_complexity: AssessedComplexity | None = None


@dataclass(frozen=True)
class FixReviewVerdict:
    """A single per-criterion verdict from the fix reviewer."""

    criterion_id: str
    verdict: str
    evidence: str


@dataclass(frozen=True)
class FixReviewResult:
    """Parsed fix reviewer output."""

    verdicts: list[FixReviewVerdict] = field(default_factory=list)
    overall_verdict: str = "FAIL"
    summary: str = ""
    is_parse_failure: bool = False


def build_pr_body(
    *,
    spec_name: str | None = None,
    task_group_id: str | None = None,
    task_group_title: str | None = None,
    changed_files: list[str],
    issue_number: int | None = None,
    issue_title: str | None = None,
) -> str:
    """Build a Markdown PR body for af code or nightshift fix sessions.

    Pure function with no side effects.  All parameters are keyword-only.

    Requirements: 02-REQ-5.1, 02-REQ-5.2, 02-REQ-5.3, 02-REQ-5.E1,
                  02-REQ-5.E2, 61-REQ-7.2
    """
    sections: list[str] = []

    if issue_number is not None and issue_title is not None and spec_name is None:
        sections.append(f"## Summary\n\nFix #{issue_number}: {issue_title}")
    elif spec_name is not None:
        sections.append(f"## Summary\n\n{spec_name}")
    else:
        sections.append("## Summary")

    if task_group_id is not None and task_group_title is not None and issue_number is None:
        sections.append(f"## Task Group\n\n{task_group_id}: {task_group_title}")

    if changed_files:
        file_list = "\n".join(f"- {f}" for f in changed_files)
        sections.append(f"## Changed Files\n\n{file_list}")
    else:
        sections.append("## Changed Files")

    body = "\n\n".join(sections) + "\n"

    if issue_number is not None and spec_name is None:
        body += f"\nFixes #{issue_number}\n"

    return body
