"""Unit tests for config dead code removal.

Test Spec: TS-130-1 through TS-130-13, TS-130-E1 through TS-130-E4,
           TS-130-SMOKE-1, TS-130-SMOKE-2
Requirements: 130-REQ-1.*, 130-REQ-2.*, 130-REQ-3.*, 130-REQ-4.*,
              130-REQ-5.1, 130-REQ-6.1, 130-REQ-8.1
"""

from __future__ import annotations

import tomllib
import warnings
from pathlib import Path

import agentfox.core.config as config_mod
from afaudit.events import AuditEventType
from agentfox.core.config import AgentFoxConfig, OrchestratorConfig, load_config
from agentfox.core.config_gen import (
    _BOUNDS_MAP_OVERRIDES,
    _PROMOTED_DEFAULTS,
    _VISIBLE_SECTIONS,
    generate_default_config,
)

# ---------------------------------------------------------------------------
# Unit tests: field absence (TS-130-1 through TS-130-4)
# ---------------------------------------------------------------------------


class TestFieldAbsence:
    """Verify removed fields are absent from config models."""

    def test_quality_gate_absent(self) -> None:
        """TS-130-1: OrchestratorConfig has no quality_gate field.

        Requirement: 130-REQ-1.1
        """
        assert "quality_gate" not in OrchestratorConfig.model_fields

    def test_quality_gate_timeout_absent(self) -> None:
        """TS-130-2: OrchestratorConfig has no quality_gate_timeout field.

        Requirement: 130-REQ-1.2
        """
        assert "quality_gate_timeout" not in OrchestratorConfig.model_fields

    def test_model_config_absent(self) -> None:
        """TS-130-3: ModelConfig class is not defined in config module.

        Requirement: 130-REQ-2.1
        """
        assert not hasattr(config_mod, "ModelConfig")

    def test_agent_fox_config_no_models(self) -> None:
        """TS-130-4: AgentFoxConfig has no models field.

        Requirement: 130-REQ-2.2
        """
        assert "models" not in AgentFoxConfig.model_fields


# ---------------------------------------------------------------------------
# Unit tests: config_gen metadata absence (TS-130-5 through TS-130-9, TS-130-11)
# ---------------------------------------------------------------------------


