"""Workspace health check, force-clean, and run-level pre-flight logic.

Single responsibility: assess and optionally remediate repository working
tree state before session dispatch.

Requirements: 118-REQ-1.1, 118-REQ-1.3, 118-REQ-1.E1, 118-REQ-1.E2,
              118-REQ-2.1, 118-REQ-2.E1, 118-REQ-2.E2,
              118-REQ-8.1, 118-REQ-8.2, 118-REQ-8.E1
"""

from __future__ import annotations

import logging
import time
from dataclasses import dataclass, field
from pathlib import Path

from agentfox.workspace.git import run_git
from agentfox.workspace.worktree import _cleanup_empty_ancestors, _safe_rmtree

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class HealthReport:
    """Result of a workspace health check.

    Attributes:
        untracked_files: Untracked files in the repo (excluding .gitignore).
        dirty_index_files: Files staged in the index but not committed.
    """

    untracked_files: list[str]
    dirty_index_files: list[str]

    @property
    def has_issues(self) -> bool:
        """Return True if the workspace has any issues."""
        return bool(self.untracked_files or self.dirty_index_files)

    @property
    def all_files(self) -> list[str]:
        """Return a sorted deduplicated list of all problematic files."""
        return sorted(set(self.untracked_files + self.dirty_index_files))


async def check_workspace_health(repo_root: Path) -> HealthReport:
    """Check repo working tree for untracked files and dirty index.

    Uses ``git ls-files --others --exclude-standard`` for untracked files
    and ``git diff --cached --name-only`` for dirty index.

    Fails open on git command errors: returns an empty report and logs
    a WARNING.

    Requirements: 118-REQ-1.1, 118-REQ-1.E1, 118-REQ-1.E2
    """
    untracked: list[str] = []
    dirty_index: list[str] = []

    # Detect untracked files
    try:
        rc, stdout, _stderr = await run_git(
            ["ls-files", "--others", "--exclude-standard"],
            cwd=repo_root,
            check=False,
        )
        if rc != 0:
            logger.warning(
                "git ls-files failed (rc=%d); proceeding with empty untracked list",
                rc,
            )
        else:
            untracked = [f for f in stdout.strip().split("\n") if f]
    except Exception:
        logger.warning(
            "git ls-files raised an exception; proceeding with empty untracked list",
            exc_info=True,
        )

    # Detect dirty index (staged but uncommitted changes)
    try:
        rc, stdout, _stderr = await run_git(
            ["diff", "--cached", "--name-only"],
            cwd=repo_root,
            check=False,
        )
        if rc != 0:
            logger.warning(
                "git diff --cached failed (rc=%d); proceeding with empty dirty index list",
                rc,
            )
        else:
            dirty_index = [f for f in stdout.strip().split("\n") if f]
    except Exception:
        logger.warning(
            "git diff --cached raised an exception; proceeding with empty dirty index list",
            exc_info=True,
        )

    return HealthReport(
        untracked_files=untracked,
        dirty_index_files=dirty_index,
    )


async def force_clean_workspace(
    repo_root: Path,
    report: HealthReport,
) -> HealthReport:
    """Remove untracked files and reset dirty index.

    Handles permission errors per file: logs a WARNING and keeps the
    file in the returned report. Returns an updated HealthReport
    reflecting the actual state after cleanup.

    Requirements: 118-REQ-2.1, 118-REQ-2.E1, 118-REQ-2.E2
    """
    failed_untracked: list[str] = []

    # Remove untracked files
    resolved_root = repo_root.resolve()
    for rel_path in report.untracked_files:
        abs_path = repo_root / rel_path
        resolved = abs_path.resolve()
        if not resolved.is_relative_to(resolved_root):
            logger.warning("Force-clean: skipping path outside repo root: %s", rel_path)
            failed_untracked.append(rel_path)
            continue
        try:
            abs_path.unlink()
            logger.warning("Force-clean: removed untracked file %s", rel_path)
        except OSError as exc:
            logger.warning(
                "Force-clean: could not remove %s: %s",
                rel_path,
                exc,
            )
            failed_untracked.append(rel_path)

    # Reset dirty index via git checkout -- .
    remaining_dirty: list[str] = []
    if report.dirty_index_files:
        try:
            rc, _stdout, stderr = await run_git(
                ["checkout", "--", "."],
                cwd=repo_root,
                check=False,
            )
            if rc != 0:
                logger.warning(
                    "Force-clean: git checkout -- . failed (rc=%d): %s",
                    rc,
                    stderr.strip(),
                )
                remaining_dirty = list(report.dirty_index_files)
            else:
                logger.warning(
                    "Force-clean: reset dirty index files: %s",
                    ", ".join(report.dirty_index_files),
                )
        except Exception:
            logger.warning(
                "Force-clean: git checkout raised an exception",
                exc_info=True,
            )
            remaining_dirty = list(report.dirty_index_files)

    return HealthReport(
        untracked_files=failed_untracked,
        dirty_index_files=remaining_dirty,
    )


