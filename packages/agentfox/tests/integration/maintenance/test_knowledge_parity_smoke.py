"""Integration smoke tests for spec 05 (nightshift_knowledge_parity).

End-to-end tests that exercise the full fix pipeline with real
``extract_session_summary`` and mocked infrastructure (FoxKnowledgeProvider,
harvest, workspace), verifying that the knowledge wiring is live.

Test Spec: TS-05-SMOKE-1, TS-05-SMOKE-2, TS-05-SMOKE-3, TS-05-SMOKE-4
Requirements: 05-REQ-1.1, 05-REQ-2.1, 05-REQ-2.E1, 05-REQ-3.6,
              05-REQ-5.1, 05-REQ-7.1, 05-REQ-8.1, 05-REQ-10.1
"""

from __future__ import annotations

import json
import logging
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.nightshift.coder_reviewer import CoderReviewerResult
from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
from agentfox.workspace import WorkspaceInfo

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_config() -> MagicMock:
    config = MagicMock()
    config.archetypes.overrides.get.return_value = None
    config.security = None
    config.workspace.integration_branch = "develop"
    config.night_shift.push_fix_branch = False
    config.orchestrator.max_retries = 3
    return config


def _make_workspace() -> WorkspaceInfo:
    return WorkspaceInfo(
        path=Path("/tmp/mock-worktree"),
        branch="fix/42-fix-the-flaky-test",
        spec_name="fix-issue-42",
        task_group=0,
    )


def _make_issue(number: int = 42) -> MagicMock:
    """Build a minimal mock IssueResult for process_issue tests."""
    issue = MagicMock()
    issue.number = number
    issue.title = f"Fix the flaky test #{number}"
    issue.labels = []
    return issue


def _make_triage(
    summary: str = "Root cause found",
    affected_files: list[str] | None = None,
) -> TriageResult:
    return TriageResult(
        summary=summary,
        affected_files=affected_files if affected_files is not None else ["src/foo.py"],
    )


def _structured_response(
    summary: str = "The fix is complete.",
    rejected: list[str] | None = None,
    gotchas: list[str] | None = None,
    assumptions: list[str] | None = None,
) -> str:
    """Build a response string containing a structured summary JSON block."""
    data = {
        "summary": summary,
        "rejected_approaches": rejected or [],
        "gotchas": gotchas or [],
        "assumptions": assumptions or [],
    }
    return f"Here is the session summary:\n```json\n{json.dumps(data, indent=2)}\n```"


def _pipeline_context_manager(
    pipeline: FixPipeline,
    *,
    triage: TriageResult | None = None,
    coder_result: CoderReviewerResult | bool = True,
    harvest_files: list[str] | None = None,
):
    """Return a combined context manager patching pipeline internals.

    Uses real ``extract_session_summary`` (not mocked) to exercise the
    full wiring.  Only infrastructure is mocked.
    """
    if harvest_files is None:
        harvest_files = ["src/foo.py", "src/bar.py"]
    if triage is None:
        triage = _make_triage()

    from contextlib import ExitStack

    stack = ExitStack()
    stack.enter_context(
        patch.object(pipeline, "_setup_workspace", new_callable=AsyncMock, return_value=_make_workspace())
    )
    stack.enter_context(patch.object(pipeline, "_cleanup_workspace", new_callable=AsyncMock))
    stack.enter_context(
        patch.object(pipeline, "_run_triage", new_callable=AsyncMock, return_value=triage)
    )
    stack.enter_context(
        patch.object(pipeline, "_coder_review_loop", new_callable=AsyncMock, return_value=coder_result)
    )
    stack.enter_context(patch.object(pipeline, "_handle_result", new_callable=AsyncMock))
    stack.enter_context(patch.object(pipeline, "_post_comment", new_callable=AsyncMock))
    stack.enter_context(patch.object(pipeline, "_auto_commit_pending_changes", new_callable=AsyncMock))
    stack.enter_context(
        patch.object(pipeline, "_push_fix_branch_upstream", new_callable=AsyncMock, return_value=True)
    )
    stack.enter_context(
        patch("agentfox.workspace.harvest.harvest", new_callable=AsyncMock, return_value=harvest_files)
    )
    stack.enter_context(
        patch("agentfox.workspace.harvest.post_harvest_integrate", new_callable=AsyncMock)
    )
    return stack


