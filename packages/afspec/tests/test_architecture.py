"""Tests for architecture.md support (spec 02_architecture_support).

Covers the optional ``architecture`` field on ``Spec``, architecture.md I/O
in ``load_spec``/``save``/``_save_internal``, validation neutrality,
``render_combined`` placement, and ``BootstrapSpec.set_architecture``.

All tests are expected to FAIL until the implementation is added in
subsequent task groups.
"""

from __future__ import annotations

import shutil
import tempfile
from pathlib import Path

from hypothesis import assume, given, settings
from hypothesis import strategies as st

from afspec import (
    BootstrapSpec,
    Spec,
    load_spec,
    render_combined,
    save,
    validate,
    validate_cross_file,
    validate_schema,
)
from afspec.io import _save_internal

VALID_SPEC_DIR = Path(__file__).parent / "golden" / "valid_spec"


def _make_spec_dir(dest: Path) -> Path:
    """Copy golden valid_spec fixture files to *dest*."""
    for f in VALID_SPEC_DIR.iterdir():
        if f.is_file():
            shutil.copy2(f, dest / f.name)
    return dest


# ===================================================================
# Model tests
# ===================================================================


def test_model_architecture_field() -> None:
    """TS-02-1: Spec model has architecture field with default None (02-REQ-1.1)."""
    spec_default = Spec()
    assert spec_default.architecture is None

    spec_with = Spec(architecture="# Arch")
    assert spec_with.architecture == "# Arch"


def test_model_default_none() -> None:
    """TS-02-E1: Spec construction without architecture argument (02-REQ-1.E1)."""
    from afspec.models import PRDDocument, Requirements, Tasks, TestSpec

    spec = Spec(
        prd=PRDDocument(),
        requirements=Requirements(),
        test_spec=TestSpec(),
        tasks=Tasks(),
    )
    assert spec.architecture is None


# ===================================================================
# I/O — load_spec
# ===================================================================


def test_load_with_architecture(tmp_path: Path) -> None:
    """TS-02-2: load_spec reads architecture.md when present (02-REQ-2.1)."""
    _make_spec_dir(tmp_path)
    (tmp_path / "architecture.md").write_text("# Architecture\n\nModule overview.", encoding="utf-8")
    spec = load_spec(tmp_path)
    assert spec.architecture == "# Architecture\n\nModule overview."


def test_load_without_architecture(valid_spec_dir: Path) -> None:
    """TS-02-3: load_spec sets architecture to None when absent (02-REQ-2.2)."""
    spec = load_spec(valid_spec_dir)
    assert spec.architecture is None


def test_load_empty_architecture(tmp_path: Path) -> None:
    """TS-02-E2: load_spec with empty architecture.md (02-REQ-2.E1)."""
    _make_spec_dir(tmp_path)
    (tmp_path / "architecture.md").write_text("", encoding="utf-8")
    spec = load_spec(tmp_path)
    assert spec.architecture == ""


def test_load_requires_only_four_files(valid_spec_dir: Path) -> None:
    """TS-02-10: load_spec requires only four files, not architecture.md (02-REQ-4.3)."""
    spec = load_spec(valid_spec_dir)
    assert spec is not None
    assert spec.architecture is None


# ===================================================================
# I/O — save
# ===================================================================


def test_save_with_architecture(tmp_path: Path) -> None:
    """TS-02-4: save writes architecture.md when architecture is not None (02-REQ-3.1)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="# Architecture\n\nDetails here.",
    )
    save(spec_with, tmp_path)
    arch_path = tmp_path / "architecture.md"
    assert arch_path.exists()
    content = arch_path.read_text(encoding="utf-8")
    assert content == "# Architecture\n\nDetails here."


def test_save_without_architecture(tmp_path: Path) -> None:
    """TS-02-5: save does not write architecture.md when None (02-REQ-3.2)."""
    spec = load_spec(VALID_SPEC_DIR)
    assert spec.architecture is None
    save(spec, tmp_path)
    assert not (tmp_path / "architecture.md").exists()


def test_save_empty_architecture(tmp_path: Path) -> None:
    """TS-02-E3: save writes empty architecture.md for empty string (02-REQ-3.E1)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="",
    )
    save(spec_with, tmp_path)
    arch_path = tmp_path / "architecture.md"
    assert arch_path.exists()
    content = arch_path.read_text(encoding="utf-8")
    assert content == ""


# ===================================================================
# I/O — _save_internal
# ===================================================================


