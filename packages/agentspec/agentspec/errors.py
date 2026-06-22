"""Exception hierarchy for agentspec."""

from __future__ import annotations


class AgentSpecError(Exception):
    """Base exception for all agentspec errors."""

    @property
    def category(self) -> str:
        return "internal"


class ConfigError(AgentSpecError):
    """Configuration and authentication errors."""

    @property
    def category(self) -> str:
        return "config"


class CampaignError(AgentSpecError):
    """Raised for campaign directory operation failures."""


class SessionError(AgentSpecError):
    """Raised for session state machine or persistence failures."""

    @property
    def category(self) -> str:
        return "state"


class AgentError(AgentSpecError):
    """Error during agent communication or response parsing.

    Attributes:
        detail: Human-readable description of what went wrong.
        category: Error classification for programmatic handling.
        retryable: Whether the operation may succeed on retry.
        http_status: HTTP status code from the API, if applicable.
        __cause__: The underlying exception, if any.
    """

    def __init__(
        self,
        detail: str,
        *,
        category: str = "internal",
        retryable: bool = False,
        http_status: int | None = None,
    ) -> None:
        super().__init__(detail)
        self.detail = detail
        self._category = category
        self.retryable = retryable
        self.http_status = http_status

    @property
    def category(self) -> str:
        return self._category
