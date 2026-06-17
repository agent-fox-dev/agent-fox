"""Lifecycle state machine management.

Handles lifecycle transitions (draft -> active -> sealed -> archived),
spec supersession, and archival with folder movement.
"""

from __future__ import annotations

import shutil
from pathlib import Path

from afspec.exceptions import LifecycleError
from afspec.models import Spec, Status

# ---------------------------------------------------------------------------
# Legal lifecycle transitions (via transition())
# ---------------------------------------------------------------------------

_LEGAL_LIFECYCLE_TRANSITIONS: set[tuple[Status, Status]] = {
    (Status.DRAFT, Status.ACTIVE),
    (Status.DRAFT, Status.ARCHIVED),
    (Status.ACTIVE, Status.SEALED),
    (Status.SEALED, Status.ARCHIVED),
}


def _is_legal_transition(current: Status, target: Status) -> bool:
    """Check if a lifecycle transition is legal for transition()."""
    return (current, target) in _LEGAL_LIFECYCLE_TRANSITIONS


# ---------------------------------------------------------------------------
# transition
# ---------------------------------------------------------------------------


def transition(spec: Spec, target: Status, dir: str | Path) -> Spec:
    """Transition a spec to a new lifecycle state.

    Legal transitions (via this function):
      - draft -> active (computes and stores intent hash)
      - draft -> archived
      - active -> sealed
      - sealed -> archived

    The target ``superseded`` is not valid for this function; use
    ``supersede()`` instead.

    Persists the updated spec to disk via internal save (bypassing
    mutation guards).

    Returns the updated Spec.

    Raises ``LifecycleError`` if the transition is illegal or if
    ``superseded`` is passed as the target.
    """
    current = spec.prd.frontmatter.status

    # Reject SUPERSEDED as a target — must use supersede()
    if target == Status.SUPERSEDED:
        raise LifecycleError(f"Cannot transition from {current.value} to superseded: use supersede() instead")

    # Check transition legality
    if not _is_legal_transition(current, target):
        raise LifecycleError(f"Illegal lifecycle transition from {current.value} to {target.value}")

    # For draft -> active: compute intent hash
    if current == Status.DRAFT and target == Status.ACTIVE:
        from afspec.intent import compute_intent_hash

        intent_hash = compute_intent_hash(spec.prd.body)
        spec = spec.model_copy(
            update={
                "prd": spec.prd.model_copy(
                    update={
                        "frontmatter": spec.prd.frontmatter.model_copy(
                            update={
                                "status": target,
                                "intent_hash": intent_hash,
                            }
                        )
                    }
                )
            }
        )
    else:
        # Update status only
        spec = spec.model_copy(
            update={
                "prd": spec.prd.model_copy(
                    update={"frontmatter": spec.prd.frontmatter.model_copy(update={"status": target})}
                )
            }
        )

    # Persist to disk via internal save (bypasses mutation guards)
    from afspec.io import _save_internal

    _save_internal(spec, dir)

    # Capture immutable snapshot for subsequent mutation guards
    from afspec.models import _ImmutableSnapshot

    spec._loaded = _ImmutableSnapshot(
        spec_id=spec.prd.frontmatter.spec_id,
        spec_name=spec.prd.frontmatter.spec_name,
        created_at=spec.prd.frontmatter.created_at,
    )

    return spec


# ---------------------------------------------------------------------------
# supersede
# ---------------------------------------------------------------------------

_DEPRECATION_BANNER = "> **SUPERSEDED** by spec {spec_id}. This spec is retained for historical reference only."


def supersede(spec: Spec, superseding_spec_id: str, dir: str | Path) -> Spec:
    """Mark a sealed spec as superseded.

    Sets the status to superseded, prepends a deprecation banner to the
    PRD body, and persists the result to disk via internal save.

    Only specs in ``sealed`` state can be superseded. For any other
    state, raises ``LifecycleError``.

    Returns the updated Spec.
    """
    current = spec.prd.frontmatter.status

    if current != Status.SEALED:
        raise LifecycleError(f"Cannot supersede spec in {current.value} state: only sealed specs can be superseded")

    # Prepend deprecation banner
    banner = _DEPRECATION_BANNER.format(spec_id=superseding_spec_id)
    new_body = banner + "\n\n" + spec.prd.body

    # Update status and body
    spec = spec.model_copy(
        update={
            "prd": spec.prd.model_copy(
                update={
                    "frontmatter": spec.prd.frontmatter.model_copy(update={"status": Status.SUPERSEDED}),
                    "body": new_body,
                }
            )
        }
    )

    # Persist to disk via internal save
    from afspec.io import _save_internal

    _save_internal(spec, dir)

    return spec


# ---------------------------------------------------------------------------
# move_to_archive
# ---------------------------------------------------------------------------


def move_to_archive(spec_dir: str | Path, root: str | Path) -> None:
    """Archive a spec by transitioning and moving to archive/ folder.

    Loads the spec from ``spec_dir``, transitions it to archived if
    needed, saves it, and moves the folder to ``{root}/archive/{name}``.

    State handling:
      - draft -> transitions to archived, then moves
      - sealed -> transitions to archived, then moves
      - superseded -> no transition (already terminal), just moves
      - archived -> no transition (already terminal), just moves
      - active -> raises LifecycleError (not archivable)

    Raises ``LifecycleError`` if:
      - spec_dir does not exist
      - spec is in active state
      - spec is already in the archive/ subdirectory
    """
    spec_path = Path(spec_dir)
    root_path = Path(root)

    # Check spec_dir exists
    if not spec_path.is_dir():
        raise LifecycleError(f"Spec directory does not exist: {spec_path}")

    # Check if already in archive/
    archive_path = root_path / "archive"
    try:
        spec_path.resolve().relative_to(archive_path.resolve())
        raise LifecycleError(f"Spec is already archived: {spec_path}")
    except ValueError:
        # Not in archive/ — good
        pass

    # Load the spec
    from afspec.io import load_spec

    spec = load_spec(spec_path)
    status = spec.prd.frontmatter.status

    # Active state is not archivable
    if status == Status.ACTIVE:
        raise LifecycleError("Cannot archive spec in active state: active specs must be sealed first")

    # Transition if needed (draft or sealed -> archived)
    if status in (Status.DRAFT, Status.SEALED):
        transition(spec, Status.ARCHIVED, spec_path)

    # Superseded and archived: no transition needed, just move

    # Create archive directory if needed
    archive_path.mkdir(parents=True, exist_ok=True)

    # Move spec folder to archive/
    folder_name = spec_path.name
    dest = archive_path / folder_name
    shutil.move(str(spec_path), str(dest))
