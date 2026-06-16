"""Tests for af:ignore label constants and NightShiftConfig.similarity_threshold.

Test cases: TS-110-1, TS-110-2, TS-110-3, TS-110-16

Requirements: 110-REQ-1.1, 110-REQ-1.2, 110-REQ-1.3, 110-REQ-7.1
"""

from __future__ import annotations


class TestLabelIgnoreConstant:
    """TS-110-1: LABEL_IGNORE is defined with value 'af:ignore'."""

    def test_label_constant_value(self) -> None:
        """LABEL_IGNORE == 'af:ignore'."""
        from agentfox.platform.labels import LABEL_IGNORE

        assert LABEL_IGNORE == "af:ignore"


class TestLabelIgnoreColor:
    """TS-110-2: LABEL_IGNORE_COLOR is a 6-character gray hex value."""

    def test_label_color_is_six_chars(self) -> None:
        """LABEL_IGNORE_COLOR is exactly 6 characters long."""
        from agentfox.platform.labels import LABEL_IGNORE_COLOR

        assert len(LABEL_IGNORE_COLOR) == 6

    def test_label_color_is_hex(self) -> None:
        """LABEL_IGNORE_COLOR contains only valid hex characters."""
        from agentfox.platform.labels import LABEL_IGNORE_COLOR

        assert all(c in "0123456789abcdef" for c in LABEL_IGNORE_COLOR.lower())


class TestLabelInRequiredLabels:
    """TS-110-3: af:ignore appears in REQUIRED_LABELS with correct metadata."""

    def test_af_ignore_in_required_labels(self) -> None:
        """REQUIRED_LABELS contains exactly one entry with name 'af:ignore'."""
        from agentfox.platform.labels import REQUIRED_LABELS

        matches = [s for s in REQUIRED_LABELS if s.name == "af:ignore"]
        assert len(matches) == 1

    def test_af_ignore_description_mentions_not_an_issue(self) -> None:
        """The af:ignore label description mentions 'not-an-issue'."""
        from agentfox.platform.labels import REQUIRED_LABELS

        specs = [s for s in REQUIRED_LABELS if s.name == "af:ignore"]
        assert len(specs) == 1
        assert "not-an-issue" in specs[0].description

    def test_af_ignore_has_color(self) -> None:
        """The af:ignore label has a non-empty color."""
        from agentfox.platform.labels import REQUIRED_LABELS

        specs = [s for s in REQUIRED_LABELS if s.name == "af:ignore"]
        assert len(specs) == 1
        assert len(specs[0].color) == 6


