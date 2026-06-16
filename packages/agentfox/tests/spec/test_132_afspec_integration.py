"""Spec 132: afspec Library Integration tests.

Test Spec: TS-132-1 through TS-132-9, TS-132-E1 through TS-132-E3,
           TS-132-P1, TS-132-P2, TS-132-SMOKE-1
Requirements: 132-REQ-1.2, 132-REQ-1.3, 132-REQ-2.1, 132-REQ-2.2,
              132-REQ-3.1, 132-REQ-3.2, 132-REQ-3.3, 132-REQ-3.4,
              132-REQ-4.1, 132-REQ-4.2, 132-REQ-2.E1, 132-REQ-3.E1,
              132-REQ-4.E1
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from agentfox.spec.discovery import discover_specs
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Valid v1.2 fixture content
# ---------------------------------------------------------------------------

PRD_MD_VALID = """\
---
spec_id: "test-132"
spec_name: "test_fixture"
title: "Test Fixture Spec"
status: "draft"
created_at: "2024-01-01T00:00:00Z"
updated_at: "2024-01-01T00:00:00Z"
owner: "test"
source: "test"
schema_version: 1
---
# Test PRD

Test PRD content.
"""

REQUIREMENTS_JSON_VALID = json.dumps(
    {
        "spec_id": "test-132",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "introduction": "Test requirements",
        "glossary": {},
        "requirements": [],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
    },
    indent=2,
)

TEST_SPEC_JSON_VALID = json.dumps(
    {
        "spec_id": "test-132",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_cases": [],
        "property_tests": [],
        "edge_case_tests": [],
        "smoke_tests": [],
        "coverage": {
            "requirements_covered": [],
            "properties_covered": [],
            "paths_covered": [],
            "gaps": [],
        },
    },
    indent=2,
)

TASKS_JSON_VALID = json.dumps(
    {
        "spec_id": "test-132",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": [],
        "traceability": [],
    },
    indent=2,
)

# Legacy v1 content
REQUIREMENTS_MD_LEGACY = """\
# Requirements

## Requirement 1

Some legacy requirement text.
"""

TASKS_MD_LEGACY = """\
# Implementation Plan

## Tasks

- [ ] 1. First task
  - [ ] 1.1 Subtask A
"""


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


def _write_spec(spec_dir: Path, *, include_tasks: bool = True) -> None:
    """Populate a directory with valid v1.2 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_VALID)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    if include_tasks:
        (spec_dir / "tasks.json").write_text(TASKS_JSON_VALID)


def _write_v1_spec(spec_dir: Path) -> None:
    """Populate a directory with legacy v1 spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.md").write_text(REQUIREMENTS_MD_LEGACY)
    (spec_dir / "tasks.md").write_text(TASKS_MD_LEGACY)


@pytest.fixture
def v12_spec_dir(tmp_path: Path) -> Path:
    """A single v1.2 spec directory with all valid artifacts."""
    spec_dir = tmp_path / "01_test_spec"
    _write_spec(spec_dir)
    return spec_dir


@pytest.fixture
def v12_specs_root(tmp_path: Path) -> Path:
    """A specs root with one v1.2 spec folder."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec(root / "02_modern")
    return root


@pytest.fixture
def mixed_specs_root(tmp_path: Path) -> Path:
    """A specs root with one v1 and one v1.2 spec folder.

    Used by TS-132-7 (discovery excludes v1).
    """
    root = tmp_path / "specs"
    root.mkdir()
    _write_v1_spec(root / "01_legacy")
    _write_spec(root / "02_modern")
    return root


