"""Unit tests for reviewer model tier defaults (issue #609).

Verifies that audit-review and drift-review sessions use STANDARD (Sonnet)
while fix-review retains ADVANCED (Opus). The config.toml no longer forces
reviewer to ADVANCED via the models dict.

Requirements: NS-REQ-1, NS-REQ-2, NS-REQ-3, NS-REQ-4, NS-REQ-5
Test Spec: TS-NS-1, TS-NS-2, TS-NS-3, TS-NS-4, TS-NS-5
"""

from __future__ import annotations

from agentfox.core.config import AgentFoxConfig, ArchetypesConfig
from agentfox.engine.sdk_params import resolve_model_tier


class TestReviewerModelTier609:
    """Reviewer model tier defaults for issue #609."""

    def test_audit_review_returns_standard_bare_config(self) -> None:
        """TS-NS-2: audit-review mode returns STANDARD with no config override."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer", mode="audit-review")
        assert result == "STANDARD"

    def test_drift_review_returns_standard_bare_config(self) -> None:
        """TS-NS-3: drift-review mode returns STANDARD with no config override."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer", mode="drift-review")
        assert result == "STANDARD"

    def test_fix_review_returns_advanced_from_registry(self) -> None:
        """TS-NS-4: fix-review mode returns ADVANCED from archetype registry ModeConfig."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer", mode="fix-review")
        assert result == "ADVANCED"

    def test_coder_returns_advanced_from_models_dict(self) -> None:
        """TS-NS-5: coder returns ADVANCED when models = {coder = 'ADVANCED'} is set."""
        config = AgentFoxConfig(archetypes=ArchetypesConfig(models={"coder": "ADVANCED"}))
        result = resolve_model_tier(config, "coder")
        assert result == "ADVANCED"

    def test_reviewer_default_no_mode_is_standard(self) -> None:
        """reviewer with no mode returns STANDARD (registry default)."""
        config = AgentFoxConfig()
        result = resolve_model_tier(config, "reviewer")
        assert result == "STANDARD"
