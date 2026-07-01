"""Configuration loading for agentspec.

Resolves model and auth settings from the merged ``AgentFoxConfig``
(produced by :func:`agentfox.core.config.load_config`), falling back to
the legacy ``~/.af/settings.yaml`` when no ``[spec_tool]`` section was
explicitly configured, and finally to hardcoded defaults.

Precedence (highest to lowest):

1. ``AF_SPEC_MODEL`` environment variable
2. ``[spec_tool]`` section from the merged ``AgentFoxConfig``
3. Migration fallback from ``~/.af/settings.yaml``
4. Hardcoded default ``claude-sonnet-4-6``
"""

from __future__ import annotations

import logging
import os
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING

import yaml

from agentspec.errors import ConfigError

if TYPE_CHECKING:
    from agentfox.core.config import AgentFoxConfig

logger = logging.getLogger(__name__)

_DEFAULT_MODEL = "claude-sonnet-4-6"


@dataclass
class AgentSpecConfig:
    """Resolved configuration for agentspec.

    Attributes:
        model: The Anthropic model to use for spec generation.
        auth_method: Authentication method (e.g. ``"api_key"``, ``"vertex"``).
        vertex_project: Google Cloud project ID for Vertex AI.
        vertex_region: Google Cloud region for Vertex AI.
    """

    model: str = _DEFAULT_MODEL
    auth_method: str = ""
    vertex_project: str = ""
    vertex_region: str = ""


def load_config(
    *,
    agent_fox_config: AgentFoxConfig | None = None,
) -> AgentSpecConfig:
    """Load configuration with 4-step model resolution precedence.

    When *agent_fox_config* is provided (the new path used by the ``spec``
    CLI after global config loading is wired up), settings are read from
    its ``spec_tool`` sub-config.  When omitted (backward-compat path
    used by internal callers like :class:`agentspec.session.SpecSession`),
    the function falls back to the legacy ``~/.af/settings.yaml`` file.

    Parameters:
        agent_fox_config: The merged configuration object produced by
            :func:`agentfox.core.config.load_config`.  ``None`` triggers
            the legacy settings.yaml path.

    Returns:
        A fully resolved :class:`AgentSpecConfig`.

    Raises:
        ConfigError: If ``~/.af/settings.yaml`` contains invalid YAML.
    """
    config = AgentSpecConfig()

    # ------------------------------------------------------------------
    # Step 1: AF_SPEC_MODEL environment variable (highest precedence)
    # ------------------------------------------------------------------
    env_model = os.environ.get("AF_SPEC_MODEL")
    if env_model:
        config.model = env_model
        # Env var wins unconditionally — skip file-based model resolution
        # but still populate auth fields if available.
        if agent_fox_config is not None:
            _apply_auth_fields(config, agent_fox_config)
        return config

    # ------------------------------------------------------------------
    # Step 2: Explicit [spec_tool] from merged AgentFoxConfig
    # ------------------------------------------------------------------
    if agent_fox_config is not None:
        _apply_spec_tool_fields(config, agent_fox_config)

        # Determine whether [spec_tool] was *explicitly* present in the
        # raw TOML (as opposed to Pydantic filling in defaults).
        spec_tool_explicit = getattr(agent_fox_config, "_spec_tool_explicit", False)
        if spec_tool_explicit or config.model != _DEFAULT_MODEL:
            # Explicit config — use it as-is, no fallback.
            return config

    # ------------------------------------------------------------------
    # Step 3: Migration fallback from ~/.af/settings.yaml
    # ------------------------------------------------------------------
    settings_path = Path.home() / ".af" / "settings.yaml"
    if settings_path.exists():
        found = _load_from_yaml(config, settings_path)
        if found:
            print(
                "Deprecation warning: ~/.af/settings.yaml is no longer "
                "the preferred config location. Migrate your [spec_tool] "
                "settings to $HOME/.agent-fox/config.toml.",
                file=sys.stderr,
            )
        return config

    # ------------------------------------------------------------------
    # Step 4: Hardcoded default (already set on AgentSpecConfig)
    # ------------------------------------------------------------------
    return config


def _apply_spec_tool_fields(
    config: AgentSpecConfig,
    agent_fox_config: AgentFoxConfig,
) -> None:
    """Copy all fields from ``agent_fox_config.spec_tool`` into *config*."""
    spec = agent_fox_config.spec_tool
    config.model = spec.model
    _apply_auth_fields(config, agent_fox_config)


def _apply_auth_fields(
    config: AgentSpecConfig,
    agent_fox_config: AgentFoxConfig,
) -> None:
    """Copy auth-related fields only (not model) from ``spec_tool``."""
    spec = agent_fox_config.spec_tool
    config.auth_method = spec.auth_method
    config.vertex_project = spec.vertex_project
    config.vertex_region = spec.vertex_region


def _load_from_yaml(config: AgentSpecConfig, settings_path: Path) -> bool:
    """Parse settings.yaml and populate config from the spec_tool section.

    Returns ``True`` if a ``spec_tool`` section with data was found,
    ``False`` otherwise.
    """
    try:
        data = yaml.safe_load(settings_path.read_text())
    except yaml.YAMLError as exc:
        msg = f"Invalid YAML in {settings_path}: {exc}"
        raise ConfigError(msg) from exc

    if data is None:
        return False

    if not isinstance(data, dict):
        actual_type = type(data).__name__
        msg = f"Invalid YAML in {settings_path}: expected a mapping, got {actual_type}"
        raise ConfigError(msg)

    spec_tool = data.get("spec_tool")
    if spec_tool is None:
        return False

    if not isinstance(spec_tool, dict):
        return False

    if "model" in spec_tool:
        config.model = str(spec_tool["model"])

    if "auth_method" in spec_tool:
        config.auth_method = str(spec_tool["auth_method"])

    if "vertex_project" in spec_tool:
        config.vertex_project = str(spec_tool["vertex_project"])

    if "vertex_region" in spec_tool:
        config.vertex_region = str(spec_tool["vertex_region"])

    return True
