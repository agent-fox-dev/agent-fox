"""Platform protocol: abstract issue-tracking and PR operations.

Defines the interface for platform implementations (GitHub, GitLab, etc.),
along with frozen dataclasses for issue results, comments, and PR-related
types, and a no-op ``NullPlatform`` stub.

Requirements: 03-REQ-2.1, 03-REQ-2.2, 03-REQ-2.3, 03-REQ-2.4,
              06-REQ-2.1, 06-REQ-2.2, 06-REQ-2.3, 06-REQ-2.4, 06-REQ-2.5,
              06-REQ-3.1, 06-REQ-3.2, 06-REQ-3.3, 06-REQ-3.4, 06-REQ-7.1
"""

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass(frozen=True)
class IssueResult:
    """Structured result for issue operations.

    Requirements: 03-REQ-2.2
    """

    number: int
    title: str
    html_url: str
    body: str = ""
    labels: tuple[str, ...] = ()


@dataclass(frozen=True)
class IssueComment:
    """Structured result for issue comments.

    Requirements: 03-REQ-2.3
    """

    id: int
    body: str
    user: str  # login
    created_at: str  # ISO 8601


@dataclass(frozen=True)
class PrResult:
    """Structured result for ``create_pr()`` operations.

    Requirements: 06-REQ-2.1
    """

    html_url: str
    number: int


@dataclass(frozen=True)
class PrState:
    """Current state of a pull request.

    Requirements: 06-REQ-2.2
    """

    number: int
    state: str
    merged: bool
    head_sha: str


@dataclass(frozen=True)
class CheckResult:
    """Single CI check-run result from a pull request.

    When the GitHub API returns ``output: null``, the mapping code should
    set ``output_title=""`` and ``output_summary=""``, keeping both fields
    as non-optional strings.

    Requirements: 06-REQ-2.3, 06-REQ-2.5
    """

    name: str
    status: str
    conclusion: str | None
    output_title: str
    output_summary: str


@dataclass(frozen=True)
class ReviewComment:
    """Single pull request review.

    ``submitted_at`` holds the raw ISO 8601 timestamp string as returned
    by the GitHub API — no parsing is performed.

    Requirements: 06-REQ-2.4
    """

    user: str
    state: str
    body: str
    submitted_at: str


