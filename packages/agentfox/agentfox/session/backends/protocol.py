"""Backend Protocol defining the contract for agent backend adapters.

Provides the ``Backend`` runtime-checkable Protocol that any backend adapter
must satisfy.  Concrete implementations (e.g. ``ClaudeBackend``) satisfy
this Protocol structurally without explicit inheritance.

Requirements: 02-REQ-1.1, 02-REQ-1.2, 02-REQ-1.3, 02-REQ-1.4, 02-REQ-1.5
"""

from __future__ import annotations

from collections.abc import AsyncIterator
from typing import Any, Protocol, runtime_checkable

from agentfox.session.backends.types import AgentMessage, PermissionCallback
from agentfox.ui.progress import ActivityCallback


@runtime_checkable
class Backend(Protocol):
    """Protocol defining the contract any backend adapter must satisfy.

    Members:

    - ``name`` -- read-only property returning the backend identifier string
      (e.g. ``'claude'``, ``'deepagents'``, ``'google-adk'``).
    - ``execute()`` -- async method that drives an agent session and yields
      canonical ``AgentMessage`` objects.
    - ``close()`` -- async teardown; must be idempotent.
    """

    @property
    def name(self) -> str:
        """Return backend identifier used for logging and telemetry."""
        ...

    async def execute(
        self,
        prompt: str,
        *,
        system_prompt: str,
        model: str,
        cwd: str,
        permission_callback: PermissionCallback | None = None,
        activity_callback: ActivityCallback | None = None,
        tool_error_callback: Any | None = None,
        node_id: str = "",
        archetype: str | None = None,
        max_turns: int | None = None,
        max_budget_usd: float | None = None,
        thinking: dict[str, Any] | None = None,
        effort: str | None = None,
        compaction: bool = False,
        cache_policy: str = "NONE",
    ) -> AsyncIterator[AgentMessage]:
        """Execute a session and yield canonical messages.

        Parameters match ``ClaudeBackend.execute()`` exactly so that all
        concrete backends satisfy this Protocol structurally.

        The ``cache_policy`` parameter conveys the orchestrator's caching
        preference (``"NONE"``, ``"DEFAULT"``, ``"EXTENDED"``).  Backends
        that manage their own API calls can use this to apply
        ``cache_control`` markers.  Backends wrapping an external SDK
        (e.g. Claude CLI subprocess) log the policy for observability
        but rely on the SDK's internal caching.
        """
        ...

    async def close(self) -> None:
        """Release resources.  Must be idempotent."""
        ...
