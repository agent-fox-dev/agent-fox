"""Integration tests for the lint-specs CLI command.

Test Spec: TS-09-E1, TS-09-E2, TS-09-E4, TS-09-E5, TS-09-E6,
           TS-09-E7, TS-09-E8
Requirements: 09-REQ-1.E1, 09-REQ-9.1, 09-REQ-9.2, 09-REQ-9.3,
              09-REQ-9.4, 09-REQ-9.5, 09-REQ-6.1
Fixes: #118
"""

from __future__ import annotations

import json
import os
import shutil
from pathlib import Path

import pytest
from af.app import main
from click.testing import CliRunner

# -- Fixtures ------------------------------------------------------------------

FIXTURES_DIR = Path(__file__).parent.parent / "fixtures" / "specs"


@pytest.fixture
def cli_runner() -> CliRunner:
    """Provide a Click CLI test runner."""
    return CliRunner()


def _v12_prd_content(spec_id: str, name: str) -> str:
    """Generate a valid v1.2 prd.md with YAML frontmatter."""
    return (
        f"---\n"
        f'spec_id: "{spec_id}"\n'
        f'spec_name: "{name}"\n'
        f'title: "Test Spec {name}"\n'
        f'status: "draft"\n'
        f'created_at: "2024-01-01T00:00:00Z"\n'
        f'updated_at: "2024-01-01T00:00:00Z"\n'
        f'owner: "test"\n'
        f'source: "test"\n'
        f"schema_version: 1\n"
        f"---\n"
        f"# {name}\n\nTest PRD.\n"
    )