def format_health_diagnostic(
    report: HealthReport,
    *,
    max_files: int = 20,
) -> str:
    """Format a HealthReport into an actionable error message.

    Includes:
    - File list (truncated at ``max_files`` with "... and N more")
    - ``git clean -fd`` remediation command
    - ``--force-clean`` suggestion for automatic cleanup

    Requirements: 118-REQ-8.1, 118-REQ-8.2, 118-REQ-8.E1
    """
    lines: list[str] = []
    lines.append("Workspace has issues that would block harvest:")
    lines.append("")

    all_files = report.all_files
    shown = all_files[:max_files]

    if report.untracked_files:
        untracked_shown = [f for f in shown if f in report.untracked_files]
        if untracked_shown:
            lines.append("Untracked files:")
            for f in untracked_shown:
                lines.append(f"  {f}")

    if report.dirty_index_files:
        dirty_shown = [f for f in shown if f in report.dirty_index_files]
        if dirty_shown:
            lines.append("Staged but uncommitted files:")
            for f in dirty_shown:
                lines.append(f"  {f}")

    if len(all_files) > max_files:
        overflow = len(all_files) - max_files
        lines.append(f"  ... and {overflow} more")

    lines.append("")
    lines.append("Remediation:")
    lines.append("  git clean -fd          # remove untracked files")
    lines.append("  git checkout -- .      # reset staged changes")
    lines.append("")
    lines.append("Or re-run with --force-clean to automatically clean the workspace.")

    return "\n".join(lines)


# ---------------------------------------------------------------------------
# Run-level pre-flight workspace check
# ---------------------------------------------------------------------------

_STALE_LOCK_AGE_SECONDS = 3600
_CREDENTIAL_CHECK_TIMEOUT = 10


@dataclass
class WorkspacePreflightResult:
    """Result of a run-level workspace pre-flight check."""

    push_available: bool = True
    issues_found: list[str] = field(default_factory=list)
    worktrees_pruned: bool = False
    stale_locks_found: list[str] = field(default_factory=list)
    stale_worktrees_removed: int = 0


@dataclass
class _StaleCleanupResult:
    """Internal result of stale worktree cleanup."""

    removed: int = 0
    pruned: bool = False


async def cleanup_stale_worktrees(repo_root: Path) -> int:
    """Remove all worktree directories under ``.agent-fox/worktrees/``.

    At orchestrator startup there are no active sessions, so every directory
    under the worktrees root is stale — left over from a prior interrupted
    run.  Each worktree is removed via ``git worktree remove --force``,
    falling back to ``_safe_rmtree`` if the git command fails.  Empty
    parent directories are cleaned up via ``_cleanup_empty_ancestors``.

    Ends with a single ``git worktree prune`` to clean the registry.

    Best-effort: never raises.  Returns the count of worktrees removed.

    Issue: #629, #694
    """
    result = await _cleanup_stale_worktrees_impl(repo_root)
    return result.removed


async def _cleanup_stale_worktrees_impl(repo_root: Path) -> _StaleCleanupResult:
    """Core implementation of stale worktree cleanup.

    Returns a ``_StaleCleanupResult`` with both the removal count and
    whether ``git worktree prune`` succeeded, so that
    ``run_preflight_workspace_check`` can populate ``worktrees_pruned``
    from the single prune call without running a redundant second one.
    """
    result = _StaleCleanupResult()
    worktrees_root = repo_root / ".agent-fox" / "worktrees"
    if not worktrees_root.is_dir():
        # No worktree directories, but still prune the git registry to
        # clean up any stale entries referencing deleted directories.
        try:
            rc, _stdout, _stderr = await run_git(
                ["worktree", "prune"], cwd=repo_root, check=False
            )
            result.pruned = rc == 0
        except Exception:
            logger.debug("git worktree prune failed during stale cleanup", exc_info=True)
        return result

    # Collect worktree paths registered in git that live under our root.
    registered: set[str] = set()
    try:
        _rc, stdout, _stderr = await run_git(
            ["worktree", "list", "--porcelain"],
            cwd=repo_root,
            check=False,
        )
        for line in stdout.splitlines():
            stripped = line.strip()
            if stripped.startswith("worktree "):
                wt_path = stripped[len("worktree ") :]
                try:
                    if Path(wt_path).is_relative_to(worktrees_root):
                        registered.add(wt_path)
                except (ValueError, TypeError):
                    pass
    except Exception:
        logger.debug("Could not list worktrees during stale cleanup", exc_info=True)

    # Track paths that were successfully removed for ancestor cleanup.
    removed_paths: list[Path] = []

    # Phase 1: remove registered worktrees via git worktree remove --force
    for wt_path_str in registered:
        wt_path = Path(wt_path_str)
        if not wt_path.exists():
            continue
        try:
            rc, _, _ = await run_git(
                ["worktree", "remove", "--force", str(wt_path)],
                cwd=repo_root,
                check=False,
            )
            if rc != 0 and wt_path.exists():
                _safe_rmtree(wt_path)
            if not wt_path.exists():
                result.removed += 1
                removed_paths.append(wt_path)
                logger.info("Removed stale worktree: %s", wt_path)
        except Exception:
            logger.debug("Failed to remove registered worktree %s", wt_path, exc_info=True)

    # Phase 2: remove any remaining directories (orphans not in git registry)
    try:
        for child in _walk_leaf_dirs(worktrees_root):
            if child.is_dir():
                try:
                    _safe_rmtree(child)
                    if not child.exists():
                        result.removed += 1
                        removed_paths.append(child)
                        logger.info("Removed orphan worktree directory: %s", child)
                except Exception:
                    logger.debug("Failed to remove orphan directory %s", child, exc_info=True)
    except Exception:
        logger.debug("Failed to scan for orphan worktree directories", exc_info=True)

    # Phase 3: clean up empty ancestor directories using the consolidated
    # _cleanup_empty_ancestors from worktree.py (issue #694).
    for wt_path in removed_paths:
        try:
            _cleanup_empty_ancestors(wt_path, worktrees_root)
        except Exception:
            logger.debug(
                "Failed to clean up empty ancestors for %s", wt_path, exc_info=True
            )

    # Phase 4: prune the git worktree registry (single prune per startup)
    try:
        rc, _stdout, _stderr = await run_git(
            ["worktree", "prune"], cwd=repo_root, check=False
        )
        result.pruned = rc == 0
    except Exception:
        logger.debug("git worktree prune failed during stale cleanup", exc_info=True)

    if result.removed:
        logger.info("Cleaned up %d stale worktree(s) from prior run", result.removed)

    return result


