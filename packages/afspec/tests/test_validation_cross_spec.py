"""Tests for cross-spec interface consistency validation.

Covers ``validate_cross_spec()`` from ``afspec.validation`` and
``load_dependent_interfaces()`` from ``afspec.discovery``.
"""

from __future__ import annotations

import json
from pathlib import Path

from afspec.discovery import DependencyGraph, load_dependent_interfaces
from afspec.models import (
    DependencyEdge,
    ExternalAPI,
    ExternalAPISymbol,
    Requirements,
    Spec,
    TaskDependency,
    Tasks,
    TestSpec,
)
from afspec.validation import validate_cross_spec

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_spec(
    spec_id: str,
    glossary: dict[str, str] | None = None,
    external_apis: list[ExternalAPI] | None = None,
    dependencies: list[TaskDependency] | None = None,
) -> Spec:
    """Build a minimal ``Spec`` for cross-spec validation tests."""
    reqs = Requirements(
        spec_id=spec_id,
        spec_name=spec_id,
        glossary=glossary or {},
        external_apis=external_apis or [],
    )
    tasks = Tasks(
        spec_id=spec_id,
        spec_name=spec_id,
        dependencies=dependencies or [],
    )
    test_spec = TestSpec(spec_id=spec_id, spec_name=spec_id)
    return Spec(requirements=reqs, test_spec=test_spec, tasks=tasks)


def _make_graph(
    edges: list[DependencyEdge] | None = None,
    spec_ids: list[str] | None = None,
) -> DependencyGraph:
    """Build a ``DependencyGraph`` from explicit edges and spec IDs."""
    return DependencyGraph(
        edge_list=edges or [],
        all_spec_ids=spec_ids or [],
    )


def _setup_spec_folder(
    root: Path,
    folder_name: str,
    spec_id: str,
    spec_name: str,
    *,
    glossary: dict[str, str] | None = None,
    external_apis: list[dict] | None = None,
    deps: list[dict] | None = None,
) -> Path:
    """Create a minimal spec folder with prd.md, requirements.json, and tasks.json."""
    folder = root / folder_name
    folder.mkdir(parents=True, exist_ok=True)

    prd = (
        f'---\nspec_id: "{spec_id}"\nspec_name: "{spec_name}"\n'
        f'title: "Test {spec_id}"\nstatus: "active"\n'
        f'created_at: "2026-01-01T00:00:00Z"\n'
        f'updated_at: "2026-01-01T00:00:00Z"\n'
        f'owner: "test"\nsource: "test"\nsupersedes: []\ntags: []\n'
        f"intent_hash: null\nschema_version: 1\n---\n# {spec_name}\n"
    )
    (folder / "prd.md").write_text(prd)

    req_data = {
        "$schema": "https://agent-fox.dev/schemas/requirements.v1.json",
        "spec_id": spec_id,
        "spec_name": spec_name,
        "schema_version": 1,
        "introduction": f"Test introduction for {spec_name}",
        "glossary": glossary or {},
        "requirements": [],
        "correctness_properties": [],
        "execution_paths": [],
        "error_handling": [],
        "external_apis": external_apis or [],
    }
    (folder / "requirements.json").write_text(json.dumps(req_data, indent=2) + "\n")

    tasks_data = {
        "$schema": "https://agent-fox.dev/schemas/tasks.v1.json",
        "spec_id": spec_id,
        "spec_name": spec_name,
        "schema_version": 1,
        "test_commands": {"spec_tests": "pytest", "linter": "ruff"},
        "dependencies": deps or [],
        "task_groups": [
            {
                "id": 1,
                "title": "Tests",
                "kind": "tests",
                "subtasks": [
                    {
                        "id": "1.1",
                        "title": "t",
                        "state": "pending",
                        "requirement_refs": [],
                        "test_spec_refs": [],
                        "details": "d",
                    }
                ],
                "verification": {"id": "1.V", "checks": ["pass"]},
            },
            {
                "id": 2,
                "title": "Wiring",
                "kind": "wiring_verification",
                "subtasks": [
                    {
                        "id": "2.1",
                        "title": "t",
                        "state": "pending",
                        "requirement_refs": [],
                        "test_spec_refs": [],
                        "details": "d",
                    }
                ],
                "verification": {"id": "2.V", "checks": ["pass"]},
            },
        ],
        "traceability": [],
    }
    (folder / "tasks.json").write_text(json.dumps(tasks_data, indent=2) + "\n")

    return folder


