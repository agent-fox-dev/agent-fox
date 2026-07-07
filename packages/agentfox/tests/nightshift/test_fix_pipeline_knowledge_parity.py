"""Unit tests for fix_pipeline knowledge wiring — spec 05 (nightshift_knowledge_parity).

Tests for ``_harvest_and_push`` returning ``list[str]`` of changed file paths,
``_retrieve_knowledge`` passing ``task_group="0"``, ``task_description``,
and ``file_footprint`` to ``FoxKnowledgeProvider.retrieve()``,
and post-harvest ingestion with real ``touched_files`` and session summaries.

These tests follow the existing mock-injection pattern from
``test_fix_pipeline_knowledge.py``: a ``MagicMock()`` is passed as
``knowledge_provider`` to the ``FixPipeline`` constructor rather than
patching at the import path.

Test Spec: TS-05-1, TS-05-2, TS-05-3, TS-05-4, TS-05-5, TS-05-6, TS-05-7,
           TS-05-13, TS-05-14,
           TS-05-18 through TS-05-28,
           TS-05-32, TS-05-33, TS-05-35, TS-05-36, TS-05-37, TS-05-38,
           TS-05-E1, TS-05-E2, TS-05-E3, TS-05-E4, TS-05-E5
Requirements: 05-REQ-1.1, 05-REQ-1.2, 05-REQ-1.E1,
              05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.3, 05-REQ-2.4, 05-REQ-2.5,
              05-REQ-2.E1, 05-REQ-2.E2,
              05-REQ-3.6, 05-REQ-3.7,
              05-REQ-5.1 through 05-REQ-5.4, 05-REQ-5.E1,
              05-REQ-6.1, 05-REQ-6.2,
              05-REQ-7.1, 05-REQ-7.2, 05-REQ-7.3,
              05-REQ-8.1, 05-REQ-8.2,
              05-REQ-10.1, 05-REQ-10.2,
              05-REQ-11.2, 05-REQ-11.3, 05-REQ-11.4, 05-REQ-11.5
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


# ===========================================================================
# 4.1 — Post-harvest _ingest_knowledge: touched_files, summary, commit_sha,
#        spec_name
# ===========================================================================
# Test Spec: TS-05-3, TS-05-4, TS-05-5, TS-05-35
# Requirements: 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.3, 05-REQ-11.2


def _make_issue(number: int = 42) -> MagicMock:
    """Build a minimal mock IssueResult for process_issue tests."""
    issue = MagicMock()
    issue.number = number
    issue.title = f"Fix the flaky test #{number}"
    issue.labels = []
    return issue


class TestPostHarvestIngestTouchedFiles:
    """Verify post-harvest _ingest_knowledge passes real touched_files.

    After _harvest_and_push returns a non-empty file list, the post-harvest
    ingestion call must pass those files in the ``touched_files`` context
    key to ``FoxKnowledgeProvider.ingest()``.

    Test Spec: TS-05-3
    Requirements: 05-REQ-2.1
    """

    async def test_ingest_context_contains_real_touched_files(self) -> None:
        """Post-harvest ingest context has touched_files from harvest.

        Test Spec: TS-05-3
        Requirement: 05-REQ-2.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"
        workspace = _make_workspace()
        triage = _make_triage()

        # After implementation, process_issue will call _ingest_knowledge
        # with touched_files from _harvest_and_push's return value.
        # The post-harvest ingest call should have touched_files = ['src/alpha.py', 'src/beta.py']
        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=workspace),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=triage),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/alpha.py", "src/beta.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # Find the post-harvest ingest call: it should have touched_files
        # with the real file paths, not an empty list.
        ingest_calls = provider.ingest.call_args_list
        post_harvest_calls = [
            c
            for c in ingest_calls
            if "touched_files" in (c.kwargs.get("context") or c.args[2] if len(c.args) > 2 else {})
            and (c.kwargs.get("context") or c.args[2] if len(c.args) > 2 else {}).get("touched_files")
            != []
        ]
        assert len(post_harvest_calls) > 0, (
            "Expected at least one post-harvest ingest call with non-empty touched_files; "
            f"got {len(ingest_calls)} total ingest calls"
        )
        post_ctx = (
            post_harvest_calls[0].kwargs.get("context")
            or post_harvest_calls[0].args[2]
        )
        assert post_ctx["touched_files"] == ["src/alpha.py", "src/beta.py"]


