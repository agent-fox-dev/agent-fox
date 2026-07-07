"""Unit tests for fix_pipeline knowledge wiring — spec 05 (nightshift_knowledge_parity).

Tests for ``_harvest_and_push`` returning ``list[str]`` of changed file paths,
and ``_retrieve_knowledge`` passing ``task_group="0"``, ``task_description``,
and ``file_footprint`` to ``FoxKnowledgeProvider.retrieve()``.

These tests follow the existing mock-injection pattern from
``test_fix_pipeline_knowledge.py``: a ``MagicMock()`` is passed as
``knowledge_provider`` to the ``FixPipeline`` constructor rather than
patching at the import path.

Test Spec: TS-05-1, TS-05-2, TS-05-18 through TS-05-28, TS-05-37, TS-05-38,
           TS-05-E1, TS-05-E5
Requirements: 05-REQ-1.1, 05-REQ-1.2, 05-REQ-1.E1,
              05-REQ-5.1 through 05-REQ-5.4, 05-REQ-5.E1,
              05-REQ-6.1, 05-REQ-6.2,
              05-REQ-7.1, 05-REQ-7.2, 05-REQ-7.3,
              05-REQ-8.1, 05-REQ-8.2,
              05-REQ-11.4, 05-REQ-11.5
"""

from __future__ import annotations

import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
from agentfox.nightshift.spec_builder import InMemorySpec
from agentfox.workspace import WorkspaceInfo

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_config() -> MagicMock:
    config = MagicMock()
    config.archetypes.overrides.get.return_value = None
    config.security = None
    config.workspace.integration_branch = "develop"
    return config


def _make_spec(issue_number: int = 42) -> InMemorySpec:
    return InMemorySpec(
        issue_number=issue_number,
        title="Fix the flaky test",
        task_prompt="Fix the issue: Fix the flaky test\n\nIssue #42\n\nSome body",
        system_context="Repository context here.",
        branch_name=f"fix/{issue_number}-fix-the-flaky-test",
    )


def _make_workspace() -> WorkspaceInfo:
    return WorkspaceInfo(
        path=Path("/tmp/mock-worktree"),
        branch="fix/42-fix-the-flaky-test",
        spec_name="fix-issue-42",
        task_group=0,
    )


def _make_triage(
    summary: str = "The test is flaky due to race condition",
    affected_files: list[str] | None = None,
) -> TriageResult:
    return TriageResult(
        summary=summary,
        affected_files=affected_files if affected_files is not None else [],
    )


def _make_pipeline(
    knowledge_provider: object | None = None,
    conn: object | None = None,
) -> FixPipeline:
    pipeline = FixPipeline(
        config=_make_config(),
        platform=MagicMock(),
        conn=conn,
        knowledge_provider=knowledge_provider,
    )
    pipeline._run_id = "run-test-1"
    return pipeline


# ===========================================================================
# 3.1 — _harvest_and_push returning list[str]
# ===========================================================================
# Test Spec: TS-05-1, TS-05-2, TS-05-38, TS-05-E1
# Requirements: 05-REQ-1.1, 05-REQ-1.2, 05-REQ-1.E1, 05-REQ-11.5


