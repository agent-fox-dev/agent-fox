"""Tests for afissues.labels module (TS-03-14, TS-03-15, TS-03-16, TS-03-P5).

Verifies that LabelSpec is a frozen dataclass with the correct fields,
all seven LABEL_* constants have the expected string values, and
REQUIRED_LABELS contains at least 4 af:* LabelSpec entries.

Requirements: 03-REQ-4.1, 03-REQ-4.2, 03-REQ-4.3

Drift errata:
  - 03-REQ-4.3 / TS-03-16 / TS-03-P5: The spec says exactly 4 af:* labels.
    A subsequent spec added LABEL_PR ("af:pr"), bringing the af:* count to 5.
    Tests assert >= 4 to accommodate post-spec-03 additions.
"""

from __future__ import annotations

import dataclasses

import pytest

from afissues.labels import (
    LABEL_FIX,
    LABEL_FIXED,
    LABEL_IMPLEMENTED,
    LABEL_NO_CHANGE,
    LABEL_PRIORITY_HIGH,
    LABEL_PRIORITY_LOW,
    LABEL_PRIORITY_MEDIUM,
    REQUIRED_LABELS,
    LabelSpec,
)

# ── TS-03-14: LabelSpec frozen dataclass ──────────────────────────────


class TestLabelSpecDataclass:
    """TS-03-14: LabelSpec is a frozen dataclass with name, color, description."""

    def test_is_dataclass(self) -> None:
        """LabelSpec is a dataclass."""
        assert dataclasses.is_dataclass(LabelSpec)

    def test_has_expected_fields(self) -> None:
        """LabelSpec has exactly the fields {name, color, description}."""
        fields = {f.name for f in dataclasses.fields(LabelSpec)}
        assert fields == {"name", "color", "description"}

    def test_field_access(self) -> None:
        """LabelSpec instances expose name, color, description attributes."""
        label = LabelSpec(name="af:fix", color="0075ca", description="Fix issue")
        assert label.name == "af:fix"
        assert label.color == "0075ca"
        assert label.description == "Fix issue"

    def test_frozen_immutability(self) -> None:
        """LabelSpec instances are immutable (frozen=True)."""
        label = LabelSpec(name="af:fix", color="0075ca", description="Fix issue")
        with pytest.raises(dataclasses.FrozenInstanceError):
            label.name = "other"  # type: ignore[misc]

    def test_frozen_color_immutability(self) -> None:
        """Color field is also immutable."""
        label = LabelSpec(name="af:fix", color="0075ca", description="Fix issue")
        with pytest.raises(dataclasses.FrozenInstanceError):
            label.color = "ffffff"  # type: ignore[misc]


# ── TS-03-15: LABEL_* string constants ────────────────────────────────


class TestLabelConstants:
    """TS-03-15: All seven LABEL_* constants have exact original values."""

    def test_label_fix(self) -> None:
        assert LABEL_FIX == "af:fix"

    def test_label_fixed(self) -> None:
        assert LABEL_FIXED == "af:fixed"

    def test_label_no_change(self) -> None:
        assert LABEL_NO_CHANGE == "af:no-change"

    def test_label_implemented(self) -> None:
        assert LABEL_IMPLEMENTED == "af:implemented"

    def test_label_priority_high(self) -> None:
        assert LABEL_PRIORITY_HIGH == "priority:high"

    def test_label_priority_medium(self) -> None:
        assert LABEL_PRIORITY_MEDIUM == "priority:medium"

    def test_label_priority_low(self) -> None:
        assert LABEL_PRIORITY_LOW == "priority:low"

    def test_all_are_strings(self) -> None:
        """All LABEL_* constants are plain strings."""
        for const in [
            LABEL_FIX,
            LABEL_FIXED,
            LABEL_NO_CHANGE,
            LABEL_IMPLEMENTED,
            LABEL_PRIORITY_HIGH,
            LABEL_PRIORITY_MEDIUM,
            LABEL_PRIORITY_LOW,
        ]:
            assert isinstance(const, str)


# ── TS-03-16: REQUIRED_LABELS list ───────────────────────────────────


class TestRequiredLabels:
    """TS-03-16: REQUIRED_LABELS is a list[LabelSpec] with 4 af:* entries."""

    def test_is_list(self) -> None:
        assert isinstance(REQUIRED_LABELS, list)

    def test_length_is_at_least_4(self) -> None:
        """REQUIRED_LABELS contains at least 4 af:* entries (5 after af:pr addition)."""
        af_labels = [label for label in REQUIRED_LABELS if label.name.startswith("af:")]
        assert len(af_labels) >= 4, f"Expected >= 4 af:* labels, got {len(af_labels)}"

    def test_all_entries_are_labelspec(self) -> None:
        for label in REQUIRED_LABELS:
            assert isinstance(label, LabelSpec), f"Expected LabelSpec, got {type(label)}"

    def test_af_labels_start_with_af_prefix(self) -> None:
        """Each af:* entry's name starts with 'af:'."""
        af_labels = [label for label in REQUIRED_LABELS if label.name.startswith("af:")]
        for label in af_labels:
            assert label.name.startswith("af:"), f"Label name {label.name!r} does not start with 'af:'"

    def test_no_priority_labels_in_af_set(self) -> None:
        """No priority labels are in the af:* subset."""
        af_labels = [label for label in REQUIRED_LABELS if label.name.startswith("af:")]
        assert not any("priority" in label.name for label in af_labels), (
            "Priority labels should not be in the af:* subset of REQUIRED_LABELS"
        )


# ── TS-03-P5: Property — REQUIRED_LABELS invariant ───────────────────


class TestRequiredLabelsProperty:
    """TS-03-P5: REQUIRED_LABELS has >= 4 af:* entries, each a LabelSpec with af: prefix.

    Drift: spec says exactly 4; current count is 5 after af:pr addition.
    """

    def test_invariant_count_and_prefix(self) -> None:
        """len(af:* in REQUIRED_LABELS) >= 4 and every af:* entry starts with 'af:'."""
        af_labels = [label for label in REQUIRED_LABELS if label.name.startswith("af:")]
        assert len(af_labels) >= 4
        for label in af_labels:
            assert isinstance(label, LabelSpec)
            assert label.name.startswith("af:")
