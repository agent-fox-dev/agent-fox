"""Session lifecycle: workspace, hooks, prompts, execution, harvest, cleanup.

Handles the full lifecycle of a coding session for a single task graph
node. Extracted from cli/code.py to keep CLI wiring thin.

Requirements: 16-REQ-5.1, 16-REQ-5.E1, 06-REQ-1.1, 06-REQ-2.1,
              05-REQ-1.1, 11-REQ-4.2, 13-REQ-2.1, 13-REQ-7.1,
              40-REQ-7.1, 40-REQ-7.2, 40-REQ-7.3, 40-REQ-11.3,
              05-REQ-4.1, 05-REQ-4.2, 42-REQ-3.2, 53-REQ-5.1
"""

from __future__ import annotations

import asyncio
import json
import logging
import re
from datetime import UTC, datetime
from pathlib import Path

from afaudit.emit import emit_audit_event
from afaudit.events import AuditEventType, AuditSeverity
from afaudit.sink import SessionOutcome, SinkDispatcher

from agentfox.core.config import AgentFoxConfig
from agentfox.core.errors import IntegrationError
from agentfox.core.models import resolve_model
from agentfox.core.node_id import parse_node_id
from agentfox.core.prompt_safety import sanitize_prompt_content
from agentfox.engine.audit_helpers import calculate_session_cost
from agentfox.engine.review_persistence import persist_review_findings
from agentfox.engine.sdk_params import (
    clamp_instances,
    resolve_max_budget,
    resolve_model_tier,
    resolve_model_variant,
    resolve_security_config,
    resolve_session_params,
)
from agentfox.engine.state import SessionRecord
from agentfox.knowledge.db import ContextKnowledgeDB, KnowledgeDB
from agentfox.knowledge.fox_provider import KnowledgeProvider
from agentfox.session.prompt import (
    assemble_context,
    build_system_prompt,
    build_task_prompt,
)
from agentfox.session.session import run_session
from agentfox.spec.parser import parse_tasks
from agentfox.ui.progress import ActivityCallback
from agentfox.workspace import (
    WorkspaceInfo,
    create_worktree,
    destroy_worktree,
    ensure_integration_branch,
    run_git,
)
from agentfox.workspace.harvest import harvest, post_harvest_integrate

logger = logging.getLogger(__name__)

# Archetypes whose outputs are captured as structured findings via
# _persist_review_findings rather than free-form factual knowledge.
# Skipping LLM extraction for these avoids ~18k-token overhead per session
# when the extraction reliably returns zero facts.
_REVIEW_ARCHETYPES: frozenset[str] = frozenset({"reviewer", "verifier"})

_BUDGET_EXHAUST_RATIO: float = 0.9


def extract_subtask_descriptions(spec_dir: Path, task_group: int) -> list[str]:
    """Extract the first non-metadata detail from each subtask in a task group.

    Parses the v1.2 spec via ``parse_tasks`` and scans the rendered body
    of the target group.  Each subtask line (``- [...] ID Title``) starts a
    new subtask; subsequent indented bullets (``  - text``) are details.  The
    first detail whose text does not start with ``_`` is captured.

    Args:
        spec_dir: Path to the spec folder (e.g., .agent-fox/specs/12_rate_limiting/).
        task_group: The task group number to extract from.

    Returns:
        List of description strings. Empty if the spec cannot be loaded, the
        group is not found, or no subtasks have non-metadata details.

    Requirements: 94-REQ-1.1, 94-REQ-1.2, 94-REQ-1.E1, 94-REQ-1.E2
    """
    if not spec_dir.is_dir():
        return []

    try:
        groups = parse_tasks(spec_dir)
    except Exception:
        logger.debug("extract_subtask_descriptions: failed to parse %s", spec_dir, exc_info=True)
        return []

    target_group = None
    for g in groups:
        if g.number == task_group:
            target_group = g
            break

    if target_group is None:
        return []

    # Parse the rendered body to extract first non-metadata detail per subtask.
    # Body format from _render_group_body:
    #   - [x] 1.1 Title
    #     - detail 1
    #     - detail 2
    #   - [ ] 1.2 Another title
    #     - detail 3
    descriptions: list[str] = []
    in_subtask = False
    found_first = False

    for line in target_group.body.splitlines():
        # Subtask line: starts with "- [" at column 0
        if line.startswith("- ["):
            in_subtask = True
            found_first = False
            continue

        if in_subtask and not found_first:
            stripped = line.strip()
            if stripped.startswith("- "):
                bullet_text = stripped[2:].strip()
                if not bullet_text.startswith("_"):
                    descriptions.append(bullet_text)
                    found_first = True

    return descriptions


_DESIGN_FILE_REF = re.compile(
    r"\*\*`([a-zA-Z0-9_/.\-]+\.\w+)`\*\*\s*\(modified\)",
)


def _extract_spec_file_footprint(spec_dir: Path) -> list[str]:
    """Extract the list of files a spec modifies from its architecture.md.

    Returns file paths marked ``(modified)`` in the spec's
    ``architecture.md``.  Returns an empty list when the file is
    missing, unreadable, or contains no ``(modified)`` references.
    """
    target = spec_dir / "architecture.md"
    try:
        content = target.read_text(encoding="utf-8")
        return _DESIGN_FILE_REF.findall(content)
    except Exception:
        return []


