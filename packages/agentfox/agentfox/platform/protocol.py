"""Platform protocol: abstract issue-tracking and PR operations.

Defines the interface for platform implementations (GitHub, GitLab, etc.).

Requirements: 61-REQ-8.1, 65-REQ-4.1, 86-REQ-1.5, 02-REQ-6.1, 02-REQ-6.2
"""

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass(frozen=True)
class IssueResult:
    """Structured result for issue operations.

    Requirements: 28-REQ-2.2
    """

    number: int
    title: str
    html_url: str
    body: str = ""
    labels: tuple[str, ...] = ()


@dataclass(frozen=True)
class IssueComment:
    """Structured result for issue comments.

    Requirements: 86-REQ-1.3
    """

    id: int
    body: str
    user: str  # login
    created_at: str  # ISO 8601


@runtime_checkable
class PlatformProtocol(Protocol):
    """Abstract forge operations for issue and PR management.

    Requirements: 61-REQ-8.1, 86-REQ-1.5
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

        Requirements: 86-REQ-1.1, 86-REQ-1.2
        """
        ...

    async def list_issue_comments(
        self,
        issue_number: int,
    ) -> list[IssueComment]:
        """List all comments on an issue in chronological order.

        Requirements: 86-REQ-1.3
        """
        ...

    async def get_issue(
        self,
        issue_number: int,
    ) -> IssueResult:
        """Fetch a single issue by number.

        Requirements: 86-REQ-1.4
        """
        ...

    async def update_issue(
        self,
        issue_number: int,
        body: str,
    ) -> None:
        """Update the body of an existing issue.

        Used to append markers (e.g. ``<!-- af:knowledge-ingested -->``)
        to issue bodies after they have been processed.
        """
        ...

    async def create_label(
        self,
        name: str,
        color: str,
        description: str = "",
    ) -> None:
        """Create a label on the repository, succeeding silently if it exists.

        Uses POST /repos/{owner}/{repo}/labels.  Treats a 422
        "already_exists" response as success so this method is safe to
        call on every ``af init`` run.

        Args:
            name: Label name (e.g. ``"af:fix"``).
            color: Six-character hex color without leading ``#``
                   (e.g. ``"12ec39"``).
            description: Optional human-readable description.

        Requirements: 358-REQ-1, 358-REQ-2
        """
        ...

    async def create_pr(
        self,
        *,
        title: str,
        body: str,
        head: str,
        base: str,
    ) -> str:
        """Create a pull request and return its ``html_url``.

        Raises:
            IntegrationError: On API failure (non-201 response from GitHub,
                excluding 422 "PR already exists" which is handled as success).

        Requirements: 02-REQ-6.1
        """
        ...

    async def close(self) -> None: ...


class NullPlatform:
    """No-op stub implementation of ``PlatformProtocol``.

    Used when no platform is configured.  All issue operations are no-ops;
    ``create_pr()`` raises ``NotImplementedError`` because PR creation
    requires a real platform — callers must check platform availability
    via ``create_platform_safe()`` before attempting PR creation.

    Requirements: 02-REQ-6.2, 02-REQ-6.E1
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
    ) -> str:
        """Always raises — PR creation requires a real platform.

        Raises:
            NotImplementedError: Always. Callers must check platform
                availability via ``create_platform_safe()`` before calling.

        Requirements: 02-REQ-6.2, 02-REQ-6.E1
        """
        raise NotImplementedError(
            "create_pr() called on NullPlatform — this should never be "
            "reached. Ensure platform availability is checked via "
            "create_platform_safe() before calling create_pr()"
        )

    async def close(self) -> None:
        """No-op."""
