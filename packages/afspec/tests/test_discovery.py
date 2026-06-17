"""Tests for spec discovery and dependency graph."""

from __future__ import annotations

import json
from pathlib import Path

import pytest

from afspec import (
    SpecError,
    build_dependency_graph,
    discover_specs,
)


def _setup_spec_folder(root: Path, name: str, spec_id: str, deps: list[dict] | None = None) -> Path:
    """Create a minimal spec folder with prd.md and tasks.json for discovery tests."""
    folder = root / name
    folder.mkdir(parents=True, exist_ok=True)
    prd = f"""---
spec_id: "{spec_id}"
spec_name: "{name.split("_", 1)[1] if "_" in name else name}"
title: "Test {spec_id}"
status: "draft"
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
owner: "test"
source: "test"
supersedes: []
tags: []
intent_hash: null
schema_version: 1
---
# Test

## Intent

Test intent.
"""
    (folder / "prd.md").write_text(prd)

    tasks_data = {
        "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
        "spec_id": spec_id,
        "spec_name": name.split("_", 1)[1] if "_" in name else name,
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": deps or [],
        "task_groups": [
            {
                "id": 1,
                "kind": "tests",
                "title": "Tests",
                "subtasks": [],
                "verification": {"id": "1.V", "checks": ["pass"]},
            },
            {
                "id": 2,
                "kind": "wiring_verification",
                "title": "Wiring",
                "subtasks": [],
                "verification": {"id": "2.V", "checks": ["pass"]},
            },
        ],
        "traceability": [],
    }
    (folder / "tasks.json").write_text(json.dumps(tasks_data, indent=2) + "\n")

    req_data = {
        "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
        "spec_id": spec_id,
        "spec_name": name.split("_", 1)[1] if "_" in name else name,
        "schema_version": 1,
        "introduction": "Test",
        "glossary": {},
        "requirements": [],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
    }
    (folder / "requirements.json").write_text(json.dumps(req_data, indent=2) + "\n")

    ts_data = {
        "$schema": "https://agent-fox.dev/schemas/test_spec.v1.json",
        "spec_id": spec_id,
        "spec_name": name.split("_", 1)[1] if "_" in name else name,
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
    }
    (folder / "test_spec.json").write_text(json.dumps(ts_data, indent=2) + "\n")

    return folder


# ---------------------------------------------------------------------------
# TS-01-37: discover_specs finds spec folders (01-REQ-9.1)
# ---------------------------------------------------------------------------


def test_discover_specs(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_foo", "01")
    _setup_spec_folder(tmp_path, "02_bar", "02")
    archive = tmp_path / "archive" / "03_old"
    archive.mkdir(parents=True)
    (tmp_path / "readme.md").write_text("not a spec")

    metas = discover_specs(tmp_path)
    assert len(metas) == 2
    ids = [m.spec_id for m in metas]
    assert "01" in ids
    assert "02" in ids
    assert "03" not in ids


# ---------------------------------------------------------------------------
# TS-01-38: SpecMeta loaded from frontmatter (01-REQ-9.2)
# ---------------------------------------------------------------------------


def test_discover_loads_metadata(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_foo", "01")
    metas = discover_specs(tmp_path)
    m = metas[0]
    assert m.spec_id == "01"
    assert m.spec_name == "foo"
    assert m.status == "draft"
    assert str(m.dir).endswith("01_foo")


# ---------------------------------------------------------------------------
# TS-01-39: build_dependency_graph constructs graph (01-REQ-9.3)
# ---------------------------------------------------------------------------


def test_build_dependency_graph(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_alpha", "01")
    dep = {
        "depends_on_spec": "01",
        "from_group": 1,
        "to_group": 1,
        "relationship": "depends on alpha",
        "sentinel": False,
    }
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep])

    metas = discover_specs(tmp_path)
    graph = build_dependency_graph(metas, tmp_path)
    order = graph.topological_sort()
    assert order.index("01") < order.index("02")


# ---------------------------------------------------------------------------
# TS-01-50: DependencyGraph dependencies returns direct dependencies (01-REQ-9.4)
# ---------------------------------------------------------------------------


def test_graph_dependencies(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_alpha", "01")
    dep1 = {"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    dep2 = {"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "dep2", "sentinel": False}
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep1])
    dep3 = {"depends_on_spec": "02", "from_group": 1, "to_group": 1, "relationship": "dep3", "sentinel": False}
    _setup_spec_folder(tmp_path, "03_gamma", "03", deps=[dep2, dep3])

    metas = discover_specs(tmp_path)
    graph = build_dependency_graph(metas, tmp_path)
    deps = graph.dependencies("03")
    assert len(deps) == 2
    dep_specs = [d.from_spec for d in deps]
    assert "01" in dep_specs
    assert "02" in dep_specs


