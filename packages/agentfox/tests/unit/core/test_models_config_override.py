"""Unit tests for config.toml overrides of MODEL_REGISTRY and TIER_DEFAULTS.

Test Spec: TS-NS-1 through TS-NS-5
Requirements: 759-REQ-1, 759-REQ-2, 759-REQ-3, 759-REQ-4
"""

from __future__ import annotations

import pytest
from agentfox.core.config import ModelRegistryConfig, ModelsConfig, TierDefaultsConfig
from agentfox.core.errors import ConfigError
from agentfox.core.models import resolve_model


class TestResolveModelWithRegistryOverride:
    """TS-NS-1: resolve_model uses models_config.registry to resolve a new model."""

    def test_registry_override_by_tier(self) -> None:
        """AC-1: New model in registry resolves when tier matches.

        Requirement: 759-REQ-1
        """
        mc = ModelsConfig(registry=ModelRegistryConfig(**{"claude-haiku-5-0": {"tier": "SIMPLE"}}))
        result = resolve_model("SIMPLE", models_config=mc)
        assert result == "claude-haiku-5-0"

    def test_registry_override_model_id_resolution(self) -> None:
        """Model added via registry can also be resolved directly by model ID.

        Requirement: 759-REQ-1
        """
        mc = ModelsConfig(registry=ModelRegistryConfig(**{"claude-haiku-5-0": {"tier": "SIMPLE"}}))
        result = resolve_model("claude-haiku-5-0", models_config=mc)
        assert result == "claude-haiku-5-0"

    def test_registry_override_does_not_affect_other_tiers(self) -> None:
        """An override for SIMPLE does not change STANDARD or ADVANCED defaults.

        Requirement: 759-REQ-1
        """
        mc = ModelsConfig(registry=ModelRegistryConfig(**{"claude-haiku-5-0": {"tier": "SIMPLE"}}))
        standard_result = resolve_model("STANDARD", models_config=mc)
        assert standard_result == "claude-sonnet-4-6"


class TestResolveModelWithTierDefaultsOverride:
    """TS-NS-2: resolve_model uses models_config.tier_defaults to redirect a tier."""

    def test_tier_default_override(self) -> None:
        """AC-2: tier_default override redirects SIMPLE to a new model.

        Requirement: 759-REQ-2
        """
        mc = ModelsConfig(
            registry=ModelRegistryConfig(**{"claude-haiku-5-0": {"tier": "SIMPLE"}}),
            tier_defaults=TierDefaultsConfig(**{"SIMPLE": "claude-haiku-5-0"}),
        )
        result = resolve_model("SIMPLE", models_config=mc)
        assert result == "claude-haiku-5-0"

    def test_tier_default_override_standard(self) -> None:
        """tier_defaults override for STANDARD redirects to specified model.

        Requirement: 759-REQ-2
        """
        mc = ModelsConfig(
            registry=ModelRegistryConfig(**{"claude-haiku-5-0": {"tier": "STANDARD"}}),
            tier_defaults=TierDefaultsConfig(**{"STANDARD": "claude-haiku-5-0"}),
        )
        result = resolve_model("STANDARD", models_config=mc)
        assert result == "claude-haiku-5-0"


class TestResolveModelEmptyModelsConfig:
    """TS-NS-3: resolve_model with empty ModelsConfig produces identical results."""

    def test_empty_models_config_simple(self) -> None:
        """SIMPLE tier with empty ModelsConfig returns same as no config.

        Requirement: 759-REQ-3
        """
        mc = ModelsConfig()
        assert resolve_model("SIMPLE", models_config=mc) == resolve_model("SIMPLE")

    def test_empty_models_config_standard(self) -> None:
        """STANDARD tier with empty ModelsConfig returns same as no config.

        Requirement: 759-REQ-3
        """
        mc = ModelsConfig()
        assert resolve_model("STANDARD", models_config=mc) == resolve_model("STANDARD")

    def test_empty_models_config_advanced(self) -> None:
        """ADVANCED tier with empty ModelsConfig returns same as no config.

        Requirement: 759-REQ-3
        """
        mc = ModelsConfig()
        assert resolve_model("ADVANCED", models_config=mc) == resolve_model("ADVANCED")

    def test_none_models_config(self) -> None:
        """models_config=None produces identical results to omitting it entirely.

        Requirement: 759-REQ-3
        """
        assert resolve_model("SIMPLE", models_config=None) == resolve_model("SIMPLE")
        assert resolve_model("STANDARD", models_config=None) == resolve_model("STANDARD")


class TestResolveModelInvalidTierInRegistry:
    """TS-NS-4: ConfigError raised at config-load time for invalid tier in registry.

    Validation now happens inside ModelRegistryConfig's model_validator so that
    load_config() surfaces the error immediately rather than deferring it to
    resolve_model() invocation time.
    """

    def test_invalid_tier_raises_config_error(self) -> None:
        """AC-4: ModelRegistryConfig with invalid tier raises ConfigError at construction.

        Requirement: 759-REQ-4
        """
        with pytest.raises(ConfigError):
            ModelRegistryConfig(**{"my-model": {"tier": "INVALID_TIER"}})

    def test_invalid_tier_error_message_mentions_tier(self) -> None:
        """ConfigError for invalid tier includes the bad tier name.

        Requirement: 759-REQ-4
        """
        with pytest.raises(ConfigError) as exc_info:
            ModelRegistryConfig(**{"my-model": {"tier": "BOGUS"}})
        assert "BOGUS" in str(exc_info.value)

    def test_invalid_tier_via_models_config_raises(self) -> None:
        """ConfigError raised when ModelsConfig is built with invalid registry tier.

        Requirement: 759-REQ-4
        """
        with pytest.raises(ConfigError):
            ModelsConfig(registry=ModelRegistryConfig(**{"my-model": {"tier": "INVALID_TIER"}}))


class TestConfigTemplateContainsModelsSections:
    """TS-NS-5: generate_config_template() includes commented [models] sections."""

    def test_template_contains_models_section(self) -> None:
        """Template has commented '# [models]' section.

        Requirement: 759-REQ-1
        """
        from agentfox.core.config import AgentFoxConfig
        from agentfox.core.config_gen import extract_schema, generate_config_template

        schema = extract_schema(AgentFoxConfig)
        template = generate_config_template(schema)
        assert "# [models]" in template

    def test_template_contains_models_registry_section(self) -> None:
        """Template has commented '# [models.registry]' subsection.

        Requirement: 759-REQ-1
        """
        from agentfox.core.config import AgentFoxConfig
        from agentfox.core.config_gen import extract_schema, generate_config_template

        schema = extract_schema(AgentFoxConfig)
        template = generate_config_template(schema)
        assert "# [models.registry]" in template

    def test_template_contains_models_tier_defaults_section(self) -> None:
        """Template has commented '# [models.tier_defaults]' subsection.

        Requirement: 759-REQ-2
        """
        from agentfox.core.config import AgentFoxConfig
        from agentfox.core.config_gen import extract_schema, generate_config_template

        schema = extract_schema(AgentFoxConfig)
        template = generate_config_template(schema)
        assert "# [models.tier_defaults]" in template

    def test_models_section_is_only_commented_not_active(self) -> None:
        """The [models] header must appear only as '# [models]', never as active '[models]'.

        Requirement: 759-REQ-1
        """
        from agentfox.core.config import AgentFoxConfig
        from agentfox.core.config_gen import extract_schema, generate_config_template

        schema = extract_schema(AgentFoxConfig)
        template = generate_config_template(schema)
        active_header_present = any(line.strip() == "[models]" for line in template.splitlines())
        assert not active_header_present