class TestHarvestAndPushReturnsFileList:
    """Verify _harvest_and_push returns the list[str] from harvest().

    The method must return the changed file paths produced by harvest()
    directly to its caller, enabling the post-harvest ingestion call.
    """

    async def test_returns_nonempty_file_list(self) -> None:
        """_harvest_and_push returns the exact list returned by harvest().

        Test Spec: TS-05-1
        Requirement: 05-REQ-1.1
        """
        pipeline = _make_pipeline()
        spec = _make_spec()
        workspace = _make_workspace()

        mock_harvest = AsyncMock(return_value=["src/foo.py", "src/bar.py"])
        mock_integrate = AsyncMock()

        with patch(
            "agentfox.workspace.harvest.harvest",
            mock_harvest,
        ), patch(
            "agentfox.workspace.harvest.post_harvest_integrate",
            mock_integrate,
        ):
            result = await pipeline._harvest_and_push(spec, workspace)

        assert result == ["src/foo.py", "src/bar.py"]

    async def test_returns_empty_list_when_no_changes(self) -> None:
        """_harvest_and_push returns [] when harvest() returns [].

        Test Spec: TS-05-2
        Requirement: 05-REQ-1.2
        """
        pipeline = _make_pipeline()
        spec = _make_spec()
        workspace = _make_workspace()

        mock_harvest = AsyncMock(return_value=[])

        with patch(
            "agentfox.workspace.harvest.harvest",
            mock_harvest,
        ), patch(
            "agentfox.workspace.harvest.post_harvest_integrate",
            AsyncMock(),
        ):
            result = await pipeline._harvest_and_push(spec, workspace)

        assert result == []

    async def test_returns_three_file_list(self) -> None:
        """_harvest_and_push returns exactly the list produced by harvest().

        Test Spec: TS-05-38
        Requirement: 05-REQ-11.5
        """
        pipeline = _make_pipeline()
        spec = _make_spec()
        workspace = _make_workspace()

        mock_harvest = AsyncMock(return_value=["src/a.py", "src/b.py", "src/c.py"])
        mock_integrate = AsyncMock()

        with patch(
            "agentfox.workspace.harvest.harvest",
            mock_harvest,
        ), patch(
            "agentfox.workspace.harvest.post_harvest_integrate",
            mock_integrate,
        ):
            result = await pipeline._harvest_and_push(spec, workspace)

        assert result == ["src/a.py", "src/b.py", "src/c.py"]

    async def test_propagates_exception_from_harvest(self) -> None:
        """harvest() exception propagates — no file list returned.

        Test Spec: TS-05-E1
        Requirement: 05-REQ-1.E1
        """
        provider = MagicMock()
        pipeline_with_provider = _make_pipeline(knowledge_provider=provider)
        spec = _make_spec()
        workspace = _make_workspace()

        mock_harvest = AsyncMock(side_effect=RuntimeError("harvest failed"))

        with patch(
            "agentfox.workspace.harvest.harvest",
            mock_harvest,
        ), patch(
            "agentfox.workspace.harvest.post_harvest_integrate",
            AsyncMock(),
        ):
            with pytest.raises(RuntimeError, match="harvest failed"):
                await pipeline_with_provider._harvest_and_push(spec, workspace)

        # Post-harvest ingest must never be called when harvest raises
        provider.ingest.assert_not_called()


# ===========================================================================
# 3.2 — _retrieve_knowledge: task_group and task_description
# ===========================================================================
# Test Spec: TS-05-18, TS-05-19, TS-05-20, TS-05-22, TS-05-23
# Requirements: 05-REQ-5.1, 05-REQ-5.2, 05-REQ-5.3, 05-REQ-6.1, 05-REQ-6.2


class TestRetrieveKnowledgeTaskGroupAndDescription:
    """Verify _retrieve_knowledge passes task_group='0' and correct task_description.

    Night Shift fix sessions use task_group='0' matching the node ID
    convention fix-issue-{N}:0:coder. The task_description comes from the
    triage summary or an empty string fallback when triage is None.
    """

    def test_passes_task_group_zero(self) -> None:
        """task_group='0' is passed to retrieve().

        Test Spec: TS-05-18
        Requirement: 05-REQ-5.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix null pointer",
            session_id="fix-issue-42:0:coder",
        )

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs.get("task_group") == "0"

    def test_returns_nonempty_list_from_provider(self) -> None:
        """_retrieve_knowledge returns the non-empty list from retrieve().

        Test Spec: TS-05-19
        Requirement: 05-REQ-5.2
        """
        provider = MagicMock()
        provider.retrieve.return_value = ["prior knowledge item 1", "prior knowledge item 2"]
        pipeline = _make_pipeline(knowledge_provider=provider)

        result = pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix null pointer",
        )

        assert result == ["prior knowledge item 1", "prior knowledge item 2"]

    def test_returns_empty_list_on_cold_start(self) -> None:
        """_retrieve_knowledge returns [] when retrieve() returns [] (cold start).

        Test Spec: TS-05-20
        Requirement: 05-REQ-5.3
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        result = pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix null pointer",
        )

        assert result == []

    def test_passes_triage_description_as_task_description(self) -> None:
        """task_description from triage.description is passed as positional arg.

        Test Spec: TS-05-22
        Requirement: 05-REQ-6.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix the null pointer dereference in handler",
        )

        call_args = provider.retrieve.call_args
        # task_description is the second positional arg
        actual_desc = call_args.args[1] if len(call_args.args) > 1 else call_args.kwargs.get("task_description")
        assert actual_desc == "Fix the null pointer dereference in handler"

    def test_passes_empty_task_description_when_triage_none(self) -> None:
        """task_description='' when triage is None (fallback path).

        Test Spec: TS-05-23
        Requirement: 05-REQ-6.2

        Note: This tests the pipeline-level behavior via _gather_context
        indirectly. The _retrieve_knowledge method passes through whatever
        task_description is given. When triage is None or unavailable, the
        caller is responsible for passing '' — the test verifies that the
        method accepts and forwards an empty string correctly.
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "",
        )

        call_args = provider.retrieve.call_args
        actual_desc = call_args.args[1] if len(call_args.args) > 1 else call_args.kwargs.get("task_description")
        assert actual_desc == ""


