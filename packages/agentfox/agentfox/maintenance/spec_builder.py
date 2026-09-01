"""In-memory spec builder and branch name utilities.

Requirements: 61-REQ-6.1, 61-REQ-6.2, 01-REQ-1.1 through 01-REQ-7.2
"""

import re
from dataclasses import dataclass
from typing import TYPE_CHECKING

from afspec.models import (
    Criterion,
    PRDDocument,
    PRDFrontmatter,
    Requirement,
    Requirements,
    Spec,
    Subtask,
    SubtaskState,
    TaskGroup,
    TaskGroupKind,
    Tasks,
    TestCase,
    TestSpec,
    UserStory,
)

from agentfox.core.prompt_safety import sanitize_prompt_content
from afissues.protocol import IssueResult

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


def build_afspec_from_triage(
    triage_result: "TriageResult",
    issue_number: int,
) -> Spec:
    """Build an afspec Spec from triage results.

    Converts a TriageResult into a fully populated in-memory afspec Spec
    with Requirements, TestSpec, Tasks, and PRDDocument — no file I/O.

    Requirements: 01-REQ-1.1, 01-REQ-1.2, 01-REQ-1.3,
                  01-REQ-2.1 through 01-REQ-2.5,
                  01-REQ-3.1 through 01-REQ-3.5,
                  01-REQ-4.1, 01-REQ-4.2,
                  01-REQ-5.1, 01-REQ-5.2,
                  01-REQ-7.1, 01-REQ-7.2
    """
    criteria = triage_result.criteria

    # --- Requirements mapping (01-REQ-2) ---
    requirements: list[Requirement] = []
    for n, c in enumerate(criteria, start=1):
        desc = c.description if c.description else ""
        preconds = c.preconditions if c.preconditions else ""
        expected = c.expected if c.expected else ""
        assertion = c.assertion if c.assertion else ""

        ac_criterion = Criterion(
            id=f"NS-REQ-{n}.1",
            condition=preconds,
            action=expected,
        )

        requirements.append(
            Requirement(
                id=f"NS-REQ-{n}",
                title=desc,
                user_story=UserStory(goal=desc),
                acceptance_criteria=[ac_criterion],
                edge_cases=[],
            )
        )

    # --- TestCase derivation (01-REQ-3) ---
    test_cases: list[TestCase] = []
    for n, c in enumerate(criteria, start=1):
        desc = c.description if c.description else ""
        preconds = c.preconditions if c.preconditions else ""
        expected = c.expected if c.expected else ""
        assertion = c.assertion if c.assertion else ""

        # Convert preconditions string to list
        preconditions_list: list[str] = [preconds] if preconds else []

        test_cases.append(
            TestCase(
                id=f"TS-NS-{n}",
                requirement_id=f"NS-REQ-{n}",
                description=desc,
                preconditions=preconditions_list,
                expected=expected,
                assertion_pseudocode=assertion,
                input="",
                kind="acceptance",
            )
        )

    # --- TaskGroup and Subtask construction (01-REQ-4) ---
    subtasks: list[Subtask] = []
    if criteria:
        for n, c in enumerate(criteria, start=1):
            desc = c.description if c.description else ""
            preconds = c.preconditions if c.preconditions else ""
            expected = c.expected if c.expected else ""
            assertion = c.assertion if c.assertion else ""

            details: list[str] = []
            if preconds:
                details.append(preconds)
            if expected:
                details.append(expected)
            if assertion:
                details.append(assertion)

            subtasks.append(
                Subtask(
                    id=f"1.{n}",
                    title=desc,
                    details=details,
                    state=SubtaskState.PENDING,
                    test_spec_refs=[f"TS-NS-{n}"],
                    requirement_refs=[f"NS-REQ-{n}"],
                )
            )
    else:
        # 01-REQ-1.E1, 01-REQ-4.E1: fallback Subtask
        subtasks.append(
            Subtask(
                id="1.1",
                title="Fix the issue",
                details=[],
                state=SubtaskState.PENDING,
                test_spec_refs=[],
                requirement_refs=[],
            )
        )

    task_group = TaskGroup(
        id=1,
        kind=TaskGroupKind.TESTS,
        title=f"Fix issue #{issue_number}",
        subtasks=subtasks,
    )

    # --- PRDDocument construction (01-REQ-5) ---
    issue_body = triage_result.issue_body if hasattr(triage_result, "issue_body") else ""
    frontmatter = PRDFrontmatter(
        spec_id=f"fix-{issue_number}",
        spec_name=f"fix_issue_{issue_number}",
    )
    prd = PRDDocument(
        frontmatter=frontmatter,
        body=issue_body if issue_body else "",
    )

    # --- Assemble final Spec (01-REQ-1.1) ---
    return Spec(
        requirements=Requirements(requirements=requirements),
        test_spec=TestSpec(test_cases=test_cases),
        tasks=Tasks(task_groups=[task_group]),
        prd=prd,
    )


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
    task_prompt = (
        f"Fix the issue: {safe_title} (#{issue.number})\n\n"
        "Refer to the issue description and acceptance criteria in the context above."
    )
    return InMemorySpec(
        issue_number=issue.number,
        title=issue.title,
        task_prompt=task_prompt,
        system_context=safe_body,
        branch_name=branch,
    )
