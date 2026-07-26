"""Tests for spec 06: PR lifecycle labels, dataclasses, and protocol extensions.

Task group 1 — failing tests for:
  - LABEL_PR constant and REQUIRED_LABELS bootstrap entry (TS-06-1, TS-06-2, TS-06-3, TS-06-E1)
  - PrResult and PrState frozen dataclasses (TS-06-4, TS-06-5, TS-06-E2)
  - CheckResult and ReviewComment frozen dataclasses (TS-06-6, TS-06-7, TS-06-8, TS-06-9, TS-06-E2)
  - PlatformProtocol new method declarations and NullPlatform stubs (TS-06-10 through TS-06-13)

Requirements: 06-REQ-1.1, 06-REQ-1.2, 06-REQ-1.3, 06-REQ-1.E1,
              06-REQ-2.1 through 06-REQ-2.6, 06-REQ-2.E1,
              06-REQ-3.1 through 06-REQ-3.4
"""

from __future__ import annotations

import dataclasses
import inspect
from typing import get_type_hints
from unittest.mock import AsyncMock, MagicMock, patch

import pytest

# ---------------------------------------------------------------------------
# TS-06-1: LABEL_PR constant and REQUIRED_LABELS bootstrap entry
# ---------------------------------------------------------------------------


class TestLabelPRConstant:
    """TS-06-1: LABEL_PR == 'af:pr' and REQUIRED_LABELS contains matching LabelSpec."""

    def test_label_pr_value(self) -> None:
        """LABEL_PR must equal 'af:pr'."""
        from afissues.labels import LABEL_PR

        assert LABEL_PR == "af:pr"

    def test_required_labels_contains_pr_spec(self) -> None:
        """REQUIRED_LABELS must contain a LabelSpec with name == 'af:pr'."""
        from afissues.labels import REQUIRED_LABELS

        names = {s.name for s in REQUIRED_LABELS}
        assert "af:pr" in names, f"'af:pr' not found in REQUIRED_LABELS names: {names}"

    def test_pr_label_spec_color(self) -> None:
        """The af:pr LabelSpec must have color '#1d76db'."""
        from afissues.labels import REQUIRED_LABELS

        pr_spec = next(s for s in REQUIRED_LABELS if s.name == "af:pr")
        assert pr_spec.color == "#1d76db"

    def test_pr_label_spec_description(self) -> None:
        """The af:pr LabelSpec must have description 'Pull request created — awaiting merge'."""
        from afissues.labels import REQUIRED_LABELS

        pr_spec = next(s for s in REQUIRED_LABELS if s.name == "af:pr")
        assert pr_spec.description == "Pull request created — awaiting merge"


# ---------------------------------------------------------------------------
# TS-06-2: Bootstrap routine creates af:pr label when absent
# ---------------------------------------------------------------------------


class TestBootstrapCreatesLabel:
    """TS-06-2: Bootstrap routine calls GitHub API to create af:pr label."""

    async def test_bootstrap_creates_af_pr_label(self) -> None:
        """Bootstrap must call create_label for af:pr when the label is absent."""
        from agentfox.workspace.init_project import _ensure_platform_labels_async

        from afissues.labels import REQUIRED_LABELS

        mock_platform = AsyncMock()
        mock_platform.create_label = AsyncMock(return_value=None)

        with (
            patch("agentfox.core.config.load_config", return_value=MagicMock()),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
        ):
            result = await _ensure_platform_labels_async(MagicMock())

        # Should create labels for all REQUIRED_LABELS (which now includes af:pr)
        assert result == len(REQUIRED_LABELS)

        # Verify af:pr was among the labels created
        call_names = [call.args[0] for call in mock_platform.create_label.call_args_list]
        assert "af:pr" in call_names, f"'af:pr' not in created labels: {call_names}"

        # Verify color matches
        pr_call = next(c for c in mock_platform.create_label.call_args_list if c.args[0] == "af:pr")
        assert pr_call.args[1] == "#1d76db"


# ---------------------------------------------------------------------------
# TS-06-3: LABEL_PR re-exported from top-level afissues
# ---------------------------------------------------------------------------


class TestLabelPRReExport:
    """TS-06-3: LABEL_PR importable from top-level afissues package."""

    def test_import_label_pr_from_afissues(self) -> None:
        """'from afissues import LABEL_PR' must succeed and equal 'af:pr'."""
        from afissues import LABEL_PR

        assert LABEL_PR == "af:pr"


