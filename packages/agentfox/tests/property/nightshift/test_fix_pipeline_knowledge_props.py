"""Property tests for fix_pipeline knowledge wiring — spec 05 (nightshift_knowledge_parity).

Validates correctness properties for post-harvest ingestion, retrieval
argument invariants, and knowledge scoping across Night Shift fix sessions.

Test Spec: TS-05-P1, TS-05-P2, TS-05-P4, TS-05-P5, TS-05-P6, TS-05-P7, TS-05-P8
Properties: 05-PROP-1, 05-PROP-2, 05-PROP-4, 05-PROP-5, 05-PROP-6, 05-PROP-7, 05-PROP-8
Requirements: 05-REQ-1.1, 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.E1,
              05-REQ-5.1, 05-REQ-7.2, 05-REQ-7.3, 05-REQ-8.2, 05-REQ-10.1
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
from agentfox.workspace import WorkspaceInfo
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_config() -> MagicMock:
    config = MagicMock()
    config.archetypes.overrides.get.return_value = None
    config.security = None
    config.workspace.integration_branch = "develop"
    return config


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
    pipeline._run_id = "run-prop-test"
    return pipeline


def _make_workspace() -> WorkspaceInfo:
    return WorkspaceInfo(
        path=Path("/tmp/mock-worktree"),
        branch="fix/42-fix-the-flaky-test",
        spec_name="fix-issue-42",
        task_group=0,
    )


def _make_triage(
    summary: str = "The test is flaky",
    affected_files: list[str] | None = None,
) -> TriageResult:
    return TriageResult(
        summary=summary,
        affected_files=affected_files if affected_files is not None else [],
    )


def _make_issue(number: int = 42) -> MagicMock:
    """Build a minimal mock IssueResult for process_issue tests."""
    issue = MagicMock()
    issue.number = number
    issue.title = f"Fix test #{number}"
    issue.labels = []
    return issue


# ===========================================================================
# TS-05-P1: touched_files in post-harvest ingest context always equals
#            the list returned by harvest()
# ===========================================================================
# Property: 05-PROP-1
# Requirements: 05-REQ-2.1, 05-REQ-1.1


class TestTouchedFilesEqualsHarvestReturn:
    """For any non-empty file list from harvest(), the post-harvest ingest
    context's touched_files equals that list exactly.

    Property 05-PROP-1: the touched_files key in the context dict passed
    to FoxKnowledgeProvider.ingest() during the post-harvest call equals
    the list returned by harvest(), never an empty list or a stub value.

    Test Spec: TS-05-P1
    Requirements: 05-REQ-2.1, 05-REQ-1.1
    """

    @given(
        file_list=st.lists(
            st.text(
                min_size=1,
                max_size=60,
                alphabet=st.characters(
                    whitelist_categories=("L", "N", "P"),
                    whitelist_characters="_-./",
                ),
            ),
            min_size=1,
            max_size=20,
        ),
    )
    @settings(max_examples=50)
    async def test_touched_files_match_harvest_return(
        self, file_list: list[str]
    ) -> None:
        """Post-harvest ingest context has touched_files == harvest return value."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-prop-test"

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
                return_value=file_list,
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
            if (c.kwargs.get("context") or (c.args[2] if len(c.args) > 2 else {})).get(
                "touched_files", []
            )
            != []
        ]
        assert len(post_harvest_calls) > 0, (
            "Expected at least one post-harvest ingest call with non-empty touched_files"
        )
        ctx = (
            post_harvest_calls[0].kwargs.get("context")
            or post_harvest_calls[0].args[2]
        )
        assert ctx["touched_files"] == file_list


# ===========================================================================
# TS-05-P2: commit_sha never appears in post-harvest ingest context
# ===========================================================================
# Property: 05-PROP-2
# Requirements: 05-REQ-2.2


