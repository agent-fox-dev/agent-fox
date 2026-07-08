"""Integration branch management: ensure, sync, and reconcile.

Requirements: 19-REQ-1.1 through 19-REQ-1.6,
              45-REQ-3.2, 45-REQ-5.1, 45-REQ-5.2, 45-REQ-5.E1, 45-REQ-6.2,
              118-REQ-5.1, 118-REQ-5.2, 118-REQ-5.3, 118-REQ-5.E1
"""

from __future__ import annotations

import logging
from pathlib import Path

from afaudit.emit import emit_audit_event
from afaudit.events import AuditEventType, AuditSeverity

from agentfox.core.errors import WorkspaceError
from agentfox.workspace.git import (
    detect_default_branch,
    local_branch_exists,
    remote_branch_exists,
    run_git,
)
from agentfox.workspace.merge_agent import run_merge_agent
from agentfox.workspace.merge_lock import MergeLock

logger = logging.getLogger(__name__)


async def ensure_integration_branch(repo_root: Path, branch: str) -> None:
    """Ensure a local integration branch exists and is up-to-date.

    1. Fetch origin (warn and continue on failure).
    2. If local branch exists:
       a. If origin/<branch> exists and local is behind, fast-forward.
       b. If diverged, warn and use local as-is.
    3. If local branch does not exist:
       a. If origin/<branch> exists, create tracking branch.
       b. Otherwise, create from default branch.

    Raises:
        WorkspaceError: If no suitable base branch can be found.

    Requirements: 19-REQ-1.1, 19-REQ-1.2, 19-REQ-1.3, 19-REQ-1.5, 19-REQ-1.6,
                  118-REQ-5.E1
    """
    remote_ref = f"origin/{branch}"

    fetch_ok = True
    try:
        await run_git(["fetch", "origin"], cwd=repo_root)
    except WorkspaceError as exc:
        logger.warning("Failed to fetch from origin; proceeding with local state only")
        fetch_ok = False
        emit_audit_event(
            None,
            "",
            AuditEventType.DEVELOP_FETCH_FAILED,
            severity=AuditSeverity.WARNING,
            payload={"reason": str(exc)},
        )

    has_local = await local_branch_exists(repo_root, branch)

    if has_local:
        if fetch_ok:
            has_remote = await remote_branch_exists(repo_root, branch)
            if has_remote:
                await _sync_integration_with_remote(repo_root, branch)
        logger.info("Local %s branch is ready", branch)
        return

    if fetch_ok:
        has_remote = await remote_branch_exists(repo_root, branch)
        if has_remote:
            await run_git(
                ["branch", branch, remote_ref],
                cwd=repo_root,
            )
            logger.info("Created local %s branch tracking %s", branch, remote_ref)
            return

    default_branch = await detect_default_branch(repo_root)
    await run_git(
        ["branch", branch, default_branch],
        cwd=repo_root,
    )
    logger.info("Created local %s branch from '%s'", branch, default_branch)


async def _sync_integration_with_remote(
    repo_root: Path,
    branch: str,
    *,
    _lock_held: bool = False,
) -> str | None:
    """Synchronize local integration branch with its remote counterpart.

    Checks commit counts to determine if local is behind, ahead,
    or diverged from remote. Fast-forwards if behind only.

    Requirements: 19-REQ-1.6, 19-REQ-1.E1, 19-REQ-1.E4,
                  45-REQ-3.2, 45-REQ-5.1, 45-REQ-5.2, 45-REQ-5.E1, 45-REQ-6.2,
                  121-REQ-4.1, 121-REQ-4.2, 121-REQ-4.E1
    """
    remote_ref = f"origin/{branch}"

    _rc, remote_ahead_str, _stderr = await run_git(
        ["rev-list", "--count", f"{branch}..{remote_ref}"],
        cwd=repo_root,
        check=False,
    )
    remote_ahead = int(remote_ahead_str.strip()) if remote_ahead_str.strip() else 0

    _rc, local_ahead_str, _stderr = await run_git(
        ["rev-list", "--count", f"{remote_ref}..{branch}"],
        cwd=repo_root,
        check=False,
    )
    local_ahead = int(local_ahead_str.strip()) if local_ahead_str.strip() else 0

    if remote_ahead == 0:
        return None

    if _lock_held:
        return await _sync_integration_under_lock(repo_root, branch, remote_ahead, local_ahead)

    lock = MergeLock(repo_root)
    async with lock:
        return await _sync_integration_under_lock(repo_root, branch, remote_ahead, local_ahead)


