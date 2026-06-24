"""Unit tests for knowledge system integration in the fix pipeline.

Verifies that the fix pipeline retrieves knowledge context before coder
sessions and ingests knowledge after completed sessions.
"""

from __future__ import annotations

from unittest.mock import MagicMock, patch

from agentfox.nightshift.fix_pipeline import FixPipeline, TriageResult
from agentfox.nightshift.spec_builder import InMemorySpec

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_config() -> MagicMock:
    config = MagicMock()
    config.archetypes.overrides.get.return_value = None
    config.archetypes.max_turns.get.return_value = None
    config.archetypes.thinking.get.return_value = None
    config.archetypes.models.get.return_value = None
    config.archetypes.allowlists.get.return_value = None
    config.security = None
    return config


def _make_spec() -> InMemorySpec:
    return InMemorySpec(
        issue_number=42,
        title="Fix the flaky test",
        task_prompt="Fix the issue: Fix the flaky test\n\nIssue #42\n\nSome body",
        system_context="Repository context here.",
        branch_name="fix/42-fix-the-flaky-test",
    )


def _make_triage() -> TriageResult:
    return TriageResult(summary="The test is flaky due to race condition")


def _make_pipeline(
    knowledge_provider: object | None = None,
    conn: object | None = None,
) -> FixPipeline:
    return FixPipeline(
        config=_make_config(),
        platform=MagicMock(),
        conn=conn,
        knowledge_provider=knowledge_provider,
    )


# ---------------------------------------------------------------------------
# _retrieve_knowledge
# ---------------------------------------------------------------------------


class TestRetrieveKnowledge:
    def test_returns_empty_when_no_provider(self) -> None:
        pipeline = _make_pipeline(knowledge_provider=None)
        result = pipeline._retrieve_knowledge("fix-issue-42", "some description")
        assert result == []

    def test_delegates_to_provider(self) -> None:
        provider = MagicMock()
        provider.retrieve.return_value = ["[REVIEW] critical: race condition"]
        pipeline = _make_pipeline(knowledge_provider=provider)

        result = pipeline._retrieve_knowledge(
            "fix-issue-42",
            "flaky test",
            session_id="fix-issue-42:0:coder",
        )

        assert result == ["[REVIEW] critical: race condition"]
        provider.retrieve.assert_called_once_with(
            "fix-issue-42",
            "flaky test",
            task_group=None,
            session_id="fix-issue-42:0:coder",
        )

    def test_returns_empty_on_exception(self) -> None:
        provider = MagicMock()
        provider.retrieve.side_effect = RuntimeError("DB gone")
        pipeline = _make_pipeline(knowledge_provider=provider)

        result = pipeline._retrieve_knowledge("fix-issue-42", "test")
        assert result == []


# ---------------------------------------------------------------------------
# _format_knowledge_context
# ---------------------------------------------------------------------------


class TestFormatKnowledgeContext:
    def test_empty_list_returns_empty_string(self) -> None:
        assert FixPipeline._format_knowledge_context([]) == ""

    def test_formats_as_memory_facts(self) -> None:
        items = ["[REVIEW] critical: race", "[ERRATA] note: workaround"]
        result = FixPipeline._format_knowledge_context(items)
        assert result.startswith("## Memory Facts")
        assert "[REVIEW] critical: race" in result
        assert "[ERRATA] note: workaround" in result
        assert result.count("- ") == 2


# ---------------------------------------------------------------------------
# _ingest_knowledge
# ---------------------------------------------------------------------------


class TestIngestKnowledge:
    def test_noop_when_no_provider(self) -> None:
        pipeline = _make_pipeline(knowledge_provider=None)
        pipeline._run_id = "run-1"
        pipeline._ingest_knowledge("node-1", "fix-issue-42", "completed")

    def test_delegates_to_provider(self) -> None:
        provider = MagicMock()
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-1"

        pipeline._ingest_knowledge(
            "fix-issue-42:0:coder",
            "fix-issue-42",
            "completed",
            archetype="coder",
            attempt=2,
        )

        provider.ingest.assert_called_once()
        args = provider.ingest.call_args
        assert args[0][0] == "fix-issue-42:0:coder"
        assert args[0][1] == "fix-issue-42"
        ctx = args[0][2]
        assert ctx["session_status"] == "completed"
        assert ctx["archetype"] == "coder"
        assert ctx["attempt"] == 2
        assert ctx["run_id"] == "run-1"

    def test_catches_exception(self) -> None:
        provider = MagicMock()
        provider.ingest.side_effect = RuntimeError("DB error")
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-1"

        pipeline._ingest_knowledge("node-1", "fix-issue-42", "completed")