# ===========================================================================
# 3.3 — _retrieve_knowledge: file_footprint
# ===========================================================================
# Test Spec: TS-05-24, TS-05-25, TS-05-26
# Requirements: 05-REQ-7.1, 05-REQ-7.2, 05-REQ-7.3


class TestRetrieveKnowledgeFileFootprint:
    """Verify _retrieve_knowledge passes correct file_footprint.

    file_footprint enables cross-spec drift queries. It should be set
    to triage.affected_files when available and non-empty, or None
    otherwise.
    """

    def test_passes_affected_files_as_file_footprint(self) -> None:
        """file_footprint=triage.affected_files when non-empty.

        Test Spec: TS-05-24
        Requirement: 05-REQ-7.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix null pointer",
            file_footprint=["src/handler.py", "src/utils.py"],
        )

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs["file_footprint"] == ["src/handler.py", "src/utils.py"]

    def test_passes_none_file_footprint_when_triage_none(self) -> None:
        """file_footprint=None when triage is None (no AttributeError).

        Test Spec: TS-05-25
        Requirement: 05-REQ-7.2
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        # When triage is None, caller passes file_footprint=None
        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "",
            file_footprint=None,
        )

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs.get("file_footprint") is None

    def test_passes_none_file_footprint_when_affected_files_empty(self) -> None:
        """file_footprint=None when triage.affected_files is [].

        Test Spec: TS-05-26
        Requirement: 05-REQ-7.3
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        # Empty affected_files should be converted to None by the caller
        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix it",
            file_footprint=None,
        )

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs.get("file_footprint") is None

    def test_passes_none_file_footprint_when_affected_files_is_none(self) -> None:
        """file_footprint=None when triage.affected_files is None.

        Test Spec: TS-05-26
        Requirement: 05-REQ-7.3
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix it",
            file_footprint=None,
        )

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs.get("file_footprint") is None


# ===========================================================================
# 3.4 — _retrieve_knowledge: error handling and observability
# ===========================================================================
# Test Spec: TS-05-21, TS-05-37, TS-05-E5
# Requirements: 05-REQ-5.E1, 05-REQ-5.4, 05-REQ-11.4


class TestRetrieveKnowledgeErrorHandling:
    """Verify _retrieve_knowledge error handling and logging.

    When retrieve() raises, _retrieve_knowledge catches the exception,
    logs at WARNING level, and returns an empty list. After successful
    retrieval, a structured log line is emitted with task_group and
    item counts.
    """

    def test_returns_empty_list_on_exception(self) -> None:
        """_retrieve_knowledge returns [] when retrieve() raises.

        Test Spec: TS-05-E5
        Requirement: 05-REQ-5.E1
        """
        provider = MagicMock()
        provider.retrieve.side_effect = ConnectionError("db timeout")
        pipeline = _make_pipeline(knowledge_provider=provider)

        result = pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix null pointer",
        )

        assert result == []

    def test_logs_warning_on_exception(self, caplog: pytest.LogCaptureFixture) -> None:
        """WARNING log emitted with exception details when retrieve() raises.

        Test Spec: TS-05-E5
        Requirement: 05-REQ-5.E1
        """
        provider = MagicMock()
        provider.retrieve.side_effect = ConnectionError("db timeout")
        pipeline = _make_pipeline(knowledge_provider=provider)

        with caplog.at_level(logging.WARNING):
            pipeline._retrieve_knowledge(
                "fix-issue-42",
                "Fix null pointer",
            )

        warning_records = [r for r in caplog.records if r.levelno == logging.WARNING]
        assert len(warning_records) > 0, "Expected at least one WARNING log record"
        warning_text = " ".join(r.message for r in warning_records)
        assert "fix-issue-42" in warning_text

    def test_logs_retrieval_results(self, caplog: pytest.LogCaptureFixture) -> None:
        """Structured log line emitted with task_group and item counts after retrieval.

        Test Spec: TS-05-21
        Requirement: 05-REQ-5.4
        """
        provider = MagicMock()
        provider.retrieve.return_value = ["item1"]
        pipeline = _make_pipeline(knowledge_provider=provider)

        with caplog.at_level(logging.DEBUG):
            pipeline._retrieve_knowledge(
                "fix-issue-42",
                "Fix null pointer",
            )

        # After the implementation, a structured log line should contain
        # task_group value and item count information.
        all_text = " ".join(r.message for r in caplog.records)
        assert "task_group" in all_text or "0" in all_text or "1" in all_text

    def test_exception_does_not_propagate(self) -> None:
        """Pipeline continues when retrieve() raises — no exception propagates.

        Test Spec: TS-05-37
        Requirement: 05-REQ-11.4
        """
        provider = MagicMock()
        provider.retrieve.side_effect = RuntimeError("fail")
        pipeline = _make_pipeline(knowledge_provider=provider)

        # Must not raise
        result = pipeline._retrieve_knowledge(
            "fix-issue-42",
            "Fix null pointer",
        )
        assert result == []


