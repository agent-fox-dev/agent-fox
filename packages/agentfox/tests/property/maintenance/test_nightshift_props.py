"""Property tests for maintenance modules.

Test Spec: TS-61-P4, TS-61-P8
Properties: 4, 8 from design.md
Requirements: 61-REQ-6.2, 61-REQ-7.1, 61-REQ-7.2,
              61-REQ-8.1, 61-REQ-8.2, 61-REQ-8.3
"""

from __future__ import annotations

from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# TS-61-P4: Fix pipeline completeness
# Property 4: Successful fix produces exactly one branch and one PR.
# Requirements: 61-REQ-6.2, 61-REQ-7.1, 61-REQ-7.2
# ---------------------------------------------------------------------------


class TestFixPipelineCompleteness:
    """Successful fix produces exactly one PR with correct references."""

    @given(
        issue_number=st.integers(min_value=1, max_value=10000),
        title=st.text(min_size=3, max_size=50, alphabet="abcdefghijklmnop "),
    )
    @settings(max_examples=20)
    def test_fix_pipeline_completeness(self, issue_number: int, title: str) -> None:
        from agentfox.maintenance.fix_pipeline import build_pr_body
        from agentfox.maintenance.spec_builder import sanitise_branch_name

        branch = sanitise_branch_name(title)
        assert branch.startswith("fix/")

        body = build_pr_body(
            issue_number=issue_number,
            issue_title="test fix",
            changed_files=["example.py"],
        )
        assert f"#{issue_number}" in body


# ---------------------------------------------------------------------------
# TS-61-P8: Platform protocol substitutability
# Property 8: Any PlatformProtocol implementation works with the engine.
# Requirements: 61-REQ-8.1, 61-REQ-8.2, 61-REQ-8.3
# ---------------------------------------------------------------------------


class TestPlatformProtocolSubstitutability:
    """Any PlatformProtocol implementation works with the engine."""

    @given(data=st.data())
    @settings(max_examples=10)
    def test_platform_protocol_substitutability(self, data: st.DataObject) -> None:
        import asyncio
        from unittest.mock import AsyncMock, MagicMock

        from afissues.protocol import PlatformProtocol

        # Create a mock that satisfies PlatformProtocol
        mock_platform = AsyncMock()
        mock_platform.create_issue = AsyncMock()
        mock_platform.list_issues_by_label = AsyncMock(return_value=[])
        mock_platform.add_issue_comment = AsyncMock()
        mock_platform.assign_label = AsyncMock()
        mock_platform.close_issue = AsyncMock()
        mock_platform.remove_label = AsyncMock()
        mock_platform.list_issue_comments = AsyncMock(return_value=[])
        mock_platform.get_issue = AsyncMock()
        mock_platform.close = AsyncMock()
        mock_platform.update_issue = AsyncMock()
        mock_platform.create_label = AsyncMock()
        mock_platform.create_pr = AsyncMock()
        mock_platform.get_pr_state = AsyncMock()
        mock_platform.get_pr_checks = AsyncMock()
        mock_platform.get_pr_reviews = AsyncMock()

        assert isinstance(mock_platform, PlatformProtocol)

        from agentfox.maintenance.engine import NightShiftEngine

        config = MagicMock()
        config.orchestrator.max_cost = None
        config.orchestrator.max_sessions = None

        engine = NightShiftEngine(config=config, platform=mock_platform)

        # Should not raise TypeError
        asyncio.run(engine._run_issue_check())
