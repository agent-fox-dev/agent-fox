"""Tests for bootstrap mode (TS-01-28, TS-01-29, TS-01-30, TS-01-E13, TS-01-E14, TS-01-SMOKE-5)."""

from __future__ import annotations

from pathlib import Path

from afspec import BootstrapSpec, load_spec, validate

# ---------------------------------------------------------------------------
# TS-01-28: BootstrapSpec returns usable handle (01-REQ-7.1)
# ---------------------------------------------------------------------------


def test_bootstrap_create() -> None:
    bs = BootstrapSpec("05", "my_feature")
    assert bs is not None


# ---------------------------------------------------------------------------
# TS-01-29: BootstrapSpec set methods accept artifacts (01-REQ-7.2)
# ---------------------------------------------------------------------------


def test_bootstrap_set_artifacts(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(spec.prd)
    bs.set_requirements(spec.requirements)
    bs.set_test_spec(spec.test_spec)
    bs.set_tasks(spec.tasks)
    # No exception raised — artifacts stored for later finalization


# ---------------------------------------------------------------------------
# TS-01-30: Finalize validates and returns Spec (01-REQ-7.3)
# ---------------------------------------------------------------------------


def test_bootstrap_finalize(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(spec.prd)
    bs.set_requirements(spec.requirements)
    bs.set_test_spec(spec.test_spec)
    bs.set_tasks(spec.tasks)
    result = bs.finalize()
    # finalize returns a tuple (Spec | None, list[ValidationError])
    finalized_spec, errs = result
    assert finalized_spec is not None
    assert len(errs) == 0


# ---------------------------------------------------------------------------
# TS-01-E13: Finalize with missing artifacts (01-REQ-7.E1)
# ---------------------------------------------------------------------------


def test_finalize_incomplete(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    bs = BootstrapSpec("05", "feat")
    bs.set_prd(spec.prd)
    bs.set_requirements(spec.requirements)
    # test_spec and tasks NOT set
    result = bs.finalize()
    finalized_spec, errs = result
    assert finalized_spec is None
    assert any("test_spec" in e.message for e in errs)
    assert any("tasks" in e.message for e in errs)


# ---------------------------------------------------------------------------
# TS-01-E14: BootstrapSpec artifact overwrite (01-REQ-7.E2)
# ---------------------------------------------------------------------------


def test_bootstrap_overwrite(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(spec.prd)
    bs.set_requirements(spec.requirements)
    # Set requirements a second time — should overwrite
    bs.set_requirements(spec.requirements)
    bs.set_test_spec(spec.test_spec)
    bs.set_tasks(spec.tasks)
    result = bs.finalize()
    finalized_spec, errs = result
    assert finalized_spec is not None


# ---------------------------------------------------------------------------
# TS-01-SMOKE-5: Bootstrap spec creation end-to-end (PATH-5)
# ---------------------------------------------------------------------------


def test_smoke_bootstrap(valid_spec_dir: Path) -> None:
    spec = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(spec.prd)
    bs.set_requirements(spec.requirements)
    bs.set_test_spec(spec.test_spec)
    bs.set_tasks(spec.tasks)
    finalized_spec, errs = bs.finalize()
    assert finalized_spec is not None
    assert len(errs) == 0
    # The finalized spec should also pass full validation
    validation_errs = validate(finalized_spec)
    assert len(validation_errs) == 0