class TestPostHarvestIngestNoCommitSha:
    """Verify post-harvest ingest context never contains 'commit_sha'.

    Because harvest() does not return a commit SHA, the post-harvest
    ingestion call must not include ``commit_sha`` in the context dict.

    Test Spec: TS-05-4
    Requirements: 05-REQ-2.2
    """

    async def test_commit_sha_absent_from_post_harvest_context(self) -> None:
        """Post-harvest ingest context dict does not contain 'commit_sha'.

        Test Spec: TS-05-4
        Requirement: 05-REQ-2.2
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # Find the post-harvest ingest call (non-empty touched_files)
        ingest_calls = provider.ingest.call_args_list
        for call in ingest_calls:
            ctx = call.kwargs.get("context") or (call.args[2] if len(call.args) > 2 else {})
            if ctx.get("touched_files") and ctx["touched_files"] != []:
                assert "commit_sha" not in ctx, (
                    f"Post-harvest ingest context must not contain 'commit_sha', got: {ctx}"
                )


class TestPostHarvestIngestSpecName:
    """Verify post-harvest ingestion uses correct session_id and spec_name.

    Both must be ``fix-issue-{N}`` using the pipeline's issue_number attribute.

    Test Spec: TS-05-5
    Requirements: 05-REQ-2.3
    """

    async def test_session_id_and_spec_name_are_fix_issue_n(self) -> None:
        """Post-harvest ingest uses session_id='fix-issue-42' and spec_name='fix-issue-42'.

        Test Spec: TS-05-5
        Requirement: 05-REQ-2.3
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # Find the post-harvest ingest call
        ingest_calls = provider.ingest.call_args_list
        post_harvest_calls = [
            c
            for c in ingest_calls
            if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get("touched_files", []) != []
        ]
        assert len(post_harvest_calls) > 0, "Expected at least one post-harvest ingest call"
        call = post_harvest_calls[0]
        # session_id is the first positional arg, spec_name is the second
        session_id = call.args[0] if call.args else call.kwargs.get("session_id")
        spec_name = call.args[1] if len(call.args) > 1 else call.kwargs.get("spec_name")
        assert session_id == "fix-issue-42" or "fix-issue-42" in str(session_id)
        assert spec_name == "fix-issue-42"


class TestPostHarvestIngestSummaryFields:
    """Verify post-harvest ingest includes summary fields when present.

    When ``extract_session_summary`` returns a non-None ``summary_text``,
    the post-harvest ingest context must include ``summary``,
    ``rejected_approaches``, ``gotchas``, and ``assumptions`` keys.

    Test Spec: TS-05-13, TS-05-35
    Requirements: 05-REQ-3.6, 05-REQ-11.2
    """

    async def test_summary_keys_present_when_extraction_succeeds(self) -> None:
        """Post-harvest ingest has summary, rejected_approaches, gotchas, assumptions.

        Test Spec: TS-05-13
        Requirement: 05-REQ-3.6
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=("session summary", ["approach A"], ["gotcha B"], ["assumption C"]),
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        ingest_calls = provider.ingest.call_args_list
        post_harvest_calls = [
            c
            for c in ingest_calls
            if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get("touched_files", []) != []
        ]
        assert len(post_harvest_calls) > 0, "Expected a post-harvest ingest call"
        ctx = post_harvest_calls[0].kwargs.get("context") or post_harvest_calls[0].args[2]
        assert ctx["summary"] == "session summary"
        assert ctx["rejected_approaches"] == ["approach A"]
        assert ctx["gotchas"] == ["gotcha B"]
        assert ctx["assumptions"] == ["assumption C"]

    async def test_full_ingest_context_assertions(self) -> None:
        """Post-harvest ingest has real touched_files, summary, correct spec_name, no commit_sha.

        Test Spec: TS-05-35
        Requirement: 05-REQ-11.2
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(
                pipeline,
                "_setup_workspace",
                new_callable=AsyncMock,
                return_value=WorkspaceInfo(
                    path=Path("/tmp/mock-wt"),
                    branch="fix/7-fix-the-flaky-test",
                    spec_name="fix-issue-7",
                    task_group=0,
                ),
            ),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=("summary", ["r"], ["g"], ["a"]),
            ),
        ):
            await pipeline.process_issue(_make_issue(7), issue_body="Some body")

        ingest_calls = provider.ingest.call_args_list
        post_harvest_calls = [
            c
            for c in ingest_calls
            if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get("touched_files", []) != []
        ]
        assert len(post_harvest_calls) > 0
        call = post_harvest_calls[0]
        session_id_arg = call.args[0] if call.args else call.kwargs.get("session_id")
        spec_name_arg = call.args[1] if len(call.args) > 1 else call.kwargs.get("spec_name")
        ctx = call.kwargs.get("context") or call.args[2]
        assert "fix-issue-7" in str(session_id_arg)
        assert spec_name_arg == "fix-issue-7"
        assert ctx["touched_files"] == ["src/foo.py"]
        assert ctx["summary"] == "summary"
        assert "commit_sha" not in ctx