def _walk_leaf_dirs(root: Path) -> list[Path]:
    """Return leaf directories under *root* (dirs with no subdirectories)."""
    leaves: list[Path] = []
    try:
        for child in root.rglob("*"):
            if child.is_dir() and not any(c.is_dir() for c in child.iterdir()):
                leaves.append(child)
    except Exception:
        pass
    return leaves


async def run_preflight_workspace_check(repo_root: Path) -> WorkspacePreflightResult:
    """Run workspace health checks once at the start of an orchestrator run.

    Validates:
    1. Prune stale git worktree entries.
    2. Check for stale .git lock files (age > 1 hour).
    3. Test git credential availability via ``git ls-remote``.

    All checks are best-effort: failures log warnings but never raise.
    """
    result = WorkspacePreflightResult()

    # 1. Remove stale worktree directories and prune the registry (single
    #    prune per startup — issue #694).
    try:
        cleanup_result = await _cleanup_stale_worktrees_impl(repo_root)
        result.stale_worktrees_removed = cleanup_result.removed
        result.worktrees_pruned = cleanup_result.pruned
        if cleanup_result.pruned:
            logger.info("Run pre-flight: pruned stale worktree entries")
        else:
            msg = "git worktree prune failed during stale cleanup"
            result.issues_found.append(msg)
            logger.warning("Run pre-flight: %s", msg)
    except Exception:
        logger.warning("Run pre-flight: stale worktree cleanup raised exception", exc_info=True)

    # 2. Check for stale lock files in .git/
    git_dir = repo_root / ".git"
    if git_dir.is_dir():
        now = time.time()
        try:
            for lock_file in git_dir.glob("*.lock"):
                try:
                    age = now - lock_file.stat().st_mtime
                    if age > _STALE_LOCK_AGE_SECONDS:
                        result.stale_locks_found.append(str(lock_file.name))
                        msg = f"Stale lock file: {lock_file.name} (age {int(age)}s)"
                        result.issues_found.append(msg)
                        logger.warning("Run pre-flight: %s", msg)
                except OSError:
                    pass
        except OSError:
            logger.debug("Run pre-flight: could not scan .git/ for lock files", exc_info=True)

    # 3. Test git credential availability
    try:
        rc, _stdout, stderr = await run_git(
            ["ls-remote", "--exit-code", "origin", "HEAD"],
            cwd=repo_root,
            check=False,
            timeout=_CREDENTIAL_CHECK_TIMEOUT,
        )
        if rc != 0:
            lower_stderr = stderr.lower()
            if "terminal prompts disabled" in lower_stderr or "authentication" in lower_stderr:
                result.push_available = False
                msg = "Git push credentials unavailable — push will be disabled for this run"
                result.issues_found.append(msg)
                logger.warning("Run pre-flight: %s", msg)
            else:
                msg = f"git ls-remote failed (rc={rc}): {stderr.strip()}"
                result.issues_found.append(msg)
                logger.warning("Run pre-flight: %s", msg)
    except Exception:
        logger.warning("Run pre-flight: credential check raised exception", exc_info=True)

    return result