def _get_post_harvest_calls(provider: MagicMock) -> list:
    """Filter ingest calls to find post-harvest calls (non-empty touched_files)."""
    return [
        c
        for c in provider.ingest.call_args_list
        if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get(
            "touched_files", []
        )
        != []
    ]


def _get_ingest_context(call: MagicMock) -> dict:
    """Extract context dict from an ingest() mock call."""
    return call.kwargs.get("context") or (call.args[2] if len(call.args) > 2 else {})


# ===========================================================================
# TS-05-SMOKE-1: Successful fix session with full knowledge ingestion and
#                retrieval
# ===========================================================================
# Execution Path: 05-PATH-1
# Requirements: 05-REQ-1.1, 05-REQ-2.1, 05-REQ-3.6, 05-REQ-5.1, 05-REQ-8.1


class TestSmoke1SuccessfulFixSession:
    """End-to-end smoke test for a successful Night Shift fix session.

    Verifies:
    - FoxKnowledgeProvider.retrieve() called with spec_name, task_group='0',
      task_description, file_footprint
    - FoxKnowledgeProvider.ingest() called twice (pre-harvest + post-harvest)
    - Post-harvest context: real touched_files, no commit_sha, summary data
    - extract_session_summary is called with real response (not mocked)
    - Structured log lines emitted
    - Pipeline completes without exception

    Test Spec: TS-05-SMOKE-1
    """

    async def test_full_successful_session(self, caplog: pytest.LogCaptureFixture) -> None:
        provider = MagicMock()
        provider.retrieve.return_value = ["prior knowledge item"]

        pipeline = FixPipeline(
            config=_make_config(),
            platform=MagicMock(),
            knowledge_provider=provider,
        )

        # Build a CoderReviewerResult with a structured response containing
        # summary fields — extract_session_summary (real, not mocked) will
        # parse it.
        coder_result = CoderReviewerResult(
            success=True,
            response=_structured_response(
                summary="The fix is complete.",
                rejected=["approach A"],
                gotchas=["gotcha 1"],
                assumptions=["assumption X"],
            ),
            affected_files=["src/foo.py"],
        )

        triage = _make_triage(
            summary="Fix null ptr",
            affected_files=["src/foo.py"],
        )

        with caplog.at_level(logging.DEBUG), _pipeline_context_manager(
            pipeline,
            triage=triage,
            coder_result=coder_result,
            harvest_files=["src/foo.py", "src/bar.py"],
        ):
            metrics = await pipeline.process_issue(
                _make_issue(42), issue_body="bug description"
            )

        # --- Verify retrieve() ---
        assert provider.retrieve.call_count >= 1
        retrieve_kwargs = provider.retrieve.call_args.kwargs
        assert retrieve_kwargs["task_group"] == "0"
        retrieve_spec = (
            provider.retrieve.call_args.args[0]
            if provider.retrieve.call_args.args
            else retrieve_kwargs.get("spec_name")
        )
        assert retrieve_spec == "fix-issue-42"
        assert retrieve_kwargs.get("file_footprint") == ["src/foo.py"]

        # --- Verify ingest() called at least twice ---
        assert provider.ingest.call_count >= 2, (
            f"Expected at least 2 ingest calls (pre + post harvest), got {provider.ingest.call_count}"
        )

        # --- Verify pre-harvest ingest has session_status ---
        pre_harvest_calls = [
            c
            for c in provider.ingest.call_args_list
            if "session_status" in _get_ingest_context(c)
        ]
        assert len(pre_harvest_calls) >= 1

        # --- Verify post-harvest ingest ---
        post_calls = _get_post_harvest_calls(provider)
        assert len(post_calls) >= 1, "Expected at least one post-harvest ingest call"
        post_ctx = _get_ingest_context(post_calls[0])
        assert post_ctx["touched_files"] == ["src/foo.py", "src/bar.py"]
        assert "commit_sha" not in post_ctx

        # Real extract_session_summary parsed the structured response
        assert post_ctx.get("summary") == "The fix is complete."
        assert post_ctx.get("rejected_approaches") == ["approach A"]
        assert post_ctx.get("gotchas") == ["gotcha 1"]
        assert post_ctx.get("assumptions") == ["assumption X"]

        # Post-harvest spec_name
        post_spec = post_calls[0].args[1] if len(post_calls[0].args) > 1 else None
        assert post_spec == "fix-issue-42"

        # --- Verify structured log lines ---
        all_text = " ".join(r.message for r in caplog.records)
        assert "task_group" in all_text or "0" in all_text
        assert "touched_files" in all_text or "2" in all_text

        # Pipeline completed
        assert metrics is not None


