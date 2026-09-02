"""Property-based tests for spec 06: PR lifecycle correctness properties.

Task group 3 — property tests (subtask 3.4) for:
  - TS-06-P3: null output yields output_title='' and output_summary='' as str
  - TS-06-P4: all four dataclasses raise FrozenInstanceError on any field mutation
  - TS-06-P6: NullPlatform always raises NotImplementedError for new PR methods

Requirements: 06-REQ-2.1 through 06-REQ-2.5, 06-REQ-5.3, 06-REQ-3.4
"""

from __future__ import annotations

import dataclasses

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st


# ---------------------------------------------------------------------------
# TS-06-P3: CheckResult null output — always non-optional strings.
#
# Property: 06-PROP-3
# Validates: 06-REQ-2.5, 06-REQ-5.3
# ---------------------------------------------------------------------------


class TestCheckResultNullOutput:
    """TS-06-P3: null output always yields output_title='' and output_summary='' as str."""

    @given(
        name=st.text(min_size=1, max_size=100),
        status=st.sampled_from(["queued", "in_progress", "completed"]),
        conclusion=st.one_of(
            st.none(),
            st.sampled_from(["success", "failure", "neutral", "cancelled", "timed_out"]),
        ),
    )
    @settings(max_examples=50, deadline=None)
    def test_null_output_yields_empty_strings(
        self,
        name: str,
        status: str,
        conclusion: str | None,
    ) -> None:
        """CheckResult with null output has output_title=='' and output_summary==''."""
        from afissues.protocol import CheckResult

        result = CheckResult(
            name=name,
            status=status,
            conclusion=conclusion,
            output_title="",
            output_summary="",
        )
        assert result.output_title == ""
        assert result.output_summary == ""
        assert isinstance(result.output_title, str)
        assert isinstance(result.output_summary, str)


# ---------------------------------------------------------------------------
# TS-06-P4: Dataclass immutability — all four types are frozen.
#
# Property: 06-PROP-4
# Validates: 06-REQ-2.1, 06-REQ-2.2, 06-REQ-2.3, 06-REQ-2.4
# ---------------------------------------------------------------------------


class TestDataclassImmutability:
    """TS-06-P4: all four dataclasses raise FrozenInstanceError on mutation."""

    @given(
        html_url=st.text(min_size=1, max_size=200),
        number=st.integers(min_value=1, max_value=10**9),
    )
    @settings(max_examples=20, deadline=None)
    def test_pr_result_frozen(self, html_url: str, number: int) -> None:
        """PrResult raises FrozenInstanceError on field assignment."""
        from afissues.protocol import PrResult

        instance = PrResult(html_url=html_url, number=number)
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.html_url = "mutated"  # type: ignore[misc]
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.number = 999  # type: ignore[misc]

    @given(
        number=st.integers(min_value=1, max_value=10**9),
        state=st.sampled_from(["open", "closed"]),
        merged=st.booleans(),
        head_sha=st.text(min_size=40, max_size=40, alphabet="0123456789abcdef"),
    )
    @settings(max_examples=20, deadline=None)
    def test_pr_state_frozen(
        self,
        number: int,
        state: str,
        merged: bool,
        head_sha: str,
    ) -> None:
        """PrState raises FrozenInstanceError on field assignment."""
        from afissues.protocol import PrState

        instance = PrState(number=number, state=state, merged=merged, head_sha=head_sha)
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.number = 999  # type: ignore[misc]
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.state = "mutated"  # type: ignore[misc]
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.merged = not merged  # type: ignore[misc]
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.head_sha = "mutated"  # type: ignore[misc]

    @given(
        name=st.text(min_size=1, max_size=100),
        status=st.sampled_from(["queued", "in_progress", "completed"]),
        conclusion=st.one_of(st.none(), st.sampled_from(["success", "failure"])),
        output_title=st.text(min_size=0, max_size=100),
        output_summary=st.text(min_size=0, max_size=100),
    )
    @settings(max_examples=20, deadline=None)
    def test_check_result_frozen(
        self,
        name: str,
        status: str,
        conclusion: str | None,
        output_title: str,
        output_summary: str,
    ) -> None:
        """CheckResult raises FrozenInstanceError on field assignment."""
        from afissues.protocol import CheckResult

        instance = CheckResult(
            name=name,
            status=status,
            conclusion=conclusion,
            output_title=output_title,
            output_summary=output_summary,
        )
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.name = "mutated"  # type: ignore[misc]

    @given(
        user=st.text(min_size=1, max_size=50),
        state=st.sampled_from(["APPROVED", "CHANGES_REQUESTED", "COMMENTED"]),
        body=st.text(min_size=0, max_size=200),
        submitted_at=st.text(min_size=10, max_size=30),
    )
    @settings(max_examples=20, deadline=None)
    def test_review_comment_frozen(
        self,
        user: str,
        state: str,
        body: str,
        submitted_at: str,
    ) -> None:
        """ReviewComment raises FrozenInstanceError on field assignment."""
        from afissues.protocol import ReviewComment

        instance = ReviewComment(
            user=user,
            state=state,
            body=body,
            submitted_at=submitted_at,
        )
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.user = "mutated"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-06-P6: NullPlatform always raises NotImplementedError for new methods.
#
# Property: 06-PROP-6
# Validates: 06-REQ-3.4
# ---------------------------------------------------------------------------


class TestNullPlatformAlwaysRaises:
    """TS-06-P6: NullPlatform raises NotImplementedError for all three new methods."""

    @given(pr_number=st.integers(min_value=1, max_value=10**9))
    @settings(max_examples=20, deadline=None)
    @pytest.mark.asyncio
    async def test_get_pr_state_raises(self, pr_number: int) -> None:
        """NullPlatform.get_pr_state always raises NotImplementedError."""
        from afissues.protocol import NullPlatform

        null = NullPlatform()
        with pytest.raises(NotImplementedError):
            await null.get_pr_state(pr_number)

    @given(pr_number=st.integers(min_value=1, max_value=10**9))
    @settings(max_examples=20, deadline=None)
    @pytest.mark.asyncio
    async def test_get_pr_checks_raises(self, pr_number: int) -> None:
        """NullPlatform.get_pr_checks always raises NotImplementedError."""
        from afissues.protocol import NullPlatform

        null = NullPlatform()
        with pytest.raises(NotImplementedError):
            await null.get_pr_checks(pr_number)

    @given(pr_number=st.integers(min_value=1, max_value=10**9))
    @settings(max_examples=20, deadline=None)
    @pytest.mark.asyncio
    async def test_get_pr_reviews_raises(self, pr_number: int) -> None:
        """NullPlatform.get_pr_reviews always raises NotImplementedError."""
        from afissues.protocol import NullPlatform

        null = NullPlatform()
        with pytest.raises(NotImplementedError):
            await null.get_pr_reviews(pr_number)
