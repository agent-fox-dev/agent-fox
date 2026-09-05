"""Tests for issue #605: per-node budget ceiling increase.

Covers NS-REQ-1 through NS-REQ-5 as specified in the issue requirements.

The budget exhaustion ratio is 0.9 (session_lifecycle._BUDGET_EXHAUST_RATIO),
so a session is considered exhausted when cost >= budget * 0.9.
"""

from __future__ import annotations

import tomllib
from pathlib import Path
from unittest.mock import AsyncMock, MagicMock, patch

import pytest
from afaudit.sink import SessionOutcome
from agentfox.core.config import AgentFoxConfig, PerArchetypeConfig
from agentfox.core.config_gen import generate_default_config
from agentfox.engine.sdk_params import resolve_max_budget
from agentfox.knowledge.db import KnowledgeDB
from agentfox.workspace import WorkspaceInfo

_MOCK_KB = MagicMock(spec=KnowledgeDB)


# ---------------------------------------------------------------------------
# TS-NS-1: Default global budget ceiling >= 20.0
# ---------------------------------------------------------------------------


class TestDefaultBudgetCeiling:
    """NS-REQ-1: default max_budget_usd accommodates a full Opus coder session."""

    def test_default_budget_at_least_20(self) -> None:
        """TS-NS-1: AgentFoxConfig() default max_budget_usd >= 20.0."""
        config = AgentFoxConfig()
        assert config.orchestrator.max_budget_usd >= 20.0, f"Expected >= 20.0, got {config.orchestrator.max_budget_usd}"

    def test_default_budget_not_8(self) -> None:
        """Regression: default must not be the old 8.0 value."""
        config = AgentFoxConfig()
        assert config.orchestrator.max_budget_usd != 8.0


# ---------------------------------------------------------------------------
# TS-NS-2: PerArchetypeConfig.max_budget_usd field
# ---------------------------------------------------------------------------


class TestPerArchetypeMaxBudget:
    """NS-REQ-2: PerArchetypeConfig exposes optional max_budget_usd field."""

    def test_field_exists_in_model_fields(self) -> None:
        """TS-NS-2: PerArchetypeConfig.model_fields contains max_budget_usd."""
        assert "max_budget_usd" in PerArchetypeConfig.model_fields

    def test_default_is_none(self) -> None:
        """TS-NS-2: max_budget_usd defaults to None (inherit global)."""
        cfg = PerArchetypeConfig()
        assert cfg.max_budget_usd is None

    def test_toml_parses_per_archetype_budget(self, tmp_path: Path) -> None:
        """TS-NS-2: TOML [archetypes.overrides.coder] max_budget_usd = 25.0 parses correctly."""
        config_file = tmp_path / "config.toml"
        config_file.write_text("[archetypes.overrides.coder]\nmax_budget_usd = 25.0\n")
        from agentfox.core.config import load_config

        config = load_config(config_file)
        assert config.archetypes.overrides["coder"].max_budget_usd == 25.0

    def test_negative_budget_rejected(self) -> None:
        """TS-NS-2: Negative max_budget_usd is rejected by pydantic validation."""
        import pydantic

        with pytest.raises((pydantic.ValidationError, ValueError)):
            PerArchetypeConfig(max_budget_usd=-1.0)


# ---------------------------------------------------------------------------
# TS-NS-3: resolve_max_budget respects per-archetype override
# ---------------------------------------------------------------------------