class TestConfigGenMetadata:
    """Verify stale metadata entries are removed from config_gen."""

    def test_visible_sections_no_models(self) -> None:
        """TS-130-5: _VISIBLE_SECTIONS does not include 'models'.

        Requirements: 130-REQ-2.3, 130-REQ-2.4
        """
        assert "models" not in _VISIBLE_SECTIONS

    def test_promoted_defaults_no_quality_gate(self) -> None:
        """TS-130-6: _PROMOTED_DEFAULTS excludes quality_gate.

        Requirement: 130-REQ-1.3
        """
        assert ("orchestrator", "quality_gate") not in _PROMOTED_DEFAULTS

    def test_promoted_defaults_overrides_no_quality_gate(self) -> None:
        """Direct test for 130-REQ-1.4: _PROMOTED_DEFAULTS_OVERRIDES excludes quality_gate.

        Requirement: 130-REQ-1.4
        """
        from agentfox.core.config_gen import _PROMOTED_DEFAULTS_OVERRIDES

        assert ("orchestrator", "quality_gate") not in _PROMOTED_DEFAULTS_OVERRIDES

    def test_phantom_routing_bounds_absent(self) -> None:
        """TS-130-7: _BOUNDS_MAP_OVERRIDES has no phantom RoutingConfig entries.

        Requirement: 130-REQ-4.1
        """
        assert ("RoutingConfig", "training_threshold") not in _BOUNDS_MAP_OVERRIDES
        assert ("RoutingConfig", "accuracy_threshold") not in _BOUNDS_MAP_OVERRIDES
        assert ("RoutingConfig", "retrain_interval") not in _BOUNDS_MAP_OVERRIDES

    def test_phantom_routing_descriptions_absent(self) -> None:
        """TS-130-8: No phantom RoutingConfig fields exist in the model.

        Requirement: 130-REQ-4.2
        """
        from agentfox.core.config import RoutingConfig

        assert "training_threshold" not in RoutingConfig.model_fields
        assert "accuracy_threshold" not in RoutingConfig.model_fields
        assert "retrain_interval" not in RoutingConfig.model_fields

    def test_drift_bounds_include_none(self) -> None:
        """TS-130-9: pre_flight_drift_block_threshold bounds include None.

        Requirement: 130-REQ-5.1
        """
        bounds = _BOUNDS_MAP_OVERRIDES[("ReviewerConfig", "pre_flight_drift_block_threshold")]
        assert "None" in bounds

    def test_no_model_config_in_bounds(self) -> None:
        """TS-130-11: No _BOUNDS_MAP_OVERRIDES key starts with 'ModelConfig'.

        Requirements: 130-REQ-1.5, 130-REQ-2.5
        """
        model_config_keys = [k for k in _BOUNDS_MAP_OVERRIDES if k[0] == "ModelConfig"]
        assert model_config_keys == []

    def test_no_quality_gate_in_config(self) -> None:
        """Verify quality_gate field is absent from OrchestratorConfig.

        Requirement: 130-REQ-1.5
        """
        assert "quality_gate" not in OrchestratorConfig.model_fields

    def test_schema_deprecated_fields_gone(self) -> None:
        """TS-130-5 (supplemental): _SCHEMA_DEPRECATED_FIELDS is empty or absent.

        Requirement: 130-REQ-2.4
        """
        # After removal, the set should either not exist or be empty
        try:
            from agentfox.core.config_gen import _SCHEMA_DEPRECATED_FIELDS

            assert ("models", "coding") not in _SCHEMA_DEPRECATED_FIELDS
        except ImportError:
            pass  # Acceptable: set was entirely removed


# ---------------------------------------------------------------------------
# Unit tests: audit event absence (TS-130-10)
# ---------------------------------------------------------------------------


class TestAuditEventAbsence:
    """Verify removed audit events are absent."""

    def test_quality_gate_result_event_absent(self) -> None:
        """TS-130-10: AuditEventType has no QUALITY_GATE_RESULT member.

        Requirement: 130-REQ-6.1
        """
        assert "QUALITY_GATE_RESULT" not in AuditEventType.__members__


# ---------------------------------------------------------------------------
# Unit tests: template content (TS-130-12, TS-130-13)
# ---------------------------------------------------------------------------


class TestTemplateContent:
    """Verify generated config template excludes removed items."""

    def test_template_no_quality_gate(self) -> None:
        """TS-130-12: Generated template does not mention quality_gate.

        Requirements: 130-REQ-1.3, 130-REQ-1.4
        """
        template = generate_default_config()
        assert "quality_gate" not in template

    def test_template_no_models_section(self) -> None:
        """TS-130-13: Generated template has no [models] section.

        Requirement: 130-REQ-2.3
        """
        template = generate_default_config()
        assert "[models]" not in template
        assert "# [models]" not in template


# ---------------------------------------------------------------------------
# Edge case tests (TS-130-E1 through TS-130-E4)
# ---------------------------------------------------------------------------


