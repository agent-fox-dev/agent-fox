"""Tests for agentspec configuration loading."""

from __future__ import annotations

from pathlib import Path

import pytest
from hypothesis import HealthCheck, given, settings
from hypothesis import strategies as st


class TestConfigLoading:
    """Tests for load_config and AgentSpecConfig."""

    def test_load_from_yaml(
        self,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
    ) -> None:
        """Config loads from settings.yaml."""
        settings_yaml.write_text("spec_tool:\n  model: claude-opus-4-6\n")
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "claude-opus-4-6"

    def test_env_overrides_yaml(
        self,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
        monkeypatch: pytest.MonkeyPatch,
    ) -> None:
        """Environment variables override settings.yaml values."""
        settings_yaml.write_text("spec_tool:\n  model: claude-opus-4-6\n")
        monkeypatch.setenv("AF_SPEC_MODEL", "claude-haiku-4-5-20251001")
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "claude-haiku-4-5-20251001"

    def test_config_fields(self) -> None:
        """AgentSpecConfig has required fields."""
        from agentspec.config import AgentSpecConfig

        config = AgentSpecConfig()
        assert hasattr(config, "model")

    def test_defaults(
        self,
        clean_env: None,
        mock_home: Path,
    ) -> None:
        """Default config values when no config file or env vars."""
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "STANDARD"


class TestConfigEdgeCases:
    """Edge case tests for configuration loading."""

    def test_invalid_yaml(
        self,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
    ) -> None:
        """Invalid YAML raises ConfigError with file path."""
        settings_yaml.write_text(":::bad yaml")
        from agentspec.config import load_config
        from agentspec.errors import ConfigError

        with pytest.raises(ConfigError) as exc_info:
            load_config()
        assert str(settings_yaml) in str(exc_info.value)

    def test_missing_section(
        self,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
    ) -> None:
        """Missing spec_tool section uses defaults."""
        settings_yaml.write_text("other_tool:\n  key: value\n")
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "STANDARD"

    def test_unknown_keys(
        self,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
    ) -> None:
        """Unknown keys in spec_tool are silently ignored."""
        settings_yaml.write_text("spec_tool:\n  unknown_key: value\n  model: test-model\n")
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "test-model"


class TestConfigProperties:
    """Property-based tests for configuration."""

    @settings(suppress_health_check=[HealthCheck.function_scoped_fixture])
    @given(
        env_model=st.text(
            min_size=1,
            alphabet=st.characters(categories=("L", "N")),
        ),
    )
    def test_env_always_overrides_yaml(
        self,
        env_model: str,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
        monkeypatch: pytest.MonkeyPatch,
    ) -> None:
        """Env vars always override YAML values."""
        monkeypatch.setenv("AF_SPEC_MODEL", env_model)
        settings_yaml.write_text("spec_tool:\n  model: different-value\n")
        from agentspec.config import load_config

        config = load_config()
        assert config.model == env_model

    @settings(suppress_health_check=[HealthCheck.function_scoped_fixture])
    @given(iteration=st.integers(min_value=0, max_value=10))
    def test_defaults_consistent(
        self,
        iteration: int,
        clean_env: None,
        mock_home: Path,
    ) -> None:
        """Defaults are consistent across invocations."""
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "STANDARD"


class TestConfigSmoke:
    """Integration smoke tests for configuration."""

    def test_config_load_end_to_end(
        self,
        clean_env: None,
        mock_home: Path,
        settings_yaml: Path,
        monkeypatch: pytest.MonkeyPatch,
    ) -> None:
        """Full config load from YAML + env var override."""
        settings_yaml.write_text("spec_tool:\n  model: yaml-model\n")
        monkeypatch.setenv("AF_SPEC_MODEL", "env-model")
        from agentspec.config import load_config

        config = load_config()
        assert config.model == "env-model"