class TestResolveMaxBudget:
    """NS-REQ-3: resolve_max_budget respects per-archetype max_budget_usd."""

    def test_per_archetype_override_takes_precedence(self) -> None:
        """TS-NS-3: coder override of 25.0 overrides global 12.0."""
        config = AgentFoxConfig(
            orchestrator={"max_budget_usd": 12.0},  # type: ignore[arg-type]
            archetypes={"overrides": {"coder": {"max_budget_usd": 25.0}}},  # type: ignore[arg-type]
        )
        assert resolve_max_budget(config, "coder") == 25.0

    def test_fallback_to_global_when_no_override(self) -> None:
        """TS-NS-3: reviewer (no override) falls back to global 12.0."""
        config = AgentFoxConfig(
            orchestrator={"max_budget_usd": 12.0},  # type: ignore[arg-type]
            archetypes={"overrides": {"coder": {"max_budget_usd": 25.0}}},  # type: ignore[arg-type]
        )
        assert resolve_max_budget(config, "reviewer") == 12.0

    def test_no_archetype_arg_uses_global(self) -> None:
        """TS-NS-3: resolve_max_budget() with no archetype uses global."""
        config = AgentFoxConfig(
            orchestrator={"max_budget_usd": 15.0},  # type: ignore[arg-type]
        )
        assert resolve_max_budget(config) == 15.0

    def test_per_archetype_zero_returns_none(self) -> None:
        """Per-archetype max_budget_usd=0.0 means unlimited (None)."""
        config = AgentFoxConfig(
            archetypes={"overrides": {"coder": {"max_budget_usd": 0.0}}},  # type: ignore[arg-type]
        )
        assert resolve_max_budget(config, "coder") is None

    def test_none_override_falls_back_to_global(self) -> None:
        """Per-archetype max_budget_usd=None (default) falls back to global."""
        config = AgentFoxConfig(
            orchestrator={"max_budget_usd": 18.0},  # type: ignore[arg-type]
            archetypes={"overrides": {"coder": {}}},  # max_budget_usd not set
        )
        assert resolve_max_budget(config, "coder") == 18.0


# ---------------------------------------------------------------------------
# TS-NS-4: config_gen does not output max_budget_usd = 8.0
# ---------------------------------------------------------------------------


class TestConfigGenTemplate:
    """NS-REQ-4: generated TOML template reflects new default, not hardcoded 8.0."""

    def test_template_does_not_contain_8_0(self) -> None:
        """TS-NS-4: generated template does not contain 'max_budget_usd = 8.0'."""
        template = generate_default_config()
        assert "max_budget_usd = 8.0" not in template, (
            "Template must not contain the old hardcoded max_budget_usd = 8.0"
        )

    def test_template_budget_reflects_new_default(self) -> None:
        """TS-NS-4: generated template max_budget_usd >= 20.0."""
        template = generate_default_config()
        parsed = tomllib.loads(template)
        actual = parsed["orchestrator"]["max_budget_usd"]
        assert actual >= 20.0, f"Template max_budget_usd is {actual}, expected >= 20.0"


# ---------------------------------------------------------------------------
# TS-NS-5: session_lifecycle uses archetype-resolved budget
#
# _BUDGET_EXHAUST_RATIO = 0.9, so threshold = budget * 0.9.
# Key scenario: cost=$9.5 with global=$8.0, coder_override=$15.0
#   - Without fix: uses global $8.0 → threshold=$7.2 → $9.5 >= $7.2 → IS exhausted
#   - With fix:    uses coder $15.0 → threshold=$13.5 → $9.5 < $13.5 → NOT exhausted
# ---------------------------------------------------------------------------