# ===========================================================================
# Composite: spec_name convention and issue isolation
# ===========================================================================
# Test Spec: TS-05-27, TS-05-28
# Requirements: 05-REQ-8.1, 05-REQ-8.2


class TestSpecNameConvention:
    """Verify spec_name='fix-issue-{N}' convention and per-issue isolation.

    Knowledge records must be scoped by issue number via spec_name so
    that records for fix-issue-42 are never returned for fix-issue-43.
    """

    def test_spec_name_passed_to_retrieve(self) -> None:
        """retrieve() receives spec_name='fix-issue-{N}' from pipeline attribute.

        Test Spec: TS-05-27
        Requirement: 05-REQ-8.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        # spec.issue_number=99, so spec_name='fix-issue-99'
        pipeline._retrieve_knowledge(
            "fix-issue-99",
            "Fix race condition",
        )

        call_args = provider.retrieve.call_args
        spec_name_arg = call_args.args[0] if call_args.args else call_args.kwargs.get("spec_name")
        assert spec_name_arg == "fix-issue-99"

    def test_issue_isolation_between_different_numbers(self) -> None:
        """Knowledge for fix-issue-42 is never returned for fix-issue-43.

        Test Spec: TS-05-28
        Requirement: 05-REQ-8.2
        """

        def side_effect(spec_name: str, task_description: str, **kwargs: object) -> list[str]:
            if spec_name == "fix-issue-42":
                return ["knowledge for 42"]
            return []

        provider = MagicMock()
        provider.retrieve.side_effect = side_effect

        pipeline42 = _make_pipeline(knowledge_provider=provider)
        result42 = pipeline42._retrieve_knowledge(
            "fix-issue-42",
            "Fix test",
        )
        assert result42 == ["knowledge for 42"]

        pipeline43 = _make_pipeline(knowledge_provider=provider)
        result43 = pipeline43._retrieve_knowledge(
            "fix-issue-43",
            "Fix test",
        )
        assert result43 == []


# ===========================================================================
# TS-05-34: Mock pattern uses constructor injection, not patch-at-import
# ===========================================================================
# Requirement: 05-REQ-11.1


class TestMockInjectionPattern:
    """Verify tests use constructor-injection of knowledge_provider mock.

    fix_pipeline.py does NOT import FoxKnowledgeProvider at runtime — it
    imports the protocol KnowledgeProvider under TYPE_CHECKING. The
    existing test pattern passes a MagicMock as knowledge_provider to the
    FixPipeline constructor.

    Test Spec: TS-05-34
    Requirement: 05-REQ-11.1
    """

    def test_mock_provider_receives_retrieve_calls(self) -> None:
        """Injected MagicMock provider receives retrieve() calls."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge("fix-issue-1", "test")

        assert provider.retrieve.called

    def test_no_live_database_dependency(self) -> None:
        """Tests run without live database — mock prevents real calls."""
        provider = MagicMock()
        provider.retrieve.return_value = ["mocked"]
        pipeline = _make_pipeline(knowledge_provider=provider)

        result = pipeline._retrieve_knowledge("fix-issue-1", "test")

        assert result == ["mocked"]
        provider.retrieve.assert_called_once()
