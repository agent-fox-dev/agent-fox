"""Agent backend and canonical message types.

Provides the ``Backend`` Protocol, ``create_backend()`` factory, the
``ClaudeBackend`` adapter, and canonical message types used throughout
the session layer.

``ClaudeBackend`` is lazily imported on first access so that importing
this package does not pull in SDK dependencies.

Requirements: 26-REQ-1.1, 26-REQ-2.1, 02-REQ-2.1, 02-REQ-2.3, 02-REQ-5.1
"""

from agentfox.session.backends.protocol import Backend
from agentfox.session.backends.types import (
    AgentMessage,
    AssistantMessage,
    PermissionCallback,
    ResultMessage,
    ToolUseMessage,
)

_VALID_BACKENDS = ["claude", "deepagents", "google"]


def create_backend(name: str) -> Backend:
    """Create a backend instance by name using lazy imports.

    Args:
        name: Backend identifier (e.g. ``'claude'``).  The user-facing
            name ``'google'`` is mapped internally to ``GoogleADKBackend``.

    Returns:
        A ``Backend`` instance.

    Raises:
        ConfigError: If *name* is not a recognised backend, or if the
            required SDK is not installed.

    Requirements: 02-REQ-2.1, 02-REQ-2.2, 02-REQ-2.3, 02-REQ-2.4,
                  02-REQ-2.5, 02-REQ-2.6
    """
    from agentfox.core.errors import ConfigError

    if name == "claude":
        try:
            from agentfox.session.backends.claude import (
                ClaudeBackend as _Claude,
            )
        except ImportError:
            raise ConfigError(
                'Backend "claude" requires claude-agent-sdk. Install it with: pip install claude-agent-sdk'
            )
        return _Claude()

    if name == "deepagents":
        from agentfox.session.backends.deepagents import (
            DeepAgentsBackend as _DeepAgents,
        )

        return _DeepAgents()

    if name in ("google", "google-adk"):
        from agentfox.session.backends.google_adk import (
            GoogleADKBackend as _GoogleADK,
        )

        return _GoogleADK()

    raise ConfigError(f"Unknown backend: '{name}'. Valid backends are: {_VALID_BACKENDS}")


def __getattr__(name: str) -> object:
    """Lazily import ``ClaudeBackend`` to avoid eager SDK loading.

    Requirements: 02-REQ-2.3
    """
    if name == "ClaudeBackend":
        from agentfox.session.backends.claude import ClaudeBackend

        return ClaudeBackend
    if name == "DeepAgentsBackend":
        from agentfox.session.backends.deepagents import DeepAgentsBackend

        return DeepAgentsBackend
    if name == "GoogleADKBackend":
        from agentfox.session.backends.google_adk import GoogleADKBackend

        return GoogleADKBackend
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


__all__ = [
    "AgentMessage",
    "AssistantMessage",
    "Backend",
    "ClaudeBackend",
    "DeepAgentsBackend",
    "GoogleADKBackend",
    "PermissionCallback",
    "ResultMessage",
    "ToolUseMessage",
    "create_backend",
]
