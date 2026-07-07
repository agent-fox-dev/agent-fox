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

_VALID_BACKENDS = ["claude"]


def create_backend(name: str) -> Backend:
    """Create a backend instance by name using lazy imports.

    Args:
        name: Backend identifier (e.g. ``'claude'``).

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
                'Backend "claude" requires claude-agent-sdk. '
                "Install it with: pip install claude-agent-sdk"
            )
        return _Claude()

    raise ConfigError(
        f"Unknown backend: '{name}'. Valid backends are: {_VALID_BACKENDS}"
    )


def __getattr__(name: str) -> object:
    """Lazily import ``ClaudeBackend`` to avoid eager SDK loading.

    Requirements: 02-REQ-2.3
    """
    if name == "ClaudeBackend":
        from agentfox.session.backends.claude import ClaudeBackend

        return ClaudeBackend
    raise AttributeError(f"module {__name__!r} has no attribute {name!r}")


__all__ = [
    "AgentMessage",
    "AssistantMessage",
    "Backend",
    "ClaudeBackend",
    "PermissionCallback",
    "ResultMessage",
    "ToolUseMessage",
    "create_backend",
]