@runtime_checkable
class PlatformProtocol(Protocol):
    """Abstract forge operations for issue and PR management.

    Requirements: 03-REQ-2.1
    """

    async def create_issue(
        self,
        title: str,
        body: str,
        labels: list[str] | None = None,
    ) -> IssueResult: ...

    async def list_issues_by_label(
        self,
        label: str,
        state: str = "open",
        *,
        sort: str = "created",
        direction: str = "asc",
    ) -> list[IssueResult]: ...

    async def add_issue_comment(
        self,
        issue_number: int,
        body: str,
    ) -> None: ...

    async def assign_label(
        self,
        issue_number: int,
        label: str,
    ) -> None: ...

    async def close_issue(
        self,
        issue_number: int,
        comment: str | None = None,
    ) -> None: ...

    async def remove_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """Remove a label from an issue.

        Succeeds silently if the label is not present (idempotent).
        """
        ...

    async def list_issue_comments(
        self,
        issue_number: int,
    ) -> list[IssueComment]:
        """List all comments on an issue in chronological order."""
        ...

    async def get_issue(
        self,
        issue_number: int,
    ) -> IssueResult:
        """Fetch a single issue by number."""
        ...

    async def update_issue(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Update the body of an existing issue."""
        ...

    async def create_label(
        self,
        name: str,
        color: str,
        description: str = "",
    ) -> None:
        """Create a label on the repository, succeeding silently if it exists."""
        ...

    async def create_pr(
        self,
        *,
        title: str,
        body: str,
        head: str,
        base: str,
    ) -> PrResult:
        """Create a pull request and return a structured ``PrResult``.

        Returns:
            PrResult with ``html_url`` and ``number`` populated.

        Raises ``IntegrationError`` on API failure.

        Requirements: 06-REQ-7.1
        """
        ...

    async def get_pr_state(self, pr_number: int) -> PrState:
        """Fetch the current state of a pull request.

        Requirements: 06-REQ-3.1
        """
        ...

    async def get_pr_checks(self, pr_number: int) -> list[CheckResult]:
        """Fetch CI check-run results for a pull request.

        Requirements: 06-REQ-3.2
        """
        ...

    async def get_pr_reviews(self, pr_number: int) -> list[ReviewComment]:
        """Fetch review comments for a pull request.

        Requirements: 06-REQ-3.3
        """
        ...

    async def close(self) -> None: ...


class NullPlatform:
    """No-op stub implementation of ``PlatformProtocol``.

    Used when no platform is configured.  All issue operations are no-ops;
    ``create_pr()`` raises ``NotImplementedError`` because PR creation
    requires a real platform — callers must check platform availability
    via ``create_platform_safe()`` before attempting PR creation.

    Requirements: 03-REQ-2.4, 03-REQ-2.E1
    """

    async def create_issue(
        self,
        title: str,
        body: str,
        labels: list[str] | None = None,
    ) -> IssueResult:
        """No-op: returns a dummy IssueResult."""
        return IssueResult(number=0, title=title, html_url="")

    async def list_issues_by_label(
        self,
        label: str,
        state: str = "open",
        *,
        sort: str = "created",
        direction: str = "asc",
    ) -> list[IssueResult]:
        """No-op: returns an empty list."""
        return []

    async def add_issue_comment(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """No-op."""

    async def assign_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """No-op."""

    async def close_issue(
        self,
        issue_number: int,
        comment: str | None = None,
    ) -> None:
        """No-op."""

    async def remove_label(
        self,
        issue_number: int,
        label: str,
    ) -> None:
        """No-op."""

    async def list_issue_comments(
        self,
        issue_number: int,
    ) -> list[IssueComment]:
        """No-op: returns an empty list."""
        return []

    async def get_issue(
        self,
        issue_number: int,
    ) -> IssueResult:
        """No-op: returns a dummy IssueResult."""
        return IssueResult(number=issue_number, title="", html_url="")

    async def update_issue(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """No-op."""

    async def create_label(
        self,
        name: str,
        color: str,
        description: str = "",
    ) -> None:
        """No-op."""

    async def create_pr(
        self,
        *,
        title: str,
        body: str,
        head: str,
        base: str,
    ) -> PrResult:
        """Always raises — PR creation requires a real platform.

        Raises:
            NotImplementedError: Always. Callers must check platform
                availability via ``create_platform_safe()`` before calling.

        Requirements: 03-REQ-2.E1
        """
        raise NotImplementedError(
            "create_pr() called on NullPlatform — this should never be "
            "reached. Ensure platform availability is checked via "
            "create_platform_safe() before calling create_pr()"
        )

    async def get_pr_state(self, pr_number: int) -> PrState:
        """Always raises — PR state queries require a real platform.

        Requirements: 06-REQ-3.4
        """
        raise NotImplementedError(
            "get_pr_state() called on NullPlatform — this should never be "
            "reached. Ensure platform availability is checked via "
            "create_platform_safe() before calling get_pr_state()"
        )

    async def get_pr_checks(self, pr_number: int) -> list[CheckResult]:
        """Always raises — PR check queries require a real platform.

        Requirements: 06-REQ-3.4
        """
        raise NotImplementedError(
            "get_pr_checks() called on NullPlatform — this should never be "
            "reached. Ensure platform availability is checked via "
            "create_platform_safe() before calling get_pr_checks()"
        )

    async def get_pr_reviews(self, pr_number: int) -> list[ReviewComment]:
        """Always raises — PR review queries require a real platform.

        Requirements: 06-REQ-3.4
        """
        raise NotImplementedError(
            "get_pr_reviews() called on NullPlatform — this should never be "
            "reached. Ensure platform availability is checked via "
            "create_platform_safe() before calling get_pr_reviews()"
        )

    async def close(self) -> None:
        """No-op."""