# ===========================================================================
# Tests for validate_cross_spec
# ===========================================================================


class TestValidateCrossSpec:
    """Cross-spec interface consistency checks."""

    # -----------------------------------------------------------------------
    # cross-spec-1: duplicate symbol with different signature
    # -----------------------------------------------------------------------

    def test_duplicate_symbol_different_signature_produces_error(self) -> None:
        """Two specs defining the same symbol with different signatures
        must produce a cross-spec-1 error."""
        sym_a = ExternalAPISymbol(
            name="do_thing",
            import_path="pkg.module",
            signature="(x: int) -> str",
            notes="",
        )
        sym_b = ExternalAPISymbol(
            name="do_thing",
            import_path="pkg.module",
            signature="(x: str) -> bool",
            notes="",
        )
        api_a = ExternalAPI(package="pkg", version="1.0", symbols=[sym_a])
        api_b = ExternalAPI(package="pkg", version="1.0", symbols=[sym_b])

        specs = {
            "01": _make_spec("01", external_apis=[api_a]),
            "02": _make_spec("02", external_apis=[api_b]),
        }
        graph = _make_graph(spec_ids=["01", "02"])

        errors = validate_cross_spec(specs, graph)
        assert len(errors) >= 1
        cross1 = [e for e in errors if e.rule == "cross-spec-1"]
        assert len(cross1) == 1
        assert "do_thing" in cross1[0].message

    # -----------------------------------------------------------------------
    # cross-spec-1: same symbol, same signature => no error
    # -----------------------------------------------------------------------

    def test_same_symbol_same_signature_no_error(self) -> None:
        """Two specs with the same symbol and identical signature produce
        no cross-spec-1 errors."""
        sym = ExternalAPISymbol(
            name="do_thing",
            import_path="pkg.module",
            signature="(x: int) -> str",
            notes="",
        )
        api = ExternalAPI(package="pkg", version="1.0", symbols=[sym])

        specs = {
            "01": _make_spec("01", external_apis=[api]),
            "02": _make_spec("02", external_apis=[api]),
        }
        graph = _make_graph(spec_ids=["01", "02"])

        errors = validate_cross_spec(specs, graph)
        cross1 = [e for e in errors if e.rule == "cross-spec-1"]
        assert cross1 == []

    # -----------------------------------------------------------------------
    # cross-spec-2: glossary conflict
    # -----------------------------------------------------------------------

    def test_glossary_conflict_produces_error(self) -> None:
        """Same glossary term with different definitions across specs
        must produce a cross-spec-2 error."""
        specs = {
            "01": _make_spec("01", glossary={"widget": "A UI control element"}),
            "02": _make_spec("02", glossary={"widget": "A data transfer object"}),
        }
        graph = _make_graph(spec_ids=["01", "02"])

        errors = validate_cross_spec(specs, graph)
        cross2 = [e for e in errors if e.rule == "cross-spec-2"]
        assert len(cross2) == 1
        assert "widget" in cross2[0].message

    # -----------------------------------------------------------------------
    # cross-spec-2: glossary agreement => no error
    # -----------------------------------------------------------------------

    def test_glossary_agreement_no_error(self) -> None:
        """Same glossary term with identical definitions across specs
        produces no cross-spec-2 errors."""
        specs = {
            "01": _make_spec("01", glossary={"widget": "A UI control element"}),
            "02": _make_spec("02", glossary={"widget": "A UI control element"}),
        }
        graph = _make_graph(spec_ids=["01", "02"])

        errors = validate_cross_spec(specs, graph)
        cross2 = [e for e in errors if e.rule == "cross-spec-2"]
        assert cross2 == []

    # -----------------------------------------------------------------------
    # cross-spec-3: unknown dependency
    # -----------------------------------------------------------------------

    def test_unknown_dependency_produces_error(self) -> None:
        """A task dependency referencing a spec_id absent from the specs
        dict must produce a cross-spec-3 error."""
        dep = TaskDependency(
            depends_on_spec="99",
            from_group=1,
            to_group=1,
            relationship="uses",
        )
        specs = {
            "01": _make_spec("01", dependencies=[dep]),
        }
        graph = _make_graph(spec_ids=["01"])

        errors = validate_cross_spec(specs, graph)
        cross3 = [e for e in errors if e.rule == "cross-spec-3"]
        assert len(cross3) == 1
        assert "99" in cross3[0].message

    # -----------------------------------------------------------------------
    # Edge cases
    # -----------------------------------------------------------------------

    def test_no_specs_no_errors(self) -> None:
        """Empty specs dict and empty graph produce no errors."""
        errors = validate_cross_spec({}, _make_graph())
        assert errors == []

    def test_no_dependencies_no_cross_spec_errors(self) -> None:
        """Two specs with no dependencies, no shared symbols, and
        disjoint glossaries produce no errors."""
        specs = {
            "01": _make_spec("01", glossary={"alpha": "first"}),
            "02": _make_spec("02", glossary={"beta": "second"}),
        }
        graph = _make_graph(spec_ids=["01", "02"])

        errors = validate_cross_spec(specs, graph)
        assert errors == []


