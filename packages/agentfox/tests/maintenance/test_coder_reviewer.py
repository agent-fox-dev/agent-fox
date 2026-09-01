"""Unit tests for CoderReviewerLoop return object extensions.

Verifies that ``CoderReviewerLoop.run()`` returns an object with ``response``
and ``affected_files`` fields, correctly populated on successful coder
sessions and defaulting to empty on early-exit / exhaustion paths.

Test Spec: TS-05-29, TS-05-30, TS-05-31, TS-05-39, TS-05-40
Requirements: 05-REQ-9.1, 05-REQ-9.2, 05-REQ-9.3, 05-REQ-9.E1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

from agentfox.maintenance.coder_reviewer import CoderReviewerLoop
from agentfox.maintenance.fix_pipeline import FixMetrics, FixReviewResult, TriageResult
from agentfox.maintenance.spec_builder import InMemorySpec
from agentfox.workspace import WorkspaceInfo

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_spec(issue_number: int = 42) -> InMemorySpec:
    return InMemorySpec(
        issue_number=issue_number,
        title="Fix the flaky test",
        task_prompt="Fix the issue: Fix the flaky test",
        system_context="Repository context here.",
        branch_name=f"fix/{issue_number}-fix-the-flaky-test",
    )


def _make_triage(
    affected_files: list[str] | None = None,
    summary: str = "The test is flaky due to race condition",
) -> TriageResult:
    return TriageResult(
        summary=summary,
        affected_files=affected_files or [],
    )


def _make_workspace() -> WorkspaceInfo:
    return WorkspaceInfo(
        path=Path("/tmp/mock-worktree"),
        branch="fix/42-fix-the-flaky-test",
        spec_name="fix-issue-42",
        task_group=0,
    )


def _make_mock_pipeline() -> MagicMock:
    """Build a MagicMock pipeline with the attributes CoderReviewerLoop.run() needs."""
    pipeline = MagicMock()
    pipeline._config.orchestrator.max_retries = 3
    pipeline._run_id = "run-test-1"
    pipeline._format_review_comment.return_value = "## Review\nPASS"
    pipeline._post_comment = AsyncMock()
    return pipeline


def _make_coder_outcome(response: str = "The fix applied cleanly.") -> MagicMock:
    """Build a mock coder session outcome with a response attribute."""
    outcome = MagicMock()
    outcome.response = response
    outcome.input_tokens = 100
    outcome.output_tokens = 50
    outcome.cache_read_input_tokens = 0
    outcome.cache_creation_input_tokens = 0
    outcome.duration_ms = 2000
    outcome.status = "completed"
    outcome.error_message = None
    return outcome


def _patch_model_resolution():
    """Context manager that patches model resolution imports used inside run()."""
    return (
        patch("agentfox.core.models.resolve_model", return_value="claude-sonnet-4-6"),
        patch("agentfox.engine.sdk_params.resolve_model_tier", return_value="standard"),
        patch("agentfox.engine.sdk_params.resolve_model_variant", return_value=None),
    )


# ---------------------------------------------------------------------------
# TS-05-29: Return object has response and affected_files fields
# ---------------------------------------------------------------------------


class TestCoderReviewerReturnObjectFields:
    """Verify the return object includes ``response`` and ``affected_files``.

    The return type must have ``response: str`` (default ``""``) and
    ``affected_files: list[str]`` (default ``[]``) without introducing
    a completely new return type — the existing type is extended in-place.

    Test Spec: TS-05-29
    Requirements: 05-REQ-9.1
    """

    async def test_result_has_response_attribute(self) -> None:
        """The run() return object has a 'response' attribute."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        review_result = FixReviewResult(overall_verdict="PASS")
        coder_outcome = _make_coder_outcome()

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(affected_files=["src/handler.py"]),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert hasattr(result, "response"), (
            f"Return object {type(result).__name__} must have a 'response' attribute"
        )

    async def test_result_has_affected_files_attribute(self) -> None:
        """The run() return object has an 'affected_files' attribute."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        review_result = FixReviewResult(overall_verdict="PASS")
        coder_outcome = _make_coder_outcome()

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(affected_files=["src/handler.py"]),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert hasattr(result, "affected_files"), (
            f"Return object {type(result).__name__} must have an 'affected_files' attribute"
        )

    async def test_response_default_is_empty_string(self) -> None:
        """The 'response' field defaults to '' on exhaustion (no PASS verdict)."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 0  # Only one attempt
        loop = CoderReviewerLoop(pipeline)

        review_result = FixReviewResult(overall_verdict="FAIL")
        coder_outcome = _make_coder_outcome(response="")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.response == ""

    async def test_affected_files_default_is_empty_list(self) -> None:
        """The 'affected_files' field defaults to [] on exhaustion."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 0
        loop = CoderReviewerLoop(pipeline)

        review_result = FixReviewResult(overall_verdict="FAIL")
        coder_outcome = _make_coder_outcome(response="")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.affected_files == []


# ---------------------------------------------------------------------------
# TS-05-30, TS-05-39: Successful path populates response and affected_files
# ---------------------------------------------------------------------------


class TestCoderReviewerSuccessfulPath:
    """Verify fields after a successful coder session (reviewer PASS).

    When a coder session completes and the reviewer gives PASS verdict,
    ``response`` should contain the last assistant message text and
    ``affected_files`` should contain the triage file paths.

    Test Spec: TS-05-30, TS-05-39
    Requirements: 05-REQ-9.2, 05-REQ-12.1
    """

    async def test_response_set_to_coder_message_text(self) -> None:
        """response equals the last assistant message text from coder outcome."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="The fix applied cleanly.")
        review_result = FixReviewResult(overall_verdict="PASS")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(affected_files=["src/handler.py"]),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.response == "The fix applied cleanly."

    async def test_affected_files_set_to_triage_file_paths(self) -> None:
        """affected_files equals the triage output's affected_files list."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="Fixed successfully.")
        review_result = FixReviewResult(overall_verdict="PASS")
        triage = _make_triage(affected_files=["src/handler.py", "src/utils.py"])

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=triage,
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.affected_files == ["src/handler.py", "src/utils.py"]

    async def test_both_fields_populated_together(self) -> None:
        """Both response and affected_files are populated on a single successful run."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="Fixed successfully.")
        review_result = FixReviewResult(overall_verdict="PASS")
        triage = _make_triage(affected_files=["src/main.py"])

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=triage,
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert hasattr(result, "response")
        assert hasattr(result, "affected_files")
        assert result.response == "Fixed successfully."
        assert result.affected_files == ["src/main.py"]


