"""agentspec — AI-powered spec creation library.

Re-exports key types from submodules for convenient access.
"""

from __future__ import annotations

from agentspec.campaign import Campaign, CampaignMetadata
from agentspec.config import AgentSpecConfig, load_config
from agentspec.errors import AgentError, AgentSpecError, CampaignError, ConfigError, SessionError
from agentspec.session import (
    Assessment,
    GenerateResult,
    Question,
    RepairSuggestion,
    SessionState,
    SpecSession,
    ValidationResult,
)

__all__ = [
    "AgentError",
    "Campaign",
    "AgentSpecConfig",
    "AgentSpecError",
    "Assessment",
    "CampaignError",
    "CampaignMetadata",
    "ConfigError",
    "GenerateResult",
    "Question",
    "RepairSuggestion",
    "SessionError",
    "SessionState",
    "SpecSession",
    "ValidationResult",
    "load_config",
]