# ===========================================================================
# Tests for load_dependent_interfaces
# ===========================================================================


class TestLoadDependentInterfaces:
    """Filesystem-based tests for ``load_dependent_interfaces``."""

    def test_load_dependent_interfaces_with_deps(self, tmp_path: Path) -> None:
        """When beta depends on alpha, loading beta's dependent interfaces
        must return alpha's glossary, external APIs, and interface symbols."""
        _setup_spec_folder(
            tmp_path,
            "01_alpha",
            spec_id="01",
            spec_name="alpha",
            glossary={"token": "An auth token"},
            external_apis=[
                {
                    "package": "authlib",
                    "version": "2.0",
                    "symbols": [
                        {
                            "name": "verify",
                            "import_path": "authlib.core",
                            "signature": "(token: str) -> bool",
                            "notes": "",
                        }
                    ],
                }
            ],
        )
        _setup_spec_folder(
            tmp_path,
            "02_beta",
            spec_id="02",
            spec_name="beta",
            deps=[
                {
                    "depends_on_spec": "01",
                    "from_group": 1,
                    "to_group": 1,
                    "relationship": "uses",
                }
            ],
        )

        results = load_dependent_interfaces("02", tmp_path)

        assert len(results) == 1
        upstream = results[0]
        assert upstream["spec_id"] == "01"
        assert upstream["spec_name"] == "alpha"
        assert upstream["glossary"] == {"token": "An auth token"}
        assert len(upstream["external_apis"]) == 1
        assert upstream["external_apis"][0]["package"] == "authlib"
        # interface_symbols is a list (may be empty if no requirements)
        assert isinstance(upstream["interface_symbols"], list)

    def test_load_dependent_interfaces_no_deps(self, tmp_path: Path) -> None:
        """A spec with no dependencies returns an empty list."""
        _setup_spec_folder(
            tmp_path,
            "01_solo",
            spec_id="01",
            spec_name="solo",
        )

        results = load_dependent_interfaces("01", tmp_path)
        assert results == []

    def test_load_dependent_interfaces_nonexistent_spec(self, tmp_path: Path) -> None:
        """Requesting dependencies for an unknown spec_id returns []."""
        _setup_spec_folder(
            tmp_path,
            "01_only",
            spec_id="01",
            spec_name="only",
        )

        results = load_dependent_interfaces("99", tmp_path)
        assert results == []
