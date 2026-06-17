"""Configuration loading from YAML and environment variables.

Reads ``~/.af/settings.yaml`` and environment variables to produce an
``AgentSpecConfig``.
"""

from __future__ import annotations

import logging
import os
from dataclasses import dataclass
from pathlib import Path

import yaml

from agentspec.errors import ConfigError

logger = logging.getLogger(__name__)


@dataclass
class AgentSpecConfig:
    """Resolved configuration for agentspec.

    Attributes:
        model: The Anthropic model to use for spec generation.
    """

    model: str = "claude-sonnet-4-6"


def load_config() -> AgentSpecConfig:
    """Load configuration from ``~/.af/settings.yaml`` and env vars.

    Reads the ``spec_tool`` section from the settings file, then applies
    environment variable overrides.

    Raises:
        ConfigError: If the settings file contains invalid YAML.
    """
    config = AgentSpecConfig()

    settings_path = Path.home() / ".af" / "settings.yaml"
    if settings_path.exists():
        _load_from_yaml(config, settings_path)

    _apply_env_overrides(config)

    return config


def _load_from_yaml(config: AgentSpecConfig, settings_path: Path) -> None:
    """Parse settings.yaml and populate config from the spec_tool section."""
    try:
        data = yaml.safe_load(settings_path.read_text())
    except yaml.YAMLError as exc:
        msg = f"Invalid YAML in {settings_path}: {exc}"
        raise ConfigError(msg) from exc

    if data is None:
        return

    if not isinstance(data, dict):
        actual_type = type(data).__name__
        msg = f"Invalid YAML in {settings_path}: expected a mapping, got {actual_type}"
        raise ConfigError(msg)

    spec_tool = data.get("spec_tool")
    if spec_tool is None:
        return

    if not isinstance(spec_tool, dict):
        return

    if "model" in spec_tool:
        config.model = str(spec_tool["model"])


def _apply_env_overrides(config: AgentSpecConfig) -> None:
    """Override config values from environment variables.

    Environment variables take precedence over YAML values:
    - AF_SPEC_MODEL -> config.model
    """
    env_model = os.environ.get("AF_SPEC_MODEL")
    if env_model is not None:
        config.model = env_model