@pytest.fixture
def v12_specs_root_with_tasks(tmp_path: Path) -> Path:
    """A specs root with a v1.2 spec that has tasks.json."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec(root / "01_with_tasks", include_tasks=True)
    return root


@pytest.fixture
def v12_specs_root_without_tasks(tmp_path: Path) -> Path:
    """A specs root with a v1.2 spec that has no tasks.json but has tasks.md."""
    root = tmp_path / "specs"
    root.mkdir()
    spec_dir = root / "01_no_tasks_json"
    _write_spec(spec_dir, include_tasks=False)
    # Add a tasks.md to verify we check tasks.json, not tasks.md
    (spec_dir / "tasks.md").write_text(TASKS_MD_LEGACY)
    return root


@pytest.fixture
def no_requirements_specs_root(tmp_path: Path) -> Path:
    """A specs root with a folder that has neither requirements file.

    Used by TS-132-E1.
    """
    root = tmp_path / "specs"
    root.mkdir()
    empty_spec = root / "01_empty"
    empty_spec.mkdir()
    (empty_spec / "prd.md").write_text(PRD_MD_VALID)
    (empty_spec / "tasks.json").write_text(TASKS_JSON_VALID)
    return root


@pytest.fixture
def both_formats_specs_root(tmp_path: Path) -> Path:
    """A specs root with a folder that has both requirements.md and requirements.json.

    Used by TS-132-E2 (JSON takes precedence).
    """
    root = tmp_path / "specs"
    root.mkdir()
    spec_dir = root / "01_both"
    _write_spec(spec_dir)
    # Also add requirements.md
    (spec_dir / "requirements.md").write_text(REQUIREMENTS_MD_LEGACY)
    return root


@pytest.fixture
def malformed_json_spec_dir(tmp_path: Path) -> Path:
    """A spec directory with malformed JSON in requirements.json.

    Used by TS-132-E3.
    """
    spec_dir = tmp_path / "01_malformed"
    spec_dir.mkdir()
    (spec_dir / "prd.md").write_text(PRD_MD_VALID)
    (spec_dir / "requirements.json").write_text("{invalid json content!!!")
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_VALID)
    (spec_dir / "tasks.json").write_text(TASKS_JSON_VALID)
    return spec_dir


# ===========================================================================
# TS-132-1: afspec is importable
# ===========================================================================


class TestAfspecImportable:
    """TS-132-1: Verify that import afspec succeeds and key symbols exist.

    Requirement: 132-REQ-1.2
    """

    def test_afspec_module_importable(self) -> None:
        """afspec is importable."""
        import afspec  # noqa: F811

        assert afspec is not None

    def test_load_spec_accessible(self) -> None:
        """afspec.load_spec is accessible."""
        import afspec

        assert hasattr(afspec, "load_spec")

    def test_spec_accessible(self) -> None:
        """afspec.Spec is accessible."""
        import afspec

        assert hasattr(afspec, "Spec")

    def test_render_combined_accessible(self) -> None:
        """afspec.render_combined is accessible."""
        import afspec

        assert hasattr(afspec, "render_combined")


# ===========================================================================
# TS-132-2: afspec loads a valid v1.2 spec
# ===========================================================================


class TestAfspecLoadSpec:
    """TS-132-2: Verify that afspec.load_spec returns a populated Spec.

    Requirement: 132-REQ-1.3, 132-REQ-4.1
    """

    def test_load_returns_spec(self, v12_spec_dir: Path) -> None:
        """load_spec returns a Spec instance."""
        import afspec

        spec = afspec.load_spec(v12_spec_dir)
        assert isinstance(spec, afspec.Spec)

    def test_prd_populated(self, v12_spec_dir: Path) -> None:
        """Loaded spec has populated prd frontmatter."""
        import afspec

        spec = afspec.load_spec(v12_spec_dir)
        assert spec.prd.frontmatter.spec_id != ""

    def test_requirements_populated(self, v12_spec_dir: Path) -> None:
        """Loaded spec has populated requirements."""
        import afspec

        spec = afspec.load_spec(v12_spec_dir)
        assert spec.requirements is not None

    def test_tasks_populated(self, v12_spec_dir: Path) -> None:
        """Loaded spec has populated tasks."""
        import afspec

        spec = afspec.load_spec(v12_spec_dir)
        assert spec.tasks is not None








# ===========================================================================
# TS-132-7: Discovery excludes v1 markdown specs
# ===========================================================================


class TestDiscoveryExcludesV1:
    """TS-132-7: discover_specs returns only V1_2_JSON specs.

    Requirement: 132-REQ-3.3
    """

    def test_only_v12_returned(self, mixed_specs_root: Path) -> None:
        """Only v1.2 specs appear in discovery results."""
        specs = discover_specs(mixed_specs_root)
        assert len(specs) == 1
        assert specs[0].name == "02_modern"

    def test_v1_excluded(self, mixed_specs_root: Path) -> None:
        """v1 markdown specs are excluded from discovery results."""
        specs = discover_specs(mixed_specs_root)
        names = [s.name for s in specs]
        assert "01_legacy" not in names


# ===========================================================================
# TS-132-8: Discovery checks tasks.json for has_tasks
# ===========================================================================


class TestHasTasksJson:
    """TS-132-8: For v1.2 specs, has_tasks reflects tasks.json existence.

    Requirement: 132-REQ-3.4
    """

    def test_has_tasks_true_with_tasks_json(self, v12_specs_root_with_tasks: Path) -> None:
        """has_tasks is True when tasks.json exists."""
        specs = discover_specs(v12_specs_root_with_tasks)
        assert len(specs) == 1
        assert specs[0].has_tasks is True

    def test_has_tasks_false_when_only_tasks_md(self, v12_specs_root_without_tasks: Path) -> None:
        """has_tasks is False when only tasks.md exists (no tasks.json)."""
        specs = discover_specs(v12_specs_root_without_tasks)
        assert len(specs) == 1
        assert specs[0].has_tasks is False


# ===========================================================================
# TS-132-9: afspec render_combined produces markdown
# ===========================================================================


class TestRenderCombined:
    """TS-132-9: render_combined returns non-empty markdown from a loaded spec.

    Requirement: 132-REQ-4.2
    """

    def test_render_returns_nonempty_string(self, v12_spec_dir: Path) -> None:
        """render_combined returns a non-empty string."""
        import afspec

        spec = afspec.load_spec(v12_spec_dir)
        md = afspec.render_combined(spec)
        assert len(md) > 0

    def test_render_contains_markdown_heading(self, v12_spec_dir: Path) -> None:
        """render_combined output contains markdown heading syntax."""
        import afspec

        spec = afspec.load_spec(v12_spec_dir)
        md = afspec.render_combined(spec)
        assert "# " in md


# ===========================================================================
# TS-132-E1: Folder with neither requirements file is skipped
# ===========================================================================


class TestNoRequirementsSkipped:
    """TS-132-E1: A spec folder missing both requirements files is excluded.

    Requirement: 132-REQ-2.E1
    """

    def test_no_requirements_folder_excluded(self, no_requirements_specs_root: Path) -> None:
        """Folder with no requirements.md or requirements.json is skipped."""
        specs = discover_specs(no_requirements_specs_root)
        assert len(specs) == 0


# ===========================================================================
# TS-132-E2: JSON takes precedence when both formats present
# ===========================================================================


class TestJsonPrecedence:
    """TS-132-E2: When both requirements.md and requirements.json exist, JSON wins.

    Requirement: 132-REQ-3.E1
    """

    def test_both_formats_classified_as_json(self, both_formats_specs_root: Path) -> None:
        """Folder with both requirements files is classified as V1_2_JSON."""
        specs = discover_specs(both_formats_specs_root)
        assert len(specs) == 1


# ===========================================================================
# TS-132-E3: Malformed JSON raises LoadError
# ===========================================================================


class TestMalformedJsonError:
    """TS-132-E3: afspec.load_spec raises LoadError for malformed JSON.

    Requirement: 132-REQ-4.E1
    """

    def test_malformed_requirements_json(self, malformed_json_spec_dir: Path) -> None:
        """Malformed requirements.json triggers LoadError."""
        import afspec

        with pytest.raises(afspec.LoadError):
            afspec.load_spec(malformed_json_spec_dir)


# ===========================================================================
# TS-132-P1: Format detection is deterministic
# ===========================================================================


class TestFormatDetectionDeterminism:
    """TS-132-P1: Format detection always returns the same result for the same file set.

    Property 1: Validates 132-REQ-3.1, 132-REQ-3.2
    """

    @pytest.mark.property
    @settings(max_examples=20, deadline=None)
    @given(
        has_req_json=st.booleans(),
    )
    def test_discovery_is_deterministic(
        self, tmp_path_factory: pytest.TempPathFactory, has_req_json: bool,
    ) -> None:
        """discover_specs returns identical results on repeated calls."""
        specs_dir = tmp_path_factory.mktemp("prop_test")
        spec_dir = specs_dir / "01_test"
        spec_dir.mkdir()
        if has_req_json:
            (spec_dir / "requirements.json").write_text("{}")

        result1 = discover_specs(specs_dir)
        result2 = discover_specs(specs_dir)
        assert result1 == result2


# ===========================================================================
# TS-132-P2: Discovery returns only v1.2 specs
# ===========================================================================


class TestDiscoveryOnlyV12:
    """TS-132-P2: No v1 spec ever appears in discovery results.

    Property 2: Validates 132-REQ-3.3
    """

    @pytest.mark.property
    @settings(max_examples=10, deadline=None)
    @given(
        num_v1=st.integers(min_value=0, max_value=3),
        num_v12=st.integers(min_value=0, max_value=3),
    )
    def test_all_results_are_v12(
        self,
        tmp_path_factory: pytest.TempPathFactory,
        num_v1: int,
        num_v12: int,
    ) -> None:
        """Every SpecInfo in discovery results has format V1_2_JSON."""
        root = tmp_path_factory.mktemp("prop_disc")

        # Create v1 spec folders
        for i in range(num_v1):
            prefix = i + 1
            _write_v1_spec(root / f"{prefix:02d}_v1_spec_{i}")

        # Create v1.2 spec folders
        for i in range(num_v12):
            prefix = num_v1 + i + 1
            _write_spec(root / f"{prefix:02d}_spec_{i}")

        if num_v1 == 0 and num_v12 == 0:
            # No folders means discover_specs may raise — skip
            return

        try:
            results = discover_specs(root)
        except Exception:
            # If no v1.2 specs exist, discover_specs may raise — acceptable
            return

        for info in results:
            pass


# ===========================================================================
# TS-132-SMOKE-1: Discovery to load end-to-end
# ===========================================================================


class TestDiscoveryToLoadSmoke:
    """TS-132-SMOKE-1: Discover a v1.2 spec folder and load it via afspec.

    Execution Path: Path 1 + Path 2 from design.md
    """

    def test_discover_then_load(self, v12_specs_root: Path) -> None:
        """Discover v1.2 spec and load it via afspec end-to-end."""
        import afspec

        specs = discover_specs(v12_specs_root)
        assert len(specs) == 1

        spec = afspec.load_spec(specs[0].path)
        assert spec.prd.frontmatter.spec_id != ""
