"""Pricing entry and backward compatibility tests.

Test Spec: TS-14-37, TS-14-38, TS-14-39, TS-14-40, TS-14-41
Requirements: 14-REQ-10.1, 14-REQ-10.2, 14-REQ-10.3, 14-REQ-11.1, 14-REQ-11.2
"""

from __future__ import annotations

import re
from pathlib import Path

import pytest
from agentfox.core.config import _default_pricing_models, load_config

# ---------------------------------------------------------------------------
# TS-14-37: _default_pricing_models contains both claude-opus-4-6 and
#           claude-opus-4-6[1m] as distinct entries
# Requirement: 14-REQ-10.1
# ---------------------------------------------------------------------------


class TestPricingModelsContainOpus1m:
    """Verify _default_pricing_models returns entries for both opus models."""

    def test_both_opus_entries_present(self) -> None:
        """TS-14-37: Both 'claude-opus-4-6' and 'claude-opus-4-6[1m]' are in the pricing dict."""
        models = _default_pricing_models()
        model_ids = list(models.keys())
        assert "claude-opus-4-6" in model_ids
        assert "claude-opus-4-6[1m]" in model_ids

    def test_opus_entries_are_distinct(self) -> None:
        """TS-14-37 corollary: The two opus entries are separate, not aliased."""
        models = _default_pricing_models()
        assert models.get("claude-opus-4-6") is not models.get("claude-opus-4-6[1m]")


# ---------------------------------------------------------------------------
# TS-14-38: claude-opus-4-6[1m] pricing entry has all required pricing fields
# Requirement: 14-REQ-10.2
# ---------------------------------------------------------------------------


class TestPricingFieldsOpus1m:
    """Verify claude-opus-4-6[1m] pricing entry has all required fields with non-None values."""

    def test_all_pricing_fields_present_and_numeric(self) -> None:
        """TS-14-38: input/output/cache_read/cache_creation price fields are non-None numeric."""
        models = _default_pricing_models()
        entry = models["claude-opus-4-6[1m]"]
        assert entry.input_price_per_m is not None and isinstance(entry.input_price_per_m, (int, float))
        assert entry.output_price_per_m is not None and isinstance(entry.output_price_per_m, (int, float))
        assert entry.cache_read_price_per_m is not None and isinstance(entry.cache_read_price_per_m, (int, float))
        assert entry.cache_creation_price_per_m is not None and isinstance(
            entry.cache_creation_price_per_m, (int, float)
        )

    def test_pricing_fields_are_positive(self) -> None:
        """TS-14-38 corollary: Pricing rates should be positive (real pricing, not defaults)."""
        models = _default_pricing_models()
        entry = models["claude-opus-4-6[1m]"]
        assert entry.input_price_per_m > 0, "input_price_per_m should be positive"
        assert entry.output_price_per_m > 0, "output_price_per_m should be positive"
        assert entry.cache_read_price_per_m > 0, "cache_read_price_per_m should be positive"
        assert entry.cache_creation_price_per_m > 0, "cache_creation_price_per_m should be positive"


# ---------------------------------------------------------------------------
# TS-14-39: Source code adjacent to claude-opus-4-6[1m] pricing entry
#           contains a date comment recording when rates were retrieved
# Requirement: 14-REQ-10.3
# ---------------------------------------------------------------------------


class TestPricingSourceDateComment:
    """Verify the source code has a date comment near the opus[1m] pricing entry."""

    def test_opus_1m_pricing_has_date_comment(self) -> None:
        """TS-14-39: A comment with a YYYY-MM-DD date is adjacent to the opus[1m] entry."""
        source_path = Path(__file__).resolve().parents[2] / "agentfox" / "core" / "config.py"
        source = source_path.read_text(encoding="utf-8")
        idx = source.find("claude-opus-4-6[1m]")
        assert idx != -1, "claude-opus-4-6[1m] not found in config.py source"
        # Check a 400-char window around the entry for a date comment
        snippet = source[max(0, idx - 300) : idx + 300]
        assert re.search(r"#.*retrieved.*\d{4}-\d{2}-\d{2}", snippet, re.IGNORECASE), (
            "Expected a comment with retrieval date (YYYY-MM-DD) adjacent to the claude-opus-4-6[1m] pricing entry"
        )


# ---------------------------------------------------------------------------
# TS-14-40: resolve_model called with a tier name returns TIER_DEFAULTS model ID
# Requirement: 14-REQ-11.1
# ---------------------------------------------------------------------------


class TestResolveModelTierResolution:
    """Verify resolve_model(tier) returns the expected TIER_DEFAULTS model ID for each tier."""

    @pytest.mark.parametrize("tier", ["SIMPLE", "STANDARD", "ADVANCED"])
    def test_resolve_model_tier_returns_tier_default(self, tier: str) -> None:
        """TS-14-40: resolve_model(tier) == TIER_DEFAULTS[tier] for all tiers."""
        from agentfox.core.models import TIER_DEFAULTS, resolve_model

        assert resolve_model(tier) == TIER_DEFAULTS[tier]


# ---------------------------------------------------------------------------
# TS-14-41: Config with only model_tier loads without error (#764)
# Requirement: 14-REQ-11.2
# ---------------------------------------------------------------------------


class TestConfigWithModelTierOnly:
    """Verify config.toml with only model_tier (no extra variant keys) loads cleanly.

    After #764 the variant dimension was removed entirely.  A config that sets
    model_tier under [archetypes.overrides.coder] must parse without error and
    expose the archetype override with the correct tier value.
    """

    def test_toml_with_model_tier_only_loads_cleanly(self, tmp_path: Path) -> None:
        """TS-14-41: TOML with model_tier only; config loads without error."""
        config_file = tmp_path / "config.toml"
        config_file.write_text('[archetypes.overrides.coder]\nmodel_tier = "ADVANCED"\n')
        config = load_config(path=config_file)
        assert "coder" in config.archetypes.overrides
        assert config.archetypes.overrides["coder"].model_tier == "ADVANCED"
