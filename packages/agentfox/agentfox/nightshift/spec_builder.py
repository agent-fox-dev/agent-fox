"""In-memory spec builder and branch name utilities.

Requirements: 61-REQ-6.1, 61-REQ-6.2
"""

from __future__ import annotations

import re
from dataclasses import dataclass, field
from typing import TYPE_CHECKING

from agentfox.core.prompt_safety import sanitize_prompt_content
from agentfox.platform.protocol import IssueResult

if TYPE_CHECKING:
    from agentfox.nightshift.fix_pipeline import TriageResult


@dataclass(frozen=True)
class InMemorySpec:
    """Lightweight spec for the fix engine.

    Requirements: 61-REQ-6.1
    """

    issue_number: int
    title: str
    task_prompt: str
    system_context: str
    branch_name: str


@dataclass(frozen=True)
class AfspecContext:
    """Structured afspec context built from triage criteria.

    Produced by :func:`build_afspec_from_triage` and consumed by
    :func:`render_inmemory_spec_sections`.

    Requirements: 02-REQ-1.1, 02-REQ-2.1
    """

    requirements: list[str] = field(default_factory=list)
    test_specifications: list[str] = field(default_factory=list)
    tasks: list[str] = field(default_factory=list)


def build_afspec_from_triage(spec: InMemorySpec, triage: TriageResult) -> AfspecContext:
    """Build an afspec context from triage results.

    Constructs a structured specification from the triage criteria,
    suitable for rendering into markdown sections via
    :func:`render_inmemory_spec_sections`.

    Requirements: 02-REQ-1.1, 02-REQ-2.1
    """
    requirements: list[str] = []
    test_specs: list[str] = []
    tasks: list[str] = []

    for c in triage.criteria:
        requirements.append(f"- [{c.id}] {c.description}")
        test_specs.append(
            f"- [{c.id}] Preconditions: {c.preconditions}; "
            f"Expected: {c.expected}; Assertion: {c.assertion}"
        )
        tasks.append(f"- [ ] Verify {c.id}: {c.description}")

    return AfspecContext(
        requirements=requirements,
        test_specifications=test_specs,
        tasks=tasks,
    )


def render_inmemory_spec_sections(afspec: AfspecContext) -> str:
    """Render an afspec context into markdown sections.

    Returns a string containing ``## Requirements``,
    ``## Test Specification``, and ``## Tasks`` sections.

    Requirements: 02-REQ-1.1, 02-REQ-2.1
    """
    lines: list[str] = []
    lines.append("## Requirements")
    lines.extend(afspec.requirements)
    lines.append("")
    lines.append("## Test Specification")
    lines.extend(afspec.test_specifications)
    lines.append("")
    lines.append("## Tasks")
    lines.extend(afspec.tasks)
    lines.append("")
    return "\n".join(lines)


def sanitise_branch_name(title: str, issue_number: int | None = None) -> str:
    """Convert an issue title to a sanitised branch name.

    When ``issue_number`` is provided, returns ``fix/{N}-{slug}`` or
    ``fix/{N}`` when the sanitised slug is empty.  When ``issue_number``
    is ``None``, returns ``fix/{slug}`` (backward-compatible behaviour).

    Requirements: 61-REQ-6.2, 93-REQ-2.1, 93-REQ-2.2, 93-REQ-2.E1
    """
    slug = title.lower()
    # Replace spaces and underscores with hyphens
    slug = re.sub(r"[\s_]+", "-", slug)
    # Remove anything that isn't alphanumeric or hyphen
    slug = re.sub(r"[^a-z0-9-]", "", slug)
    # Collapse multiple hyphens
    slug = re.sub(r"-+", "-", slug)
    # Strip leading/trailing hyphens
    slug = slug.strip("-")

    if issue_number is not None:
        if slug:
            return f"fix/{issue_number}-{slug}"
        return f"fix/{issue_number}"
    return f"fix/{slug}"


def build_in_memory_spec(issue: IssueResult, issue_body: str) -> InMemorySpec:
    """Build a lightweight in-memory spec from a platform issue.

    Requirements: 61-REQ-6.1
    """
    branch = sanitise_branch_name(issue.title, issue.number)
    safe_title = sanitize_prompt_content(issue.title, label="issue-title")
    safe_body = sanitize_prompt_content(issue_body, label="issue-body")
    task_prompt = f"Fix the issue: {safe_title}\n\nIssue #{issue.number}\n\n{safe_body}"
    return InMemorySpec(
        issue_number=issue.number,
        title=issue.title,
        task_prompt=task_prompt,
        system_context=safe_body,
        branch_name=branch,
    )
