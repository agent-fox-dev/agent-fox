"""Git worktree lifecycle management: create and destroy isolated workspaces."""

from __future__ import annotations

import logging
import re
import shutil
from dataclasses import dataclass
from pathlib import Path

from agentfox.core.errors import WorkspaceError
from agentfox.workspace.git import (
    branch_used_by_worktree,
    create_branch,
    delete_branch,
    run_git,
)

logger = logging.getLogger(__name__)


def _safe_rmtree(path: Path) -> None:
    """Remove a directory tree without following symlinks to external targets.

    Unlinks any symlinks found inside *path* (removing the link itself, not the
    target) before delegating to ``shutil.rmtree``.  This prevents CWE-59
    (Improper Link Resolution) where a symlink inside the worktree could cause
    ``shutil.rmtree`` to delete files outside the repository.
    """
    for item in path.rglob("*"):
        if item.is_symlink():
            item.unlink()  # remove the link itself, not the target
    shutil.rmtree(path, ignore_errors=True)


@dataclass(frozen=True)
class WorkspaceInfo:
    """Metadata about a created workspace."""

    path: Path
    branch: str
    spec_name: str
    task_group: int
    role: str | None = None
    mode: str | None = None


def _cleanup_empty_ancestors(
    worktree_path: Path,
    root: Path,
) -> None:
    """Remove empty directories from worktree_path up to (not including) root.

    Walks upward from ``worktree_path``, removing directories that are empty.
    Stops when it reaches ``root`` (which is never removed), encounters a
    non-empty directory, or a removal fails.

    Errors are swallowed: ``PermissionError`` triggers a WARNING log, other
    ``OSError`` (e.g. directory not empty) silently stops traversal.

    The traversal is depth-agnostic — it handles both 2-level paths
    (``spec/task_group``) and 4-level paths (``spec/task_group/role/mode``)
    without modification (09-REQ-6.2).

    Requirements: 80-REQ-3.1, 80-REQ-3.2, 80-REQ-3.E1, 80-REQ-3.E2, 09-REQ-6.2
    """
    current = worktree_path
    while current != root:
        # Safety guard: never escape above root
        try:
            current.relative_to(root)
        except ValueError:
            break

        if not current.exists():
            current = current.parent
            continue

        try:
            current.rmdir()  # Only succeeds when the directory is empty
            logger.debug("Removed empty directory: %s", current)
        except PermissionError as exc:
            logger.warning("Failed to remove directory %s: %s", current, exc)
            break
        except OSError:
            # Directory is not empty or another OS error — stop traversal
            break

        current = current.parent


async def create_worktree(
    repo_root: Path,
    spec_name: str,
    task_group: int,
    base_branch: str,
    branch_name: str | None = None,
    role: str | None = None,
    mode: str | None = None,
) -> WorkspaceInfo:
    """Create an isolated git worktree for a coding session.

    Creates a worktree at ``.agent-fox/worktrees/{spec_name}/{task_group}``
    (2-level path) when *mode* is absent, or at
    ``.agent-fox/worktrees/{spec_name}/{task_group}/{role}/{mode}``
    (4-level path) when *mode* is present.  The branch name follows the
    same pattern: ``feature/{spec_name}/{task_group}`` or
    ``feature/{spec_name}/{task_group}/{role}/{mode}``.

    When *branch_name* is provided it is used as the git branch instead
    of the derived convention.  The worktree filesystem path is always
    derived from *spec_name*, *task_group*, and optionally *role*/*mode*.

    Empty-string values for *role* and *mode* are normalised to ``None``
    via ``effective_mode = mode or None`` / ``effective_role = role or None``.

    If *mode* is set but *role* is absent (``None`` or ``""``), a
    WARNING-level log is emitted and ``"unknown"`` is substituted as the
    role segment.

    If a stale worktree or branch exists, it is removed first.

    Requirements: 80-REQ-1.2, 80-REQ-3.2, 09-REQ-1, 09-REQ-2, 09-REQ-5

    Raises:
        WorkspaceError: If worktree creation fails.
    """
    if not re.fullmatch(r"[a-zA-Z0-9_-]+", spec_name):
        raise WorkspaceError(f"Invalid spec name: {spec_name!r}")

    # Normalise empty strings to None (09-REQ-1.2)
    effective_mode = mode or None
    effective_role = role or None

    worktrees_root = repo_root / ".agent-fox" / "worktrees"

    # Path and branch derivation (09-REQ-2)
    if effective_mode is None:
        # 2-level path — role is silently ignored (09-REQ-2.1, 09-REQ-2.3)
        worktree_path = worktrees_root / spec_name / str(task_group)
        branch_name = branch_name or f"feature/{spec_name}/{task_group}"
        # When mode is absent, normalise role to None for WorkspaceInfo
        effective_role = None
    else:
        # 4-level path — mode is present
        if effective_role is None:
            # Mode set but role absent: substitute 'unknown' + WARNING (09-REQ-2.4)
            logger.warning(
                "worktree: mode=%r was provided but role is None/empty "
                "for spec=%r task_group=%s — defaulting role to 'unknown'. "
                "Check graph config.",
                effective_mode,
                spec_name,
                task_group,
            )
            effective_role = "unknown"
        worktree_path = (
            worktrees_root / spec_name / str(task_group)
            / effective_role / effective_mode
        )
        branch_name = branch_name or (
            f"feature/{spec_name}/{task_group}"
            f"/{effective_role}/{effective_mode}"
        )

    # Clean up orphaned empty sibling directories under the spec directory.
    # These are left over from prior crashed or partial cleanup runs.
    spec_dir = worktrees_root / spec_name
    if spec_dir.exists():
        for child in list(spec_dir.iterdir()):
            if child.is_dir() and not any(child.iterdir()):
                try:
                    child.rmdir()
                    logger.debug("Removed orphaned empty directory: %s", child)
                except OSError as exc:
                    logger.warning("Could not remove orphaned directory %s: %s", child, exc)

    # Clean up stale worktree if it exists (03-REQ-1.E1)
    if worktree_path.exists():
        logger.info("Removing stale worktree at %s", worktree_path)
        await run_git(
            ["worktree", "remove", "--force", str(worktree_path)],
            cwd=repo_root,
            check=False,
        )
        # If git worktree remove didn't fully clean up, remove manually
        if worktree_path.exists():
            _safe_rmtree(worktree_path)

        # Clean up empty ancestor directories from the stale removal (80-REQ-3.2)
        _cleanup_empty_ancestors(worktree_path, worktrees_root)

    # Prune worktree registry to clean up any stale entries
    await run_git(["worktree", "prune"], cwd=repo_root, check=False)

    # Post-prune verification: ensure the branch is no longer referenced (80-REQ-1.2)
    still_referenced = await branch_used_by_worktree(repo_root, branch_name)
    if still_referenced:
        # Second prune attempt (80-REQ-1.E1)
        await run_git(["worktree", "prune"], cwd=repo_root, check=False)
        still_referenced = await branch_used_by_worktree(repo_root, branch_name)

    if still_referenced:
        logger.warning(
            "Branch '%s' is still referenced by a worktree after two prune attempts; skipping stale branch deletion",
            branch_name,
        )
    else:
        # Clean up stale feature branch if it exists (03-REQ-1.E2)
        await delete_branch(repo_root, branch_name, force=True)

        # Also delete the remote tracking branch to prevent divergent
        # histories when the branch is recreated from a newer base.
        await run_git(
            ["push", "origin", "--delete", branch_name],
            cwd=repo_root,
            check=False,
        )

    # Create the feature branch from the base branch tip
    await create_branch(repo_root, branch_name, base_branch)

    # Ensure parent directory exists
    worktree_path.parent.mkdir(parents=True, exist_ok=True)

    # Create the worktree with the feature branch checked out
    await run_git(
        ["worktree", "add", str(worktree_path), branch_name],
        cwd=repo_root,
    )

    return WorkspaceInfo(
        path=worktree_path,
        branch=branch_name,
        spec_name=spec_name,
        task_group=task_group,
        role=effective_role,
        mode=effective_mode,
    )