async def _sync_integration_under_lock(repo_root: Path, branch: str, remote_ahead: int, local_ahead: int) -> str | None:
    """Execute the integration branch sync strategies under the merge lock.

    Requirements: 118-REQ-5.1, 118-REQ-5.2, 118-REQ-5.3
    """
    remote_ref = f"origin/{branch}"
    sync_method: str | None = None

    if local_ahead > 0 and remote_ahead > 0:
        logger.info(
            "Local %s has diverged from %s (%d local, %d remote commits). Attempting rebase.",
            branch,
            remote_ref,
            local_ahead,
            remote_ahead,
        )

        _rc, current_ref, _ = await run_git(
            ["symbolic-ref", "--short", "HEAD"],
            cwd=repo_root,
            check=False,
        )
        original_branch = current_ref.strip() if _rc == 0 else ""

        rc_co, _, _ = await run_git(
            ["checkout", branch],
            cwd=repo_root,
            check=False,
        )
        if rc_co != 0:
            logger.warning(
                "Could not checkout %s for rebase. Using local as-is.",
                branch,
            )
            emit_audit_event(
                None,
                "",
                AuditEventType.DEVELOP_SYNC_FAILED,
                severity=AuditSeverity.WARNING,
                payload={
                    "reason": f"Could not checkout {branch} for rebase",
                    "local_ahead": local_ahead,
                    "remote_ahead": remote_ahead,
                },
            )
            return None

        rc_rb, _, stderr_rb = await run_git(
            ["rebase", remote_ref],
            cwd=repo_root,
            check=False,
        )
        if rc_rb == 0:
            sync_method = "rebase"
            logger.info(
                "Rebased %d local commit(s) onto %s successfully.",
                local_ahead,
                remote_ref,
            )
        else:
            await run_git(["rebase", "--abort"], cwd=repo_root, check=False)
            logger.info("Rebase failed; attempting merge commit fallback.")

            rc_merge, stdout_merge, stderr_merge = await run_git(
                ["merge", "--no-edit", remote_ref],
                cwd=repo_root,
                check=False,
            )
            if rc_merge == 0:
                sync_method = "merge"
                logger.info(
                    "Merged %s into local %s via merge commit.",
                    remote_ref,
                    branch,
                )
            else:
                await run_git(["merge", "--abort"], cwd=repo_root, check=False)
                logger.info("Merge commit failed; spawning merge agent to resolve conflicts.")

                conflict_output = stderr_merge.strip() or stdout_merge.strip() or "merge conflict"
                resolved = await run_merge_agent(
                    worktree_path=repo_root,
                    conflict_output=conflict_output,
                    model_id="ADVANCED",
                )
                if resolved:
                    sync_method = "merge-agent"
                    logger.info(
                        "Merge agent resolved %s-sync conflicts successfully.",
                        branch,
                    )
                else:
                    logger.warning(
                        "Merge agent failed to resolve %s-sync conflicts. Using local %s as-is; verify manually.",
                        branch,
                        branch,
                    )
                    emit_audit_event(
                        None,
                        "",
                        AuditEventType.DEVELOP_SYNC_FAILED,
                        severity=AuditSeverity.WARNING,
                        payload={
                            "reason": "Merge agent failed to resolve conflicts",
                            "local_ahead": local_ahead,
                            "remote_ahead": remote_ahead,
                        },
                    )
                    if original_branch and original_branch != branch:
                        await run_git(
                            ["checkout", original_branch],
                            cwd=repo_root,
                            check=False,
                        )
                    return None

        if original_branch and original_branch != branch:
            await run_git(
                ["checkout", original_branch],
                cwd=repo_root,
                check=False,
            )

        if sync_method is not None:
            emit_audit_event(
                None,
                "",
                AuditEventType.DEVELOP_SYNC,
                payload={
                    "method": sync_method,
                    "local_ahead": local_ahead,
                    "remote_ahead": remote_ahead,
                },
            )
        return sync_method

    logger.info(
        "Fast-forwarding local %s (%d commits behind %s)",
        branch,
        remote_ahead,
        remote_ref,
    )

    rc_ff, _, _ = await run_git(
        ["merge", "--ff-only", remote_ref],
        cwd=repo_root,
        check=False,
    )
    if rc_ff != 0:
        await run_git(
            ["branch", "-f", branch, remote_ref],
            cwd=repo_root,
        )

    sync_method = "fast-forward"

    emit_audit_event(
        None,
        "",
        AuditEventType.DEVELOP_SYNC,
        payload={
            "method": sync_method,
            "local_ahead": local_ahead,
            "remote_ahead": remote_ahead,
        },
    )
    return sync_method