async def _capture_integration_head(repo_root: Path, branch: str) -> str:
    """Return the current SHA of the integration branch HEAD.

    Returns empty string if git rev-parse fails.

    Requirements: 35-REQ-1.1, 35-REQ-1.E1
    """
    from agentfox.workspace.git import run_git

    try:
        rc, stdout, _stderr = await run_git(
            ["rev-parse", branch],
            cwd=repo_root,
            check=False,
        )
        if rc != 0:
            logger.warning(
                "git rev-parse %s failed (returncode %d) in %s",
                branch,
                rc,
                repo_root,
            )
            return ""
        return stdout.strip()
    except Exception as exc:
        logger.warning(
            "Failed to capture %s HEAD in %s: %s",
            branch,
            repo_root,
            exc,
        )
        return ""


def compose_enriched_summary(
    summary: str,
    rejected_approaches: list[dict[str, str]] | None = None,
    gotchas: list[str] | None = None,
    assumptions: list[str] | None = None,
) -> str:
    """Merge structured session-summary fields into a single enriched text.

    Combines the narrative *summary* with optional structured fields
    (``rejected_approaches``, ``gotchas``, ``assumptions``) into a single
    string suitable for storage in the ``session_summaries`` table.

    Each section is separated by a newline character.  No trailing newline
    is appended after the final section.

    When none of the structured fields are present or all are empty, the
    raw *summary* text is returned unchanged.

    Malformed ``rejected_approaches`` entries (missing ``approach`` or
    ``reason`` key) are silently skipped.

    Requirements: 11-REQ-3.1, 11-REQ-3.2, 11-REQ-3.3, 11-REQ-3.4,
                  11-REQ-3.6, 11-REQ-1.E1, 11-REQ-3.E1, 11-REQ-3.E2
    """
    sections: list[str] = []

    if summary:
        sections.append(summary)

    # Rejected approaches — skip malformed entries (11-REQ-1.E1).
    if rejected_approaches:
        for entry in rejected_approaches:
            if not isinstance(entry, dict):
                continue
            approach = entry.get("approach")
            reason = entry.get("reason")
            if approach and reason:
                sections.append(f"Tried: {approach} — rejected because: {reason}")

    # Gotchas
    if gotchas:
        for gotcha in gotchas:
            if gotcha:
                sections.append(f"Watch out: {gotcha}")

    # Assumptions
    if assumptions:
        for assumption in assumptions:
            if assumption:
                sections.append(f"Assumes: {assumption}")

    return "\n".join(sections)


