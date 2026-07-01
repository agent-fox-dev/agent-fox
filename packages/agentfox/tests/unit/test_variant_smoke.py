"""Smoke tests for model variant support end-to-end wiring.

Test Spec: TS-14-SMOKE-1, TS-14-SMOKE-2, TS-14-SMOKE-3, TS-14-SMOKE-4, TS-14-SMOKE-5
Requirements: 14-REQ-7.2, 14-REQ-9.1, 14-REQ-11.1, 14-REQ-8.4, 14-REQ-12.1
"""

from __future__ import annotations

import logging
from unittest.mock import MagicMock, patch

import pytest
from agentfox.archetypes import ArchetypeEntry
from agentfox.core.config import (
    AgentFoxConfig,
    ArchetypesConfig,
    PerArchetypeConfig,
)
from agentfox.core.escalation import EscalationLadder
from agentfox.core.models import (
    TIER_DEFAULTS,
    ModelTier,
    resolve_model,
)
from agentfox.engine.sdk_params import resolve_model_variant

# NodeSessionRunner import chain pulls in rich.
# Runtime tests that instantiate NodeSessionRunner are skipped when rich is
# unavailable; tests using only sdk_params/models/escalation work fine.
try:
    import rich  # noqa: F401

    _has_rich = True
except ModuleNotFoundError:
    _has_rich = False

_skip_no_rich = pytest.mark.skipif(not _has_rich, reason="rich not installed; NodeSessionRunner import chain fails")


# ---------------------------------------------------------------------------
# TS-14-SMOKE-1: ADVANCED archetype with default_model_variant='extended'
#                resolves to claude-opus-4-6[1m] through NodeSessionRunner
# Execution Path: 14-PATH-1
# Requirements: 14-REQ-7.2, 14-REQ-12.1, 14-REQ-6.1, 14-REQ-8.4
# ---------------------------------------------------------------------------


