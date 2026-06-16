"""Backing module for the ``fix`` CLI command.

Provides ``run_fix()`` as a callable entry point for the fix loop,
usable without the Click framework.

Requirements: 59-REQ-5.1, 59-REQ-5.2, 59-REQ-5.3
"""

from __future__ import annotations

import logging
from dataclasses import dataclass
from pathlib import Path
from typing import TYPE_CHECKING, Any

from agentfox.fix.fix import run_fix_loop
from agentfox.fix.spec_gen import FixSpec
from agentfox.session.session import run_session
from agentfox.workspace import WorkspaceInfo

if TYPE_CHECKING:
    from agentfox.core.config import AgentFoxConfig
    from agentfox.fix.fix import FixSessionRunner

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class FixResult:
    """Structured result from a fix run."""

    passes_completed: int
    clusters_resolved: int
    clusters_remaining: int
    sessions_consumed: int
    termination_reason: str
    total_cost: float = 0.0


def _build_fix_session_runner(
    config: AgentFoxConfig,
    project_root: Path,
    activity_callback: Any | None = None,
) -> FixSessionRunner:
    """Build a session runner callable for the fix loop."""

    async def _run(fix_spec: FixSpec) -> float:
        workspace = WorkspaceInfo(
            path=project_root,
            branch="",
            spec_name=f"fix:{fix_spec.cluster_label}",
            task_group=0,
        )
        system_prompt = (
            "You are an auto-fix coding agent. Fix the quality check "
            "failures described below. Make minimal, targeted changes."
        )
        outcome = await run_session(
            workspace=workspace,
            node_id=f"fix:{fix_spec.cluster_label}",
            system_prompt=system_prompt,
            task_prompt=fix_spec.task_prompt,
            config=config,
            activity_callback=activity_callback,
        )
        from agentfox.core.config import PricingConfig
        from agentfox.core.models import calculate_cost, resolve_model
        from agentfox.engine.sdk_params import resolve_model_tier

        model_entry = resolve_model(resolve_model_tier(config, "coder"))
        pricing = getattr(config, "pricing", PricingConfig())
        return calculate_cost(
            outcome.input_tokens,
            outcome.output_tokens,
            model_entry.model_id,
            pricing,
        )

    return _run


async def run_fix(
    config: AgentFoxConfig,
    issue_url: str | None = None,
    *,
    max_attempts: int = 3,
    auto_pr: bool = False,
    dry_run: bool = False,
    auto: bool = False,
    improve_passes: int = 3,
) -> FixResult:
    """Run the fix loop for quality check failures.

    Requirements: 59-REQ-5.1, 59-REQ-5.2, 59-REQ-5.3
    """

    project_root = Path.cwd()

    runner: FixSessionRunner | None = None
    if not dry_run:
        runner = _build_fix_session_runner(config, project_root)

    result = await run_fix_loop(
        project_root=project_root,
        config=config,
        max_passes=max_attempts,
        session_runner=runner,
    )

    return FixResult(
        passes_completed=result.passes_completed,
        clusters_resolved=result.clusters_resolved,
        clusters_remaining=result.clusters_remaining,
        sessions_consumed=result.sessions_consumed,
        termination_reason=str(result.termination_reason),
        total_cost=getattr(result, "total_cost", 0.0),
    )