# ---------------------------------------------------------------------------
# TS-05-31, TS-05-40: Early-exit paths return defaults
# ---------------------------------------------------------------------------


class TestCoderReviewerEarlyExit:
    """Verify fields on early-exit paths (loop exhaustion, aborted).

    When the coder-reviewer loop exhausts all retries (all verdicts FAIL)
    or encounters an early exit, the result should have ``response=""``
    and ``affected_files=[]`` with no uninitialized fields.

    Test Spec: TS-05-31, TS-05-40
    Requirements: 05-REQ-9.3, 05-REQ-9.E1, 05-REQ-12.2
    """

    async def test_exhausted_loop_has_empty_response(self) -> None:
        """response is '' when all retries are exhausted (all FAIL verdicts)."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 0  # Only one attempt
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="Attempted fix.")
        review_result = FixReviewResult(overall_verdict="FAIL", summary="Tests still fail")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(affected_files=["src/handler.py"]),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.response == ""

    async def test_exhausted_loop_has_empty_affected_files(self) -> None:
        """affected_files is [] when all retries are exhausted."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 0
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="Attempted fix.")
        review_result = FixReviewResult(overall_verdict="FAIL")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(affected_files=["src/handler.py"]),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.affected_files == []

    async def test_no_attribute_error_on_field_access(self) -> None:
        """Accessing response and affected_files never raises AttributeError."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 0
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="")
        review_result = FixReviewResult(overall_verdict="FAIL")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        # These accesses must not raise AttributeError
        _ = result.response
        _ = result.affected_files

    async def test_exhausted_loop_with_multiple_retries(self) -> None:
        """response='' and affected_files=[] after multiple failed retries."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 2  # 3 total attempts
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="Retry attempt.")
        review_result = FixReviewResult(overall_verdict="FAIL")

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(affected_files=["src/main.py"]),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.response == ""
        assert result.affected_files == []

    async def test_empty_triage_affected_files_returns_empty_list(self) -> None:
        """affected_files=[] when triage has no affected_files."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        coder_outcome = _make_coder_outcome(response="Fixed it.")
        review_result = FixReviewResult(overall_verdict="PASS")
        triage = _make_triage(affected_files=[])

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=triage,
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result.affected_files == []


# ---------------------------------------------------------------------------
# TS-05-29 additional: backward compatibility with bool semantics
# ---------------------------------------------------------------------------


class TestCoderReviewerBoolCompatibility:
    """Verify the return object preserves bool-like truthiness semantics.

    The existing call site in fix_pipeline.py uses ``if not success:``
    where ``success = await self._coder_review_loop(...)``. The extended
    return type must be truthy on PASS and falsy on exhaustion to avoid
    breaking existing control flow.

    Requirement: 05-REQ-9.1 (no new return type — extend existing)
    """

    async def test_truthy_on_pass(self) -> None:
        """Return object is truthy when reviewer verdict is PASS."""
        pipeline = _make_mock_pipeline()
        loop = CoderReviewerLoop(pipeline)

        review_result = FixReviewResult(overall_verdict="PASS")
        coder_outcome = _make_coder_outcome()

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert result, "Return object should be truthy on PASS verdict"

    async def test_falsy_on_exhaustion(self) -> None:
        """Return object is falsy when loop exhausts all retries."""
        pipeline = _make_mock_pipeline()
        pipeline._config.orchestrator.max_retries = 0
        loop = CoderReviewerLoop(pipeline)

        review_result = FixReviewResult(overall_verdict="FAIL")
        coder_outcome = _make_coder_outcome()

        p1, p2, p3 = _patch_model_resolution()
        with (
            p1,
            p2,
            p3,
            patch.object(loop, "_run_coder_phase", new_callable=AsyncMock, return_value=coder_outcome),
            patch.object(loop, "_run_reviewer_phase", new_callable=AsyncMock, return_value=review_result),
        ):
            result = await loop.run(
                spec=_make_spec(),
                triage=_make_triage(),
                metrics=FixMetrics(),
                workspace=_make_workspace(),
            )

        assert not result, "Return object should be falsy on exhaustion"