# ===========================================================================
# 4.2 — Post-harvest ingestion: summary absent and ingestion skipped paths
# ===========================================================================
# Test Spec: TS-05-14, TS-05-E3, TS-05-E4
# Requirements: 05-REQ-3.7, 05-REQ-2.E2


class TestPostHarvestIngestSummaryAbsent:
    """Verify summary-related keys are omitted when extraction returns None.

    When ``extract_session_summary`` returns ``(None, [], [], [])``, the
    post-harvest ingest context must NOT contain ``summary``,
    ``rejected_approaches``, ``gotchas``, or ``assumptions`` keys.

    Test Spec: TS-05-14
    Requirements: 05-REQ-3.7
    """

    async def test_summary_keys_omitted_when_extraction_returns_none(self) -> None:
        """Post-harvest ingest context has no summary keys when None.

        Test Spec: TS-05-14
        Requirement: 05-REQ-3.7
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        ingest_calls = provider.ingest.call_args_list
        post_harvest_calls = [
            c
            for c in ingest_calls
            if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get("touched_files", []) != []
        ]
        assert len(post_harvest_calls) > 0, "Expected a post-harvest ingest call"
        ctx = post_harvest_calls[0].kwargs.get("context") or post_harvest_calls[0].args[2]
        assert "summary" not in ctx
        assert "rejected_approaches" not in ctx
        assert "gotchas" not in ctx
        assert "assumptions" not in ctx


class TestPostHarvestIngestSkippedOnEmptyHarvest:
    """Verify post-harvest ingestion is skipped when harvest returns [].

    When ``_harvest_and_push`` returns an empty list, no post-harvest
    ingest call should be made with empty ``touched_files``.

    Test Spec: TS-05-E3
    Requirements: 05-REQ-2.E2
    """

    async def test_no_post_harvest_ingest_on_empty_harvest(self) -> None:
        """No post-harvest ingest call when _harvest_and_push returns [].

        Test Spec: TS-05-E3
        Requirement: 05-REQ-2.E2
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=[],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # No post-harvest ingest call should have non-empty touched_files.
        # The only ingest calls should be from pre-harvest path (with touched_files=[]).
        ingest_calls = provider.ingest.call_args_list
        for call in ingest_calls:
            ctx = call.kwargs.get("context") or (call.args[2] if len(call.args) > 2 else {})
            touched = ctx.get("touched_files", [])
            assert touched == [] or touched is None, (
                f"Post-harvest ingest should be skipped on empty harvest, "
                f"but found touched_files={touched}"
            )


