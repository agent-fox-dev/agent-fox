"""Tests for afissues.__init__ public re-export surface (TS-03-22).

Verifies that all 15 spec-03 public symbols are re-exported from the
top-level ``afissues`` namespace and importable without knowing which
sub-module each symbol lives in.

Note: __all__ may contain additional symbols from later specs (e.g.
GiteaPlatform from spec 04/05). Tests assert the 15 spec-03 symbols
are present without constraining the total count.

Requirements: 03-REQ-6.1
"""

from __future__ import annotations

import afissues

# The 15 spec-03 public symbols that must be re-exported.
_SPEC_03_SYMBOLS = [
    # afissues.protocol
    "PlatformProtocol",
    "NullPlatform",
    "IssueResult",
    "IssueComment",
    # afissues.github
    "GitHubPlatform",
    "parse_github_remote",
    # afissues.labels
    "LabelSpec",
    "LABEL_FIX",
    "LABEL_FIXED",
    "LABEL_NO_CHANGE",
    "LABEL_IMPLEMENTED",
    "LABEL_PRIORITY_HIGH",
    "LABEL_PRIORITY_MEDIUM",
    "LABEL_PRIORITY_LOW",
    "REQUIRED_LABELS",
]


# ── TS-03-22: All public symbols re-exported from afissues ─────────


class TestPublicReExports:
    """TS-03-22: All 15 spec-03 symbols importable from top-level afissues."""

    def test_all_15_symbols_in_namespace(self) -> None:
        """Every spec-03 symbol is accessible as an attribute of afissues."""
        missing = [s for s in _SPEC_03_SYMBOLS if not hasattr(afissues, s)]
        assert not missing, f"Missing symbols from afissues namespace: {missing}"

    def test_all_15_symbols_in_all(self) -> None:
        """Every spec-03 symbol appears in afissues.__all__."""
        missing = [s for s in _SPEC_03_SYMBOLS if s not in afissues.__all__]
        assert not missing, f"Symbols missing from __all__: {missing}"

    def test_import_protocol_types(self) -> None:
        """PlatformProtocol, NullPlatform, IssueResult, IssueComment importable."""
        from afissues import IssueComment, IssueResult, NullPlatform, PlatformProtocol

        assert PlatformProtocol is not None
        assert NullPlatform is not None
        assert IssueResult is not None
        assert IssueComment is not None

    def test_import_github_types(self) -> None:
        """GitHubPlatform and parse_github_remote importable."""
        from afissues import GitHubPlatform, parse_github_remote

        assert GitHubPlatform is not None
        assert parse_github_remote is not None

    def test_import_label_constants(self) -> None:
        """All label constants and LabelSpec importable."""
        from afissues import (
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

        assert LabelSpec is not None
        assert LABEL_FIX == "af:fix"
        assert LABEL_FIXED == "af:fixed"
        assert LABEL_NO_CHANGE == "af:no-change"
        assert LABEL_IMPLEMENTED == "af:implemented"
        assert LABEL_PRIORITY_HIGH == "priority:high"
        assert LABEL_PRIORITY_MEDIUM == "priority:medium"
        assert LABEL_PRIORITY_LOW == "priority:low"
        assert REQUIRED_LABELS is not None

    def test_reexported_protocol_types_match_submodule(self) -> None:
        """Re-exported symbols are the same objects as in their source modules."""
        from afissues import protocol

        assert afissues.PlatformProtocol is protocol.PlatformProtocol
        assert afissues.NullPlatform is protocol.NullPlatform
        assert afissues.IssueResult is protocol.IssueResult
        assert afissues.IssueComment is protocol.IssueComment

    def test_reexported_github_types_match_submodule(self) -> None:
        """Re-exported GitHub symbols are the same objects as in afissues.github."""
        from afissues import github

        assert afissues.GitHubPlatform is github.GitHubPlatform
        assert afissues.parse_github_remote is github.parse_github_remote

    def test_reexported_label_constants_match_submodule(self) -> None:
        """Re-exported label symbols are the same objects as in afissues.labels."""
        from afissues import labels

        assert afissues.LabelSpec is labels.LabelSpec
        assert afissues.LABEL_FIX is labels.LABEL_FIX
        assert afissues.LABEL_FIXED is labels.LABEL_FIXED
        assert afissues.LABEL_NO_CHANGE is labels.LABEL_NO_CHANGE
        assert afissues.LABEL_IMPLEMENTED is labels.LABEL_IMPLEMENTED
        assert afissues.LABEL_PRIORITY_HIGH is labels.LABEL_PRIORITY_HIGH
        assert afissues.LABEL_PRIORITY_MEDIUM is labels.LABEL_PRIORITY_MEDIUM
        assert afissues.LABEL_PRIORITY_LOW is labels.LABEL_PRIORITY_LOW
        assert afissues.REQUIRED_LABELS is labels.REQUIRED_LABELS

    def test_total_symbol_count_at_least_15(self) -> None:
        """__all__ contains at least the 15 spec-03 symbols (may have more from later specs)."""
        assert len(afissues.__all__) >= 15, (
            f"Expected at least 15 symbols in __all__, got {len(afissues.__all__)}"
        )