def _v12_requirements_json(spec_id: str, name: str) -> str:
    """Generate valid v1.2 requirements.json content."""
    return json.dumps(
        {
            "spec_id": spec_id,
            "spec_name": name,
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


def _v12_test_spec_json(spec_id: str, name: str) -> str:
    """Generate valid v1.2 test_spec.json content."""
    return json.dumps(
        {
            "spec_id": spec_id,
            "spec_name": name,
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


def _v12_tasks_json(
    spec_id: str,
    name: str,
    *,
    all_completed: bool = False,
    produce_finding: bool = False,
) -> str:
    """Generate v1.2 tasks.json content.

    If all_completed, all subtasks have state "done".
    Otherwise, subtasks have state "pending".

    If produce_finding, the last task group uses kind "standard" instead
    of "wiring_verification", which triggers a schema validation error
    from afspec.validate().
    """
    state = "done" if all_completed else "pending"
    last_kind = "standard" if produce_finding else "wiring_verification"
    return json.dumps(
        {
            "spec_id": spec_id,
            "spec_name": name,
            "schema_version": 1,
            "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
            "dependencies": [],
            "task_groups": [
                {
                    "id": 1,
                    "title": "Write tests",
                    "kind": "tests",
                    "subtasks": [
                        {
                            "id": f"{spec_id}-T-1.1",
                            "title": "Test thing",
                            "state": state,
                            "requirement_refs": [],
                            "test_spec_refs": [],
                        },
                    ],
                    "verification": {
                        "id": f"{spec_id}-T-1.V",
                        "checks": [],
                    },
                },
                {
                    "id": 2,
                    "title": "Wiring verification",
                    "kind": last_kind,
                    "subtasks": [
                        {
                            "id": f"{spec_id}-T-2.1",
                            "title": "Verify wiring",
                            "state": state,
                            "requirement_refs": [],
                            "test_spec_refs": [],
                        },
                    ],
                    "verification": {
                        "id": f"{spec_id}-T-2.V",
                        "checks": [],
                    },
                },
            ],
            "traceability": [],
        },
        indent=2,
    )


def _create_spec_with_tasks(
    specs_dir: Path,
    name: str,
    *,
    all_completed: bool = False,
) -> Path:
    """Create a minimal v1.2 spec directory with proper JSON artifacts.

    If all_completed is True, all subtask states are "completed".
    Otherwise, subtask states are "not_started".
    """
    spec_dir = specs_dir / name
    spec_dir.mkdir(exist_ok=True)

    # Use spec name prefix as spec_id (e.g., "01" from "01_done_spec")
    spec_id = f"test-{name[:2]}"

    # v1.2 artifacts
    (spec_dir / "prd.md").write_text(_v12_prd_content(spec_id, name))
    (spec_dir / "requirements.json").write_text(_v12_requirements_json(spec_id, name))
    (spec_dir / "test_spec.json").write_text(_v12_test_spec_json(spec_id, name))
    (spec_dir / "tasks.json").write_text(
        _v12_tasks_json(spec_id, name, all_completed=all_completed, produce_finding=True)
    )
    return spec_dir


def _setup_project_with_specs(
    project_dir: Path,
    spec_fixtures: list[str],
    *,
    produce_finding: bool = False,
) -> None:
    """Create a minimal project with selected fixture specs.

    Creates .agent-fox/config.toml and copies fixture spec directories
    into .specs/ with NN_ prefixes.  Adds valid v1.2 JSON artifacts so
    discover_specs (which filters out v1 markdown) includes them.

    When produce_finding is True, the tasks.json uses 'standard' as the
    last group kind, which triggers a schema validation error.
    """
    # Create config directory
    agent_fox_dir = project_dir / ".agent-fox"
    agent_fox_dir.mkdir(exist_ok=True)
    (agent_fox_dir / "config.toml").write_text("")

    # Create .specs/ directory and copy fixtures
    specs_dir = project_dir / ".specs"
    specs_dir.mkdir(exist_ok=True)

    for i, fixture_name in enumerate(spec_fixtures, start=1):
        src = FIXTURES_DIR / fixture_name
        dst = specs_dir / f"{i:02d}_{fixture_name}"
        if src.exists():
            shutil.copytree(src, dst)
            name = dst.name
            spec_id = f"test-{i:02d}"

            # Add valid v1.2 artifacts so discover_specs includes the
            # spec and afspec.load_spec() can parse it.
            if not (dst / "requirements.json").exists():
                (dst / "requirements.json").write_text(_v12_requirements_json(spec_id, name))
            if not (dst / "test_spec.json").exists():
                (dst / "test_spec.json").write_text(_v12_test_spec_json(spec_id, name))
            if not (dst / "tasks.json").exists():
                (dst / "tasks.json").write_text(_v12_tasks_json(spec_id, name, produce_finding=produce_finding))
            # Ensure prd.md has YAML frontmatter for afspec.load_spec()
            prd_path = dst / "prd.md"
            if prd_path.exists():
                existing = prd_path.read_text()
                if not existing.startswith("---"):
                    prd_path.write_text(_v12_prd_content(spec_id, name))


# -- TS-09-E1: No specs directory ----------------------------------------------


class TestNoSpecsDirectory:
    """TS-09-E1: No specs directory.

    Requirements: 09-REQ-1.E1, 09-REQ-9.4
    Verify lint-specs reports error when .specs/ does not exist.
    """

    def test_exits_with_code_one(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """lint-specs exits with code 1 when no .specs/ directory."""
        # Create minimal project without .specs/
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir()
        (agent_fox_dir / "config.toml").write_text("")

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["lint-specs"])
            assert result.exit_code == 1
        finally:
            os.chdir(original_dir)

    def test_output_indicates_no_specs(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """lint-specs output mentions no specifications found."""
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir()
        (agent_fox_dir / "config.toml").write_text("")

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["lint-specs"])
            output_lower = result.output.lower()
            assert "no specifications" in output_lower or "error" in output_lower
        finally:
            os.chdir(original_dir)


# -- TS-09-E2: Empty specs directory -------------------------------------------


class TestEmptySpecsDirectory:
    """TS-09-E2: Empty specs directory.

    Requirements: 09-REQ-1.E1, 09-REQ-9.4
    Verify lint-specs reports error when .specs/ exists but is empty.
    """

    def test_exits_with_code_one(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """lint-specs exits with code 1 when .specs/ is empty."""
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir()
        (agent_fox_dir / "config.toml").write_text("")
        (tmp_path / ".specs").mkdir()

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["lint-specs"])
            assert result.exit_code == 1
        finally:
            os.chdir(original_dir)


# -- TS-09-E4: JSON output format ---------------------------------------------


class TestJsonOutputFormat:
    """TS-09-E4: JSON output format.

    Requirements: 09-REQ-9.1, 09-REQ-9.3
    Verify --json produces valid JSON with correct structure.
    """

    def test_json_output_is_valid(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """--json produces valid JSON output."""
        _setup_project_with_specs(tmp_path, ["incomplete_spec"])

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["--json", "lint-specs"])
            data = json.loads(result.output)
            assert "findings" in data
            assert "summary" in data
        finally:
            os.chdir(original_dir)

    def test_json_summary_counts_match(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """JSON summary total matches number of findings."""
        _setup_project_with_specs(tmp_path, ["incomplete_spec"])

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["--json", "lint-specs"])
            data = json.loads(result.output)
            assert data["summary"]["total"] == len(data["findings"])
        finally:
            os.chdir(original_dir)


# -- TS-09-E6: Table output includes summary line -----------------------------


class TestTableOutputSummary:
    """TS-09-E6: Table output includes summary line.

    Requirements: 09-REQ-9.2
    Verify table output includes summary with severity counts.
    """

    def test_table_output_contains_error_text(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Table output contains 'error' severity text."""
        _setup_project_with_specs(tmp_path, ["incomplete_spec"], produce_finding=True)

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["lint-specs"])
            assert "error" in result.output.lower()
        finally:
            os.chdir(original_dir)


# -- TS-09-E7: Exit code 0 when only warnings ---------------------------------


class TestExitCodeZeroWarningsOnly:
    """TS-09-E7: Exit code 0 when only warnings.

    Requirements: 09-REQ-9.4, 09-REQ-9.5
    Verify exit code is 0 when only Warning/Hint findings exist.
    """

    def test_warnings_only_exits_zero(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """lint-specs exits with code 0 when only warnings are present."""
        _setup_project_with_specs(tmp_path, ["warnings_only_spec"])

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["lint-specs"])
            assert result.exit_code == 0
        finally:
            os.chdir(original_dir)


# -- TS-09-E8: Valid dependencies produce no findings --------------------------


class TestValidDependenciesIntegration:
    """TS-09-E8: Valid dependencies produce no findings.

    Requirements: 09-REQ-6.1
    Verify valid dependency references don't produce error findings.
    """

    def test_valid_deps_no_error_findings(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Valid dependency references produce no broken-dependency findings."""
        # Create a project with two specs: one that depends on the other
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir()
        (agent_fox_dir / "config.toml").write_text("")

        specs_dir = tmp_path / ".specs"
        specs_dir.mkdir()

        # Create target spec (01_core_foundation) with all files
        target = specs_dir / "01_core_foundation"
        target.mkdir()
        for filename in [
            "prd.md",
            "requirements.md",
            "design.md",
            "test_spec.md",
            "tasks.md",
        ]:
            (target / filename).write_text(f"# {filename}\n")
        (target / "tasks.md").write_text("# Tasks\n\n- [ ] 1. Task\n  - [ ] 1.1 Sub\n  - [ ] 1.V Verify\n")
        (target / "requirements.md").write_text(
            "# Requirements\n\n### Requirement 1: Thing\n\n1. [01-REQ-1.1] Must do thing.\n"
        )
        (target / "test_spec.md").write_text("# Test Spec\n\n**Requirement:** 01-REQ-1.1\n")

        # Create referencing spec (02_dependent) with valid dep
        referencing = specs_dir / "02_dependent"
        referencing.mkdir()
        for filename in ["requirements.md", "design.md", "test_spec.md", "tasks.md"]:
            (referencing / filename).write_text(f"# {filename}\n")
        (referencing / "prd.md").write_text(
            "# PRD\n\n## Dependencies\n\n"
            "| This Spec | Depends On | What It Uses |\n"
            "|-----------|-----------|---------------|\n"
            "| 02_dependent | 01_core_foundation | Types |\n"
        )
        (referencing / "tasks.md").write_text("# Tasks\n\n- [ ] 1. Task\n  - [ ] 1.1 Sub\n  - [ ] 1.V Verify\n")
        (referencing / "requirements.md").write_text(
            "# Requirements\n\n### Requirement 1: Feature\n\n1. [02-REQ-1.1] Must do feature.\n"
        )
        (referencing / "test_spec.md").write_text("# Test Spec\n\n**Requirement:** 02-REQ-1.1\n")

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["--json", "lint-specs"])
            data = json.loads(result.output)
            broken_deps = [f for f in data["findings"] if f["rule"] == "broken-dependency"]
            assert len(broken_deps) == 0
        finally:
            os.chdir(original_dir)


# -- Issue #118: --all flag skips implemented specs ---------------------------


class TestAllFlagDefaultSkipsImplemented:
    """Issue #118: Default behavior skips fully-implemented specs.

    Verify that lint-specs only lints specs with incomplete tasks by default,
    and --all includes all specs.
    """

    def _setup_mixed_project(self, tmp_path: Path) -> None:
        """Create a project with one implemented and one incomplete spec."""
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir(exist_ok=True)
        (agent_fox_dir / "config.toml").write_text("")
        specs_dir = tmp_path / ".specs"
        specs_dir.mkdir(exist_ok=True)
        _create_spec_with_tasks(specs_dir, "01_done_spec", all_completed=True)
        _create_spec_with_tasks(specs_dir, "02_wip_spec", all_completed=False)

    def test_default_skips_implemented_spec(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Default lint-specs does not report findings for completed specs."""
        self._setup_mixed_project(tmp_path)
        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["--json", "lint-specs"])
            data = json.loads(result.output)
            spec_names = {f["spec_name"] for f in data["findings"]}
            assert "01_done_spec" not in spec_names
            assert "02_wip_spec" in spec_names
        finally:
            os.chdir(original_dir)

    def test_all_flag_includes_implemented_spec(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """--all includes findings for completed specs."""
        self._setup_mixed_project(tmp_path)
        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["--json", "lint-specs", "--all"])
            data = json.loads(result.output)
            spec_names = {f["spec_name"] for f in data["findings"]}
            assert "01_done_spec" in spec_names
            assert "02_wip_spec" in spec_names
        finally:
            os.chdir(original_dir)

    def test_all_specs_implemented_shows_no_findings(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """When all specs are implemented, default lint reports no findings."""
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir(exist_ok=True)
        (agent_fox_dir / "config.toml").write_text("")
        specs_dir = tmp_path / ".specs"
        specs_dir.mkdir(exist_ok=True)
        _create_spec_with_tasks(specs_dir, "01_done", all_completed=True)

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["lint-specs"])
            assert "No findings" in result.output or result.exit_code == 0
        finally:
            os.chdir(original_dir)

    def test_archived_dep_not_flagged_as_broken(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """Dependency on an archived spec does not produce broken-dependency."""
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir(exist_ok=True)
        (agent_fox_dir / "config.toml").write_text("")
        specs_dir = tmp_path / ".specs"
        specs_dir.mkdir(exist_ok=True)

        # Create an archived spec with tasks
        archive_dir = specs_dir / "archive"
        archive_dir.mkdir()
        archived = archive_dir / "01_archived_dep"
        archived.mkdir()
        for f in ["prd.md", "requirements.md", "design.md", "test_spec.md"]:
            (archived / f).write_text(f"# {f}\n")
        (archived / "tasks.md").write_text("# Tasks\n\n- [x] 1. Task\n  - [x] 1.1 Sub\n  - [x] 1.V Verify\n")

        # Create an active spec that depends on the archived one
        _create_spec_with_tasks(specs_dir, "02_active_spec", all_completed=False)
        (specs_dir / "02_active_spec" / "prd.md").write_text(
            '---\nspec_id: "test-02"\nspec_name: "02_active_spec"\n'
            'title: "Active Spec"\nstatus: "draft"\n'
            'created_at: "2024-01-01T00:00:00Z"\n'
            'updated_at: "2024-01-01T00:00:00Z"\n'
            'owner: "test"\nsource: "test"\nschema_version: 1\n---\n'
            "# PRD\n\n## Dependencies\n\n"
            "| This Spec | Depends On | What It Uses |\n"
            "|-----------|-----------|---------------|\n"
            "| 02_active_spec | 01_archived_dep | Archived types |\n"
        )

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            result = cli_runner.invoke(main, ["--json", "lint-specs"])
            data = json.loads(result.output)
            broken = [f for f in data["findings"] if f["rule"] == "broken-dependency"]
            assert len(broken) == 0, f"Unexpected broken-dependency findings: {broken}"
        finally:
            os.chdir(original_dir)

    def test_spec_without_tasks_md_is_linted(self, cli_runner: CliRunner, tmp_path: Path) -> None:
        """A spec without tasks.md is considered not implemented and is linted."""
        agent_fox_dir = tmp_path / ".agent-fox"
        agent_fox_dir.mkdir(exist_ok=True)
        (agent_fox_dir / "config.toml").write_text("")
        specs_dir = tmp_path / ".specs"
        specs_dir.mkdir(exist_ok=True)

        # Create a spec with no tasks.md (v1.2 format for discovery)
        spec_dir = specs_dir / "01_no_tasks"
        spec_dir.mkdir()
        (spec_dir / "prd.md").write_text("# PRD\n")
        (spec_dir / "requirements.json").write_text("{}")
        (spec_dir / "requirements.md").write_text("# Requirements\n")

        original_dir = os.getcwd()
        os.chdir(tmp_path)
        try:
            # Use table mode — JSON mode can have log warnings mixed in
            result = cli_runner.invoke(main, ["lint-specs"])
            assert "01_no_tasks" in result.output
        finally:
            os.chdir(original_dir)