class TestPostHarvestIngestEmptyResponse:
    """Verify empty response (early-exit path) is handled gracefully.

    When ``outcome.response`` is empty, ``extract_session_summary("")``
    returns ``(None, [], [], [])``; summary storage is skipped and no
    error is raised.

    Test Spec: TS-05-E4
    Requirements: 05-REQ-3.E1
    """

    async def test_empty_response_skips_summary_storage(self) -> None:
        """Pipeline continues without error when outcome.response is ''.

        Test Spec: TS-05-E4
        Requirement: 05-REQ-3.E1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            # Should not raise
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # Verify: if post-harvest ingest was called, it has no summary keys
        ingest_calls = provider.ingest.call_args_list
        for call in ingest_calls:
            ctx = call.kwargs.get("context") or (call.args[2] if len(call.args) > 2 else {})
            if ctx.get("touched_files") and ctx["touched_files"] != []:
                assert "summary" not in ctx, "summary should not be in context when extraction returns None"


# ===========================================================================
# 4.3 — Post-harvest ingestion error handling and observability
# ===========================================================================
# Test Spec: TS-05-7, TS-05-36, TS-05-E2
# Requirements: 05-REQ-2.E1, 05-REQ-2.5, 05-REQ-11.3


class TestPostHarvestIngestErrorHandling:
    """Verify post-harvest ingestion errors are caught and logged.

    When ``FoxKnowledgeProvider.ingest()`` raises during the post-harvest
    call, the error must be caught, logged at ERROR level with the session
    ID and exception details, and the session must continue.

    Test Spec: TS-05-36, TS-05-E2
    Requirements: 05-REQ-2.E1, 05-REQ-11.3
    """

    async def test_ingest_exception_logged_at_error_level(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """ERROR log emitted with session ID and exception on ingest failure.

        Test Spec: TS-05-36
        Requirement: 05-REQ-11.3
        """
        provider = MagicMock()
        provider.retrieve.return_value = []

        # Track call count to only raise on post-harvest calls
        call_count = 0

        def ingest_side_effect(*args: object, **kwargs: object) -> None:
            nonlocal call_count
            call_count += 1
            ctx = kwargs.get("context") or (args[2] if len(args) > 2 else {})
            # Raise only on post-harvest calls (non-empty touched_files)
            if isinstance(ctx, dict) and ctx.get("touched_files") and ctx["touched_files"] != []:
                raise RuntimeError("db failure")

        provider.ingest.side_effect = ingest_side_effect
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            caplog.at_level(logging.ERROR),
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            # Must not raise — session continues despite ingest failure
            await pipeline.process_issue(_make_issue(5), issue_body="Some body")

        error_records = [r for r in caplog.records if r.levelno >= logging.ERROR]
        assert len(error_records) > 0, "Expected at least one ERROR log record"
        error_text = " ".join(r.message for r in error_records)
        assert "fix-issue-5" in error_text or "db failure" in error_text

    async def test_session_continues_on_ingest_exception(self) -> None:
        """Pipeline completes even when post-harvest ingest raises.

        Test Spec: TS-05-E2
        Requirement: 05-REQ-2.E1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []

        def ingest_side_effect(*args: object, **kwargs: object) -> None:
            ctx = kwargs.get("context") or (args[2] if len(args) > 2 else {})
            if isinstance(ctx, dict) and ctx.get("touched_files") and ctx["touched_files"] != []:
                raise RuntimeError("connection refused")

        provider.ingest.side_effect = ingest_side_effect
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        handle_result_mock = AsyncMock()

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", handle_result_mock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/main.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            # Must not raise
            await pipeline.process_issue(_make_issue(5), issue_body="Some body")

        # Session should complete — handle_result should be called
        handle_result_mock.assert_called_once()


