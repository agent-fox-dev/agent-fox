"""AI model registry.

Defines the model tier enum, model entry dataclass, a registry of known
models, and functions for model resolution and cost calculation.

Pricing has been moved to config.toml via PricingConfig (spec 34).

Requirements: 01-REQ-5.1, 01-REQ-5.2, 01-REQ-5.3, 01-REQ-5.4, 01-REQ-5.E1,
              34-REQ-2.3, 34-REQ-2.4, 34-REQ-5.2
"""

from __future__ import annotations

import hashlib
import logging
from dataclasses import dataclass
from enum import StrEnum
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from agentfox.core.config import ModelsConfig, PricingConfig

logger = logging.getLogger(__name__)


def content_hash(text: str) -> str:
    """Compute SHA-256 hex digest of a text string."""
    return hashlib.sha256(text.encode("utf-8")).hexdigest()


class ModelTier(StrEnum):
    SIMPLE = "SIMPLE"
    STANDARD = "STANDARD"
    ADVANCED = "ADVANCED"


@dataclass(frozen=True)
class ModelEntry:
    model_id: str
    tier: ModelTier


MODEL_REGISTRY: dict[str, ModelEntry] = {
    "claude-haiku-4-5": ModelEntry("claude-haiku-4-5", ModelTier.SIMPLE),
    "claude-sonnet-4-6": ModelEntry("claude-sonnet-4-6", ModelTier.STANDARD),
    "claude-opus-4-6": ModelEntry("claude-opus-4-6", ModelTier.ADVANCED),
    "claude-opus-4-6[1m]": ModelEntry("claude-opus-4-6[1m]", ModelTier.ADVANCED),
}

TIER_DEFAULTS: dict[ModelTier, str] = {
    ModelTier.SIMPLE: "claude-haiku-4-5",
    ModelTier.STANDARD: "claude-sonnet-4-6",
    ModelTier.ADVANCED: "claude-opus-4-6",
}


def resolve_model(
    name: str,
    *,
    models_config: ModelsConfig | None = None,
) -> str:
    """Resolve a tier name or model ID to a model ID string.

    Accepts either a tier name (e.g. "SIMPLE", "STANDARD", "ADVANCED")
    or a specific model ID (e.g. "claude-sonnet-4-6").

    When *models_config* is provided, its ``registry`` and ``tier_defaults``
    entries are merged on top of the built-in :data:`MODEL_REGISTRY` and
    :data:`TIER_DEFAULTS` respectively, allowing projects to add new models
    or redirect tier defaults via ``config.toml``.

    Args:
        name: A tier name (e.g. ``"ADVANCED"``) or a model ID string.
        models_config: Optional config-based registry and tier-default
            overrides loaded from ``config.toml``.

    Returns:
        A model ID string (e.g. ``"claude-opus-4-6"``).

    Raises:
        ConfigError: If *name* is not a recognized tier or model ID, or if
            a registry entry in *models_config* contains an invalid tier.

    Requirements: 14-REQ-7.1, 14-REQ-7.2, 14-REQ-7.3, 14-REQ-7.4,
                  14-REQ-9.1, 14-REQ-9.2, 14-REQ-9.3,
                  759-REQ-1, 759-REQ-2, 759-REQ-3, 759-REQ-4
    """
    from agentfox.core.errors import ConfigError

    # Build effective registries, applying config overrides when provided.
    if models_config is not None:
        # Config registry entries are validated first so any ConfigError is raised early.
        config_registry: dict[str, ModelEntry] = {}
        for model_id, entry_raw in (models_config.registry.model_extra or {}).items():
            entry = entry_raw if isinstance(entry_raw, dict) else {}
            tier_name = entry.get("tier")
            valid_tiers = [t.value for t in ModelTier]
            try:
                entry_tier = ModelTier(tier_name)
            except (ValueError, TypeError):
                raise ConfigError(
                    f"Invalid tier '{tier_name}' for model '{model_id}' in models.registry. "
                    f"Valid tiers: {', '.join(valid_tiers)}",
                    model=model_id,
                )
            config_registry[model_id] = ModelEntry(model_id, entry_tier)

        # Config entries are inserted first; built-in entries fill in the rest.
        effective_registry: dict[str, ModelEntry] = {**config_registry}
        for model_id, entry in MODEL_REGISTRY.items():
            if model_id not in effective_registry:
                effective_registry[model_id] = entry

        # Process tier_defaults overrides from [models.tier_defaults].
        effective_tier_defaults: dict[ModelTier, str] = dict(TIER_DEFAULTS)
        for tier_name_str, model_id_val in (models_config.tier_defaults.model_extra or {}).items():
            valid_tiers = [t.value for t in ModelTier]
            try:
                override_tier = ModelTier(tier_name_str)
            except ValueError:
                raise ConfigError(
                    f"Invalid tier '{tier_name_str}' in models.tier_defaults. Valid tiers: {', '.join(valid_tiers)}",
                )
            effective_tier_defaults[override_tier] = model_id_val
    else:
        effective_registry = MODEL_REGISTRY
        effective_tier_defaults = TIER_DEFAULTS

    # Try as a tier name first.
    try:
        tier = ModelTier(name)
    except ValueError:
        tier = None

    if tier is not None:
        return effective_tier_defaults[tier]

    # Try as a direct model ID.
    if name in effective_registry:
        return name

    valid_options = sorted(effective_registry.keys())
    raise ConfigError(
        f"Unknown model '{name}'. Valid options: {', '.join(valid_options)}",
        model=name,
        valid_options=valid_options,
    )


def calculate_cost(
    input_tokens: int,
    output_tokens: int,
    model_id: str,
    pricing: PricingConfig,
    *,
    cache_read_input_tokens: int = 0,
    cache_creation_input_tokens: int = 0,
) -> float:
    """Calculate estimated cost in USD using config-based pricing.

    Falls back to zero cost if model not found in pricing config.

    Args:
        input_tokens: Number of input tokens consumed.
        output_tokens: Number of output tokens produced.
        model_id: The model identifier string.
        pricing: The pricing configuration with per-model rates.
        cache_read_input_tokens: Number of cache-read input tokens.
        cache_creation_input_tokens: Number of cache-creation input tokens.

    Returns:
        Estimated cost in USD as a float.

    Requirements: 34-REQ-2.3, 34-REQ-2.4
    """
    model_pricing = pricing.models.get(model_id)
    if model_pricing is None:
        logger.warning(
            "Model '%s' not found in pricing config; using zero cost",
            model_id,
        )
        return 0.0

    input_cost = (input_tokens / 1_000_000) * model_pricing.input_price_per_m
    output_cost = (output_tokens / 1_000_000) * model_pricing.output_price_per_m
    cache_read_cost = (cache_read_input_tokens / 1_000_000) * model_pricing.cache_read_price_per_m
    cache_creation_cost = (cache_creation_input_tokens / 1_000_000) * model_pricing.cache_creation_price_per_m
    return input_cost + output_cost + cache_read_cost + cache_creation_cost
