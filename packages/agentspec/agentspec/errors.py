"""Exception hierarchy for agentspec."""


class AgentSpecError(Exception):
    """Base exception for all agentspec errors."""


class ConfigError(AgentSpecError):
    """Configuration and authentication errors."""


class CampaignError(AgentSpecError):
    """Raised for campaign directory operation failures."""


class SessionError(AgentSpecError):
    """Raised for session state machine or persistence failures."""


class AgentError(AgentSpecError):
    """Error during agent communication or response parsing.

    Attributes:
        detail: Human-readable description of what went wrong.
        __cause__: The underlying exception, if any.
    """

    def __init__(self, detail: str) -> None:
        super().__init__(detail)
        self.detail = detail