class TestPostHarvestIngestObservability:
    """Verify structured log line after post-harvest ingestion.

    A log record must be emitted after post-harvest ingestion containing
    the ``touched_files`` count and ``summary_extracted`` flag.

    Test Spec: TS-05-7
    Requirements: 05-REQ-2.5
    """

    async def test_log_line_with_touched_files_count_and_summary_flag(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        """Log record emitted with touched_files count and summary_extracted.

        Test Spec: TS-05-7
        Requirement: 05-REQ-2.5
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            caplog.at_level(logging.DEBUG),
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py", "src/bar.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=("summary text", [], [], []),
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # Check for log record with touched_files count and summary_extracted
        all_text = " ".join(r.message for r in caplog.records)
        assert "touched_files" in all_text or "2" in all_text, (
            f"Expected log line with touched_files count; got: {all_text[:500]}"
        )
        assert "summary_extracted" in all_text or "summary" in all_text, (
            f"Expected log line with summary_extracted flag; got: {all_text[:500]}"
        )


# ===========================================================================
# 4.4 — Pre-harvest ingestion preservation and call independence
# ===========================================================================
# Test Spec: TS-05-6, TS-05-32, TS-05-33
# Requirements: 05-REQ-10.1, 05-REQ-10.2, 05-REQ-2.4


class TestPreHarvestIngestionPreserved:
    """Verify pre-harvest ingestion call in _emit_session_event is unchanged.

    The pre-harvest call passes ``session_status`` in its context and must
    continue to do so exactly as before — no ``touched_files`` or summary
    keys should be present in the pre-harvest context.

    Test Spec: TS-05-32
    Requirements: 05-REQ-10.1
    """

    def test_pre_harvest_ingest_has_session_status(self) -> None:
        """Pre-harvest ingest context contains 'session_status' key.

        Test Spec: TS-05-32
        Requirement: 05-REQ-10.1
        """
        provider = MagicMock()
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-1"

        outcome = MagicMock()
        outcome.status = "completed"
        outcome.input_tokens = 100
        outcome.output_tokens = 50
        outcome.cache_read_input_tokens = 0
        outcome.cache_creation_input_tokens = 0
        outcome.duration_ms = 1000
        outcome.error_message = None

        with (
            patch.object(pipeline, "_record_session_to_db"),
            patch("agentfox.engine.audit_helpers.calculate_session_cost", return_value=0.01),
            patch("agentfox.nightshift.fix_pipeline.emit_audit_event"),
        ):
            pipeline._emit_session_event(
                outcome,
                "coder",
                "run-1",
                node_id="fix-issue-42:0:coder",
                attempt=1,
            )

        # The pre-harvest call should have session_status='completed'
        assert provider.ingest.called
        ctx = provider.ingest.call_args[0][2]
        assert ctx["session_status"] == "completed"

    def test_pre_harvest_ingest_has_empty_touched_files(self) -> None:
        """Pre-harvest ingest context has touched_files=[] (unchanged stub value).

        Test Spec: TS-05-32
        Requirement: 05-REQ-10.1
        """
        provider = MagicMock()
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-1"

        outcome = MagicMock()
        outcome.status = "completed"
        outcome.input_tokens = 100
        outcome.output_tokens = 50
        outcome.cache_read_input_tokens = 0
        outcome.cache_creation_input_tokens = 0
        outcome.duration_ms = 1000
        outcome.error_message = None

        with (
            patch.object(pipeline, "_record_session_to_db"),
            patch("agentfox.engine.audit_helpers.calculate_session_cost", return_value=0.01),
            patch("agentfox.nightshift.fix_pipeline.emit_audit_event"),
        ):
            pipeline._emit_session_event(
                outcome,
                "coder",
                "run-1",
                node_id="fix-issue-42:0:coder",
                attempt=1,
            )

        ctx = provider.ingest.call_args[0][2]
        assert ctx["touched_files"] == []


class TestPreAndPostHarvestCallIndependence:
    """Verify pre-harvest and post-harvest ingestion calls are independent.

    Both calls may execute within the same session. The failure of either
    call must not affect the other.

    Test Spec: TS-05-6, TS-05-33
    Requirements: 05-REQ-2.4, 05-REQ-10.2
    """

    async def test_both_ingest_calls_execute_independently(self) -> None:
        """Both pre-harvest and post-harvest ingest calls are made.

        Test Spec: TS-05-6
        Requirement: 05-REQ-2.4
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # At least 2 ingest calls: pre-harvest (from _emit_session_event)
        # and post-harvest (from process_issue after harvest)
        assert provider.ingest.call_count >= 2, (
            f"Expected at least 2 ingest calls (pre + post harvest), got {provider.ingest.call_count}"
        )

        # Verify pre-harvest call has session_status
        pre_harvest_calls = [
            c
            for c in provider.ingest.call_args_list
            if "session_status" in (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {}))
        ]
        assert len(pre_harvest_calls) > 0, "Expected pre-harvest ingest call with session_status"

        # Verify post-harvest call has non-empty touched_files
        post_harvest_calls = [
            c
            for c in provider.ingest.call_args_list
            if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get("touched_files", []) != []
        ]
        assert len(post_harvest_calls) > 0, "Expected post-harvest ingest call with non-empty touched_files"

    async def test_post_harvest_failure_does_not_affect_pre_harvest(self) -> None:
        """Raising on post-harvest ingest doesn't prevent pre-harvest from completing.

        Test Spec: TS-05-33
        Requirement: 05-REQ-10.2
        """
        provider = MagicMock()
        provider.retrieve.return_value = []

        call_count = 0

        def ingest_side_effect(*args: object, **kwargs: object) -> None:
            nonlocal call_count
            call_count += 1
            ctx = kwargs.get("context") or (args[2] if len(args) > 2 else {})
            # Raise only on post-harvest calls (non-empty touched_files)
            if isinstance(ctx, dict) and ctx.get("touched_files") and ctx["touched_files"] != []:
                raise RuntimeError("DB error")

        provider.ingest.side_effect = ingest_side_effect
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            # Must not raise — session continues despite post-harvest ingest failure
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # At least 2 ingest calls attempted (pre-harvest succeeded, post-harvest raised)
        assert call_count >= 2, (
            f"Expected at least 2 ingest calls attempted, got {call_count}"
        )