def test_save_internal_with_architecture(tmp_path: Path) -> None:
    """TS-02-6: _save_internal writes architecture.md when not None (02-REQ-3.3)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="# Internal arch",
    )
    _save_internal(spec_with, tmp_path)
    arch_path = tmp_path / "architecture.md"
    assert arch_path.exists()
    content = arch_path.read_text(encoding="utf-8")
    assert content == "# Internal arch"


def test_save_internal_without_architecture(tmp_path: Path) -> None:
    """TS-02-7: _save_internal does not write architecture.md when None (02-REQ-3.4)."""
    spec = load_spec(VALID_SPEC_DIR)
    assert spec.architecture is None
    _save_internal(spec, tmp_path)
    assert not (tmp_path / "architecture.md").exists()


# ===================================================================
# Validation
# ===================================================================


def test_validate_schema_ignores_architecture() -> None:
    """TS-02-8: validate_schema does not validate architecture content (02-REQ-4.1)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_none = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture=None,
    )
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="not json {{{",
    )
    # Verify architecture is actually set on the specs
    assert spec_none.architecture is None
    assert spec_with.architecture == "not json {{{"

    errors_none = validate_schema(spec_none)
    errors_with = validate_schema(spec_with)
    assert errors_none == errors_with


def test_validate_cross_file_ignores_architecture() -> None:
    """TS-02-9: validate_cross_file does not check architecture (02-REQ-4.2)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_none = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture=None,
    )
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="anything",
    )
    # Verify architecture is actually set
    assert spec_none.architecture is None
    assert spec_with.architecture == "anything"

    errors_none = validate_cross_file(spec_none)
    errors_with = validate_cross_file(spec_with)
    assert errors_none == errors_with


def test_validate_unchanged_by_architecture() -> None:
    """TS-02-E4: validation unchanged by architecture presence (02-REQ-4.E1)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_none = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture=None,
    )
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="# Arch\nContent",
    )
    # Verify architecture is actually set
    assert spec_none.architecture is None
    assert spec_with.architecture == "# Arch\nContent"

    assert validate(spec_none) == validate(spec_with)


# ===================================================================
# Rendering
# ===================================================================


def test_render_combined_with_architecture() -> None:
    """TS-02-11: render_combined includes architecture between PRD and requirements (02-REQ-5.1)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="# Architecture\n\nOverview content.",
    )
    output = render_combined(spec_with)

    # Architecture content must appear in the output
    assert "# Architecture" in output
    assert "Overview content." in output

    # Architecture must appear after PRD body and before requirements
    prd_body = spec_with.prd.body.rstrip()
    arch_pos = output.find("# Architecture")
    req_pos = output.find("# Requirements:")
    prd_pos = output.find(prd_body)
    assert prd_pos < arch_pos < req_pos

    # Separated by horizontal rules
    between_prd_and_arch = output[prd_pos + len(prd_body) : arch_pos]
    assert "---" in between_prd_and_arch
    between_arch_and_req = output[arch_pos:req_pos]
    assert "---" in between_arch_and_req


def test_render_combined_without_architecture() -> None:
    """TS-02-12: render_combined without architecture matches current behavior (02-REQ-5.2)."""
    spec = load_spec(VALID_SPEC_DIR)
    # Confirm architecture is None (this assertion exercises the field)
    assert spec.architecture is None

    output = render_combined(spec)
    # With architecture=None, PRD body transitions directly to requirements
    # through a single separator — no extra section between them.
    prd_body = spec.prd.body.rstrip()
    prd_end = output.find(prd_body) + len(prd_body)
    req_start = output.find("# Requirements:")
    between = output[prd_end:req_start].strip()
    assert between == "---"


def test_render_combined_empty_architecture() -> None:
    """TS-02-E5: render_combined with empty architecture string (02-REQ-5.E1)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="",
    )
    output = render_combined(spec_with)
    # Architecture field is empty string (not None), so separator
    # structure should still be present around the empty content.
    assert spec_with.architecture == ""
    # Two separators between PRD body and requirements
    # (one after PRD, one after empty architecture section)
    prd_body = spec_with.prd.body.rstrip()
    prd_end = output.find(prd_body) + len(prd_body)
    req_start = output.find("# Requirements:")
    between = output[prd_end:req_start]
    assert between.count("---") == 2


# ===================================================================
# Bootstrap
# ===================================================================


def test_bootstrap_set_architecture() -> None:
    """TS-02-13: BootstrapSpec.set_architecture stores content (02-REQ-6.1)."""
    bs = BootstrapSpec("02", "test")
    bs.set_architecture("# Arch content")
    assert bs._architecture == "# Arch content"


def test_bootstrap_finalize_with_architecture(valid_spec_dir: Path) -> None:
    """TS-02-14: finalize includes architecture when set (02-REQ-6.2)."""
    source = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(source.prd)
    bs.set_requirements(source.requirements)
    bs.set_test_spec(source.test_spec)
    bs.set_tasks(source.tasks)
    bs.set_architecture("# My Arch")
    spec, errors = bs.finalize()
    assert spec is not None
    assert spec.architecture == "# My Arch"


