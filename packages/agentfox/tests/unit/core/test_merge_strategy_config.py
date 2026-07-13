"""Tests for WorkspaceConfig.merge_strategy field and config_gen integration.

Test Spec: TS-02-1 (default), TS-02-2 (valid values), TS-02-3 (invalid value),
           TS-02-4 (config_gen visibility), TS-02-E1 (absent field default),
           TS-02-E2 (empty string and None), TS-02-P1 (field invariant property)
Requirements: 02-REQ-1.1, 02-REQ-1.2, 02-REQ-1.3, 02-REQ-1.4,
              02-REQ-1.E1, 02-REQ-1.E2
"""

from __future__ import annotations

import logging

import pytest
from agentfox.core.config import WorkspaceConfig
from agentfox.core.config_gen import (
    _PROMOTED_DEFAULTS,
    _VISIBLE_SECTIONS,
    generate_default_config,
)
from hypothesis import given, settings
from hypothesis import strategies as st
from pydantic import ValidationError

# ---------------------------------------------------------------------------
# TS-02-1: WorkspaceConfig exposes merge_strategy defaulting to 'direct'
# ---------------------------------------------------------------------------


class TestMergeStrategyDefault:
    """TS-02-1: WorkspaceConfig exposes a merge_strategy field typed as
    Literal['direct','branch','pr'] defaulting to 'direct' when absent.

    Requirements: 02-REQ-1.1
    """

    def test_default_merge_strategy_is_direct(self) -> None:
        """WorkspaceConfig() with no merge_strategy defaults to 'direct'."""
        cfg = WorkspaceConfig()
        assert cfg.merge_strategy == "direct"

    def test_merge_strategy_field_exists_on_model(self) -> None:
        """The merge_strategy field is declared on WorkspaceConfig."""
        assert "merge_strategy" in WorkspaceConfig.model_fields


# ---------------------------------------------------------------------------
# TS-02-2: WorkspaceConfig accepts each of the three valid literal values
# ---------------------------------------------------------------------------


class TestMergeStrategyValidValues:
    """TS-02-2: WorkspaceConfig accepts each valid literal value.

    Requirements: 02-REQ-1.2
    """

    @pytest.mark.parametrize("value", ["direct", "branch", "pr"])
    def test_valid_literal_accepted(self, value: str) -> None:
        """Each valid merge_strategy literal constructs successfully."""
        cfg = WorkspaceConfig(merge_strategy=value)
        assert cfg.merge_strategy == value


# ---------------------------------------------------------------------------
# TS-02-3: WorkspaceConfig rejects unrecognized string values
# ---------------------------------------------------------------------------


class TestMergeStrategyInvalidValue:
    """TS-02-3: WorkspaceConfig raises ValidationError for invalid values.

    Requirements: 02-REQ-1.3
    """

    def test_invalid_string_raises_validation_error(self) -> None:
        """merge_strategy='squash' raises Pydantic ValidationError."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy="squash")

    @pytest.mark.parametrize("bad_value", ["merge", "rebase", "fast-forward", "DIRECT", "PR"])
    def test_various_invalid_strings_rejected(self, bad_value: str) -> None:
        """Various invalid strings are all rejected by Pydantic validation."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy=bad_value)


# ---------------------------------------------------------------------------
# TS-02-E1: Pre-existing config without merge_strategy silently defaults
# ---------------------------------------------------------------------------


class TestMergeStrategyAbsentDefault:
    """TS-02-E1: Loading config without merge_strategy defaults to 'direct'.

    Requirements: 02-REQ-1.E1
    """

    def test_absent_field_defaults_to_direct(self) -> None:
        """WorkspaceConfig with existing fields but no merge_strategy defaults to 'direct'."""
        cfg = WorkspaceConfig(integration_branch="main", force_clean=False)
        assert cfg.merge_strategy == "direct"

    def test_absent_field_no_warnings(self, caplog: pytest.LogCaptureFixture) -> None:
        """No warning is emitted when merge_strategy is absent."""
        with caplog.at_level(logging.WARNING):
            WorkspaceConfig(integration_branch="develop")
        merge_warnings = [r for r in caplog.records if "merge_strategy" in r.message]
        assert len(merge_warnings) == 0


# ---------------------------------------------------------------------------
# TS-02-E2: Empty string and None raise ValidationError
# ---------------------------------------------------------------------------