class TestSessionLifecycleArchetypeBudget:
    """NS-REQ-5: budget exhaustion uses archetype-resolved budget."""

    @pytest.mark.asyncio
    async def test_coder_cost_below_archetype_ceiling_not_exhausted(self) -> None:
        """TS-NS-5: coder at $9.5 with coder budget $15.0 is NOT exhausted.

        Global budget is $8.0 (below cost), but the per-archetype coder budget
        is $15.0 (above cost * 0.9 = $13.5), so exhaustion must NOT fire.
        """
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config = AgentFoxConfig(
            orchestrator={"max_budget_usd": 8.0},  # type: ignore[arg-type]
            archetypes={"overrides": {"coder": {"max_budget_usd": 15.0}}},  # type: ignore[arg-type]
        )

        sink = MagicMock()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB, sink_dispatcher=sink, archetype="coder")

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
            error_message="subtype=error, num_turns=200",
            input_tokens=2_000_000,
            output_tokens=100_000,
            cache_read_input_tokens=4_000_000,
            cache_creation_input_tokens=0,
            duration_ms=900_000,
        )

        with (
            patch.object(runner, "_execute_session", new_callable=AsyncMock, return_value=failed_outcome),
            patch.object(
                runner,
                "_harvest_and_integrate",
                new_callable=AsyncMock,
                return_value=("failed", "subtype=error, num_turns=200", [], False),
            ),
            patch(
                "agentfox.engine.session_lifecycle.calculate_session_cost",
                return_value=9.5,  # < 15.0 * 0.9 = 13.5 → NOT exhausted against coder budget
            ),
            patch(
                "agentfox.engine.session_lifecycle._capture_integration_head",
                new_callable=AsyncMock,
                return_value="",
            ),
            patch("agentfox.engine.session_lifecycle.emit_audit_event"),
            patch.object(runner, "_extract_knowledge_and_findings", new_callable=AsyncMock),
        ):
            record = await runner._run_and_harvest("spec:1", 1, workspace, "sys", "task", Path("/tmp"))

        # $9.5 < $15.0 * 0.9 ($13.5): NOT exhausted despite exceeding global budget $8.0
        assert record.is_budget_exhausted is False, (
            f"Expected not budget-exhausted (cost=$9.5 < coder threshold=$13.5), "
            f"but got is_budget_exhausted=True. error_message={record.error_message!r}"
        )

    @pytest.mark.asyncio
    async def test_coder_cost_above_archetype_ceiling_is_exhausted(self) -> None:
        """TS-NS-5: coder at $14.0 with coder budget $15.0 IS exhausted.

        $14.0 >= $15.0 * 0.9 = $13.5 → exhausted.
        """
        from agentfox.engine.session_lifecycle import NodeSessionRunner

        config = AgentFoxConfig(
            orchestrator={"max_budget_usd": 8.0},  # type: ignore[arg-type]
            archetypes={"overrides": {"coder": {"max_budget_usd": 15.0}}},  # type: ignore[arg-type]
        )

        sink = MagicMock()
        runner = NodeSessionRunner("spec:1", config, knowledge_db=_MOCK_KB, sink_dispatcher=sink, archetype="coder")

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
            error_message="subtype=error, num_turns=350",
            input_tokens=4_000_000,
            output_tokens=200_000,
            cache_read_input_tokens=9_000_000,
            cache_creation_input_tokens=0,
            duration_ms=1_500_000,
        )

        with (
            patch.object(runner, "_execute_session", new_callable=AsyncMock, return_value=failed_outcome),
            patch.object(
                runner,
                "_harvest_and_integrate",
                new_callable=AsyncMock,
                return_value=("failed", "subtype=error, num_turns=350", [], False),
            ),
            patch(
                "agentfox.engine.session_lifecycle.calculate_session_cost",
                return_value=14.0,  # >= 15.0 * 0.9 = 13.5 → exhausted
            ),
            patch(
                "agentfox.engine.session_lifecycle._capture_integration_head",
                new_callable=AsyncMock,
                return_value="",
            ),
            patch("agentfox.engine.session_lifecycle.emit_audit_event"),
            patch.object(runner, "_extract_knowledge_and_findings", new_callable=AsyncMock),
        ):
            record = await runner._run_and_harvest("spec:1", 1, workspace, "sys", "task", Path("/tmp"))

        assert record.is_budget_exhausted is True
        assert "Budget exhausted" in (record.error_message or "")
        # Error message should reference the coder budget ($15.00), not global ($8.00)
        assert "$15.00" in (record.error_message or ""), (
            f"Expected coder budget $15.00 in error, got: {record.error_message!r}"
        )