# ---------------------------------------------------------------------------
# TS-06-E1: Bootstrap raises IntegrationError on label creation failure
# ---------------------------------------------------------------------------


class TestBootstrapLabelCreationError:
    """TS-06-E1: IntegrationError propagates when GitHub API fails on label creation."""

    async def test_label_creation_error_propagates(self) -> None:
        """Bootstrap must propagate IntegrationError from label creation failure."""
        from agentfox.workspace.init_project import _ensure_platform_labels_async

        from afissues.errors import IntegrationError

        mock_platform = AsyncMock()
        mock_platform.create_label = AsyncMock(side_effect=IntegrationError("500 error"))

        with (
            patch("agentfox.core.config.load_config", return_value=MagicMock()),
            patch(
                "agentfox.nightshift.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
        ):
            # The current bootstrap catches exceptions per-label and continues.
            # Spec 06-REQ-1.E1 requires IntegrationError to propagate and halt.
            # This test will fail until the bootstrap is updated.
            with pytest.raises(IntegrationError):
                await _ensure_platform_labels_async(MagicMock())


# ---------------------------------------------------------------------------
# TS-06-4: PrResult frozen dataclass
# ---------------------------------------------------------------------------


class TestPrResult:
    """TS-06-4: PrResult is a frozen dataclass with html_url: str and number: int."""

    def test_is_dataclass(self) -> None:
        from afissues.protocol import PrResult

        assert dataclasses.is_dataclass(PrResult)

    def test_is_frozen(self) -> None:
        from afissues.protocol import PrResult

        assert PrResult.__dataclass_params__.frozen is True

    def test_field_access(self) -> None:
        from afissues.protocol import PrResult

        r = PrResult(html_url="https://github.com/owner/repo/pull/1", number=1)
        assert r.html_url == "https://github.com/owner/repo/pull/1"
        assert r.number == 1

    def test_has_expected_fields(self) -> None:
        from afissues.protocol import PrResult

        fields = {f.name for f in dataclasses.fields(PrResult)}
        assert fields == {"html_url", "number"}


# ---------------------------------------------------------------------------
# TS-06-5: PrState frozen dataclass
# ---------------------------------------------------------------------------


class TestPrState:
    """TS-06-5: PrState is a frozen dataclass with number, state, merged, head_sha."""

    def test_is_dataclass(self) -> None:
        from afissues.protocol import PrState

        assert dataclasses.is_dataclass(PrState)

    def test_is_frozen(self) -> None:
        from afissues.protocol import PrState

        assert PrState.__dataclass_params__.frozen is True

    def test_field_access(self) -> None:
        from afissues.protocol import PrState

        s = PrState(number=42, state="open", merged=False, head_sha="abc123")
        assert s.number == 42
        assert s.state == "open"
        assert s.merged is False
        assert s.head_sha == "abc123"

    def test_has_expected_fields(self) -> None:
        from afissues.protocol import PrState

        fields = {f.name for f in dataclasses.fields(PrState)}
        assert fields == {"number", "state", "merged", "head_sha"}


# ---------------------------------------------------------------------------
# TS-06-6: CheckResult frozen dataclass
# ---------------------------------------------------------------------------


class TestCheckResult:
    """TS-06-6: CheckResult frozen dataclass with name, status, conclusion, output fields."""

    def test_is_dataclass(self) -> None:
        from afissues.protocol import CheckResult

        assert dataclasses.is_dataclass(CheckResult)

    def test_is_frozen(self) -> None:
        from afissues.protocol import CheckResult

        assert CheckResult.__dataclass_params__.frozen is True

    def test_field_access(self) -> None:
        from afissues.protocol import CheckResult

        c = CheckResult(
            name="ci",
            status="completed",
            conclusion="success",
            output_title="OK",
            output_summary="All passed",
        )
        assert c.name == "ci"
        assert c.status == "completed"
        assert c.conclusion == "success"
        assert c.output_title == "OK"
        assert c.output_summary == "All passed"

    def test_conclusion_accepts_none(self) -> None:
        from afissues.protocol import CheckResult

        c = CheckResult(name="ci", status="queued", conclusion=None, output_title="", output_summary="")
        assert c.conclusion is None

    def test_has_expected_fields(self) -> None:
        from afissues.protocol import CheckResult

        fields = {f.name for f in dataclasses.fields(CheckResult)}
        assert fields == {"name", "status", "conclusion", "output_title", "output_summary"}


# ---------------------------------------------------------------------------
# TS-06-7: ReviewComment frozen dataclass
# ---------------------------------------------------------------------------


class TestReviewComment:
    """TS-06-7: ReviewComment frozen dataclass with user, state, body, submitted_at."""

    def test_is_dataclass(self) -> None:
        from afissues.protocol import ReviewComment

        assert dataclasses.is_dataclass(ReviewComment)

    def test_is_frozen(self) -> None:
        from afissues.protocol import ReviewComment

        assert ReviewComment.__dataclass_params__.frozen is True

    def test_field_access(self) -> None:
        from afissues.protocol import ReviewComment

        r = ReviewComment(
            user="alice",
            state="APPROVED",
            body="LGTM",
            submitted_at="2026-07-26T09:31:34Z",
        )
        assert r.user == "alice"
        assert r.state == "APPROVED"
        assert r.body == "LGTM"
        assert r.submitted_at == "2026-07-26T09:31:34Z"

    def test_submitted_at_stored_as_raw_string(self) -> None:
        """submitted_at must be stored as-is without parsing to datetime."""
        from afissues.protocol import ReviewComment

        ts = "2026-07-26T09:31:34Z"
        r = ReviewComment(user="alice", state="APPROVED", body="LGTM", submitted_at=ts)
        assert isinstance(r.submitted_at, str)
        assert r.submitted_at == ts

    def test_has_expected_fields(self) -> None:
        from afissues.protocol import ReviewComment

        fields = {f.name for f in dataclasses.fields(ReviewComment)}
        assert fields == {"user", "state", "body", "submitted_at"}


# ---------------------------------------------------------------------------
# TS-06-8: CheckResult null output maps to empty strings
# ---------------------------------------------------------------------------


class TestCheckResultNullOutput:
    """TS-06-8: When GitHub check-run output is null, output_title and output_summary are ''."""

    def test_null_output_maps_to_empty_strings(self) -> None:
        """Constructing CheckResult with empty strings for null output must work."""
        from afissues.protocol import CheckResult

        # Simulates what the mapping code should do with output: null
        check_run_data = {
            "name": "lint",
            "status": "completed",
            "conclusion": "failure",
            "output": None,
        }

        output = check_run_data["output"]
        result = CheckResult(
            name=check_run_data["name"],
            status=check_run_data["status"],
            conclusion=check_run_data["conclusion"],
            output_title=output["title"] if output else "",
            output_summary=output["summary"] if output else "",
        )

        assert result.output_title == ""
        assert result.output_summary == ""
        assert isinstance(result.output_title, str)
        assert isinstance(result.output_summary, str)


# ---------------------------------------------------------------------------
# TS-06-9: All four dataclasses importable from top-level afissues
# ---------------------------------------------------------------------------


class TestDataclassReExports:
    """TS-06-9: PrResult, PrState, CheckResult, ReviewComment importable from afissues."""

    def test_import_pr_result(self) -> None:
        from afissues import PrResult

        assert PrResult is not None

    def test_import_pr_state(self) -> None:
        from afissues import PrState

        assert PrState is not None

    def test_import_check_result(self) -> None:
        from afissues import CheckResult

        assert CheckResult is not None

    def test_import_review_comment(self) -> None:
        from afissues import ReviewComment

        assert ReviewComment is not None

    def test_reexported_types_match_submodule(self) -> None:
        """Re-exported symbols must be the same objects as in afissues.protocol."""
        import afissues
        from afissues import protocol

        assert afissues.PrResult is protocol.PrResult
        assert afissues.PrState is protocol.PrState
        assert afissues.CheckResult is protocol.CheckResult
        assert afissues.ReviewComment is protocol.ReviewComment


# ---------------------------------------------------------------------------
# TS-06-E2: Frozen dataclass mutation raises FrozenInstanceError
# ---------------------------------------------------------------------------


class TestFrozenDataclassMutation:
    """TS-06-E2: Mutating fields on any of the four frozen dataclasses raises FrozenInstanceError."""

    def test_pr_result_mutation_raises(self) -> None:
        from afissues.protocol import PrResult

        r = PrResult(html_url="u", number=1)
        with pytest.raises(dataclasses.FrozenInstanceError):
            r.number = 2  # type: ignore[misc]

    def test_pr_state_mutation_raises(self) -> None:
        from afissues.protocol import PrState

        s = PrState(number=1, state="open", merged=False, head_sha="x")
        with pytest.raises(dataclasses.FrozenInstanceError):
            s.state = "closed"  # type: ignore[misc]

    def test_check_result_mutation_raises(self) -> None:
        from afissues.protocol import CheckResult

        c = CheckResult(name="n", status="s", conclusion=None, output_title="", output_summary="")
        with pytest.raises(dataclasses.FrozenInstanceError):
            c.name = "x"  # type: ignore[misc]

    def test_review_comment_mutation_raises(self) -> None:
        from afissues.protocol import ReviewComment

        r = ReviewComment(user="u", state="APPROVED", body="b", submitted_at="2026-01-01T00:00:00Z")
        with pytest.raises(dataclasses.FrozenInstanceError):
            r.user = "z"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-06-10: PlatformProtocol.get_pr_state method declaration
# ---------------------------------------------------------------------------


class TestProtocolGetPrState:
    """TS-06-10: PlatformProtocol declares get_pr_state(pr_number: int) -> PrState."""

    def test_method_exists(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert hasattr(PlatformProtocol, "get_pr_state")

    def test_is_coroutine_function(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert inspect.iscoroutinefunction(PlatformProtocol.get_pr_state)

    def test_return_type_is_pr_state(self) -> None:
        from afissues.protocol import PlatformProtocol, PrState

        hints = get_type_hints(PlatformProtocol.get_pr_state)
        assert hints["return"] is PrState

    def test_accepts_pr_number_int(self) -> None:
        from afissues.protocol import PlatformProtocol

        hints = get_type_hints(PlatformProtocol.get_pr_state)
        assert hints["pr_number"] is int


# ---------------------------------------------------------------------------
# TS-06-11: PlatformProtocol.get_pr_checks method declaration
# ---------------------------------------------------------------------------


class TestProtocolGetPrChecks:
    """TS-06-11: PlatformProtocol declares get_pr_checks(pr_number: int) -> list[CheckResult]."""

    def test_method_exists(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert hasattr(PlatformProtocol, "get_pr_checks")

    def test_is_coroutine_function(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert inspect.iscoroutinefunction(PlatformProtocol.get_pr_checks)

    def test_return_type_is_list_check_result(self) -> None:
        from afissues.protocol import CheckResult, PlatformProtocol

        hints = get_type_hints(PlatformProtocol.get_pr_checks)
        assert hints["return"] == list[CheckResult]

    def test_accepts_pr_number_int(self) -> None:
        from afissues.protocol import PlatformProtocol

        hints = get_type_hints(PlatformProtocol.get_pr_checks)
        assert hints["pr_number"] is int


# ---------------------------------------------------------------------------
# TS-06-12: PlatformProtocol.get_pr_reviews method declaration
# ---------------------------------------------------------------------------


class TestProtocolGetPrReviews:
    """TS-06-12: PlatformProtocol declares get_pr_reviews(pr_number: int) -> list[ReviewComment]."""

    def test_method_exists(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert hasattr(PlatformProtocol, "get_pr_reviews")

    def test_is_coroutine_function(self) -> None:
        from afissues.protocol import PlatformProtocol

        assert inspect.iscoroutinefunction(PlatformProtocol.get_pr_reviews)

    def test_return_type_is_list_review_comment(self) -> None:
        from afissues.protocol import PlatformProtocol, ReviewComment

        hints = get_type_hints(PlatformProtocol.get_pr_reviews)
        assert hints["return"] == list[ReviewComment]

    def test_accepts_pr_number_int(self) -> None:
        from afissues.protocol import PlatformProtocol

        hints = get_type_hints(PlatformProtocol.get_pr_reviews)
        assert hints["pr_number"] is int


# ---------------------------------------------------------------------------
# TS-06-13: NullPlatform new methods raise NotImplementedError
# ---------------------------------------------------------------------------


class TestNullPlatformPrMethods:
    """TS-06-13: NullPlatform.get_pr_state/checks/reviews raise NotImplementedError."""

    async def test_get_pr_state_raises(self) -> None:
        from afissues.protocol import NullPlatform

        null = NullPlatform()
        with pytest.raises(NotImplementedError):
            await null.get_pr_state(1)

    async def test_get_pr_checks_raises(self) -> None:
        from afissues.protocol import NullPlatform

        null = NullPlatform()
        with pytest.raises(NotImplementedError):
            await null.get_pr_checks(1)

    async def test_get_pr_reviews_raises(self) -> None:
        from afissues.protocol import NullPlatform

        null = NullPlatform()
        with pytest.raises(NotImplementedError):
            await null.get_pr_reviews(1)
