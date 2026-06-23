"""af: CLI for the agentfox autonomous coding-agent orchestrator.

Requirements: 04-REQ-1.2 — BannerGroup and handle_agent_fox_errors removed;
error handling is now provided by AgentFoxGroup from agentfox.io.
"""

from __future__ import annotations

from agentfox import __version__

__all__ = ["__version__"]
