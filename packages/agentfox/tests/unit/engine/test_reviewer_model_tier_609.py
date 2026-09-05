"""Unit tests for reviewer model tier defaults (issue #609).

Verifies the tier each reviewer mode resolves to. The config.toml no longer
forces reviewer to ADVANCED via the models dict.

Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-5
Test Spec: TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-5
"""

from __future__ import annotations

from agentfox.core.config import AgentFoxConfig, ArchetypesConfig
from agentfox.engine.sdk_params import resolve_model_tier


class TestReviewerModelTier609:
    """Reviewer model tier defaults for issue #609."""

    def test_audit_review_returns_advanced_bare_config(self) -> None:
        """TS-NS-2: audit-review mode returns ADVANCED with no config override (spec 15)."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer", mode="audit-review")
        assert result == "ADVANCED"

    def test_pre_flight_returns_advanced_bare_config(self) -> None:
        """TS-NS-3: pre-flight mode returns ADVANCED with no config override."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer", mode="pre-flight")
        assert result == "ADVANCED"

    def test_coder_returns_advanced_from_overrides(self) -> None:
        """TS-NS-5: coder returns ADVANCED when overrides.coder.model_tier = 'ADVANCED'."""
        from agentfox.core.config import PerArchetypeConfig

        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(overrides={"coder": PerArchetypeConfig(model_tier="ADVANCED")})
        )
        result = resolve_model_tier(config, "coder")
        assert result == "ADVANCED"

    def test_reviewer_default_no_mode_is_standard(self) -> None:
        """reviewer with no mode returns STANDARD (registry default)."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer")
        assert result == "STANDARD"
