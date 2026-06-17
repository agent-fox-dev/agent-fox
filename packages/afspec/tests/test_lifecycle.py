"""Tests for lifecycle state machine, intent hashing, and archival.

Every test is expected to FAIL because the underlying stubs raise
NotImplementedError.  The structure and assertions are correct for
when the implementation is provided.
"""

from __future__ import annotations

import re
import shutil
from pathlib import Path

import pytest
from hypothesis import assume, given
from hypothesis.strategies import text

from afspec import (
    IntentError,
    LifecycleError,
    Status,
    compute_intent_hash,
    load_spec,
    move_to_archive,
    save,
    supersede,
    transition,
)

# ---------------------------------------------------------------------------
# Legal transition table
# ---------------------------------------------------------------------------

LEGAL_TRANSITIONS: list[tuple[Status, Status]] = [
    (Status.DRAFT, Status.ACTIVE),
    (Status.ACTIVE, Status.SEALED),
    (Status.SEALED, Status.ARCHIVED),
    (Status.DRAFT, Status.ARCHIVED),
]

# States that do NOT have golden fixtures — we fabricate them via transition.
_ALL_STATUSES = list(Status)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _copy_draft(tmp_path: Path, draft_spec_dir: Path) -> Path:
    """Copy the golden draft spec into *tmp_path* and return the new dir."""
    dest = tmp_path / "spec"
    shutil.copytree(draft_spec_dir, dest)
    return dest


def _make_spec_in_state(status: Status, tmp_path: Path, draft_spec_dir: Path) -> tuple:
    """Return ``(spec, spec_dir)`` for a spec in the requested state.

    Builds up the state by walking the transition chain from draft.
    Will raise ``NotImplementedError`` until stubs are implemented.
    """
    spec_dir = _copy_draft(tmp_path, draft_spec_dir)
    spec = load_spec(spec_dir)

    chain: dict[Status, list[Status]] = {
        Status.DRAFT: [],
        Status.ACTIVE: [Status.ACTIVE],
        Status.SEALED: [Status.ACTIVE, Status.SEALED],
        Status.ARCHIVED: [Status.ACTIVE, Status.SEALED, Status.ARCHIVED],
        Status.SUPERSEDED: [Status.ACTIVE, Status.SEALED],
    }
    for target in chain.get(status, []):
        spec = transition(spec, target, spec_dir)
    if status == Status.SUPERSEDED:
        spec = supersede(spec, "99", spec_dir)
    return spec, spec_dir


# ---------------------------------------------------------------------------
# TS-01-22: Legal transitions
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "current,target",
    LEGAL_TRANSITIONS,
    ids=[f"{c.value}->{t.value}" for c, t in LEGAL_TRANSITIONS],
)
def test_legal_transitions(
    current: Status,
    target: Status,
    draft_spec_dir: Path,
    tmp_spec_dir: Path,
) -> None:
    """Each legal transition must update status and persist correctly."""
    spec, spec_dir = _make_spec_in_state(current, tmp_spec_dir, draft_spec_dir)
    result = transition(spec, target, spec_dir)
    assert result.prd.frontmatter.status == target

    reloaded = load_spec(spec_dir)
    assert reloaded.prd.frontmatter.status == target


# ---------------------------------------------------------------------------
# TS-01-23: Transition saves to disk
# ---------------------------------------------------------------------------