# ---------------------------------------------------------------------------
# _build_coder_prompt with knowledge_context
# ---------------------------------------------------------------------------


class TestBuildCoderPromptKnowledge:
    @patch("agentfox.session.prompt.build_system_prompt", return_value="system")
    def test_knowledge_context_included_in_system_prompt(self, mock_bsp: MagicMock) -> None:
        pipeline = _make_pipeline()
        spec = _make_spec()
        triage = _make_triage()

        pipeline._build_coder_prompt(
            spec,
            triage,
            knowledge_context="## Memory Facts\n\n- fact1",
        )

        context_arg = mock_bsp.call_args[1].get("context") or mock_bsp.call_args[0][0]
        assert "## Memory Facts" in context_arg
        assert "fact1" in context_arg

    @patch("agentfox.session.prompt.build_system_prompt", return_value="system")
    def test_empty_knowledge_context_not_in_prompt(self, mock_bsp: MagicMock) -> None:
        pipeline = _make_pipeline()
        spec = _make_spec()
        triage = _make_triage()

        pipeline._build_coder_prompt(spec, triage, knowledge_context="")

        context_arg = mock_bsp.call_args[1].get("context") or mock_bsp.call_args[0][0]
        assert "Memory Facts" not in context_arg


# ---------------------------------------------------------------------------
# _build_reviewer_prompt with knowledge_context
# ---------------------------------------------------------------------------


class TestBuildReviewerPromptKnowledge:
    @patch("agentfox.session.prompt.build_system_prompt", return_value="system")
    def test_knowledge_context_included(self, mock_bsp: MagicMock) -> None:
        from agentfox.nightshift.fix_pipeline import AcceptanceCriterion

        pipeline = _make_pipeline()
        spec = _make_spec()
        triage = TriageResult(
            criteria=[
                AcceptanceCriterion(
                    id="AC-1",
                    description="d",
                    preconditions="p",
                    expected="e",
                    assertion="a",
                ),
            ],
        )

        pipeline._build_reviewer_prompt(
            spec,
            triage,
            knowledge_context="## Memory Facts\n\n- errata1",
        )

        context_arg = mock_bsp.call_args[1].get("context") or mock_bsp.call_args[0][0]
        assert "## Memory Facts" in context_arg

    @patch("agentfox.session.prompt.build_system_prompt", return_value="system")
    def test_knowledge_in_empty_triage_fallback(self, mock_bsp: MagicMock) -> None:
        pipeline = _make_pipeline()
        spec = _make_spec()
        triage = TriageResult()

        pipeline._build_reviewer_prompt(
            spec,
            triage,
            knowledge_context="## Memory Facts\n\n- errata1",
        )

        context_arg = mock_bsp.call_args[1].get("context") or mock_bsp.call_args[0][0]
        assert "## Memory Facts" in context_arg


# ---------------------------------------------------------------------------
# _emit_session_event triggers ingestion
# ---------------------------------------------------------------------------


class TestEmitSessionEventIngestion:
    def test_completed_session_triggers_ingestion(self) -> None:
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

        provider.ingest.assert_called_once()
        ctx = provider.ingest.call_args[0][2]
        assert ctx["session_status"] == "completed"

    def test_failed_session_does_not_trigger_ingestion(self) -> None:
        provider = MagicMock()
        pipeline = _make_pipeline(knowledge_provider=provider)
        pipeline._run_id = "run-1"

        outcome = MagicMock()
        outcome.status = "failed"
        outcome.input_tokens = 100
        outcome.output_tokens = 50
        outcome.cache_read_input_tokens = 0
        outcome.cache_creation_input_tokens = 0
        outcome.duration_ms = 1000
        outcome.error_message = "timeout"

        with (
            patch.object(pipeline, "_record_session_to_db"),
            patch("agentfox.nightshift.fix_pipeline.emit_audit_event"),
        ):
            pipeline._emit_session_event(
                outcome,
                "coder",
                "run-1",
                node_id="fix-issue-42:0:coder",
                attempt=1,
            )

        provider.ingest.assert_not_called()