class TestMergeStrategyEmptyAndNone:
    """TS-02-E2: WorkspaceConfig raises ValidationError for empty string or None.

    Requirements: 02-REQ-1.E2
    """

    def test_empty_string_raises_validation_error(self) -> None:
        """merge_strategy='' raises Pydantic ValidationError."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy="")

    def test_none_raises_validation_error(self) -> None:
        """merge_strategy=None raises Pydantic ValidationError."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy=None)


# ---------------------------------------------------------------------------
# TS-02-4: config_gen includes merge_strategy in visible sections and
#          promoted defaults
# ---------------------------------------------------------------------------


class TestMergeStrategyConfigGen:
    """TS-02-4: config_gen.py includes merge_strategy in _VISIBLE_SECTIONS
    and _PROMOTED_DEFAULTS so the generated template contains the line
    'merge_strategy = "direct"'.

    Requirements: 02-REQ-1.4

    Note: _VISIBLE_SECTIONS is a set[str] of section names, and
    _PROMOTED_DEFAULTS is a set[tuple[str, str]] of (section, field) pairs.
    This test adapts to the actual data structures per the drift report.
    """

    def test_workspace_in_visible_sections(self) -> None:
        """'workspace' section is present in _VISIBLE_SECTIONS."""
        assert "workspace" in _VISIBLE_SECTIONS

    def test_merge_strategy_in_promoted_defaults(self) -> None:
        """('workspace', 'merge_strategy') is present in _PROMOTED_DEFAULTS."""
        assert ("workspace", "merge_strategy") in _PROMOTED_DEFAULTS

    def test_generated_template_contains_merge_strategy(self) -> None:
        """Generated config template contains 'merge_strategy = "direct"'."""
        template = generate_default_config()
        assert 'merge_strategy = "direct"' in template

    def test_generated_template_has_workspace_section(self) -> None:
        """Generated config template contains a [workspace] section header."""
        template = generate_default_config()
        assert "[workspace]" in template


# ---------------------------------------------------------------------------
# TS-02-P1: Property test — merge_strategy field is always valid
# ---------------------------------------------------------------------------


class TestMergeStrategyFieldInvariant:
    """TS-02-P1: For any WorkspaceConfig instance, merge_strategy is always
    one of 'direct', 'branch', or 'pr' and never None, empty, or unrecognized.

    Property: 02-PROP-1
    Validates: 02-REQ-1.1, 02-REQ-1.2, 02-REQ-1.3, 02-REQ-1.E1, 02-REQ-1.E2
    """

    VALID_VALUES = {"direct", "branch", "pr"}

    @pytest.mark.parametrize("value", ["direct", "branch", "pr"])
    def test_valid_values_produce_valid_model(self, value: str) -> None:
        """Valid merge_strategy values produce a model with field in VALID_VALUES."""
        cfg = WorkspaceConfig(merge_strategy=value)
        assert cfg.merge_strategy in self.VALID_VALUES

    @pytest.mark.parametrize(
        "invalid_value",
        [
            "squash",
            "merge",
            "rebase",
            "",
            "DIRECT",
            "Branch",
            "PR",
            "  direct  ",
            "direct\n",
        ],
    )
    def test_invalid_strings_raise_validation_error(self, invalid_value: str) -> None:
        """Invalid string values raise ValidationError; no model is constructed."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy=invalid_value)

    def test_none_raises_validation_error(self) -> None:
        """None raises ValidationError; no model is constructed."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy=None)

    @pytest.mark.parametrize("invalid_value", [42, 3.14, True, False, ["direct"], {"mode": "pr"}])
    def test_non_string_types_raise_validation_error(self, invalid_value: object) -> None:
        """Non-string types raise ValidationError; no model is constructed."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy=invalid_value)

    @given(value=st.sampled_from(["direct", "branch", "pr"]))
    @settings(max_examples=20)
    def test_property_valid_values_always_accepted(self, value: str) -> None:
        """Property: any valid literal always produces a model with that value."""
        cfg = WorkspaceConfig(merge_strategy=value)
        assert cfg.merge_strategy == value
        assert cfg.merge_strategy in self.VALID_VALUES

    @given(value=st.text(min_size=0, max_size=50).filter(lambda s: s not in {"direct", "branch", "pr"}))
    @settings(max_examples=50)
    def test_property_invalid_values_always_rejected(self, value: str) -> None:
        """Property: any string not in {'direct','branch','pr'} raises ValidationError."""
        with pytest.raises(ValidationError):
            WorkspaceConfig(merge_strategy=value)

    def test_default_is_always_in_valid_set(self) -> None:
        """The default value (when field is absent) is always in VALID_VALUES."""
        cfg = WorkspaceConfig()
        assert cfg.merge_strategy in self.VALID_VALUES