def test_transition_saves(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Transitioning draft->active must persist the new status to disk."""
    spec_dir = _copy_draft(tmp_spec_dir, draft_spec_dir)
    spec = load_spec(spec_dir)

    result = transition(spec, Status.ACTIVE, spec_dir)
    assert result.prd.frontmatter.status == Status.ACTIVE

    reloaded = load_spec(spec_dir)
    assert reloaded.prd.frontmatter.status == Status.ACTIVE


# ---------------------------------------------------------------------------
# TS-01-24: Draft->active computes intent hash
# ---------------------------------------------------------------------------


def test_draft_to_active_intent_hash(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Activating a draft must compute and store a 64-char hex intent hash."""
    spec_dir = _copy_draft(tmp_spec_dir, draft_spec_dir)
    spec = load_spec(spec_dir)

    result = transition(spec, Status.ACTIVE, spec_dir)
    intent_hash = result.prd.frontmatter.intent_hash
    assert intent_hash is not None
    assert len(intent_hash) == 64
    assert re.fullmatch(r"[0-9a-f]{64}", intent_hash)


# ---------------------------------------------------------------------------
# TS-01-25: Active spec rejects save when intent is modified
# ---------------------------------------------------------------------------


def test_active_intent_guard(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Saving an active spec whose Intent section changed must raise LifecycleError."""
    spec, spec_dir = _make_spec_in_state(Status.ACTIVE, tmp_spec_dir, draft_spec_dir)
    # Mutate the intent section body
    spec.prd.body = spec.prd.body.replace(
        "A draft feature for testing lifecycle transitions.",
        "Completely rewritten intent that changes the hash.",
    )
    with pytest.raises(LifecycleError):
        save(spec, spec_dir)


# ---------------------------------------------------------------------------
# TS-01-26: Sealed / superseded / archived specs reject save
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "status",
    [Status.SEALED, Status.SUPERSEDED, Status.ARCHIVED],
    ids=["sealed", "superseded", "archived"],
)
def test_sealed_save_rejected(status: Status, draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Saving a spec in a terminal state must raise LifecycleError."""
    spec, spec_dir = _make_spec_in_state(status, tmp_spec_dir, draft_spec_dir)
    with pytest.raises(LifecycleError):
        save(spec, spec_dir)


# ---------------------------------------------------------------------------
# TS-01-27: Supersede a sealed spec
# ---------------------------------------------------------------------------


def test_supersede(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Superseding a sealed spec must set status and prepend notice."""
    spec, spec_dir = _make_spec_in_state(Status.SEALED, tmp_spec_dir, draft_spec_dir)
    result = supersede(spec, "02", spec_dir)
    assert result.prd.frontmatter.status == Status.SUPERSEDED
    assert result.prd.body.startswith("> **SUPERSEDED** by spec 02")

    reloaded = load_spec(spec_dir)
    assert reloaded.prd.frontmatter.status == Status.SUPERSEDED
    assert reloaded.prd.body.startswith("> **SUPERSEDED** by spec 02")


# ---------------------------------------------------------------------------
# TS-01-40: Intent hash normalises line endings
# ---------------------------------------------------------------------------


def test_compute_intent_hash() -> None:
    """CRLF and LF must produce the same 64-char hex hash."""
    body_lf = "# Title\n\n## Intent\n\nSome intent body.\n\n## Goals\n\n- G1\n"
    body_crlf = body_lf.replace("\n", "\r\n")

    hash_lf = compute_intent_hash(body_lf)
    hash_crlf = compute_intent_hash(body_crlf)

    assert len(hash_lf) == 64
    assert re.fullmatch(r"[0-9a-f]{64}", hash_lf)
    assert hash_lf == hash_crlf


# ---------------------------------------------------------------------------
# TS-01-41: Public API smoke test for compute_intent_hash
# ---------------------------------------------------------------------------


def test_compute_intent_hash_public() -> None:
    """compute_intent_hash must return a 64-char lowercase hex string."""
    body = "# Feature\n\n## Intent\n\nDo the thing.\n\n## Goals\n\n- Win.\n"
    result = compute_intent_hash(body)
    assert isinstance(result, str)
    assert len(result) == 64
    assert all(c in "0123456789abcdef" for c in result)


# ---------------------------------------------------------------------------
# TS-01-E11: Illegal transitions
# ---------------------------------------------------------------------------


def test_illegal_transition(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Illegal transitions must raise LifecycleError with state names."""
    # draft -> sealed is illegal
    spec_dir = _copy_draft(tmp_spec_dir, draft_spec_dir)
    spec = load_spec(spec_dir)
    with pytest.raises(LifecycleError, match="draft") as exc_info:
        transition(spec, Status.SEALED, spec_dir)
    assert "sealed" in str(exc_info.value).lower()

    # sealed -> superseded via transition() is illegal (must use supersede())
    sealed_dir = tmp_spec_dir / "sealed"
    spec_sealed, _ = _make_spec_in_state(Status.SEALED, sealed_dir, draft_spec_dir)
    with pytest.raises(LifecycleError):
        transition(spec_sealed, Status.SUPERSEDED, sealed_dir / "spec")


# ---------------------------------------------------------------------------
# TS-01-E12: Intent modified on active spec
# ---------------------------------------------------------------------------


def test_intent_modified_active(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Modifying the intent of an active spec and saving must raise LifecycleError."""
    spec, spec_dir = _make_spec_in_state(Status.ACTIVE, tmp_spec_dir, draft_spec_dir)
    spec.prd.body = spec.prd.body.replace(
        "A draft feature for testing lifecycle transitions.",
        "Tampered intent text that should be rejected.",
    )
    with pytest.raises(LifecycleError, match="(?i)intent"):
        save(spec, spec_dir)


# ---------------------------------------------------------------------------
# TS-01-E19: Intent hash with no ## Intent section
# ---------------------------------------------------------------------------


def test_intent_hash_no_section() -> None:
    """compute_intent_hash must raise IntentError when ## Intent is missing."""
    body = "# Title\n\n## Goals\nStuff.\n"
    with pytest.raises(IntentError):
        compute_intent_hash(body)


# ---------------------------------------------------------------------------
# TS-01-E20: Intent hash with empty intent section
# ---------------------------------------------------------------------------


def test_intent_hash_empty() -> None:
    """An empty ## Intent section must hash to the SHA-256 of the empty string."""
    body = "## Intent\n   \n\n## Goals\n"
    result = compute_intent_hash(body)
    # SHA-256("") == e3b0c44...
    assert result == "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"


# ---------------------------------------------------------------------------
# TS-01-E28: Supersede rejects non-sealed specs
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "status",
    [Status.DRAFT, Status.ACTIVE, Status.SUPERSEDED, Status.ARCHIVED],
    ids=["draft", "active", "superseded", "archived"],
)
def test_supersede_not_sealed(status: Status, draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """supersede() must raise LifecycleError for non-sealed specs."""
    sub = tmp_spec_dir / status.value
    spec, spec_dir = _make_spec_in_state(status, sub, draft_spec_dir)
    with pytest.raises(LifecycleError, match=status.value):
        supersede(spec, "99", spec_dir)


# ---------------------------------------------------------------------------
# TS-01-E29: move_to_archive integration
# ---------------------------------------------------------------------------


def test_move_to_archive(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """move_to_archive must relocate a draft spec to archive/ with status archived."""
    root = tmp_spec_dir / "root"
    root.mkdir()
    spec_dir = root / "specs" / "my_spec"
    shutil.copytree(draft_spec_dir, spec_dir)

    move_to_archive(spec_dir, root)

    assert not spec_dir.exists(), "Original directory should be removed"
    archive_dir = root / "archive" / "my_spec"
    assert archive_dir.exists(), "Archive directory should exist"
    archived_spec = load_spec(archive_dir)
    assert archived_spec.prd.frontmatter.status == Status.ARCHIVED


# ---------------------------------------------------------------------------
# TS-01-E30: Archive error conditions
# ---------------------------------------------------------------------------


def test_archive_errors(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """move_to_archive must raise LifecycleError for bad inputs."""
    root = tmp_spec_dir / "root"
    root.mkdir()

    # Non-existent directory
    with pytest.raises(LifecycleError):
        move_to_archive(root / "no_such_dir", root)

    # Active spec
    active_dir = root / "specs" / "active_spec"
    spec, _ = _make_spec_in_state(Status.ACTIVE, active_dir, draft_spec_dir)
    with pytest.raises(LifecycleError):
        move_to_archive(active_dir / "spec", root)


# ---------------------------------------------------------------------------
# TS-01-E31: Archive rejects active spec
# ---------------------------------------------------------------------------


def test_archive_active_rejected(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """move_to_archive on an active spec must raise LifecycleError naming 'active'."""
    root = tmp_spec_dir / "root"
    root.mkdir()
    spec_dir = root / "specs" / "active_spec"
    spec, _ = _make_spec_in_state(Status.ACTIVE, spec_dir, draft_spec_dir)
    with pytest.raises(LifecycleError, match="active"):
        move_to_archive(spec_dir / "spec", root)


# ---------------------------------------------------------------------------
# TS-01-E32: Archive already-archived spec
# ---------------------------------------------------------------------------


def test_archive_already_archived(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """move_to_archive on a spec already in archive/ must raise LifecycleError."""
    root = tmp_spec_dir / "root"
    root.mkdir()
    archive_dir = root / "archive" / "old_spec"
    shutil.copytree(draft_spec_dir, archive_dir)

    with pytest.raises(LifecycleError, match="already archived"):
        move_to_archive(archive_dir, root)


# ---------------------------------------------------------------------------
# TS-01-E33: Archive superseded spec (should succeed)
# ---------------------------------------------------------------------------


def test_archive_superseded(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """move_to_archive on a superseded spec must succeed, keeping status superseded."""
    root = tmp_spec_dir / "root"
    root.mkdir()
    spec_dir = root / "specs" / "super_spec"
    spec, _ = _make_spec_in_state(Status.SUPERSEDED, spec_dir, draft_spec_dir)

    move_to_archive(spec_dir / "spec", root)

    # _make_spec_in_state copies into a "spec" subdirectory via _copy_draft,
    # so the folder actually moved is named "spec", not "super_spec".
    archive_dir = root / "archive" / "spec"
    assert archive_dir.exists()
    archived = load_spec(archive_dir)
    assert archived.prd.frontmatter.status == Status.SUPERSEDED


# ---------------------------------------------------------------------------
# Property TS-01-P6: Exhaustive transition legality
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "current,target",
    [(c, t) for c in Status for t in Status],
    ids=[f"{c.value}->{t.value}" for c in Status for t in Status],
)
def test_property_lifecycle_transitions(
    current: Status,
    target: Status,
    draft_spec_dir: Path,
    tmp_path: Path,
) -> None:
    """For all (current, target) pairs, transition succeeds iff the pair is legal.

    The target 'superseded' must always be rejected (use supersede() instead).
    Uses real specs and real temp directories — no mocking.
    """
    legal_pairs = {
        (Status.DRAFT, Status.ACTIVE),
        (Status.ACTIVE, Status.SEALED),
        (Status.SEALED, Status.ARCHIVED),
        (Status.DRAFT, Status.ARCHIVED),
    }

    sub = tmp_path / f"{current.value}_{target.value}"
    spec, spec_dir = _make_spec_in_state(current, sub, draft_spec_dir)

    if target == Status.SUPERSEDED:
        # Must always reject — superseded is not a valid transition() target
        with pytest.raises(LifecycleError):
            transition(spec, target, spec_dir)
    elif (current, target) in legal_pairs:
        # Should succeed and persist
        result = transition(spec, target, spec_dir)
        assert result.prd.frontmatter.status == target
        reloaded = load_spec(spec_dir)
        assert reloaded.prd.frontmatter.status == target
    else:
        # Should raise
        with pytest.raises(LifecycleError):
            transition(spec, target, spec_dir)


# ---------------------------------------------------------------------------
# Property TS-01-P9: Intent hash stability across line endings
# ---------------------------------------------------------------------------


@given(body=text(min_size=1, max_size=200))
def test_property_intent_hash_stability(body: str) -> None:
    """Same content with LF vs CRLF must produce the same hash.

    Only tests content normalisation (line endings), not heading changes.
    Filters out bodies containing \\r since replace("\\n", "\\r\\n") cannot
    produce a correct CRLF conversion when lone \\r is already present.
    """
    assume("\r" not in body)
    full_body = f"# Title\n\n## Intent\n\n{body}\n\n## Goals\n\n- G\n"
    full_body_crlf = full_body.replace("\n", "\r\n")

    hash_lf = compute_intent_hash(full_body)
    hash_crlf = compute_intent_hash(full_body_crlf)

    assert len(hash_lf) == 64
    assert re.fullmatch(r"[0-9a-f]{64}", hash_lf)
    assert hash_lf == hash_crlf


# ---------------------------------------------------------------------------
# TS-01-SMOKE-4: Lifecycle transition end-to-end (PATH-4)
# ---------------------------------------------------------------------------


def test_smoke_lifecycle_transition(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Full lifecycle path: draft→active with intent hash, persist to disk,
    then verify save fails after intent mutation.  No mocking.
    """
    spec_dir = _copy_draft(tmp_spec_dir, draft_spec_dir)
    spec = load_spec(spec_dir)

    # 1. Transition draft→active
    result = transition(spec, Status.ACTIVE, spec_dir)
    assert result.prd.frontmatter.status == Status.ACTIVE

    # 2. Intent hash is populated and correct
    intent_hash = result.prd.frontmatter.intent_hash
    assert intent_hash is not None
    assert len(intent_hash) == 64
    assert re.fullmatch(r"[0-9a-f]{64}", intent_hash)

    # 3. Persisted to disk correctly
    reloaded = load_spec(spec_dir)
    assert reloaded.prd.frontmatter.status == Status.ACTIVE
    assert reloaded.prd.frontmatter.intent_hash == intent_hash

    # 4. Modifying intent and saving must fail
    result.prd.body = result.prd.body.replace(
        "A draft feature for testing lifecycle transitions.",
        "Completely changed intent that should be rejected.",
    )
    with pytest.raises((LifecycleError, IntentError)):
        save(result, spec_dir)


# ---------------------------------------------------------------------------
# TS-01-SMOKE-9: Supersede and archive workflow end-to-end (PATH-4a, PATH-4b)
# ---------------------------------------------------------------------------


def test_smoke_supersede_archive(draft_spec_dir: Path, tmp_spec_dir: Path) -> None:
    """Full supersede→archive workflow: supersede a sealed spec, verify
    persistence, reject public save, then move to archive.  No mocking.
    """
    root = tmp_spec_dir / "root"
    root.mkdir()

    # Set up two sealed specs as direct children of root (not nested in a
    # helper-created 'spec/' subdirectory) so that move_to_archive moves a
    # folder with a meaningful name into archive/.
    alpha_spec_dir = root / "01_alpha"
    shutil.copytree(draft_spec_dir, alpha_spec_dir)
    spec_alpha = load_spec(alpha_spec_dir)
    spec_alpha = transition(spec_alpha, Status.ACTIVE, alpha_spec_dir)
    spec_alpha = transition(spec_alpha, Status.SEALED, alpha_spec_dir)

    beta_spec_dir = root / "02_beta"
    shutil.copytree(draft_spec_dir, beta_spec_dir)
    spec_beta = load_spec(beta_spec_dir)
    spec_beta = transition(spec_beta, Status.ACTIVE, beta_spec_dir)
    spec_beta = transition(spec_beta, Status.SEALED, beta_spec_dir)

    # 1. Supersede alpha → persists banner and status to disk
    result = supersede(spec_alpha, "03_gamma", alpha_spec_dir)
    assert result.prd.frontmatter.status == Status.SUPERSEDED
    assert result.prd.body.startswith("> **SUPERSEDED** by spec 03_gamma")

    reloaded = load_spec(alpha_spec_dir)
    assert reloaded.prd.frontmatter.status == Status.SUPERSEDED
    assert reloaded.prd.body.startswith("> **SUPERSEDED** by spec 03_gamma")

    # 2. Public save rejects superseded spec
    with pytest.raises(LifecycleError):
        save(result, alpha_spec_dir)

    # 3. Supersede rejects non-sealed
    draft_sub = tmp_spec_dir / "draft_sub"
    spec_draft, draft_dir = _make_spec_in_state(Status.DRAFT, draft_sub, draft_spec_dir)
    with pytest.raises(LifecycleError):
        supersede(spec_draft, "99", draft_dir)

    # 4. Move superseded spec to archive (skip transition, just move)
    move_to_archive(alpha_spec_dir, root)
    assert not alpha_spec_dir.exists(), "Original alpha directory should be gone"
    alpha_archive = root / "archive" / "01_alpha"
    assert alpha_archive.exists(), "Alpha should be in archive/01_alpha"
    reloaded_alpha = load_spec(alpha_archive)
    assert reloaded_alpha.prd.frontmatter.status == Status.SUPERSEDED
    assert reloaded_alpha.prd.body.startswith("> **SUPERSEDED** by spec 03_gamma")

    # 5. Move sealed spec to archive (transitions to archived)
    move_to_archive(beta_spec_dir, root)
    assert not beta_spec_dir.exists(), "Original beta directory should be gone"
    beta_archive = root / "archive" / "02_beta"
    assert beta_archive.exists(), "Beta should be in archive/02_beta"
    reloaded_beta = load_spec(beta_archive)
    assert reloaded_beta.prd.frontmatter.status == Status.ARCHIVED