class TestSmokeAdvancedExtendedResolution:
    """Smoke: ADVANCED-tier archetype with extended variant -> claude-opus-4-6[1m]."""

    def test_resolve_model_variant_then_resolve_model(self) -> None:
        """TS-14-SMOKE-1: Two-step resolution with real MODEL_REGISTRY produces
        claude-opus-4-6[1m] when variant='extended' and tier='ADVANCED'.
        """
        # Step 1: resolve_model_variant returns 'extended' via Layer 4
        config = AgentFoxConfig()
        mock_entry = ArchetypeEntry(
            name="coder",
            default_model_tier="ADVANCED",
            default_model_variant="extended",
        )
        with patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry):
            variant = resolve_model_variant(config, "coder")
        assert variant == "extended"

        # Step 2: resolve_model with real MODEL_REGISTRY
        model_id = resolve_model("ADVANCED", variant=variant)
        assert model_id == "claude-opus-4-6[1m]"

    @_skip_no_rich
    def test_node_session_runner_end_to_end(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-SMOKE-1: Full NodeSessionRunner wiring with mocked archetype
        produces claude-opus-4-6[1m] and emits no fallback DEBUG log.
        """
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        mock_parsed = MagicMock(spec_name="test_spec", group_number=1)
        mock_entry = ArchetypeEntry(
            name="coder",
            default_model_tier="ADVANCED",
            default_model_variant="extended",
        )

        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            with (
                patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry),
                patch("agentfox.engine.session_lifecycle.resolve_security_config", return_value=None),
                patch("agentfox.engine.session_lifecycle.clamp_instances", side_effect=lambda a, i, **kw: i),
                patch("agentfox.engine.session_lifecycle.parse_node_id", return_value=mock_parsed),
            ):
                runner = NodeSessionRunner(
                    node_id="test_spec_1_coder_1",
                    config=AgentFoxConfig(),
                    knowledge_db=MagicMock(),
                )
                assert runner._resolved_model_id == "claude-opus-4-6[1m]"

        # No DEBUG fallback log should be emitted for a valid match.
        fallback_logs = [
            r
            for r in caplog.records
            if r.levelno == logging.DEBUG and ("fallback" in r.message.lower() or "falling back" in r.message.lower())
        ]
        assert not fallback_logs, "No fallback log expected for valid (ADVANCED, extended) match"


# ---------------------------------------------------------------------------
# TS-14-SMOKE-2: SIMPLE-tier mode with model_variant='extended' falls back
#                to claude-haiku-4-5 with a DEBUG log
# Execution Path: 14-PATH-2
# Requirements: 14-REQ-9.1
# ---------------------------------------------------------------------------


class TestSmokeFallbackSimpleExtended:
    """Smoke: SIMPLE tier + extended variant falls back to claude-haiku-4-5."""

    def test_simple_extended_fallback_with_debug_log(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-SMOKE-2: resolve_model('SIMPLE', variant='extended') returns
        claude-haiku-4-5 with a DEBUG fallback log.
        """
        # Step 1: resolve_model_variant returns 'extended'
        config = AgentFoxConfig()
        mock_entry = ArchetypeEntry(
            name="coder",
            default_model_tier="SIMPLE",
            default_model_variant="extended",
        )
        with patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry):
            variant = resolve_model_variant(config, "coder")
        assert variant == "extended"

        # Step 2: resolve_model falls back because SIMPLE has no 'extended' variant
        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            model_id = resolve_model("SIMPLE", variant=variant)

        assert model_id == "claude-haiku-4-5"
        assert any(r.levelno == logging.DEBUG for r in caplog.records), "Expected a DEBUG-level fallback log"

    @_skip_no_rich
    def test_node_session_runner_fallback_path(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-SMOKE-2: Full NodeSessionRunner wiring with SIMPLE tier
        and extended variant falls back to haiku with DEBUG log.
        """
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        mock_parsed = MagicMock(spec_name="test_spec", group_number=1)
        mock_entry = ArchetypeEntry(
            name="coder",
            default_model_tier="SIMPLE",
            default_model_variant="extended",
        )

        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            with (
                patch("agentfox.engine.sdk_params.get_archetype", return_value=mock_entry),
                patch("agentfox.engine.session_lifecycle.resolve_security_config", return_value=None),
                patch("agentfox.engine.session_lifecycle.clamp_instances", side_effect=lambda a, i, **kw: i),
                patch("agentfox.engine.session_lifecycle.parse_node_id", return_value=mock_parsed),
            ):
                runner = NodeSessionRunner(
                    node_id="test_spec_1_coder_1",
                    config=AgentFoxConfig(),
                    knowledge_db=MagicMock(),
                )
                assert runner._resolved_model_id == "claude-haiku-4-5"

        # Fallback DEBUG log IS expected for this path.
        assert any(r.levelno == logging.DEBUG for r in caplog.records), (
            "Expected a DEBUG fallback log for SIMPLE + extended"
        )


# ---------------------------------------------------------------------------
# TS-14-SMOKE-3: Backward-compatible resolution without variant
# Execution Path: 14-PATH-3
# Requirements: 14-REQ-11.1
# ---------------------------------------------------------------------------


class TestSmokeBackwardCompatNoVariant:
    """Smoke: resolve_model('ADVANCED') without variant returns claude-opus-4-6."""

    def test_no_variant_returns_standard_opus(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-SMOKE-3: resolve_model('ADVANCED') returns 'claude-opus-4-6'
        (identical to pre-spec behavior) with no DEBUG fallback log.
        """
        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            result = resolve_model("ADVANCED")

        assert result == "claude-opus-4-6"
        assert result == TIER_DEFAULTS["ADVANCED"]

        # No fallback log expected for variant=None path.
        fallback_logs = [
            r
            for r in caplog.records
            if r.levelno == logging.DEBUG and ("fallback" in r.message.lower() or "falling back" in r.message.lower())
        ]
        assert not fallback_logs, "No fallback log expected for backward-compatible path"

    def test_all_tiers_backward_compatible(self) -> None:
        """TS-14-SMOKE-3 corollary: All tiers return TIER_DEFAULTS without variant."""
        for tier in ["SIMPLE", "STANDARD", "ADVANCED"]:
            assert resolve_model(tier) == TIER_DEFAULTS[tier]


# ---------------------------------------------------------------------------
# TS-14-SMOKE-4: EscalationLadder preserves variant='extended' across
#                STANDARD->ADVANCED escalation -> claude-opus-4-6[1m]
# Execution Path: 14-PATH-4
# Requirements: 14-REQ-8.4
# ---------------------------------------------------------------------------


class TestSmokeEscalationPreservesVariant:
    """Smoke: EscalationLadder preserves variant across escalation to ADVANCED."""

    def test_standard_to_advanced_with_extended_variant(self) -> None:
        """TS-14-SMOKE-4: ladder with starting_variant='extended' escalates
        STANDARD->ADVANCED and resolve_current_model returns claude-opus-4-6[1m].

        Uses real resolve_model (no mocks) to verify end-to-end.
        """
        ladder = EscalationLadder(
            ModelTier.STANDARD,
            ModelTier.ADVANCED,
            retries_before_escalation=0,
            starting_variant="extended",
        )
        assert ladder.current_variant == "extended"
        assert ladder.current_tier == ModelTier.STANDARD

        # Escalate to ADVANCED
        ladder.record_failure()
        assert ladder.current_tier == ModelTier.ADVANCED
        assert ladder.current_variant == "extended"

        # Resolve using real MODEL_REGISTRY
        model_id = ladder.resolve_current_model()
        assert model_id == "claude-opus-4-6[1m]"

    def test_variant_preserved_through_all_tiers(self, caplog: pytest.LogCaptureFixture) -> None:
        """TS-14-SMOKE-4 corollary: variant='extended' is preserved from
        SIMPLE through STANDARD to ADVANCED. At SIMPLE and STANDARD, fallback
        is applied (DEBUG log); at ADVANCED, exact match found.
        """
        ladder = EscalationLadder(
            ModelTier.SIMPLE,
            ModelTier.ADVANCED,
            retries_before_escalation=0,
            starting_variant="extended",
        )

        with caplog.at_level(logging.DEBUG, logger="agentfox.core.models"):
            # SIMPLE: no extended variant -> fallback
            model_simple = ladder.resolve_current_model()
            assert model_simple == "claude-haiku-4-5"

            # Escalate to STANDARD
            ladder.record_failure()
            assert ladder.current_tier == ModelTier.STANDARD
            model_standard = ladder.resolve_current_model()
            assert model_standard == "claude-sonnet-4-6"

            # Escalate to ADVANCED
            ladder.record_failure()
            assert ladder.current_tier == ModelTier.ADVANCED
            model_advanced = ladder.resolve_current_model()
            assert model_advanced == "claude-opus-4-6[1m]"

        # Variant never changed
        assert ladder.current_variant == "extended"


# ---------------------------------------------------------------------------
# TS-14-SMOKE-5: config.toml override path — model_variant='extended' under
#                [archetypes.overrides.coder] -> claude-opus-4-6[1m]
# Execution Path: 14-PATH-5
# Requirements: 14-REQ-12.1
# ---------------------------------------------------------------------------


class TestSmokeConfigOverrideVariant:
    """Smoke: config.toml model_variant='extended' for coder -> claude-opus-4-6[1m]."""

    def test_config_override_extended_variant_resolved(self) -> None:
        """TS-14-SMOKE-5: PerArchetypeConfig with model_variant='extended'
        flows through resolve_model_variant Layer 2 to resolve_model.
        """
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        model_tier="ADVANCED",
                        model_variant="extended",
                    ),
                }
            )
        )

        # Step 1: resolve_model_variant picks up Layer 2 override
        variant = resolve_model_variant(config, "coder")
        assert variant == "extended"

        # Step 2: resolve_model with real MODEL_REGISTRY
        model_id = resolve_model("ADVANCED", variant=variant)
        assert model_id == "claude-opus-4-6[1m]"

    @_skip_no_rich
    def test_node_session_runner_config_override_path(self) -> None:
        """TS-14-SMOKE-5: Full NodeSessionRunner wiring with config override
        produces claude-opus-4-6[1m].
        """
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        mock_parsed = MagicMock(spec_name="test_spec", group_number=1)
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(
                overrides={
                    "coder": PerArchetypeConfig(
                        model_tier="ADVANCED",
                        model_variant="extended",
                    ),
                }
            )
        )

        with (
            patch("agentfox.engine.session_lifecycle.resolve_security_config", return_value=None),
            patch("agentfox.engine.session_lifecycle.clamp_instances", side_effect=lambda a, i, **kw: i),
            patch("agentfox.engine.session_lifecycle.parse_node_id", return_value=mock_parsed),
        ):
            runner = NodeSessionRunner(
                node_id="test_spec_1_coder_1",
                config=config,
                knowledge_db=MagicMock(),
            )
            assert runner._resolved_model_id == "claude-opus-4-6[1m]"
