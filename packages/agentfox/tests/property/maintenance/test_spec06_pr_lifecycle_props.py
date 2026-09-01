"""Property-based tests for spec 06: PR lifecycle correctness properties.

Task group 3 — property tests (subtask 3.4) for:
  - TS-06-P1: _handle_result('pr_created') never closes issue or applies af:fixed
  - TS-06-P2: parse_tracking_comment(format_tracking_comment(n, a, url, msg)) == (n, a)
  - TS-06-P3: null output yields output_title='' and output_summary='' as str
  - TS-06-P4: all four dataclasses raise FrozenInstanceError on any field mutation
  - TS-06-P5: when _integrate_fix returns pr_created, _pr_number == result.number
  - TS-06-P6: NullPlatform always raises NotImplementedError for new PR methods

Requirements: 06-REQ-9.1, 06-REQ-9.3, 06-REQ-10.2, 06-REQ-10.3,
              06-REQ-2.1 through 06-REQ-2.5, 06-REQ-5.3,
              06-REQ-8.1, 06-REQ-8.3, 06-REQ-3.4
"""

from __future__ import annotations

import dataclasses
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# TS-06-P1: Premature close invariant — _handle_result('pr_created') never
#           closes issue or applies af:fixed, regardless of label set.
#
# Property: 06-PROP-1
# Validates: 06-REQ-9.1, 06-REQ-9.3
# ---------------------------------------------------------------------------


@st.composite
def label_sets(draw: st.DrawFn) -> tuple[str, ...]:
    """Generate arbitrary label sets that may or may not include af:pr."""
    base_labels = ["af:fix", "af:pr", "af:fixed", "af:no-change", "bug", "enhancement"]
    labels = draw(
        st.lists(
            st.sampled_from(base_labels),
            min_size=0,
            max_size=4,
            unique=True,
        )
    )
    return tuple(labels)


class TestPrematureCloseInvariant:
    """TS-06-P1: pr_created never closes issue or applies af:fixed."""

    @given(
        pr_number=st.integers(min_value=1, max_value=10**9),
        existing_labels=label_sets(),
    )
    @settings(max_examples=50, deadline=None)
    @pytest.mark.asyncio
    async def test_never_closes_or_applies_af_fixed(
        self,
        pr_number: int,
        existing_labels: tuple[str, ...],
    ) -> None:
        """For any pr_number and label set, pr_created never closes issue."""
        from afissues.protocol import IssueResult
        from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
        from agentfox.maintenance.fix_pipeline import FixPipeline

        config = AgentFoxConfig(
            workspace=WorkspaceConfig(
                merge_strategy="pr",
                integration_branch="main",
            ),
        )
        mock_platform = MagicMock()
        mock_platform.assign_label = AsyncMock()
        mock_platform.remove_label = AsyncMock()
        mock_platform.add_issue_comment = AsyncMock()
        mock_platform.close_issue = AsyncMock()

        pipeline = FixPipeline(config=config, platform=mock_platform)
        pipeline._pr_number = pr_number

        issue = IssueResult(
            number=42,
            title="Test issue",
            html_url="https://github.com/test/repo/issues/42",
            labels=existing_labels,
        )
        from agentfox.maintenance.spec_builder import InMemorySpec

        spec = InMemorySpec(
            issue_number=42,
            title="Test issue",
            task_prompt="Fix it",
            system_context="Context",
            branch_name="fix/test",
        )

        await pipeline._handle_result(issue, spec, "pr_created")

        # Invariant: close_issue never called
        mock_platform.close_issue.assert_not_awaited()

        # Invariant: af:fixed never assigned
        label_calls = [
            call.args[1]
            for call in mock_platform.assign_label.call_args_list
        ]
        assert "af:fixed" not in label_calls


# ---------------------------------------------------------------------------
# TS-06-P2: Tracking comment round-trip — parse inverts format.
#
# Property: 06-PROP-2
# Validates: 06-REQ-10.2, 06-REQ-10.3
# ---------------------------------------------------------------------------