class NodeSessionRunner:
    """Session runner for a single task graph node.

    Created by the session_runner_factory closure. Handles the full
    session lifecycle: workspace creation, hooks, context assembly,
    prompt building, session execution, artifact collection, harvest,
    and cleanup.

    Requirements: 16-REQ-5.1, 16-REQ-5.E1, 06-REQ-1.1, 06-REQ-2.1
    """

    def __init__(
        self,
        node_id: str,
        config: AgentFoxConfig,
        *,
        archetype: str = "coder",
        mode: str | None = None,
        instances: int = 1,
        sink_dispatcher: SinkDispatcher | None = None,
        knowledge_db: KnowledgeDB,
        context_knowledge_db: KnowledgeDB | ContextKnowledgeDB | None = None,
        knowledge_provider: KnowledgeProvider | None = None,
        activity_callback: ActivityCallback | None = None,
        run_id: str = "",
        timeout_override: int | None = None,
        max_turns_override: int | None = None,
        trace_enabled: bool = True,
    ) -> None:
        self._node_id = node_id
        self._config = config
        self._archetype = archetype
        self._mode = mode  # 97-REQ-5.3: mode for per-mode configuration resolution
        self._instances = clamp_instances(archetype, instances, mode=mode)
        self._sink = sink_dispatcher
        self._sink_dispatcher = sink_dispatcher  # alias for retrieval audit events
        self._knowledge_db = knowledge_db
        # 06-REQ-7.3: read-only conn for session context assembly; writes done at startup
        self._context_knowledge_db = context_knowledge_db or knowledge_db
        self._activity_callback = activity_callback
        self._run_id = run_id
        self._trace_enabled = trace_enabled
        # 75-REQ-3.5: Per-node timeout/turns overrides from timeout-aware escalation
        self._timeout_override = timeout_override
        self._max_turns_override = max_turns_override
        # 114-REQ-2.4: Use provided KnowledgeProvider or default to NoOp
        if knowledge_provider is not None:
            self._knowledge_provider = knowledge_provider
        else:
            from agentfox.knowledge.fox_provider import NoOpKnowledgeProvider

            self._knowledge_provider = NoOpKnowledgeProvider()
        parsed = parse_node_id(node_id)
        self._spec_name = parsed.spec_name
        self._task_group = parsed.group_number

        resolved_variant = resolve_model_variant(self._config, self._archetype, mode=self._mode)
        self._resolved_model_id = resolve_model(
            resolve_model_tier(self._config, self._archetype, mode=self._mode),
            variant=resolved_variant,
        )
        self._resolved_security = resolve_security_config(self._config, self._archetype, mode=self._mode)

    def _build_prompts(
        self,
        repo_root: Path,
        attempt: int,
        previous_error: str | None,
    ) -> tuple[str, str]:
        """Assemble context and build system/task prompts.

        Uses KnowledgeProvider.retrieve() to produce knowledge context,
        then passes it to assemble_context.

        Requirements: 05-REQ-4.1, 05-REQ-4.2, 114-REQ-3.1, 114-REQ-3.3
        """
        from agentfox.core.config import resolve_spec_root

        spec_dir = resolve_spec_root(self._config, repo_root) / self._spec_name

        # 114-REQ-3.1: Use KnowledgeProvider for knowledge context retrieval
        memory_facts: list[str] | None = None
        try:
            descriptions = extract_subtask_descriptions(spec_dir, self._task_group)
            task_description = "\n".join(descriptions) if descriptions else self._spec_name
            footprint = _extract_spec_file_footprint(spec_dir)
            retrieved = self._knowledge_provider.retrieve(
                self._spec_name,
                task_description,
                task_group=str(self._task_group),
                session_id=self._node_id,
                file_footprint=footprint,
            )
            if retrieved:
                memory_facts = retrieved
        except Exception:
            # 114-REQ-3.E1: Log WARNING and proceed with empty knowledge context
            logger.warning(
                "KnowledgeProvider.retrieve() failed for %s, continuing without knowledge context",
                self._spec_name,
                exc_info=True,
            )

        # 06-REQ-7.3: Use the read-only connection for context assembly.
        # Writes (_migrate_legacy_files) are performed at orchestrator
        # startup, not during context assembly.
        context = assemble_context(
            spec_dir,
            self._task_group,
            memory_facts=memory_facts,
            conn=self._context_knowledge_db.connection,
            project_root=Path.cwd(),
            archetype=self._archetype,
        )

        system_prompt = build_system_prompt(
            context=context,
            task_group=self._task_group,
            spec_name=self._spec_name,
            archetype=self._archetype,
            mode=self._mode,
            project_dir=repo_root,
        )
        task_prompt = build_task_prompt(
            task_group=self._task_group,
            spec_name=self._spec_name,
            archetype=self._archetype,
            mode=self._mode,
        )

        if previous_error and attempt > 1:
            safe_error = sanitize_prompt_content(previous_error, label="previous-error")
            task_prompt = (
                f"{task_prompt}\n\n"
                f"**Note:** This is retry attempt {attempt}. "
                f"The previous attempt failed with:\n"
                f"{safe_error}\n"
                f"Please address this error.\n"
            )

        # 53-REQ-5.1, 113-REQ-4.2: Inject active critical/major review
        # findings (including audit findings) for all coder attempts so the
        # coder can address identified issues.
        if self._archetype == "coder":
            retry_context = self._build_retry_context(self._spec_name)
            if retry_context:
                task_prompt = f"{retry_context}\n\n{task_prompt}"

        return system_prompt, task_prompt

    @staticmethod
    def _read_session_artifacts(workspace: WorkspaceInfo) -> dict | None:
        """Read session-summary.json from the worktree if it exists.

        Looks in ``.agent-fox/session-summary.json`` inside the worktree.
        Returns the parsed JSON dict or None if the file is absent or
        cannot be parsed.
        """
        from agentfox.core.node_id import AGENT_FOX_DIR, SESSION_SUMMARY_FILENAME

        summary_path = workspace.path / AGENT_FOX_DIR / SESSION_SUMMARY_FILENAME
        if not summary_path.exists():
            return None
        try:
            return json.loads(summary_path.read_text(encoding="utf-8"))
        except (json.JSONDecodeError, OSError, TypeError) as exc:
            logger.warning(
                "Failed to read session summary from %s: %s",
                summary_path,
                exc,
            )
            return None

    @staticmethod
    def _cleanup_session_artifacts(workspace: WorkspaceInfo) -> None:
        """Delete transient session artifacts from the worktree.

        Called after all consumers have read the artifacts.  Prevents
        stale files from leaking into the working directory when worktree
        cleanup is skipped or fails.
        """
        from agentfox.core.node_id import AGENT_FOX_DIR, SESSION_SUMMARY_FILENAME

        summary_path = workspace.path / AGENT_FOX_DIR / SESSION_SUMMARY_FILENAME
        try:
            summary_path.unlink(missing_ok=True)
        except OSError:
            pass

    def _build_fallback_input(
        self,
        workspace: WorkspaceInfo,
        node_id: str,
    ) -> str:
        """Construct fallback extraction input from session metadata.

        Returns a structured text block with spec name, task group,
        node ID, and commit diff. Returns empty string if no meaningful
        metadata is available.

        The ``## Changes`` section is omitted when no commits exist.

        Requirements: 52-REQ-1.2, 52-REQ-1.E1
        """
        import subprocess

        parts = [
            "# Session Knowledge Extraction",
            "",
            f"Spec: {self._spec_name}",
            f"Task Group: {self._task_group}",
            f"Node ID: {node_id}",
        ]

        # Try to get commit diff from the worktree
        try:
            result = subprocess.run(
                ["git", "diff", "HEAD~1", "--", ".", ":!.agent-fox/"],
                cwd=workspace.path,
                capture_output=True,
                text=True,
                timeout=30,
            )
            diff = result.stdout.strip() if result.returncode == 0 else ""
        except Exception:
            diff = ""

        if diff:
            safe_diff = sanitize_prompt_content(diff, label="diff", max_chars=50_000)
            parts.extend(["", "## Changes", "", safe_diff])

        return "\n".join(parts)

    async def _execute_session(
        self,
        node_id: str,
        workspace: WorkspaceInfo,
        system_prompt: str,
        task_prompt: str,
    ) -> SessionOutcome:
        """Resolve SDK params and run the coding session.

        Requirements: 56-REQ-1.2, 56-REQ-2.2, 56-REQ-3.2, 56-REQ-4.2,
                      75-REQ-3.5
        """
        # 75-REQ-3.5: Apply per-node overrides when available, otherwise
        # fall back to config-based resolution.
        params = resolve_session_params(
            self._config,
            self._archetype,
            mode=self._mode,
            max_turns_override=self._max_turns_override,
        )

        logger.info(
            "Session %s: max_turns=%s, max_budget_usd=%s, thinking=%s, effort=%s, compaction=%s, timeout_override=%s",
            node_id,
            params.max_turns,
            params.max_budget_usd,
            params.thinking,
            params.effort,
            params.compaction,
            self._timeout_override,
        )

        return await run_session(
            workspace=workspace,
            node_id=node_id,
            system_prompt=system_prompt,
            task_prompt=task_prompt,
            config=self._config,
            activity_callback=self._activity_callback,
            model_id=self._resolved_model_id,
            security_config=self._resolved_security,
            sink_dispatcher=self._sink,
            run_id=self._run_id,
            max_turns=params.max_turns,
            max_budget_usd=params.max_budget_usd,
            thinking=params.thinking,
            effort=params.effort,
            compaction=params.compaction,
            session_timeout=self._timeout_override,
            archetype=self._archetype,
        )

    async def _harvest_and_integrate(
        self,
        node_id: str,
        outcome: SessionOutcome,
        workspace: WorkspaceInfo,
        repo_root: Path,
    ) -> tuple[str, str | None, list[str], bool]:
        """Harvest changes on success and run post-harvest integration.

        Returns (status, error_message, touched_files, is_non_retryable).

        Requirements: 03-REQ-7.1, 19-REQ-3.4, 35-REQ-1.1,
                      40-REQ-11.1, 40-REQ-11.2, 118-REQ-3.1
        """
        error_message = outcome.error_message
        status = outcome.status
        touched_files: list[str] = []
        is_non_retryable = False

        if outcome.status != "completed":
            return status, error_message, touched_files, is_non_retryable

        # 03-REQ-7.1: Harvest changes into integration branch on success
        # 121-REQ-1.1: Push inside the merge lock (atomic merge+push)
        try:
            touched_files = await harvest(
                repo_root,
                workspace,
                dev_branch=self._config.workspace.integration_branch,
                force_clean=self._config.workspace.force_clean,
                push=True,
                audit_sink=self._sink,
                run_id=self._run_id,
                node_id=node_id,
            )
            # 40-REQ-11.1: Emit git.merge after successful harvest
            if touched_files:
                # Capture the resulting commit SHA for traceability
                _, sha_out, _ = await run_git(
                    ["rev-parse", "HEAD"],
                    cwd=repo_root,
                    check=False,
                )
                commit_sha = sha_out.strip()
                emit_audit_event(
                    self._sink,
                    self._run_id,
                    AuditEventType.GIT_MERGE,
                    node_id=node_id,
                    archetype=self._archetype,
                    payload={
                        "branch": workspace.branch,
                        "commit_sha": commit_sha,
                        "files_touched": touched_files,
                    },
                )
        except IntegrationError as exc:
            status = "failed"
            error_message = (
                f"Session completed but harvest failed: {exc}. "
                f"The coding work was done — the merge into "
                f"{self._config.workspace.integration_branch} "
                f"encountered a conflict."
            )
            # 118-REQ-3.1: Propagate non-retryable classification
            is_non_retryable = not exc.retryable
            # 40-REQ-11.2: Emit git.conflict on merge failure
            emit_audit_event(
                self._sink,
                self._run_id,
                AuditEventType.GIT_CONFLICT,
                node_id=node_id,
                archetype=self._archetype,
                severity=AuditSeverity.WARNING,
                payload={
                    "branch": workspace.branch,
                    "strategy": "default",
                    "error": str(exc),
                },
            )
            logger.error(
                "Harvest failed for %s after successful session: %s",
                node_id,
                exc,
            )
            return status, error_message, touched_files, is_non_retryable

        # 35-REQ-1.1: Capture integration branch HEAD SHA after successful harvest
        # 19-REQ-3.4: Post-harvest remote integration
        # 121-REQ-5.E1: Skip push in post_harvest — harvest already pushed
        if touched_files:
            try:
                await post_harvest_integrate(
                    repo_root=repo_root,
                    workspace=workspace,
                    branch=self._config.workspace.integration_branch,
                    push_already_done=True,
                )
            except Exception as exc:
                logger.warning(
                    "Post-harvest integration failed for %s: %s",
                    node_id,
                    exc,
                    exc_info=True,
                )

        return status, error_message, touched_files, is_non_retryable

    async def _mark_subtasks_done(self, workspace: WorkspaceInfo) -> None:
        """Mark all subtasks in the task group as done in the worktree.

        Loads the spec from the worktree, sets non-dropped subtasks to
        done via afspec, saves, and commits the change. The commit gets
        squash-merged with the agent's code changes during harvest.

        Best-effort: logs and continues on any failure — the DB is the
        real source of truth for completion status.
        """
        from agentfox.core.config import resolve_spec_root

        try:
            import afspec
            from afspec.mutate import complete_subtask_states

            spec_dir = resolve_spec_root(self._config, workspace.path) / self._spec_name
            if not spec_dir.is_dir():
                logger.debug("Spec dir not found in worktree for %s, skipping subtask update", self._spec_name)
                return

            spec = afspec.load_spec(spec_dir)
            updated_tasks = complete_subtask_states(spec.tasks, [self._task_group])
            spec = spec.model_copy(update={"tasks": updated_tasks})
            afspec.save(spec, spec_dir)

            from agentfox.workspace.git import run_git

            tasks_json = spec_dir / "tasks.json"
            await run_git(["add", str(tasks_json)], cwd=workspace.path, check=True)
            await run_git(
                ["commit", "-m", f"chore: mark task group {self._task_group} subtasks done"],
                cwd=workspace.path,
                check=True,
            )
        except Exception:
            logger.warning(
                "Failed to mark subtasks done for %s:%d in worktree",
                self._spec_name,
                self._task_group,
                exc_info=True,
            )

    def _ingest_knowledge(
        self,
        node_id: str,
        touched_files: list[str],
        commit_sha: str,
        session_status: str,
        *,
        repo_root: Path | None = None,
        summary: str | None = None,
        archetype: str | None = None,
        task_group: str | None = None,
        attempt: int | None = None,
        rejected_approaches: list | None = None,
        gotchas: list | None = None,
        assumptions: list | None = None,
    ) -> None:
        """Ingest knowledge from a completed session via the KnowledgeProvider.

        Builds a context dict with session metadata and delegates to the
        provider's ingest() method.

        When *summary* is provided, it is included in the context dict
        under the ``"summary"`` key so the provider can store it
        (119-REQ-5.1).  *archetype*, *task_group*, and *attempt* are also
        passed for SummaryRecord construction (119-REQ-5.2).

        11-REQ-3.5: *rejected_approaches*, *gotchas*, and *assumptions* are
        passed through for enriched summary composition in _store_summary.

        Requirements: 114-REQ-4.1, 114-REQ-4.E1, 117-REQ-1.1, 119-REQ-5.1
        """
        context: dict[str, object] = {
            "touched_files": touched_files,
            "commit_sha": commit_sha,
            "session_status": session_status,
        }
        if repo_root is not None:
            context["project_root"] = str(repo_root)
            context["sink"] = self._sink
            context["run_id"] = self._run_id
        # 119-REQ-5.1: Include summary and session metadata for summary storage.
        if summary is not None:
            context["summary"] = summary
        if archetype is not None:
            context["archetype"] = archetype
        if task_group is not None:
            context["task_group"] = task_group
        if attempt is not None:
            context["attempt"] = attempt
        # 11-REQ-3.5: Pass structured fields for enriched summary composition.
        if rejected_approaches is not None:
            context["rejected_approaches"] = rejected_approaches
        if gotchas is not None:
            context["gotchas"] = gotchas
        if assumptions is not None:
            context["assumptions"] = assumptions
        try:
            self._knowledge_provider.ingest(node_id, self._spec_name, context)
        except Exception:
            # 114-REQ-4.E1: Log WARNING and continue without retrying
            logger.warning(
                "KnowledgeProvider.ingest() failed for %s, continuing",
                node_id,
                exc_info=True,
            )

    async def _extract_knowledge_and_findings(
        self,
        node_id: str,
        attempt: int,
        workspace: WorkspaceInfo,
        outcome_response: str = "",
    ) -> None:
        """Extract review findings from session output.

        113-REQ-1.1: Reconstructs the full conversation transcript from the
        agent trace JSONL events for the session's node_id and uses it as the
        primary transcript source.
        113-REQ-1.3: Continues to use session summary for the log message.
        113-REQ-1.E1: Falls back to _build_fallback_input when trace is
        unavailable.

        Knowledge ingestion is now handled by _ingest_knowledge() via the
        KnowledgeProvider protocol (114-REQ-4.1).

        Requirements: 27-REQ-3.1, 113-REQ-1.1, 113-REQ-1.E1, 113-REQ-1.E2
        """
        # 113-REQ-1.1: Reconstruct full transcript from agent trace JSONL
        from afaudit.constants import AUDIT_DIR
        from afaudit.trace import reconstruct_transcript

        audit_dir = getattr(self, "_audit_dir", None) or AUDIT_DIR
        transcript = reconstruct_transcript(audit_dir, self._run_id, node_id)

        # 113-REQ-1.E1, 113-REQ-1.E2: Fall back to _build_fallback_input
        # when trace is unavailable or has no assistant messages
        if not transcript:
            transcript = self._build_fallback_input(workspace, node_id)
        if not transcript:
            return

        # 27-REQ-3.1: Parse and persist structured findings from
        # review archetypes (reviewer, verifier).
        # Prefer the actual session response (which contains the agent's
        # JSON output) over the fallback transcript (which is metadata).
        review_text = outcome_response or transcript
        self._persist_review_findings(review_text, node_id, attempt)

    async def _run_and_harvest(
        self,
        node_id: str,
        attempt: int,
        workspace: WorkspaceInfo,
        system_prompt: str,
        task_prompt: str,
        repo_root: Path,
    ) -> SessionRecord:
        """Execute the session, harvest on success, return a record.

        The summary artifact is read *before* the audit event emission and
        knowledge ingestion so both consumers receive the same text from a
        single read (119-REQ-5.3).

        Requirements: 05-REQ-1.1, 11-REQ-4.2, 119-REQ-4.1, 119-REQ-5.3
        """
        outcome = await self._execute_session(
            node_id,
            workspace,
            system_prompt,
            task_prompt,
        )

        cost = calculate_session_cost(
            self._config,
            self._resolved_model_id,
            outcome.input_tokens,
            outcome.output_tokens,
            cache_read_input_tokens=outcome.cache_read_input_tokens,
            cache_creation_input_tokens=outcome.cache_creation_input_tokens,
        )

        # Detect budget exhaustion: the session failed and the computed cost
        # meets or exceeds the configured budget threshold.  The session did
        # real work (high token count) so retrying would just burn the same
        # budget again.  We use cost ratio alone — not an error-message sentinel
        # — so the check remains correct regardless of what diagnostic string
        # _map_message composes from the SDK ResultMessage.
        resolved_budget = resolve_max_budget(self._config, self._archetype)
        is_budget_exhausted = (
            outcome.status == "failed"
            and resolved_budget is not None
            and cost >= resolved_budget * _BUDGET_EXHAUST_RATIO
        )
        if is_budget_exhausted:
            logger.warning(
                "Session %s budget exhausted (cost=$%.2f of $%.2f budget), will not retry",
                node_id,
                cost,
                resolved_budget,
            )

        if outcome.status == "completed" and self._archetype == "coder":
            await self._mark_subtasks_done(workspace)

        status, error_message, touched_files, is_non_retryable = await self._harvest_and_integrate(
            node_id,
            outcome,
            workspace,
            repo_root,
        )

        if is_budget_exhausted:
            error_message = f"Budget exhausted (${cost:.2f} of ${resolved_budget:.2f})"

        # 35-REQ-1.1: Capture integration branch HEAD SHA after successful harvest
        commit_sha = ""
        if touched_files and status == "completed":
            commit_sha = await _capture_integration_head(repo_root, self._config.workspace.integration_branch)

        # 119-REQ-5.3: Read session artifacts once, before both the audit
        # event emission and knowledge ingestion.
        summary_text: str | None = None
        artifacts = self._read_session_artifacts(workspace)
        # 11-REQ-1.1, 11-REQ-1.2, 11-REQ-1.3: Extract structured fields
        # from session-summary.json for enriched summary composition.
        rejected_approaches: list | None = None
        gotchas_list: list | None = None
        assumptions_list: list | None = None
        if artifacts:
            summary_text = artifacts.get("summary") or None
            rejected_approaches = artifacts.get("rejected_approaches")
            gotchas_list = artifacts.get("gotchas")
            assumptions_list = artifacts.get("assumptions")
            if not summary_text:
                logger.debug(
                    "Session artifacts present but no summary for %s",
                    node_id,
                )

        # 40-REQ-7.2, 40-REQ-7.3: Emit session.complete or session.fail
        # 119-REQ-4.1, 119-REQ-4.2: Include summary in audit payload when available
        if status == "completed":
            payload: dict = {
                "archetype": self._archetype,
                "model_id": self._resolved_model_id,
                "prompt_template": self._archetype,
                "input_tokens": outcome.input_tokens,
                "output_tokens": outcome.output_tokens,
                "cache_read_input_tokens": outcome.cache_read_input_tokens,
                "cache_creation_input_tokens": outcome.cache_creation_input_tokens,
                "cost": cost,
                "duration_ms": outcome.duration_ms,
                "files_touched": touched_files,
            }
            # 119-REQ-4.1: Add summary to audit payload when available.
            # 119-REQ-4.2: Omit the key (not null) when no summary.
            # 119-REQ-4.E1: Truncate to 2000 chars for audit payload.
            if summary_text:
                _MAX_AUDIT_SUMMARY = 2000
                if len(summary_text) > _MAX_AUDIT_SUMMARY:
                    payload["summary"] = summary_text[:_MAX_AUDIT_SUMMARY] + "..."
                else:
                    payload["summary"] = summary_text
            emit_audit_event(
                self._sink,
                self._run_id,
                AuditEventType.SESSION_COMPLETE,
                node_id=node_id,
                archetype=self._archetype,
                payload=payload,
            )
        else:
            emit_audit_event(
                self._sink,
                self._run_id,
                AuditEventType.SESSION_FAIL,
                node_id=node_id,
                archetype=self._archetype,
                severity=AuditSeverity.ERROR,
                payload={
                    "archetype": self._archetype,
                    "model_id": self._resolved_model_id,
                    "prompt_template": self._archetype,
                    "error_message": error_message or "Unknown error",
                    "attempt": attempt,
                    "input_tokens": outcome.input_tokens,
                    "output_tokens": outcome.output_tokens,
                    "cache_read_input_tokens": outcome.cache_read_input_tokens,
                    "cache_creation_input_tokens": outcome.cache_creation_input_tokens,
                    "cost": cost,
                    "duration_ms": outcome.duration_ms,
                },
            )

        # Extract review findings and ingest knowledge on success.
        if status == "completed":
            await self._extract_knowledge_and_findings(
                node_id,
                attempt,
                workspace,
                outcome_response=outcome.response,
            )
            # 120-REQ-3.1, 120-REQ-3.2: Generate summaries for reviewer/verifier
            # sessions from persisted findings/verdicts when no agent-written
            # summary exists.
            if summary_text is None and self._archetype in ("reviewer", "verifier"):
                summary_text = self._generate_archetype_session_summary(node_id)
            # 114-REQ-4.1, 119-REQ-5.1: Ingest knowledge via KnowledgeProvider.
            # Pass summary, archetype, task_group, and attempt through the
            # context dict so the provider can store the summary.
            # 11-REQ-3.5: Pass structured fields for enriched summary composition.
            self._ingest_knowledge(
                node_id,
                touched_files,
                commit_sha,
                status,
                repo_root=repo_root,
                summary=summary_text,
                archetype=self._archetype,
                task_group=str(self._task_group),
                attempt=attempt,
                rejected_approaches=rejected_approaches,
                gotchas=gotchas_list,
                assumptions=assumptions_list,
            )

        # AC-3: Track whether the session completed but harvest/merge failed so
        # execute() can preserve the feature branch for recovery.
        is_harvest_failure = outcome.status == "completed" and status == "failed"

        total_input = outcome.input_tokens + outcome.cache_read_input_tokens + outcome.cache_creation_input_tokens

        return SessionRecord(
            node_id=node_id,
            attempt=attempt,
            status=status,
            input_tokens=total_input,
            output_tokens=outcome.output_tokens,
            cost=cost,
            duration_ms=outcome.duration_ms,
            error_message=error_message,
            timestamp=datetime.now(UTC).isoformat(),
            model=self._resolved_model_id,
            files_touched=touched_files,
            archetype=self._archetype,
            commit_sha=commit_sha,
            is_transport_error=getattr(outcome, "is_transport_error", False),
            is_budget_exhausted=is_budget_exhausted,
            is_non_retryable=is_non_retryable,
            is_harvest_failure=is_harvest_failure,
        )

    def _persist_review_findings(
        self,
        transcript: str,
        node_id: str,
        attempt: int,
    ) -> None:
        """Parse and persist structured findings from review archetypes.

        Requirements: 53-REQ-1.1, 53-REQ-2.1, 53-REQ-3.1
        """
        try:
            conn = self._knowledge_db.connection
        except Exception:
            logger.warning(
                "Failed to access knowledge DB for review persistence on %s",
                node_id,
                exc_info=True,
            )
            return
        from agentfox.core.config import resolve_spec_root

        persist_review_findings(
            transcript,
            node_id,
            attempt,
            archetype=self._archetype,
            spec_name=self._spec_name,
            task_group=self._task_group,
            knowledge_db_conn=conn,
            sink=self._sink,
            run_id=self._run_id,
            mode=self._mode,
            specs_dir=resolve_spec_root(self._config, Path.cwd()),
        )

    def _generate_archetype_session_summary(self, node_id: str) -> str | None:
        """Generate a summary for reviewer/verifier sessions from DB findings/verdicts.

        Queries the findings or verdicts persisted by ``_extract_knowledge_and_findings``
        for the given session and produces a human-readable summary string via
        ``generate_archetype_summary``.

        Returns ``None`` if the DB is unavailable or an error occurs.

        Requirements: 120-REQ-3.1, 120-REQ-3.2, 120-REQ-3.E1, 120-REQ-3.E2
        """
        try:
            conn = self._knowledge_db.connection
        except Exception:
            logger.warning(
                "Knowledge DB unavailable for archetype summary generation on %s",
                node_id,
                exc_info=True,
            )
            return None

        try:
            from agentfox.knowledge.fox_provider import generate_archetype_summary

            if self._archetype == "reviewer":
                from agentfox.knowledge.review_store import query_findings_by_session

                findings = query_findings_by_session(conn, node_id)
                return generate_archetype_summary("reviewer", findings=findings)
        except Exception:
            logger.warning(
                "Failed to generate archetype summary for %s (%s)",
                node_id,
                self._archetype,
                exc_info=True,
            )
        return None

    def _build_retry_context(self, spec_name: str) -> str:
        """Query active critical/major findings for the spec and format them.

        Requirements: 53-REQ-5.1, 53-REQ-5.2, 53-REQ-5.E1
        """
        return build_retry_context(
            self._knowledge_db,
            spec_name,
            task_group=str(self._task_group),
        )

    async def _setup_workspace(
        self,
        repo_root: Path,
        node_id: str,
    ) -> WorkspaceInfo:
        """Ensure integration branch is ready and create an isolated worktree.

        19-REQ-1.1, 19-REQ-1.6: ensure integration branch exists and is
        up-to-date before creating the worktree.
        """
        branch = self._config.workspace.integration_branch
        try:
            await ensure_integration_branch(repo_root, branch)
        except Exception:
            logger.warning(
                "ensure_integration_branch failed for %s, continuing with existing branch state",
                node_id,
                exc_info=True,
            )

        return await create_worktree(
            repo_root,
            self._spec_name,
            self._task_group,
            base_branch=branch,
            role=self._archetype,
            mode=self._mode,
        )

    async def _run_session_lifecycle(
        self,
        node_id: str,
        attempt: int,
        previous_error: str | None,
        repo_root: Path,
        workspace: WorkspaceInfo,
    ) -> SessionRecord:
        """Build prompts, execute session, and read artifacts.

        Session artifacts are now read inside ``_run_and_harvest()``
        (before the audit event and knowledge ingestion), so this method
        only handles cleanup.  The summary log message uses the record
        returned from ``_run_and_harvest()`` -- no second artifact read.

        Requirements: 119-REQ-5.3
        """
        system_prompt, task_prompt = self._build_prompts(
            repo_root,
            attempt,
            previous_error,
        )

        # 40-REQ-7.1: Emit session.start audit event before SDK call
        emit_audit_event(
            self._sink,
            self._run_id,
            AuditEventType.SESSION_START,
            node_id=node_id,
            archetype=self._archetype,
            payload={
                "archetype": self._archetype,
                "model_id": self._resolved_model_id,
                "prompt_template": self._archetype,
                "attempt": attempt,
            },
        )

        record = await self._run_and_harvest(
            node_id,
            attempt,
            workspace,
            system_prompt,
            task_prompt,
            repo_root,
        )

        # Artifact reading moved into _run_and_harvest() (119-REQ-5.3).
        # Cleanup is still done here after all consumers have read.
        self._cleanup_session_artifacts(workspace)

        return record

    async def execute(
        self,
        node_id: str,
        attempt: int,
        previous_error: str | None = None,
    ) -> SessionRecord:
        """Execute a coding session and return a SessionRecord.

        Full lifecycle:
        1. Create isolated worktree
        2. Run pre-session hooks (06-REQ-1.1)
        3. Assemble context, build prompts
        4. Run coding session via claude-code-sdk
        5. Run post-session hooks (06-REQ-2.1)
        6. Read session artifacts (.session-summary.json)
        7. Harvest changes into develop on success (03-REQ-7.1)
        8. Clean up the worktree (03-REQ-2.1)

        16-REQ-5.E1: Catches all exceptions and returns a failed
        SessionRecord so the orchestrator can apply retry logic.
        """
        repo_root = Path.cwd()
        workspace: WorkspaceInfo | None = None
        _preserve_branch = False  # set True when session completed but harvest failed

        try:
            workspace = await self._setup_workspace(repo_root, node_id)
        except Exception as exc:
            logger.error(
                "Workspace setup failed for %s (attempt %d): %s",
                node_id,
                attempt,
                exc,
            )
            return SessionRecord(
                node_id=node_id,
                attempt=attempt,
                status="failed",
                input_tokens=0,
                output_tokens=0,
                cost=0.0,
                duration_ms=0,
                error_message=str(exc),
                timestamp=datetime.now(UTC).isoformat(),
                archetype=self._archetype,
                is_workspace_setup_failure=True,
            )

        try:
            record = await self._run_session_lifecycle(node_id, attempt, previous_error, repo_root, workspace)
            # AC-3: Preserve the feature branch when harvest failed so the
            # committed coder work can be recovered.
            _preserve_branch = record.is_harvest_failure
            return record

        except Exception as exc:
            logger.error(
                "Session runner failed for %s (attempt %d): %s",
                node_id,
                attempt,
                exc,
            )
            return SessionRecord(
                node_id=node_id,
                attempt=attempt,
                status="failed",
                input_tokens=0,
                output_tokens=0,
                cost=0.0,
                duration_ms=0,
                error_message=str(exc),
                timestamp=datetime.now(UTC).isoformat(),
                archetype=self._archetype,
            )

        finally:
            # 03-REQ-2.1: Always clean up the worktree. When harvest failed,
            # preserve the feature branch so committed work is recoverable.
            #
            # Shield from asyncio cancellation: CancelledError (BaseException
            # since Python 3.9) would interrupt destroy_worktree() at its
            # first await, leaving stale worktrees that block retries (#638).
            if workspace is not None:
                try:
                    await asyncio.shield(destroy_worktree(repo_root, workspace, preserve_branch=_preserve_branch))
                except BaseException:
                    logger.warning(
                        "Failed to clean up worktree for %s",
                        node_id,
                        exc_info=True,
                    )