# ---------------------------------------------------------------------------
# TS-01-51: DependencyGraph dependents returns direct dependents (01-REQ-9.5)
# ---------------------------------------------------------------------------


def test_graph_dependents(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_alpha", "01")
    dep = {"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep])
    _setup_spec_folder(tmp_path, "03_gamma", "03", deps=[dep])

    metas = discover_specs(tmp_path)
    graph = build_dependency_graph(metas, tmp_path)
    deps = graph.dependents("01")
    assert len(deps) == 2
    dep_specs = [d.to_spec for d in deps]
    assert "02" in dep_specs
    assert "03" in dep_specs


# ---------------------------------------------------------------------------
# TS-01-E16: discover_specs with non-existent root (01-REQ-9.E1)
# ---------------------------------------------------------------------------


def test_discover_nonexistent_root() -> None:
    with pytest.raises(SpecError):
        discover_specs("/nonexistent/path/xyz")


# ---------------------------------------------------------------------------
# TS-01-E17: Non-spec directories skipped (01-REQ-9.E2)
# ---------------------------------------------------------------------------


def test_discover_skips_non_matching(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_valid", "01")
    (tmp_path / "docs").mkdir()
    (tmp_path / ".hidden").mkdir()
    (tmp_path / "no_number").mkdir()

    metas = discover_specs(tmp_path)
    assert len(metas) == 1
    assert metas[0].spec_id == "01"


# ---------------------------------------------------------------------------
# TS-01-E18: Dependency cycle detected (01-REQ-9.E3)
# ---------------------------------------------------------------------------


def test_dependency_cycle(tmp_path: Path) -> None:
    dep_on_02 = {"depends_on_spec": "02", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    dep_on_01 = {"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    _setup_spec_folder(tmp_path, "01_alpha", "01", deps=[dep_on_02])
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep_on_01])

    metas = discover_specs(tmp_path)
    with pytest.raises(SpecError, match="cycle"):
        build_dependency_graph(metas, tmp_path)


# ---------------------------------------------------------------------------
# TS-01-E26: Dangling dependency reference (01-REQ-9.E4)
# ---------------------------------------------------------------------------


def test_dangling_dependency(tmp_path: Path) -> None:
    dep = {"depends_on_spec": "99", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    _setup_spec_folder(tmp_path, "01_alpha", "01")
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep])

    metas = discover_specs(tmp_path)
    with pytest.raises(SpecError, match="99"):
        build_dependency_graph(metas, tmp_path)


# ---------------------------------------------------------------------------
# TS-01-E27: dependencies/dependents with no edges returns empty list (01-REQ-9.E5)
# ---------------------------------------------------------------------------


def test_empty_dependencies(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_alpha", "01")
    dep = {"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep])

    metas = discover_specs(tmp_path)
    graph = build_dependency_graph(metas, tmp_path)

    deps = graph.dependencies("01")
    assert deps is not None
    assert len(deps) == 0

    dependents = graph.dependents("02")
    assert dependents is not None
    assert len(dependents) == 0


# ---------------------------------------------------------------------------
# TS-01-SMOKE-7: Discover specs end-to-end (PATH-7)
# ---------------------------------------------------------------------------


def test_smoke_discover(tmp_path: Path) -> None:
    _setup_spec_folder(tmp_path, "01_alpha", "01")
    dep = {"depends_on_spec": "01", "from_group": 1, "to_group": 1, "relationship": "dep", "sentinel": False}
    _setup_spec_folder(tmp_path, "02_beta", "02", deps=[dep])

    metas = discover_specs(tmp_path)
    assert len(metas) == 2

    graph = build_dependency_graph(metas, tmp_path)
    order = graph.topological_sort()
    assert order == ["01", "02"]
