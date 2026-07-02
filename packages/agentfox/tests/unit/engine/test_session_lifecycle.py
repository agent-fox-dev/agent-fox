"""Unit tests for engine/session_lifecycle.py helper methods.

Tests for NodeSessionRunner helper methods and error handling that
are not covered by the knowledge-wiring integration tests.

Requirements: 16-REQ-5.1, 16-REQ-5.E1, 26-REQ-4.4, 26-REQ-3.4
"""

from __future__ import annotations

import json
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afaudit.sink import SessionOutcome
from agentfox.core.config import AgentFoxConfig, ArchetypesConfig, PerArchetypeConfig
from agentfox.engine.sdk_params import clamp_instances
from agentfox.engine.session_lifecycle import NodeSessionRunner
from agentfox.knowledge.db import KnowledgeDB
from agentfox.workspace import WorkspaceInfo

_MOCK_KB = MagicMock(spec=KnowledgeDB)

# ---------------------------------------------------------------------------
# _clamp_instances
# ---------------------------------------------------------------------------


class TestClampInstances:
    """Tests for the _clamp_instances helper."""

    def test_coder_clamped_to_one(self) -> None:
        assert clamp_instances("coder", 3) == 1

    def test_coder_one_unchanged(self) -> None:
        assert clamp_instances("coder", 1) == 1

    def test_non_coder_max_five(self) -> None:
        assert clamp_instances("reviewer", 10) == 5

    def test_non_coder_min_one(self) -> None:
        assert clamp_instances("verifier", 0) == 1

    def test_valid_value_unchanged(self) -> None:
        assert clamp_instances("reviewer", 3) == 3


# ---------------------------------------------------------------------------
# _resolve_model_tier
# ---------------------------------------------------------------------------