def build_retry_context(
    knowledge_db: KnowledgeDB,
    spec_name: str,
    task_group: str | None = None,
) -> str:
    """Query active critical/major findings for the spec and format them.

    Returns a structured block for inclusion in coder prompts (both first
    attempt and retries), listing all active critical and major review
    findings plus drift findings.  Returns an empty string if no such
    findings exist or if the DB is unavailable.

    When ``task_group`` is provided, findings from that group AND from
    group ``"0"`` (pre-review / drift-review) are included.  Findings
    from other task groups are excluded.  This ensures the coder sees
    pre-review and drift-review findings on the very first attempt,
    not only after a failed audit-review.

    Requirements: 53-REQ-5.1, 53-REQ-5.2, 53-REQ-5.E1
    """
    try:
        from agentfox.knowledge.review_store import (
            query_active_drift_findings,
            query_active_findings,
        )

        conn = knowledge_db.connection
        findings = query_active_findings(conn, spec_name, task_group=task_group, include_prereview=True)
        drift_findings = query_active_drift_findings(conn, spec_name, task_group=task_group, include_prereview=True)

        critical_major = [f for f in findings if f.severity in ("critical", "major")]
        critical_major_drift = [f for f in drift_findings if f.severity in ("critical", "major")]

        if not critical_major and not critical_major_drift:
            return ""

        lines = [
            f"## Prior Review Findings for {spec_name}",
            "",
            "The following critical/major issues were identified in prior "
            "review sessions. Please address these in your implementation:",
            "",
        ]
        for finding in critical_major:
            ref_str = f" [{finding.requirement_ref}]" if finding.requirement_ref else ""
            safe_desc = sanitize_prompt_content(finding.description, label="review-finding")
            lines.append(f"- **{finding.severity.upper()}**{ref_str}: {safe_desc}")
        for drift in critical_major_drift:
            safe_desc = sanitize_prompt_content(drift.description, label="drift-finding")
            lines.append(f"- **{drift.severity.upper()}** (drift): {safe_desc}")
        return "\n".join(lines)

    except Exception:
        logger.warning(
            "Failed to build retry context for %s, continuing without",
            spec_name,
            exc_info=True,
        )
        return ""