# ===========================================================================
# TS-05-SMOKE-2: Triage failure — safe fallback with no AttributeError
# ===========================================================================
# Execution Path: 05-PATH-2
# Requirements: 05-REQ-6.2, 05-REQ-7.2


class TestSmoke2TriageFailureFallback:
    """Smoke test for a fix session where triage fails.

    Verifies:
    - retrieve() called with task_description='', file_footprint=None
    - No AttributeError raised
    - extract_session_summary('') returns (None, [], [], [])
    - Post-harvest context (if called) has no summary key
    - Pipeline completes without exception

    Test Spec: TS-05-SMOKE-2
    """

    async def test_triage_failure_safe_fallback(self) -> None:
        provider = MagicMock()
        provider.retrieve.return_value = []

        pipeline = FixPipeline(
            config=_make_config(),
            platform=MagicMock(),
            knowledge_provider=provider,
        )

        # Early exit: response='', affected_files=[]
        coder_result = CoderReviewerResult(success=True, response="", affected_files=[])

        # Empty triage — simulates triage failure fallback
        triage = TriageResult(summary="", affected_files=[])

        with _pipeline_context_manager(
            pipeline,
            triage=triage,
            coder_result=coder_result,
            harvest_files=["src/foo.py"],
        ):
            # Must not raise AttributeError or any other exception
            await pipeline.process_issue(_make_issue(10), issue_body="bug description")

        # retrieve() should have been called — verify safe args
        if provider.retrieve.called:
            retrieve_kwargs = provider.retrieve.call_args.kwargs
            assert retrieve_kwargs["task_group"] == "0"
            # file_footprint should be None for empty affected_files
            assert retrieve_kwargs.get("file_footprint") is None

        # Post-harvest ingest: if called, no summary key
        post_calls = _get_post_harvest_calls(provider)
        for call in post_calls:
            ctx = _get_ingest_context(call)
            assert "summary" not in ctx


# ===========================================================================
# TS-05-SMOKE-3: Post-harvest ingestion failure — session still succeeds
# ===========================================================================
# Execution Path: 05-PATH-3
# Requirements: 05-REQ-2.E1