class TestResolveModelTier:
    """Tests for NodeSessionRunner._resolve_model_tier."""

    def test_default_coder_uses_standard_tier(self) -> None:
        """Coder archetype defaults to STANDARD tier (from archetype registry, spec 15)."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), knowledge_db=_MOCK_KB)
        assert runner._resolved_model_id == "claude-sonnet-4-6"

    def test_config_override_takes_priority(self) -> None:
        """Config override in archetypes.overrides takes priority over registry."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(overrides={"coder": PerArchetypeConfig(model_tier="SIMPLE")})
        )
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB)
        assert runner._resolved_model_id == "claude-haiku-4-5"

    def test_reviewer_defaults_to_standard(self) -> None:
        """Reviewer archetype defaults to STANDARD from the registry."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), archetype="reviewer", knowledge_db=_MOCK_KB)
        assert runner._resolved_model_id == "claude-sonnet-4-6"


# ---------------------------------------------------------------------------
# _resolve_security_config
# ---------------------------------------------------------------------------


class TestResolveSecurityConfig:
    """Tests for NodeSessionRunner._resolve_security_config."""

    def test_coder_returns_none_for_global(self) -> None:
        """Coder has no default allowlist, returns None (use global)."""
        runner = NodeSessionRunner("spec:1", AgentFoxConfig(), knowledge_db=_MOCK_KB)
        assert runner._resolved_security is None

    def test_maintainer_hunt_returns_default_allowlist(self) -> None:
        """maintainer:hunt has a default allowlist from the registry (replaces triage)."""
        runner = NodeSessionRunner(
            "spec:1", AgentFoxConfig(), archetype="maintainer", mode="hunt", knowledge_db=_MOCK_KB
        )
        assert runner._resolved_security is not None
        assert runner._resolved_security.bash_allowlist is not None
        assert "ls" in runner._resolved_security.bash_allowlist
        assert "git" in runner._resolved_security.bash_allowlist

    def test_config_allowlist_overrides_registry(self) -> None:
        """Config allowlist override takes priority over registry default."""
        config = AgentFoxConfig(
            archetypes=ArchetypesConfig(overrides={"maintainer": PerArchetypeConfig(allowlist=["echo", "pwd"])})
        )
        runner = NodeSessionRunner("spec:1", config, archetype="maintainer", mode="hunt", knowledge_db=_MOCK_KB)
        assert runner._resolved_security is not None
        assert runner._resolved_security.bash_allowlist == ["echo", "pwd"]


# ---------------------------------------------------------------------------
# _read_session_artifacts
# ---------------------------------------------------------------------------


class TestReadSessionArtifacts:
    """Tests for NodeSessionRunner._read_session_artifacts."""

    def test_returns_parsed_json(self, tmp_path: Path) -> None:
        """Valid session-summary.json is parsed and returned."""
        summary = {"summary": "Did things", "tests_added_or_modified": []}
        (tmp_path / ".agent-fox").mkdir()
        (tmp_path / ".agent-fox" / "session-summary.json").write_text(json.dumps(summary))
        workspace = WorkspaceInfo(path=tmp_path, spec_name="s", task_group=1, branch="feature/s/1")
        result = NodeSessionRunner._read_session_artifacts(workspace)
        assert result == summary

    def test_returns_none_when_missing(self, tmp_path: Path) -> None:
        """Returns None when .session-summary.json does not exist."""
        workspace = WorkspaceInfo(path=tmp_path, spec_name="s", task_group=1, branch="feature/s/1")
        assert NodeSessionRunner._read_session_artifacts(workspace) is None

    def test_returns_none_on_invalid_json(self, tmp_path: Path) -> None:
        """Returns None when session-summary.json contains invalid JSON."""
        (tmp_path / ".agent-fox").mkdir(exist_ok=True)
        (tmp_path / ".agent-fox" / "session-summary.json").write_text("not valid json {{{")
        workspace = WorkspaceInfo(path=tmp_path, spec_name="s", task_group=1, branch="feature/s/1")
        assert NodeSessionRunner._read_session_artifacts(workspace) is None


class TestCleanupSessionArtifacts:
    """Tests for NodeSessionRunner._cleanup_session_artifacts."""

    def test_deletes_summary_file(self, tmp_path: Path) -> None:
        """Cleanup removes session-summary.json."""
        (tmp_path / ".agent-fox").mkdir()
        summary_path = tmp_path / ".agent-fox" / "session-summary.json"
        summary_path.write_text('{"summary": "done"}')
        workspace = WorkspaceInfo(path=tmp_path, spec_name="s", task_group=1, branch="feature/s/1")
        NodeSessionRunner._cleanup_session_artifacts(workspace)
        assert not summary_path.exists()

    def test_noop_when_missing(self, tmp_path: Path) -> None:
        """Cleanup is a no-op when the file does not exist."""
        workspace = WorkspaceInfo(path=tmp_path, spec_name="s", task_group=1, branch="feature/s/1")
        NodeSessionRunner._cleanup_session_artifacts(workspace)  # should not raise


# ---------------------------------------------------------------------------
# execute() error handling — 16-REQ-5.E1
# ---------------------------------------------------------------------------


class TestExecuteErrorHandling:
    """Verify execute() catches exceptions and returns a failed SessionRecord."""

    @pytest.mark.asyncio
    async def test_worktree_creation_failure_returns_failed_record(self) -> None:
        """If create_worktree raises, a failed SessionRecord is returned."""
        config = AgentFoxConfig()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB)

        with (
            patch(
                "agentfox.engine.session_lifecycle.ensure_integration_branch",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.create_worktree",
                new_callable=AsyncMock,
                side_effect=RuntimeError("worktree failed"),
            ),
        ):
            record = await runner.execute("spec:1", 1)

        assert record.status == "failed"
        assert record.error_message is not None
        assert "worktree failed" in record.error_message

    @pytest.mark.asyncio
    async def test_retry_prompt_includes_previous_error(self) -> None:
        """On retry (attempt > 1), the task prompt includes the previous error."""
        config = AgentFoxConfig()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB)

        workspace = WorkspaceInfo(
            path=Path("/tmp/ws"),
            spec_name="spec",
            task_group=1,
            branch="feature/spec/1",
        )

        captured_prompts: dict = {}

        async def _fake_run_and_harvest(node_id, attempt, workspace, system_prompt, task_prompt, repo_root):
            captured_prompts["task"] = task_prompt
            from datetime import UTC, datetime

            from agentfox.engine.state import SessionRecord

            return SessionRecord(
                node_id=node_id,
                attempt=attempt,
                status="completed",
                input_tokens=0,
                output_tokens=0,
                cost=0.0,
                duration_ms=0,
                error_message=None,
                timestamp=datetime.now(UTC).isoformat(),
            )

        with (
            patch(
                "agentfox.engine.session_lifecycle.ensure_integration_branch",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.create_worktree",
                new_callable=AsyncMock,
                return_value=workspace,
            ),
            patch(
                "agentfox.engine.session_lifecycle.destroy_worktree",
                new_callable=AsyncMock,
            ),
            patch.object(runner, "_run_and_harvest", _fake_run_and_harvest),
            patch(
                "agentfox.engine.session_lifecycle.assemble_context",
                return_value="context",
            ),
        ):
            await runner.execute("spec:1", 2, previous_error="type error in foo")

        assert "type error in foo" in captured_prompts["task"]
        assert "retry attempt 2" in captured_prompts["task"].lower()


# ---------------------------------------------------------------------------
# Regression: no duplicate session_outcomes rows (fixes #473)
# ---------------------------------------------------------------------------


class TestNoDuplicateSessionOutcomeWrite:
    """Verify _run_and_harvest does not write session outcomes to the sink.

    Session outcomes are written exclusively by SessionResultHandler.process()
    via state.record_session(). The old sink-based path caused duplicate rows
    in the session_outcomes table (issue #473).
    """

    @pytest.mark.asyncio
    async def test_run_and_harvest_does_not_call_sink_record_session_outcome(self) -> None:
        """_run_and_harvest must not dispatch record_session_outcome to sinks."""
        config = AgentFoxConfig()
        sink = MagicMock()
        sink.record_session_outcome = MagicMock()

        runner = NodeSessionRunner(
            "spec:1",
            config,
            knowledge_db=_MOCK_KB,
            sink_dispatcher=sink,
        )

        workspace = WorkspaceInfo(
            path=Path("/tmp/ws"),
            spec_name="spec",
            task_group=1,
            branch="feature/spec/1",
        )

        fake_outcome = SessionOutcome(
            spec_name="spec",
            task_group="1",
            node_id="spec:1",
            status="completed",
            input_tokens=100,
            output_tokens=200,
            duration_ms=5000,
        )

        with (
            patch.object(
                runner,
                "_execute_session",
                new_callable=AsyncMock,
                return_value=fake_outcome,
            ),
            patch.object(
                runner,
                "_harvest_and_integrate",
                new_callable=AsyncMock,
                return_value=("completed", None, [], False),
            ),
            patch(
                "agentfox.engine.session_lifecycle._capture_integration_head",
                new_callable=AsyncMock,
                return_value="abc123",
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
            ),
            patch.object(
                runner,
                "_extract_knowledge_and_findings",
                new_callable=AsyncMock,
            ),
        ):
            record = await runner._run_and_harvest(
                "spec:1",
                1,
                workspace,
                "sys",
                "task",
                Path("/tmp"),
            )

        assert record.status == "completed"
        sink.record_session_outcome.assert_not_called()


# ---------------------------------------------------------------------------
# Regression: no duplicate harvest.complete events (fixes #482)
# ---------------------------------------------------------------------------


class TestNoDuplicateHarvestCompleteEvent:
    """Verify _run_and_harvest does not emit harvest.complete directly.

    harvest.complete must be emitted exclusively by extract_and_store_knowledge
    (via _extract_knowledge_and_findings), which includes the real fact count
    and metadata.  The stale direct emission in _run_and_harvest with
    placeholder zeros caused a duplicate event 5–10 s later (issue #482).
    """

    @pytest.mark.asyncio
    async def test_run_and_harvest_does_not_emit_harvest_complete_directly(self) -> None:
        """emit_audit_event must NOT be called with HARVEST_COMPLETE in _run_and_harvest."""
        from afaudit.events import AuditEventType

        config = AgentFoxConfig()
        sink = MagicMock()

        runner = NodeSessionRunner(
            "spec:1",
            config,
            knowledge_db=_MOCK_KB,
            sink_dispatcher=sink,
        )

        workspace = WorkspaceInfo(
            path=Path("/tmp/ws"),
            spec_name="spec",
            task_group=1,
            branch="feature/spec/1",
        )

        fake_outcome = SessionOutcome(
            spec_name="spec",
            task_group="1",
            node_id="spec:1",
            status="completed",
            input_tokens=100,
            output_tokens=200,
            duration_ms=5000,
        )

        audit_calls: list = []

        def capture_emit(sink_arg, run_id, event_type, **kwargs):
            audit_calls.append(event_type)

        with (
            patch.object(
                runner,
                "_execute_session",
                new_callable=AsyncMock,
                return_value=fake_outcome,
            ),
            patch.object(
                runner,
                "_harvest_and_integrate",
                new_callable=AsyncMock,
                return_value=("completed", None, ["some_file.py"], False),
            ),
            patch(
                "agentfox.engine.session_lifecycle._capture_integration_head",
                new_callable=AsyncMock,
                return_value="abc123",
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
                side_effect=capture_emit,
            ),
            patch.object(
                runner,
                "_extract_knowledge_and_findings",
                new_callable=AsyncMock,
            ),
        ):
            record = await runner._run_and_harvest(
                "spec:1",
                1,
                workspace,
                "sys",
                "task",
                Path("/tmp"),
            )

        assert record.status == "completed"
        # harvest.complete must NOT be emitted directly by _run_and_harvest;
        # it is emitted exclusively by extract_and_store_knowledge.
        assert AuditEventType.HARVEST_COMPLETE not in audit_calls, (
            "harvest.complete was emitted directly by _run_and_harvest — this causes duplicate events (issue #482)"
        )


# ---------------------------------------------------------------------------
# AC-2 (issue #556): _build_prompts passes task_group to knowledge_provider.retrieve()
# ---------------------------------------------------------------------------


class TestBuildPromptsPassesTaskGroup:
    """AC-2: NodeSessionRunner._build_prompts() passes str(task_group) to retrieve().

    Issue #556: task_group was not forwarded to the knowledge provider, so
    findings from all groups were injected indiscriminately.
    """

    def test_retrieve_called_with_task_group(self, tmp_path: Path) -> None:
        """_build_prompts calls knowledge_provider.retrieve() with task_group='2'."""
        from unittest.mock import MagicMock

        # Mock a knowledge provider that records retrieve() calls
        mock_provider = MagicMock()
        mock_provider.retrieve.return_value = []

        mock_kb = MagicMock(spec=KnowledgeDB)
        mock_kb.connection = MagicMock()

        runner = NodeSessionRunner(
            "spec_01:2",
            AgentFoxConfig(),
            knowledge_db=mock_kb,
            knowledge_provider=mock_provider,
        )

        # _build_prompts needs a spec_dir; patch resolve_spec_root + assemble_context
        # resolve_spec_root is imported lazily inside _build_prompts from agentfox.core.config
        with (
            patch("agentfox.core.config.resolve_spec_root") as mock_spec_root,
            patch("agentfox.engine.session_lifecycle.assemble_context") as mock_assemble,
            patch("agentfox.engine.session_lifecycle.build_system_prompt", return_value="sys"),
            patch("agentfox.engine.session_lifecycle.build_task_prompt", return_value="task"),
            patch("agentfox.engine.session_lifecycle.extract_subtask_descriptions", return_value=["do X"]),
        ):
            mock_spec_root.return_value = tmp_path
            mock_assemble.return_value = MagicMock()
            runner._build_prompts(tmp_path, attempt=1, previous_error=None)

        mock_provider.retrieve.assert_called_once()
        call_kwargs = mock_provider.retrieve.call_args
        # task_group should be passed as keyword argument '2'
        assert call_kwargs.kwargs.get("task_group") == "2", f"Expected task_group='2', got: {call_kwargs}"


# ---------------------------------------------------------------------------
# Issue #599: Budget exhaustion detection no longer uses "Unknown error" sentinel
# AC-4
# ---------------------------------------------------------------------------


class TestBudgetExhaustionDetection:
    """AC-4: Budget exhaustion uses cost ratio alone, not an error-message sentinel.

    The old condition included `(outcome.error_message or "") in ("Unknown error", "")`
    which would never fire once _map_message starts producing diagnostic strings.
    After the fix the check is purely cost-based.
    """

    @pytest.mark.asyncio
    async def test_budget_exhausted_with_diagnostic_error_message(self) -> None:
        """Session with a diagnostic error_message and cost >= 90% of budget is
        correctly detected as budget-exhausted and not re-queued."""
        config = AgentFoxConfig()
        # Set a max_budget_usd so resolve_max_budget returns a value
        config.orchestrator.max_budget_usd = 10.0

        sink = MagicMock()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB, sink_dispatcher=sink)

        workspace = WorkspaceInfo(
            path=Path("/tmp/ws"),
            spec_name="spec",
            task_group=1,
            branch="feature/spec/1",
        )

        # Outcome: session failed with a non-sentinel diagnostic message
        # (as produced by the fixed _map_message) and large token usage
        failed_outcome = SessionOutcome(
            spec_name="spec",
            task_group="1",
            node_id="spec:1",
            status="failed",
            error_message="subtype=error, num_turns=350, total_cost_usd=9.1500",
            input_tokens=4_000_000,
            output_tokens=200_000,
            cache_read_input_tokens=9_000_000,
            cache_creation_input_tokens=0,
            duration_ms=1_500_000,
        )

        audit_calls: list = []

        def capture_emit(sink_arg, run_id, event_type, **kwargs):
            audit_calls.append(event_type)

        with (
            patch.object(
                runner,
                "_execute_session",
                new_callable=AsyncMock,
                return_value=failed_outcome,
            ),
            patch.object(
                runner,
                "_harvest_and_integrate",
                new_callable=AsyncMock,
                return_value=("failed", "subtype=error, num_turns=350, total_cost_usd=9.1500", [], False),
            ),
            patch(
                "agentfox.engine.session_lifecycle.calculate_session_cost",
                return_value=9.5,  # >= 10.0 * 0.9 = 9.0
            ),
            patch(
                "agentfox.engine.session_lifecycle._capture_integration_head",
                new_callable=AsyncMock,
                return_value="",
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
                side_effect=capture_emit,
            ),
            patch.object(
                runner,
                "_extract_knowledge_and_findings",
                new_callable=AsyncMock,
            ),
        ):
            record = await runner._run_and_harvest(
                "spec:1",
                1,
                workspace,
                "sys",
                "task",
                Path("/tmp"),
            )

        # Budget-exhausted sessions get a "Budget exhausted" error_message
        assert record.error_message is not None
        assert "Budget exhausted" in record.error_message, (
            f"Expected 'Budget exhausted' in error_message, got: {record.error_message!r}"
        )
        assert record.is_budget_exhausted is True

    @pytest.mark.asyncio
    async def test_not_budget_exhausted_when_cost_below_threshold(self) -> None:
        """Session with low cost is NOT marked as budget-exhausted,
        regardless of what the error_message contains."""
        config = AgentFoxConfig()
        config.orchestrator.max_budget_usd = 10.0

        sink = MagicMock()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB, sink_dispatcher=sink)

        workspace = WorkspaceInfo(
            path=Path("/tmp/ws"),
            spec_name="spec",
            task_group=1,
            branch="feature/spec/1",
        )

        failed_outcome = SessionOutcome(
            spec_name="spec",
            task_group="1",
            node_id="spec:1",
            status="failed",
            error_message="subtype=error, num_turns=5, total_cost_usd=0.5000",
            input_tokens=10_000,
            output_tokens=5_000,
            cache_read_input_tokens=0,
            cache_creation_input_tokens=0,
            duration_ms=5_000,
        )

        audit_calls: list = []

        def capture_emit(sink_arg, run_id, event_type, **kwargs):
            audit_calls.append(event_type)

        with (
            patch.object(
                runner,
                "_execute_session",
                new_callable=AsyncMock,
                return_value=failed_outcome,
            ),
            patch.object(
                runner,
                "_harvest_and_integrate",
                new_callable=AsyncMock,
                return_value=("failed", "subtype=error, num_turns=5, total_cost_usd=0.5000", [], False),
            ),
            patch(
                "agentfox.engine.session_lifecycle.calculate_session_cost",
                return_value=0.5,  # < 10.0 * 0.9 = 9.0
            ),
            patch(
                "agentfox.engine.session_lifecycle._capture_integration_head",
                new_callable=AsyncMock,
                return_value="",
            ),
            patch(
                "agentfox.engine.session_lifecycle.emit_audit_event",
                side_effect=capture_emit,
            ),
            patch.object(
                runner,
                "_extract_knowledge_and_findings",
                new_callable=AsyncMock,
            ),
        ):
            record = await runner._run_and_harvest(
                "spec:1",
                1,
                workspace,
                "sys",
                "task",
                Path("/tmp"),
            )

        # Cost below threshold: should not be budget-exhausted
        assert record.is_budget_exhausted is False
        if record.error_message:
            assert "Budget exhausted" not in record.error_message


# ---------------------------------------------------------------------------
# Issue #638: destroy_worktree runs even when task is cancelled
# ---------------------------------------------------------------------------


class TestWorktreeCleanupOnCancellation:
    """Verify destroy_worktree is called even when the session task is cancelled.

    Before the fix, CancelledError (BaseException since Python 3.9) would
    interrupt destroy_worktree at its first await, leaving stale worktrees
    that block retries.
    """

    @pytest.mark.asyncio
    async def test_destroy_worktree_called_on_cancellation(self) -> None:
        """destroy_worktree is invoked even when the session is cancelled."""
        import asyncio

        config = AgentFoxConfig()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB)

        workspace = WorkspaceInfo(
            path=Path("/tmp/ws"),
            spec_name="spec",
            task_group=1,
            branch="feature/spec/1",
        )

        destroy_called = asyncio.Event()

        async def _hanging_session(*_args, **_kwargs):
            await asyncio.sleep(3600)

        async def _mock_destroy(*_args, **_kwargs):
            destroy_called.set()

        with (
            patch(
                "agentfox.engine.session_lifecycle.ensure_integration_branch",
                new_callable=AsyncMock,
            ),
            patch(
                "agentfox.engine.session_lifecycle.create_worktree",
                new_callable=AsyncMock,
                return_value=workspace,
            ),
            patch(
                "agentfox.engine.session_lifecycle.destroy_worktree",
                new_callable=AsyncMock,
                side_effect=_mock_destroy,
            ),
            patch.object(runner, "_run_session_lifecycle", _hanging_session),
        ):
            task = asyncio.create_task(runner.execute("spec:1", 1))
            await asyncio.sleep(0.01)
            task.cancel()
            with pytest.raises(asyncio.CancelledError):
                await task

        assert destroy_called.is_set(), "destroy_worktree was not called after task cancellation (issue #638)"