async def destroy_worktree(
    repo_root: Path,
    workspace: WorkspaceInfo,
    *,
    preserve_branch: bool = False,
) -> None:
    """Remove a git worktree and its feature branch.

    Removes the worktree directory, prunes the worktree registry,
    verifies the branch is no longer referenced, and deletes the feature
    branch. Cleans up empty ancestor directories.

    Does not raise if the worktree or branch is already gone.

    When *preserve_branch* is True, the feature branch is renamed to
    ``stalled/<original-branch-name>`` instead of being deleted, so that
    committed work is recoverable after a harvest failure.  The worktree
    directory is still removed.

    All operations use ``workspace.path`` directly — the path is never
    re-derived from ``spec_name``/``task_group``/``role``/``mode``,
    making this function transparent to both 2-level and 4-level
    worktree path structures (09-REQ-6.1).

    Requirements: 80-REQ-1.1, 80-REQ-1.E1, 80-REQ-3.1, 09-REQ-6.1
    """
    worktrees_root = repo_root / ".agent-fox" / "worktrees"

    # 03-REQ-2.E1: If worktree path does not exist, treat removal as no-op
    if workspace.path.exists():
        # Remove the worktree via git
        await run_git(
            ["worktree", "remove", "--force", str(workspace.path)],
            cwd=repo_root,
            check=False,
        )
        # If git worktree remove didn't fully clean up, remove manually
        if workspace.path.exists():
            _safe_rmtree(workspace.path)

    # Prune worktree registry
    await run_git(["worktree", "prune"], cwd=repo_root, check=False)

    # Post-prune verification: check if branch is still referenced (80-REQ-1.1)
    still_referenced = await branch_used_by_worktree(repo_root, workspace.branch)
    if still_referenced:
        # Second prune attempt (80-REQ-1.E1)
        await run_git(["worktree", "prune"], cwd=repo_root, check=False)
        still_referenced = await branch_used_by_worktree(repo_root, workspace.branch)

    if still_referenced:
        logger.warning(
            "Branch '%s' is still referenced by a worktree after two prune attempts; skipping branch deletion",
            workspace.branch,
        )
    elif preserve_branch:
        # AC-3: Rename instead of deleting so committed coder work is recoverable.
        stalled_name = f"stalled/{workspace.branch}"
        rc, _, _ = await run_git(
            ["branch", "-m", workspace.branch, stalled_name],
            cwd=repo_root,
            check=False,
        )
        if rc == 0:
            logger.warning(
                "Harvest failed: preserved feature branch as '%s' for recovery",
                stalled_name,
            )
        else:
            # Rename failed (branch may not exist) — fall back to delete
            await delete_branch(repo_root, workspace.branch, force=True)
    else:
        # Delete the feature branch (03-REQ-2.E2: log warning if not found)
        await delete_branch(repo_root, workspace.branch, force=True)

    # Clean up empty ancestor directories (80-REQ-3.1)
    _cleanup_empty_ancestors(workspace.path, worktrees_root)