class TestOldConfigSilentIgnore:
    """Verify old config keys are silently ignored."""

    def test_old_quality_gate_silently_ignored(self) -> None:
        """TS-130-E1: TOML with quality_gate under [orchestrator] parses silently.

        Requirement: 130-REQ-1.E1
        """
        raw = tomllib.loads('[orchestrator]\nquality_gate = "make check"\nquality_gate_timeout = 120')
        config = AgentFoxConfig.model_validate(raw)
        assert config.orchestrator.parallel == 4

    def test_old_models_section_silently_ignored(self) -> None:
        """TS-130-E2: TOML with [models] section parses silently.

        Requirement: 130-REQ-2.E1
        """
        raw = tomllib.loads('[models]\ncoding = "ADVANCED"\nmemory_extraction = "SIMPLE"')
        config = AgentFoxConfig.model_validate(raw)
        assert not hasattr(config, "models") or "models" not in AgentFoxConfig.model_fields

    def test_old_skeptic_silently_ignored(self) -> None:
        """TS-130-E3: TOML with archetypes.skeptic parses silently.

        Requirements: 130-REQ-3.2, 130-REQ-3.3, 130-REQ-3.E1
        """
        raw = tomllib.loads("[archetypes]\nskeptic = true")
        config = AgentFoxConfig.model_validate(raw)
        assert config.archetypes.reviewer is True

    def test_old_triage_silently_ignored(self) -> None:
        """TS-130-E4: TOML with archetypes.triage parses without warning.

        Requirement: 130-REQ-3.1
        """
        raw = tomllib.loads("[archetypes]\ntriage = true")
        with warnings.catch_warnings():
            warnings.simplefilter("error")
            config = AgentFoxConfig.model_validate(raw)
        assert config.archetypes.reviewer is True

    def test_old_multiple_archetype_keys_silently_ignored(self) -> None:
        """All obsolete archetype keys parse without error.

        Requirements: 130-REQ-3.2, 130-REQ-3.3
        """
        raw = tomllib.loads("[archetypes]\noracle = true\nauditor = true\n")
        config = AgentFoxConfig.model_validate(raw)
        assert config.archetypes.reviewer is True

    def test_old_archetype_config_keys_silently_ignored(self) -> None:
        """Obsolete archetype config keys (skeptic_config, etc.) are silently ignored.

        Requirements: 130-REQ-3.2, 130-REQ-3.3
        """
        raw = tomllib.loads('[archetypes]\nskeptic_settings = "old"\noracle_settings = "old"\nauditor_config = "old"\n')
        config = AgentFoxConfig.model_validate(raw)
        assert config.archetypes.reviewer is True

    def test_old_fix_archetype_keys_silently_ignored(self) -> None:
        """Obsolete fix_reviewer and fix_coder keys are silently ignored.

        Requirements: 130-REQ-3.2, 130-REQ-3.3
        """
        raw = tomllib.loads("[archetypes]\nfix_reviewer = true\nfix_coder = true\n")
        config = AgentFoxConfig.model_validate(raw)
        assert config.archetypes.reviewer is True


# ---------------------------------------------------------------------------
# Integration smoke tests (TS-130-SMOKE-1, TS-130-SMOKE-2)
# ---------------------------------------------------------------------------


class TestSmoke:
    """Integration smoke tests using real components (no mocks)."""

    def test_full_config_load_after_removal(self, tmp_path: Path) -> None:
        """TS-130-SMOKE-1: Config with all removed keys loads successfully.

        Execution Path 1 from design.md.
        Requirement: 130-REQ-8.1
        """
        config_toml = tmp_path / "config.toml"
        config_toml.write_text(
            "[orchestrator]\n"
            'quality_gate = "make check"\n'
            "quality_gate_timeout = 120\n"
            "\n"
            "[models]\n"
            'coding = "ADVANCED"\n'
            "\n"
            "[archetypes]\n"
            "triage = true\n"
            "skeptic = true\n",
            encoding="utf-8",
        )
        config = load_config(config_toml)
        assert not hasattr(config, "models") or "models" not in AgentFoxConfig.model_fields
        assert "quality_gate" not in OrchestratorConfig.model_fields
        assert config.orchestrator.parallel == 4

    def test_template_generation_after_removal(self) -> None:
        """TS-130-SMOKE-2: Generated template excludes all removed items.

        Execution Path 2 from design.md.
        Requirement: 130-REQ-8.1
        """
        template = generate_default_config()
        assert "quality_gate" not in template
        assert "[models]" not in template
        assert "memory_extraction" not in template
        assert "parallel" in template
        assert "max_budget_usd" in template
