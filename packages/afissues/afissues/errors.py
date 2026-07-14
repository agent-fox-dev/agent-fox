"""Independent error hierarchy for afissues.

Defines AfIssuesError as the base exception for all platform/forge errors,
with ConfigError and IntegrationError as subclasses. This hierarchy is
intentionally independent — afissues is a standalone library with no
workspace package dependencies.

Requirements: 03-REQ-5.1, 03-REQ-5.2, 03-REQ-5.3, 03-REQ-5.4
"""

from __future__ import annotations


class AfIssuesError(Exception):
    """Base exception for all afissues errors.

    Stores arbitrary keyword arguments as structured context accessible
    via the ``context`` attribute.
    """

    def __init__(self, message: str = "", **context: object) -> None:
        super().__init__(message)
        self.context: dict[str, object] = dict(context)


class ConfigError(AfIssuesError):
    """Raised for configuration and validation errors (e.g. SSRF guard violations)."""


class IntegrationError(AfIssuesError):
    """Raised for platform API / integration errors.

    Attributes:
        retryable: Whether the operation that raised this error can be
            retried. Defaults to ``True``.
    """

    def __init__(self, message: str = "", *, retryable: bool = True, **context: object) -> None:
        super().__init__(message, **context)
        self.retryable = retryable