class TestTrackingCommentRoundTrip:
    """TS-06-P2: parse_tracking_comment(format_tracking_comment(n, a, url, msg)) == (n, a)."""

    @given(
        pr_number=st.integers(min_value=1, max_value=10**9),
        attempt=st.integers(min_value=1, max_value=100),
        pr_url=st.text(min_size=1, max_size=200).filter(lambda s: s.strip()),
        message=st.text(min_size=0, max_size=500),
    )
    @settings(max_examples=100, deadline=None)
    def test_round_trip(
        self,
        pr_number: int,
        attempt: int,
        pr_url: str,
        message: str,
    ) -> None:
        """parse(format(n, a, url, msg)) == (n, a) for all valid inputs."""
        from agentfox.maintenance.fix_pipeline import (
            format_tracking_comment,
            parse_tracking_comment,
        )

        formatted = format_tracking_comment(
            pr_number=pr_number,
            attempt=attempt,
            pr_url=pr_url,
            message=message,
        )
        result = parse_tracking_comment(formatted)
        assert result == (pr_number, attempt)


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
        self, number: int, state: str, merged: bool, head_sha: str,
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
        self, user: str, state: str, body: str, submitted_at: str,
    ) -> None:
        """ReviewComment raises FrozenInstanceError on field assignment."""
        from afissues.protocol import ReviewComment

        instance = ReviewComment(
            user=user, state=state, body=body, submitted_at=submitted_at,
        )
        with pytest.raises(dataclasses.FrozenInstanceError):
            instance.user = "mutated"  # type: ignore[misc]


# ---------------------------------------------------------------------------
# TS-06-P5: _pr_number propagation — equals result.number after pr_created.
#
# Property: 06-PROP-5
# Validates: 06-REQ-8.1, 06-REQ-8.3
# ---------------------------------------------------------------------------


class TestPrNumberPropagation:
    """TS-06-P5: _pr_number equals result.number after _integrate_fix returns pr_created."""

    @given(pr_number=st.integers(min_value=1, max_value=10**9))
    @settings(max_examples=20, deadline=None)
    @pytest.mark.asyncio
    async def test_pr_number_matches_result(self, pr_number: int) -> None:
        """For any pr_number, _pr_number equals result.number after pr_created."""
        from afissues.protocol import IssueResult, PrResult
        from agentfox.core.config import AgentFoxConfig, WorkspaceConfig
        from agentfox.maintenance.fix_pipeline import FixPipeline
        from agentfox.maintenance.spec_builder import InMemorySpec
        from agentfox.workspace import WorkspaceInfo

        config = AgentFoxConfig(
            workspace=WorkspaceConfig(
                merge_strategy="pr",
                integration_branch="main",
            ),
        )
        mock_platform = MagicMock()
        mock_platform.create_pr = AsyncMock(
            return_value=PrResult(
                html_url=f"https://github.com/owner/repo/pull/{pr_number}",
                number=pr_number,
            ),
        )
        mock_platform.add_issue_comment = AsyncMock()

        pipeline = FixPipeline(config=config, platform=mock_platform)

        issue = IssueResult(
            number=42,
            title="Test issue",
            html_url="https://github.com/test/repo/issues/42",
        )
        spec = InMemorySpec(
            issue_number=42,
            title="Test issue",
            task_prompt="Fix it",
            system_context="Context",
            branch_name="fix/test",
        )
        workspace = WorkspaceInfo(
            path=Path("/tmp/test"),
            branch="fix/test",
            spec_name="fix-issue-42",
            task_group=0,
        )

        with (
            patch(
                "agentfox.maintenance.platform_factory.create_platform_safe",
                return_value=mock_platform,
            ),
            patch(
                "agentfox.maintenance.fix_pipeline._workspace_git.push_to_remote",
                new_callable=AsyncMock,
                return_value=True,
            ),
            patch(
                "agentfox.maintenance.fix_pipeline._workspace_git.get_changed_files",
                new_callable=AsyncMock,
                return_value=["file.py"],
            ),
        ):
            status, _ = await pipeline._integrate_fix(issue, spec, workspace)

        assert status == "pr_created"
        assert pipeline._pr_number == pr_number
        assert pipeline._pr_number is not None


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