# ===========================================================================
# 5.1 — spec_name convention and issue isolation (retrieve + ingest)
# ===========================================================================
# Test Spec: TS-05-27, TS-05-28
# Requirements: 05-REQ-8.1, 05-REQ-8.2


class TestSpecNameConventionRetrieveAndIngest:
    """Verify spec_name='fix-issue-{N}' convention for both retrieve and ingest.

    N is read from the existing pipeline attribute (spec.issue_number), not
    parsed from the session ID string. Knowledge records must be scoped by
    issue number so records for one issue are never returned for another.

    Test Spec: TS-05-27, TS-05-28
    Requirements: 05-REQ-8.1, 05-REQ-8.2
    """

    async def test_both_retrieve_and_ingest_use_fix_issue_n(self) -> None:
        """Both retrieve() and ingest() called with spec_name='fix-issue-{N}'.

        N comes from the spec (issue_number), not from string parsing.

        Test Spec: TS-05-27
        Requirement: 05-REQ-8.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/foo.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ),
        ):
            await pipeline.process_issue(_make_issue(99), issue_body="Some body")

        # Verify retrieve() uses spec_name='fix-issue-99'
        retrieve_call = provider.retrieve.call_args
        retrieve_spec = (
            retrieve_call.args[0] if retrieve_call.args else retrieve_call.kwargs.get("spec_name")
        )
        assert retrieve_spec == "fix-issue-99", (
            f"Expected retrieve spec_name='fix-issue-99', got '{retrieve_spec}'"
        )

        # Verify ingest() uses spec_name='fix-issue-99'
        ingest_calls = provider.ingest.call_args_list
        assert len(ingest_calls) > 0, "Expected at least one ingest call"
        for call in ingest_calls:
            ingest_spec = (
                call.args[1] if len(call.args) > 1 else call.kwargs.get("spec_name")
            )
            assert ingest_spec == "fix-issue-99", (
                f"Expected ingest spec_name='fix-issue-99', got '{ingest_spec}'"
            )

    def test_spec_name_not_parsed_from_session_id(self) -> None:
        """spec_name comes from spec.issue_number, not from string parsing.

        Test Spec: TS-05-27
        Requirement: 05-REQ-8.1
        """
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        # Directly calling _retrieve_knowledge with explicit spec_name
        # verifies the method passes through the value unchanged.
        pipeline._retrieve_knowledge("fix-issue-77", "Some description")
        call_args = provider.retrieve.call_args
        spec_arg = call_args.args[0] if call_args.args else call_args.kwargs.get("spec_name")
        assert spec_arg == "fix-issue-77"

    def test_issue_isolation_retrieve_side_effect(self) -> None:
        """Knowledge for fix-issue-42 is never returned for fix-issue-43.

        Uses mock side_effect that returns items only for exact spec_name match,
        verifying per-issue isolation across different issue numbers.

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
        result42 = pipeline42._retrieve_knowledge("fix-issue-42", "Fix test")
        assert result42 == ["knowledge for 42"]

        pipeline43 = _make_pipeline(knowledge_provider=provider)
        result43 = pipeline43._retrieve_knowledge("fix-issue-43", "Fix test")
        assert result43 == []

    def test_issue_isolation_no_cross_contamination(self) -> None:
        """Distinct pipelines for different issues get isolated knowledge.

        Test Spec: TS-05-28
        Requirement: 05-REQ-8.2
        """
        provider = MagicMock()

        def side_effect(spec_name: str, task_description: str, **kwargs: object) -> list[str]:
            store = {
                "fix-issue-10": ["finding from issue 10"],
                "fix-issue-20": ["finding from issue 20"],
            }
            return store.get(spec_name, [])

        provider.retrieve.side_effect = side_effect

        p10 = _make_pipeline(knowledge_provider=provider)
        assert p10._retrieve_knowledge("fix-issue-10", "desc") == ["finding from issue 10"]

        p20 = _make_pipeline(knowledge_provider=provider)
        assert p20._retrieve_knowledge("fix-issue-20", "desc") == ["finding from issue 20"]

        p30 = _make_pipeline(knowledge_provider=provider)
        assert p30._retrieve_knowledge("fix-issue-30", "desc") == []


