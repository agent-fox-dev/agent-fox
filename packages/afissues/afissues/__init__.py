"""afissues — standalone platform/forge abstraction layer for agent-fox.

Re-exports all public symbols so consumers can import from the top-level
``afissues`` namespace without knowing which sub-module each symbol lives in.

Requirements: 03-REQ-6.1
"""

from afissues.github import GitHubPlatform, parse_github_remote
from afissues.labels import (
    LABEL_FIX,
    LABEL_FIXED,
    LABEL_IMPLEMENTED,
    LABEL_NO_CHANGE,
    LABEL_PRIORITY_HIGH,
    LABEL_PRIORITY_LOW,
    LABEL_PRIORITY_MEDIUM,
    REQUIRED_LABELS,
    LabelSpec,
)
from afissues.protocol import (
    IssueComment,
    IssueResult,
    NullPlatform,
    PlatformProtocol,
)

__all__ = [
    # afissues.protocol
    "PlatformProtocol",
    "NullPlatform",
    "IssueResult",
    "IssueComment",
    # afissues.github
    "GitHubPlatform",
    "parse_github_remote",
    # afissues.labels
    "LabelSpec",
    "LABEL_FIX",
    "LABEL_FIXED",
    "LABEL_NO_CHANGE",
    "LABEL_IMPLEMENTED",
    "LABEL_PRIORITY_HIGH",
    "LABEL_PRIORITY_MEDIUM",
    "LABEL_PRIORITY_LOW",
    "REQUIRED_LABELS",
]
