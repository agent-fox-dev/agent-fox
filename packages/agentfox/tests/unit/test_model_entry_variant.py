"""ModelEntry variant field and MODEL_REGISTRY variant registration tests.

Test Spec: TS-14-1, TS-14-2, TS-14-3, TS-14-E1, TS-14-4, TS-14-5, TS-14-6, TS-14-7
Requirements: 14-REQ-1.1, 14-REQ-1.2, 14-REQ-1.3, 14-REQ-1.E1,
              14-REQ-2.1, 14-REQ-2.2, 14-REQ-2.3, 14-REQ-2.4
"""

from __future__ import annotations

import pytest
from agentfox.core.models import MODEL_REGISTRY, ModelEntry, ModelTier

# ---------------------------------------------------------------------------
# TS-14-1: ModelEntry variant field defaults to None
# Requirement: 14-REQ-1.1
# ---------------------------------------------------------------------------


class TestModelEntryVariantDefault:
    """Verify that ModelEntry exposes an optional variant field defaulting to None."""

    def test_variant_defaults_to_none(self) -> None:
        """TS-14-1: ModelEntry constructed without variant has variant=None."""
        entry = ModelEntry(model_id="claude-opus-4-6", tier=ModelTier.ADVANCED)
        assert entry.variant is None


# ---------------------------------------------------------------------------
# TS-14-2: ModelEntry accepts None as a valid variant value
# Requirement: 14-REQ-1.2
# ---------------------------------------------------------------------------


class TestModelEntryVariantNone:
    """Verify that ModelEntry accepts None as a valid variant value."""

    def test_variant_accepts_none(self) -> None:
        """TS-14-2: ModelEntry with explicit variant=None is created without error."""
        entry = ModelEntry(model_id="claude-haiku-4-5", tier=ModelTier.SIMPLE, variant=None)
        assert entry.variant is None


# ---------------------------------------------------------------------------
# TS-14-3: ModelEntry accepts canonical and arbitrary variant strings
# Requirement: 14-REQ-1.3
# ---------------------------------------------------------------------------


class TestModelEntryVariantStrings:
    """Verify that ModelEntry accepts canonical variant strings and arbitrary strings."""

    @pytest.mark.parametrize("variant_value", ["fast", "standard", "extended", "turbo"])
    def test_variant_accepts_string(self, variant_value: str) -> None:
        """TS-14-3: ModelEntry accepts each variant string without error."""
        entry = ModelEntry(model_id="test-model", tier=ModelTier.ADVANCED, variant=variant_value)
        assert entry.variant == variant_value


# ---------------------------------------------------------------------------
# TS-14-E1: ModelEntry rejects non-string, non-None variant values
# Requirement: 14-REQ-1.E1
# ---------------------------------------------------------------------------


class TestModelEntryVariantTypeError:
    """Verify that ModelEntry raises TypeError or ValidationError for invalid variant types."""

    def test_variant_rejects_integer(self) -> None:
        """TS-14-E1: ModelEntry(variant=42) raises TypeError or ValidationError."""
        with pytest.raises((TypeError, ValueError)):
            ModelEntry(model_id="test", tier=ModelTier.ADVANCED, variant=42)  # type: ignore[arg-type]

    def test_variant_rejects_list(self) -> None:
        """TS-14-E1 corollary: ModelEntry(variant=[]) raises TypeError or ValidationError."""
        with pytest.raises((TypeError, ValueError)):
            ModelEntry(model_id="test", tier=ModelTier.ADVANCED, variant=[])  # type: ignore[arg-type]


# ---------------------------------------------------------------------------
# TS-14-4: MODEL_REGISTRY contains claude-haiku-4-5 with tier=SIMPLE, variant=None
# Requirement: 14-REQ-2.1
# ---------------------------------------------------------------------------


class TestModelRegistryHaiku:
    """Verify MODEL_REGISTRY contains claude-haiku-4-5 with correct tier and variant."""

    def test_haiku_in_registry(self) -> None:
        """TS-14-4: claude-haiku-4-5 entry has tier=SIMPLE and variant=None."""
        entry = MODEL_REGISTRY["claude-haiku-4-5"]
        assert entry.tier == ModelTier.SIMPLE
        assert entry.variant is None


# ---------------------------------------------------------------------------
# TS-14-5: MODEL_REGISTRY contains claude-sonnet-4-6 with tier=STANDARD, variant=None
# Requirement: 14-REQ-2.2
# ---------------------------------------------------------------------------


class TestModelRegistrySonnet:
    """Verify MODEL_REGISTRY contains claude-sonnet-4-6 with correct tier and variant."""

    def test_sonnet_in_registry(self) -> None:
        """TS-14-5: claude-sonnet-4-6 entry has tier=STANDARD and variant=None."""
        entry = MODEL_REGISTRY["claude-sonnet-4-6"]
        assert entry.tier == ModelTier.STANDARD
        assert entry.variant is None


# ---------------------------------------------------------------------------
# TS-14-6: MODEL_REGISTRY contains claude-opus-4-6 with tier=ADVANCED, variant='standard'
# Requirement: 14-REQ-2.3
# ---------------------------------------------------------------------------


class TestModelRegistryOpus:
    """Verify MODEL_REGISTRY contains claude-opus-4-6 with correct tier and variant."""

    def test_opus_in_registry(self) -> None:
        """TS-14-6: claude-opus-4-6 entry has tier=ADVANCED and variant='standard'."""
        entry = MODEL_REGISTRY["claude-opus-4-6"]
        assert entry.tier == ModelTier.ADVANCED
        assert entry.variant == "standard"


# ---------------------------------------------------------------------------
# TS-14-7: MODEL_REGISTRY contains claude-opus-4-6[1m] with tier=ADVANCED, variant='extended'
# Requirement: 14-REQ-2.4
# ---------------------------------------------------------------------------


class TestModelRegistryOpus1m:
    """Verify MODEL_REGISTRY contains claude-opus-4-6[1m] with correct tier and variant."""

    def test_opus_1m_in_registry(self) -> None:
        """TS-14-7: claude-opus-4-6[1m] entry has tier=ADVANCED and variant='extended'."""
        assert "claude-opus-4-6[1m]" in MODEL_REGISTRY
        entry = MODEL_REGISTRY["claude-opus-4-6[1m]"]
        assert entry.tier == ModelTier.ADVANCED
        assert entry.variant == "extended"