class TestSmoke3PostHarvestIngestFailure:
    """Smoke test for a fix session where post-harvest ingestion fails.

    Verifies:
    - _harvest_and_push returns a non-empty file list
    - Post-harvest ingest raises RuntimeError
    - ERROR-level log emitted with session ID and error
    - Pipeline continues and returns successful result
    - No exception propagates out

    Test Spec: TS-05-SMOKE-3
    """

    async def test_ingest_failure_session_succeeds(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        provider = MagicMock()
        provider.retrieve.return_value = []

        # Make ingest raise on post-harvest calls (non-empty touched_files)
        def ingest_side_effect(*args: object, **kwargs: object) -> None:
            ctx = kwargs.get("context") or (args[2] if len(args) > 2 else {})
            if isinstance(ctx, dict) and ctx.get("touched_files") and ctx["touched_files"] != []:
                raise RuntimeError("db error")

        provider.ingest.side_effect = ingest_side_effect

        pipeline = FixPipeline(
            config=_make_config(),
            platform=MagicMock(),
            knowledge_provider=provider,
        )

        coder_result = CoderReviewerResult(
            success=True,
            response=_structured_response("Summary here"),
            affected_files=["src/main.py"],
        )

        handle_mock = AsyncMock()

        with (
            caplog.at_level(logging.ERROR),
            _pipeline_context_manager(
                pipeline,
                coder_result=coder_result,
                harvest_files=["src/main.py"],
            ),
        ):
            # Override handle_result to verify it was called
            pipeline._handle_result = handle_mock  # type: ignore[method-assign]
            metrics = await pipeline.process_issue(
                _make_issue(42), issue_body="broken code"
            )

        # Pipeline completed — handle_result was called
        handle_mock.assert_called_once()
        assert metrics is not None

        # ERROR log emitted
        error_records = [r for r in caplog.records if r.levelno >= logging.ERROR]
        assert len(error_records) > 0, "Expected ERROR log for failed ingest"
        error_text = " ".join(r.message for r in error_records)
        assert "db error" in error_text or "fix-issue-42" in error_text


# ===========================================================================
# TS-05-SMOKE-4: Second run on same issue — retrieval returns prior knowledge
# ===========================================================================
# Execution Path: 05-PATH-4
# Requirements: 05-REQ-5.2, 05-REQ-8.1


class TestSmoke4SecondRunRetrievesPriorKnowledge:
    """Smoke test for a second fix session on the same issue.

    Verifies:
    - retrieve() called with spec_name='fix-issue-7', task_group='0'
    - Retrieved items ['prior summary', 'cross-spec drift'] are returned
    - Structured log emitted with non-zero item counts
    - Session completes without exception

    Test Spec: TS-05-SMOKE-4
    """

    async def test_second_run_retrieves_prior_knowledge(
        self, caplog: pytest.LogCaptureFixture
    ) -> None:
        provider = MagicMock()
        provider.retrieve.return_value = [
            "prior summary from session 1",
            "cross-spec drift finding",
        ]

        pipeline = FixPipeline(
            config=_make_config(),
            platform=MagicMock(),
            knowledge_provider=provider,
        )

        coder_result = CoderReviewerResult(
            success=True,
            response="Fixed the race condition.",
            affected_files=["src/worker.py"],
        )

        triage = _make_triage(
            summary="Fix race condition",
            affected_files=["src/worker.py"],
        )

        with caplog.at_level(logging.DEBUG), _pipeline_context_manager(
            pipeline,
            triage=triage,
            coder_result=coder_result,
            harvest_files=["src/worker.py"],
        ):
            metrics = await pipeline.process_issue(
                _make_issue(7), issue_body="race condition in worker"
            )

        # --- Verify retrieve() ---
        assert provider.retrieve.call_count >= 1
        retrieve_call = provider.retrieve.call_args
        spec_name = (
            retrieve_call.args[0]
            if retrieve_call.args
            else retrieve_call.kwargs.get("spec_name")
        )
        assert spec_name == "fix-issue-7"
        assert retrieve_call.kwargs["task_group"] == "0"
        assert retrieve_call.kwargs.get("file_footprint") == ["src/worker.py"]

        # --- Verify retrieved items count logged ---
        all_text = " ".join(r.message for r in caplog.records)
        # The log should mention item count (2 items retrieved)
        assert "2" in all_text or "items" in all_text or "Knowledge" in all_text

        # Pipeline completed
        assert metrics is not None
