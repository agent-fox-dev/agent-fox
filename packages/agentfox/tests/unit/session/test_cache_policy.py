"""Tests for cache policy threading through the session stack.

Verifies that cache_policy flows from config through resolve_session_params(),
run_session(), and Backend.execute(), and that cache metrics are logged.

Requirements: issue #753
"""

from __future__ import annotations

import logging
from collections.abc import AsyncIterator
from typing import Any
from unittest.mock import patch

import pytest
from afaudit.sink import SessionOutcome

from agentfox.core.config import AgentFoxConfig, CachePolicy, CachingConfig
from agentfox.engine.sdk_params import resolve_session_params
from agentfox.session.backends.types import (
    AgentMessage,
    PermissionCallback,
    ResultMessage,
)
from agentfox.session.session import _log_cache_metrics, run_session
from agentfox.ui.progress import ActivityCallback
from agentfox.workspace import WorkspaceInfo


# -- Helpers ------------------------------------------------------------------


class CaptureBackend:
    """Backend that captures the cache_policy kwarg it receives."""

    def __init__(self, result: ResultMessage | None = None) -> None:
        self.received_cache_policy: str | None = None
        self._result = result or ResultMessage(
            status="completed",
            input_tokens=100,
            output_tokens=200,
            duration_ms=5000,
            error_message=None,
            is_error=False,
        )

    @property
    def name(self) -> str:
        return "capture"

    async def execute(
        self,
        prompt: str,
        *,
        system_prompt: str,
        model: str,
        cwd: str,
        permission_callback: PermissionCallback | None = None,
        activity_callback: ActivityCallback | None = None,
        node_id: str = "",
        archetype: str | None = None,
        max_turns: int | None = None,
        max_budget_usd: float | None = None,
        thinking: dict | None = None,
        effort: str | None = None,
        compaction: bool = False,
        cache_policy: str = "NONE",
        **kwargs: Any,
    ) -> AsyncIterator[AgentMessage]:
        self.received_cache_policy = cache_policy
        yield self._result

    async def close(self) -> None:
        pass


def _workspace() -> WorkspaceInfo:
    return WorkspaceInfo(
        path="/tmp/test-workspace",
        branch="feature/test",
        spec_name="test_spec",
        task_group=1,
    )


# -- Tests: ResolvedSessionParams includes cache_policy -----------------------


class TestResolvedSessionParamsCachePolicy:
    """cache_policy is populated in ResolvedSessionParams."""

    def test_default_cache_policy(self) -> None:
        config = AgentFoxConfig()
        params = resolve_session_params(config, "coder")
        assert params.cache_policy == "DEFAULT"

    def test_none_cache_policy(self) -> None:
        config = AgentFoxConfig(
            caching=CachingConfig(cache_policy=CachePolicy.NONE),
        )
        params = resolve_session_params(config, "coder")
        assert params.cache_policy == "NONE"

    def test_extended_cache_policy(self) -> None:
        config = AgentFoxConfig(
            caching=CachingConfig(cache_policy=CachePolicy.EXTENDED),
        )
        params = resolve_session_params(config, "coder")
        assert params.cache_policy == "EXTENDED"

    def test_cache_policy_independent_of_archetype(self) -> None:
        config = AgentFoxConfig(
            caching=CachingConfig(cache_policy=CachePolicy.EXTENDED),
        )
        for archetype in ("coder", "reviewer", "verifier"):
            params = resolve_session_params(config, archetype)
            assert params.cache_policy == "EXTENDED"


# -- Tests: run_session passes cache_policy to backend -----------------------


class TestRunSessionCachePolicyFlow:
    """run_session() threads cache_policy through to the backend."""

    @pytest.mark.asyncio
    async def test_cache_policy_reaches_backend(self) -> None:
        backend = CaptureBackend()
        config = AgentFoxConfig()

        with patch("agentfox.session.session.resolve_model", return_value="claude-sonnet-4-6"):
            await run_session(
                workspace=_workspace(),
                node_id="test:1:coder",
                system_prompt="test system",
                task_prompt="test task",
                config=config,
                backend=backend,
                cache_policy="EXTENDED",
            )

        assert backend.received_cache_policy == "EXTENDED"

    @pytest.mark.asyncio
    async def test_cache_policy_defaults_to_none(self) -> None:
        backend = CaptureBackend()
        config = AgentFoxConfig()

        with patch("agentfox.session.session.resolve_model", return_value="claude-sonnet-4-6"):
            await run_session(
                workspace=_workspace(),
                node_id="test:1:coder",
                system_prompt="test system",
                task_prompt="test task",
                config=config,
                backend=backend,
            )

        assert backend.received_cache_policy == "NONE"


# -- Tests: cache metrics logging --------------------------------------------


class TestCacheMetricsLogging:
    """_log_cache_metrics() logs cache performance correctly."""

    def test_logs_cache_hit_metrics(self, caplog: pytest.LogCaptureFixture) -> None:
        outcome = SessionOutcome(
            spec_name="test",
            task_group="1",
            node_id="test:1:coder",
            status="completed",
            input_tokens=5000,
            output_tokens=2000,
            cache_read_input_tokens=8000,
            cache_creation_input_tokens=2000,
            duration_ms=10000,
            error_message=None,
            response="",
        )

        with caplog.at_level(logging.INFO):
            _log_cache_metrics(outcome, "EXTENDED")

        assert "cache_read=8000" in caplog.text
        assert "cache_creation=2000" in caplog.text
        assert "EXTENDED" in caplog.text

    def test_logs_no_cache_activity(self, caplog: pytest.LogCaptureFixture) -> None:
        outcome = SessionOutcome(
            spec_name="test",
            task_group="1",
            node_id="test:1:coder",
            status="completed",
            input_tokens=10000,
            output_tokens=2000,
            cache_read_input_tokens=0,
            cache_creation_input_tokens=0,
            duration_ms=10000,
            error_message=None,
            response="",
        )

        with caplog.at_level(logging.INFO):
            _log_cache_metrics(outcome, "DEFAULT")

        assert "no cache activity" in caplog.text

    def test_skips_logging_when_no_tokens(self, caplog: pytest.LogCaptureFixture) -> None:
        outcome = SessionOutcome(
            spec_name="test",
            task_group="1",
            node_id="test:1:coder",
            status="failed",
            input_tokens=0,
            output_tokens=0,
            cache_read_input_tokens=0,
            cache_creation_input_tokens=0,
            duration_ms=0,
            error_message="Transport error",
            response="",
        )

        with caplog.at_level(logging.INFO):
            _log_cache_metrics(outcome, "NONE")

        assert "cache" not in caplog.text.lower()

    def test_hit_percentage_calculation(self, caplog: pytest.LogCaptureFixture) -> None:
        outcome = SessionOutcome(
            spec_name="test",
            task_group="1",
            node_id="test:1:coder",
            status="completed",
            input_tokens=2000,
            output_tokens=1000,
            cache_read_input_tokens=8000,
            cache_creation_input_tokens=0,
            duration_ms=5000,
            error_message=None,
            response="",
        )

        with caplog.at_level(logging.INFO):
            _log_cache_metrics(outcome, "EXTENDED")

        # total_input = 2000 + 8000 + 0 = 10000, cache_read = 8000 -> 80.0%
        assert "80.0%" in caplog.text
