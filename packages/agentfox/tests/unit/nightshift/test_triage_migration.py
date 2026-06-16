"""Unit tests for nightshift triage migration to maintainer:hunt archetype.

Verifies that run_batch_triage uses maintainer:hunt for model tier and
security config resolution, and handles legacy config keys correctly.

Test Spec: TS-100-6, TS-100-11, TS-100-E2
Requirements: 100-REQ-2.2, 100-REQ-5.1, 100-REQ-5.2, 100-REQ-2.E1
"""

from __future__ import annotations

from unittest.mock import MagicMock, patch

import pytest
from agentfox.nightshift.dep_graph import DependencyEdge
from agentfox.platform.protocol import IssueResult

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_issue(number: int = 1) -> IssueResult:
    return IssueResult(
        number=number,
        title=f"Fix issue #{number}",
        html_url=f"https://github.com/test/repo/issues/{number}",
    )


# ===========================================================================
# TS-100-6: Triage Uses Maintainer Hunt
# Requirements: 100-REQ-2.2, 100-REQ-5.3
# ===========================================================================


class TestTriageUsesMaintainerHunt:
    """Verify run_batch_triage resolves model tier from maintainer:hunt."""

    @pytest.mark.asyncio
    async def test_resolve_model_tier_called_with_maintainer_hunt(self) -> None:
        """TS-100-6: run_batch_triage must call resolve_model_tier('maintainer', mode='hunt')."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.nightshift.triage import run_batch_triage

        config = AgentFoxConfig()
        issues = [_make_issue(1), _make_issue(2)]
        edges: list[DependencyEdge] = []

        # Mock AI call to avoid network I/O
        triage_response = '{"processing_order": [1, 2], "dependencies": [], "supersession": []}'

        with (
            patch("agentfox.nightshift.triage.resolve_model_tier") as mock_tier,
            patch("agentfox.nightshift.cost_helpers.nightshift_ai_call") as mock_ai,
        ):
            mock_tier.return_value = "STANDARD"
            mock_ai.return_value = (triage_response, MagicMock())

            await run_batch_triage(issues, edges, config)

        # Verify resolve_model_tier was called with maintainer:hunt
        assert mock_tier.called, "run_batch_triage must call resolve_model_tier (100-REQ-5.1)"
        call_args = mock_tier.call_args
        assert call_args is not None
        # First positional arg after config should be "maintainer"
        pos_args = call_args.args
        kwargs = call_args.kwargs
        archetype_arg = pos_args[1] if len(pos_args) > 1 else kwargs.get("archetype")
        mode_kwarg = kwargs.get("mode")
        assert archetype_arg == "maintainer", (
            f"resolve_model_tier should be called with archetype='maintainer', got {archetype_arg!r} (100-REQ-2.2)"
        )
        assert mode_kwarg == "hunt", (
            f"resolve_model_tier should be called with mode='hunt', got {mode_kwarg!r} (100-REQ-2.2)"
        )

    @pytest.mark.asyncio
    async def test_resolve_security_config_called_with_maintainer_hunt(self) -> None:
        """TS-100-6: run_batch_triage must call resolve_security_config('maintainer', mode='hunt').

        Requirement: 100-REQ-5.2
        """
        from agentfox.core.config import AgentFoxConfig
        from agentfox.nightshift.triage import run_batch_triage

        config = AgentFoxConfig()
        issues = [_make_issue(1)]
        edges: list[DependencyEdge] = []

        triage_response = '{"processing_order": [1], "dependencies": [], "supersession": []}'

        with (
            patch("agentfox.nightshift.triage.resolve_security_config") as mock_sec,
            patch("agentfox.nightshift.triage.resolve_model_tier", return_value="STANDARD"),
            patch("agentfox.nightshift.cost_helpers.nightshift_ai_call") as mock_ai,
        ):
            mock_sec.return_value = MagicMock()
            mock_ai.return_value = (triage_response, MagicMock())

            await run_batch_triage(issues, edges, config)

        assert mock_sec.called, "run_batch_triage must call resolve_security_config (100-REQ-5.2)"
        call_args = mock_sec.call_args
        assert call_args is not None
        pos_args = call_args.args
        kwargs = call_args.kwargs
        archetype_arg = pos_args[1] if len(pos_args) > 1 else kwargs.get("archetype")
        mode_kwarg = kwargs.get("mode")
        assert archetype_arg == "maintainer", (
            f"resolve_security_config should be called with archetype='maintainer', got {archetype_arg!r} (100-REQ-5.2)"
        )
        assert mode_kwarg == "hunt", (
            f"resolve_security_config should be called with mode='hunt', got {mode_kwarg!r} (100-REQ-5.2)"
        )


# ===========================================================================
# TS-100-11: Nightshift Model Tier Resolution
# Requirements: 100-REQ-5.1, 100-REQ-5.2
# ===========================================================================


class TestNightshiftModelTierResolution:
    """Verify nightshift resolves STANDARD tier for maintainer:hunt."""

    def test_resolve_model_tier_returns_standard(self) -> None:
        """TS-100-11: resolve_model_tier(config, 'maintainer', mode='hunt') returns 'STANDARD'."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.sdk_params import resolve_model_tier

        config = AgentFoxConfig()
        tier = resolve_model_tier(config, "maintainer", mode="hunt")
        assert tier == "STANDARD", f"Expected STANDARD tier for maintainer:hunt, got {tier!r} (100-REQ-5.1)"

    def test_resolve_security_config_returns_hunt_allowlist(self) -> None:
        """TS-100-11: resolve_security_config for maintainer:hunt returns hunt allowlist."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.sdk_params import resolve_security_config

        config = AgentFoxConfig()
        sec = resolve_security_config(config, "maintainer", mode="hunt")
        assert sec is not None, "resolve_security_config should return SecurityConfig for maintainer:hunt (100-REQ-5.2)"
        expected_allowlist = {"ls", "cat", "git", "wc", "head", "tail"}
        actual_allowlist = set(sec.bash_allowlist or [])
        assert actual_allowlist == expected_allowlist, (
            f"Hunt mode allowlist mismatch: expected {expected_allowlist}, got {actual_allowlist} (100-REQ-5.2)"
        )

    def test_resolve_model_tier_extraction_returns_standard(self) -> None:
        """TS-100-11: resolve_model_tier for maintainer:extraction also returns STANDARD."""
        from agentfox.core.config import AgentFoxConfig
        from agentfox.engine.sdk_params import resolve_model_tier

        config = AgentFoxConfig()
        tier = resolve_model_tier(config, "maintainer", mode="extraction")
        assert tier == "STANDARD", f"Expected STANDARD tier for maintainer:extraction, got {tier!r}"