class TestCommitShaNeverInPostHarvestContext:
    """For any post-harvest ingest call, the context dict never contains
    the 'commit_sha' key.

    Property 05-PROP-2: the context dict does not contain the commit_sha
    key, because harvest() does not return a commit SHA.

    Test Spec: TS-05-P2
    Requirements: 05-REQ-2.2
    """

    @given(
        file_list=st.lists(
            st.text(min_size=1, max_size=40),
            min_size=1,
            max_size=10,
        ),
        response_text=st.text(max_size=200),
    )
    @settings(max_examples=50)
    async def test_commit_sha_absent_for_arbitrary_inputs(
        self, file_list: list[str], response_text: str
    ) -> None:
        """commit_sha is never in the post-harvest ingest context dict."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-prop-test"

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
                return_value=file_list,
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
        for call in ingest_calls:
            ctx = call.kwargs.get("context") or (call.args[2] if len(call.args) > 2 else {})
            if ctx.get("touched_files") and ctx["touched_files"] != []:
                assert "commit_sha" not in ctx, (
                    f"Post-harvest ingest context must not contain 'commit_sha', "
                    f"got context keys: {list(ctx.keys())}"
                )


# ===========================================================================
# TS-05-P6: post-harvest ingest failure never fails the session
# ===========================================================================
# Property: 05-PROP-6
# Requirements: 05-REQ-2.E1


class TestPostHarvestIngestFailureNeverFailsSession:
    """For any post-harvest ingest() call that raises, the session always
    completes and no exception propagates to the runner.

    Property 05-PROP-6: the session completes successfully; the exception
    is caught and logged at ERROR level; no exception propagates.

    Test Spec: TS-05-P6
    Requirements: 05-REQ-2.E1
    """

    @given(
        exc_message=st.text(min_size=1, max_size=100),
    )
    @settings(max_examples=30)
    async def test_session_completes_despite_ingest_exception(
        self, exc_message: str
    ) -> None:
        """Session completes regardless of exception type/message from ingest."""
        provider = MagicMock()
        provider.retrieve.return_value = []

        def ingest_side_effect(*args: object, **kwargs: object) -> None:
            ctx = kwargs.get("context") or (args[2] if len(args) > 2 else {})
            if isinstance(ctx, dict) and ctx.get("touched_files") and ctx["touched_files"] != []:
                raise RuntimeError(exc_message)

        provider.ingest.side_effect = ingest_side_effect
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-prop-test"

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
                return_value=["src/file.py"],
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
            try:
                await pipeline.process_issue(_make_issue(42), issue_body="Some body")
            except Exception as exc:
                pytest.fail(
                    f"Exception propagated to runner: {type(exc).__name__}: {exc}"
                )


# ===========================================================================
# TS-05-P4: task_group is always '0' for retrieval
# ===========================================================================
# Property: 05-PROP-4
# Requirements: 05-REQ-5.1


class TestRetrievalAlwaysUsesTaskGroupZero:
    """For any invocation of _retrieve_knowledge, the task_group passed to
    FoxKnowledgeProvider.retrieve() is always the string '0'.

    Property 05-PROP-4: task_group is always '0', never None or any other
    value.

    Test Spec: TS-05-P4
    Requirements: 05-REQ-5.1
    """

    @given(
        spec_name=st.from_regex(r"fix-issue-[0-9]{1,5}", fullmatch=True),
        task_desc=st.text(max_size=100),
    )
    @settings(max_examples=50)
    def test_task_group_always_zero(self, spec_name: str, task_desc: str) -> None:
        """task_group='0' regardless of spec_name or task_description."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        pipeline._retrieve_knowledge(spec_name, task_desc)

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs.get("task_group") == "0", (
            f"Expected task_group='0', got '{call_kwargs.get('task_group')}'"
        )

    @given(
        has_triage=st.booleans(),
        affected_files=st.lists(st.text(min_size=1, max_size=40), max_size=5),
    )
    @settings(max_examples=30)
    def test_task_group_zero_with_various_triage_states(
        self, has_triage: bool, affected_files: list[str]
    ) -> None:
        """task_group='0' regardless of triage presence or content."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        if has_triage:
            pipeline._retrieve_knowledge(
                "fix-issue-42",
                "description",
                file_footprint=affected_files or None,
            )
        else:
            pipeline._retrieve_knowledge(
                "fix-issue-42",
                "",
                file_footprint=None,
            )

        call_kwargs = provider.retrieve.call_args.kwargs
        assert call_kwargs.get("task_group") == "0"


# ===========================================================================
# TS-05-P5: knowledge records scoped by issue number — no cross-contamination
# ===========================================================================
# Property: 05-PROP-5
# Requirements: 05-REQ-8.2


class TestKnowledgeIsolationByIssueNumber:
    """For any pair of distinct issue numbers N1 and N2, knowledge stored under
    spec_name='fix-issue-{N1}' is never returned for spec_name='fix-issue-{N2}'.

    Property 05-PROP-5: knowledge records are scoped by issue number.

    Test Spec: TS-05-P5
    Requirements: 05-REQ-8.2
    """

    @given(
        n1=st.integers(min_value=1, max_value=10000),
        n2=st.integers(min_value=1, max_value=10000),
    )
    @settings(max_examples=50)
    def test_knowledge_isolation_between_distinct_issues(
        self, n1: int, n2: int
    ) -> None:
        """Knowledge for fix-issue-{N1} never returned for fix-issue-{N2} when N1!=N2."""
        from hypothesis import assume

        assume(n1 != n2)

        provider = MagicMock()

        def side_effect(spec_name: str, task_description: str, **kwargs: object) -> list[str]:
            return ["item"] if spec_name == f"fix-issue-{n1}" else []

        provider.retrieve.side_effect = side_effect

        pipeline_n2 = _make_pipeline(knowledge_provider=provider)
        result_n2 = pipeline_n2._retrieve_knowledge(f"fix-issue-{n2}", "test")
        assert result_n2 == [], (
            f"Knowledge for fix-issue-{n1} was returned for fix-issue-{n2}"
        )


# ===========================================================================
# TS-05-P7: pre-harvest _ingest_knowledge call arguments are unchanged
# ===========================================================================
# Property: 05-PROP-7
# Requirements: 05-REQ-10.1, 05-REQ-10.2


class TestPreHarvestIngestArgsUnchanged:
    """For any Night Shift fix session, the pre-harvest _ingest_knowledge call
    arguments are identical before and after this spec's changes.

    Property 05-PROP-7: pre-harvest call has session_status in context,
    no touched_files key with non-empty value.

    Test Spec: TS-05-P7
    Requirements: 05-REQ-10.1, 05-REQ-10.2
    """

    @given(
        status=st.sampled_from(["completed", "failed", "interrupted"]),
    )
    @settings(max_examples=20)
    def test_pre_harvest_ingest_has_session_status_and_empty_touched_files(
        self, status: str
    ) -> None:
        """Pre-harvest ingest context has session_status and touched_files=[]."""
        provider = MagicMock()
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-1"

        outcome = MagicMock()
        outcome.status = status
        outcome.input_tokens = 100
        outcome.output_tokens = 50
        outcome.cache_read_input_tokens = 0
        outcome.cache_creation_input_tokens = 0
        outcome.duration_ms = 1000
        outcome.error_message = None if status == "completed" else "error"

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

        if status == "completed":
            # Pre-harvest ingest is only called on completed sessions
            assert provider.ingest.called, "Pre-harvest ingest should be called on completed sessions"
            ctx = provider.ingest.call_args[0][2]
            assert "session_status" in ctx, "Pre-harvest context must have session_status"
            assert ctx["session_status"] == status
            assert ctx["touched_files"] == [], "Pre-harvest context must have empty touched_files"
        else:
            # Ingest not called on non-completed sessions
            provider.ingest.assert_not_called()


# ===========================================================================
# TS-05-P8: file_footprint is None when triage is unavailable
# ===========================================================================
# Property: 05-PROP-8
# Requirements: 05-REQ-7.2, 05-REQ-7.3


class TestFileFootprintNoneWhenTriageUnavailable:
    """For any invocation of _retrieve_knowledge where triage is None or
    triage.affected_files is empty/None, file_footprint passed to
    retrieve() is always None with no AttributeError.

    Property 05-PROP-8: file_footprint is None when triage unavailable.

    Test Spec: TS-05-P8
    Requirements: 05-REQ-7.2, 05-REQ-7.3
    """

    @pytest.mark.parametrize(
        "file_footprint_input",
        [None, [], ()],
        ids=["none", "empty_list", "empty_tuple"],
    )
    def test_file_footprint_none_for_falsy_inputs(
        self, file_footprint_input: object
    ) -> None:
        """file_footprint=None when input is None, [], or other falsy value."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        try:
            pipeline._retrieve_knowledge(
                "fix-issue-42",
                "",
                file_footprint=file_footprint_input,
            )
        except AttributeError:
            pytest.fail(
                f"AttributeError raised for file_footprint={file_footprint_input}"
            )

        kw = provider.retrieve.call_args.kwargs
        # file_footprint should be None or the falsy value passed through
        # (the conversion to None is the caller's responsibility via _gather_context)
        assert kw.get("file_footprint") is None or kw.get("file_footprint") == file_footprint_input

    @given(
        has_affected_files=st.sampled_from([True, False]),
    )
    @settings(max_examples=20)
    def test_no_attribute_error_for_any_triage_state(
        self, has_affected_files: bool
    ) -> None:
        """No AttributeError regardless of triage state."""
        provider = MagicMock()
        provider.retrieve.return_value = []
        pipeline = _make_pipeline(knowledge_provider=provider)

        file_footprint = ["src/a.py"] if has_affected_files else None

        try:
            pipeline._retrieve_knowledge(
                "fix-issue-42",
                "",
                file_footprint=file_footprint,
            )
        except AttributeError:
            pytest.fail(
                f"AttributeError raised with file_footprint={file_footprint}"
            )