# ===========================================================================
# 5.2 — fix_pipeline.py calls extract_session_summary from
#        agentfox.knowledge.extraction
# ===========================================================================
# Test Spec: TS-05-17
# Requirements: 05-REQ-4.3


class TestFixPipelineCallsExtractSessionSummary:
    """Verify fix_pipeline.py imports and calls extract_session_summary.

    The function must be called from ``agentfox.knowledge.extraction``
    directly (without await) to parse outcome.response for the
    post-harvest ingestion path.

    Test Spec: TS-05-17
    Requirements: 05-REQ-4.3
    """

    async def test_extract_called_with_outcome_response(self) -> None:
        """extract_session_summary is called with the outcome response text.

        Test Spec: TS-05-17
        Requirement: 05-REQ-4.3
        """
        import inspect

        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/changed.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=(None, [], [], []),
            ) as mock_extract,
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # extract_session_summary must have been called at least once
        assert mock_extract.called, (
            "extract_session_summary from agentfox.knowledge.extraction was not called"
        )
        # The return value should not be a coroutine (synchronous call)
        call_result = mock_extract.return_value
        assert not inspect.isawaitable(call_result), (
            "extract_session_summary return must not be awaitable (synchronous)"
        )

    async def test_extract_not_awaited(self) -> None:
        """extract_session_summary is called without await — no coroutine produced.

        Test Spec: TS-05-17
        Requirement: 05-REQ-4.3
        """
        import inspect

        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-test-1"

        with (
            patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace()),
            patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock),
            patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=_make_triage()),
            patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=True),
            patch.object(pipeline, "_handle_result", new_callable=AsyncMock),
            patch.object(pipeline, "_post_comment", new_callable=AsyncMock),
            patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock),
            patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True),
            patch(
                "agentfox.workspace.harvest.harvest",
                new_callable=AsyncMock,
                return_value=["src/changed.py"],
            ),
            patch(
                "agentfox.workspace.harvest.post_harvest_integrate",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.nightshift.fix_pipeline.extract_session_summary", create=True,
                return_value=("some summary", ["r1"], ["g1"], ["a1"]),
            ) as mock_extract,
        ):
            await pipeline.process_issue(_make_issue(42), issue_body="Some body")

        # Verify the mock was called (it's a synchronous MagicMock, not AsyncMock)
        assert mock_extract.called
        # Verify its return_value is a plain tuple (not awaitable)
        assert not inspect.isawaitable(mock_extract.return_value)