def test_bootstrap_finalize_without_architecture(valid_spec_dir: Path) -> None:
    """TS-02-15: finalize sets architecture to None when not set (02-REQ-6.3)."""
    source = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(source.prd)
    bs.set_requirements(source.requirements)
    bs.set_test_spec(source.test_spec)
    bs.set_tasks(source.tasks)
    spec, errors = bs.finalize()
    assert spec is not None
    assert spec.architecture is None


# ===================================================================
# Property tests
# ===================================================================


@given(arch_content=st.text())
@settings(max_examples=50, deadline=None)
def test_property_round_trip(arch_content: str) -> None:
    """TS-02-P1: Architecture round-trip preservation (Property 1)."""
    with tempfile.TemporaryDirectory() as tmp:
        tmp_dir = Path(tmp)
        _make_spec_dir(tmp_dir)
        spec = load_spec(tmp_dir)
        spec_with = Spec(
            prd=spec.prd,
            requirements=spec.requirements,
            test_spec=spec.test_spec,
            tasks=spec.tasks,
            architecture=arch_content,
        )
        save(spec_with, tmp_dir)
        reloaded = load_spec(tmp_dir)
        assert reloaded.architecture == arch_content


def test_property_none_preserves_absence(tmp_path: Path) -> None:
    """TS-02-P2: None architecture preserves absence (Property 2)."""
    _make_spec_dir(tmp_path)
    spec = load_spec(tmp_path)
    assert spec.architecture is None
    save(spec, tmp_path)
    assert not (tmp_path / "architecture.md").exists()
    reloaded = load_spec(tmp_path)
    assert reloaded.architecture is None


@given(arch_content=st.text())
@settings(max_examples=50, deadline=None)
def test_property_validation_neutrality(arch_content: str) -> None:
    """TS-02-P3: Validation neutrality (Property 3)."""
    spec = load_spec(VALID_SPEC_DIR)
    spec_none = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture=None,
    )
    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture=arch_content,
    )
    # Verify architecture is actually set
    assert spec_with.architecture == arch_content

    assert validate(spec_none) == validate(spec_with)


@given(arch_content=st.text(min_size=1))
@settings(max_examples=50, deadline=None)
def test_property_render_ordering(arch_content: str) -> None:
    """TS-02-P4: Combined render ordering (Property 4)."""
    # Filter whitespace-only strings whose rstrip() is empty,
    # since str.find("") always returns 0 (per skeptic review).
    stripped = arch_content.rstrip()
    assume(stripped)

    spec = load_spec(VALID_SPEC_DIR)

    # Filter out content that is a substring of the PRD body, since
    # find() would match the PRD body position instead of architecture.
    assume(stripped not in spec.prd.body)

    spec_with = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture=arch_content,
    )
    output = render_combined(spec_with)
    arch_pos = output.find(stripped)
    req_pos = output.find("# Requirements:")
    assert arch_pos > 0
    assert arch_pos < req_pos


# ===================================================================
# Integration smoke tests
# ===================================================================


def test_smoke_load_save_roundtrip(tmp_path: Path) -> None:
    """TS-02-SMOKE-1: Load-save round-trip with architecture."""
    _make_spec_dir(tmp_path)
    (tmp_path / "architecture.md").write_text("# Original Architecture", encoding="utf-8")

    spec = load_spec(tmp_path)
    assert spec.architecture is not None

    spec_modified = Spec(
        prd=spec.prd,
        requirements=spec.requirements,
        test_spec=spec.test_spec,
        tasks=spec.tasks,
        architecture="# Modified",
    )
    save(spec_modified, tmp_path)
    reloaded = load_spec(tmp_path)
    assert reloaded.architecture == "# Modified"


def test_smoke_combined_rendering(tmp_path: Path) -> None:
    """TS-02-SMOKE-2: Combined rendering end-to-end."""
    _make_spec_dir(tmp_path)
    (tmp_path / "architecture.md").write_text("# Architecture\n\nComponent overview.", encoding="utf-8")

    spec = load_spec(tmp_path)
    output = render_combined(spec)
    arch_pos = output.find(spec.architecture.rstrip())
    req_pos = output.find("# Requirements:")
    assert arch_pos > 0
    assert arch_pos < req_pos


def test_smoke_bootstrap_finalize(valid_spec_dir: Path, tmp_path: Path) -> None:
    """TS-02-SMOKE-3: Bootstrap finalize with architecture."""
    source = load_spec(valid_spec_dir)
    bs = BootstrapSpec("01", "test_feature")
    bs.set_prd(source.prd)
    bs.set_requirements(source.requirements)
    bs.set_test_spec(source.test_spec)
    bs.set_tasks(source.tasks)
    bs.set_architecture("# Bootstrap Arch")
    spec, errors = bs.finalize()
    assert spec is not None
    assert len(errors) == 0

    save(spec, tmp_path)
    reloaded = load_spec(tmp_path)
    assert reloaded.architecture == "# Bootstrap Arch"
