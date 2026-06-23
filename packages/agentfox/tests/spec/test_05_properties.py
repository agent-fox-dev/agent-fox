"""Spec 05: Property-based and agent-mode integration tests.

Test Spec: TS-05-22, TS-05-23, TS-05-P1, TS-05-P2, TS-05-P3, TS-05-P4, TS-05-P5
Requirements: 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.3, 05-REQ-3.1, 05-REQ-3.2,
              05-REQ-3.E1, 05-REQ-4.1, 05-REQ-4.2, 05-REQ-4.3, 05-REQ-5.1,
              05-REQ-5.2, 05-REQ-5.4, 05-REQ-6.1
"""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

import pytest
from click.testing import CliRunner
from hypothesis import given, settings
from hypothesis import strategies as st

# ---------------------------------------------------------------------------
# Valid spec fixture content
# ---------------------------------------------------------------------------

PRD_MD = """\
---
spec_id: "test-05-prop"
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


def _make_requirements_json(
    spec_id: str = "test-05-prop",
    spec_name: str = "test_fixture",
) -> str:
    """Build a valid requirements JSON string."""
    return json.dumps(
        {
            "spec_id": spec_id,
            "spec_name": spec_name,
            "schema_version": 1,
            "introduction": "Test requirements",
            "glossary": {},
            "requirements": [
                {
                    "id": "TEST-REQ-1",
                    "title": "Test requirement",
                    "user_story": {"role": "tester", "goal": "test", "benefit": "verify"},
                    "acceptance_criteria": [
                        {
                            "id": "TEST-REQ-1.1",
                            "ears_pattern": "ubiquitous",
                            "system": "the system",
                            "action": "SHALL do something",
                        }
                    ],
                    "edge_cases": [],
                }
            ],
            "correctness_properties": [],
            "execution_paths": [],
            "error_handling": [],
        },
        indent=2,
    )


def _make_test_spec_json(
    spec_id: str = "test-05-prop",
    spec_name: str = "test_fixture",
) -> str:
    """Build a valid test_spec JSON string."""
    return json.dumps(
        {
            "spec_id": spec_id,
            "spec_name": spec_name,
            "schema_version": 1,
            "test_cases": [
                {
                    "id": "TS-TEST-1",
                    "title": "Test case",
                    "requirement_ref": "TEST-REQ-1.1",
                    "type": "unit",
                    "preconditions": ["test"],
                    "input": "test input",
                    "expected": "test output",
                    "assertion_pseudocode": "assert True",
                }
            ],
            "property_tests": [],
            "edge_case_tests": [],
            "smoke_tests": [],
            "coverage": {
                "requirements_covered": ["TEST-REQ-1.1"],
                "properties_covered": [],
                "paths_covered": [],
                "gaps": [],
            },
        },
        indent=2,
    )


def _make_tasks_json(
    spec_id: str = "test-05-prop",
    spec_name: str = "test_fixture",
) -> str:
    """Build a valid tasks JSON string."""
    return json.dumps(
        {
            "spec_id": spec_id,
            "spec_name": spec_name,
            "schema_version": 1,
            "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
            "dependencies": [],
            "task_groups": [],
            "traceability": [],
        },
        indent=2,
    )


def _write_valid_spec(spec_dir: Path, *, include_tasks: bool = True) -> None:
    """Populate a directory with valid spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(_make_requirements_json())
    (spec_dir / "test_spec.json").write_text(_make_test_spec_json())
    if include_tasks:
        (spec_dir / "tasks.json").write_text(_make_tasks_json())
    artifacts = ["requirements.json", "test_spec.json"]
    if include_tasks:
        artifacts.append("tasks.json")
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": artifacts})
    )


def _write_spec_with_schema_errors(spec_dir: Path) -> None:
    """Populate a directory with a spec that has schema errors."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    # Requirements with empty ID (schema error)
    (spec_dir / "requirements.json").write_text(
        json.dumps(
            {
                "spec_id": "test-05-prop",
                "spec_name": "test_fixture",
                "schema_version": 1,
                "introduction": "Test",
                "glossary": {},
                "requirements": [
                    {
                        "id": "",
                        "title": "",
                        "user_story": {"role": "", "goal": "", "benefit": ""},
                        "acceptance_criteria": [],
                        "edge_cases": [],
                    }
                ],
                "correctness_properties": [],
                "execution_paths": [],
                "error_handling": [],
            },
            indent=2,
        )
    )
    (spec_dir / "test_spec.json").write_text(_make_test_spec_json())
    (spec_dir / "tasks.json").write_text(_make_tasks_json())
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"]})
    )


def _write_spec_with_integrity_errors(spec_dir: Path) -> None:
    """Populate a directory with a spec that has integrity errors (uncovered requirements)."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    # Two requirements, but test_spec only covers one
    (spec_dir / "requirements.json").write_text(
        json.dumps(
            {
                "spec_id": "test-05-prop",
                "spec_name": "test_fixture",
                "schema_version": 1,
                "introduction": "Test requirements",
                "glossary": {},
                "requirements": [
                    {
                        "id": "TEST-REQ-1",
                        "title": "Covered requirement",
                        "user_story": {"role": "tester", "goal": "test", "benefit": "verify"},
                        "acceptance_criteria": [
                            {
                                "id": "TEST-REQ-1.1",
                                "ears_pattern": "ubiquitous",
                                "system": "the system",
                                "action": "SHALL do something",
                            }
                        ],
                        "edge_cases": [],
                    },
                    {
                        "id": "TEST-REQ-2",
                        "title": "Uncovered requirement",
                        "user_story": {"role": "tester", "goal": "coverage", "benefit": "verify"},
                        "acceptance_criteria": [
                            {
                                "id": "TEST-REQ-2.1",
                                "ears_pattern": "ubiquitous",
                                "system": "the system",
                                "action": "SHALL do another thing",
                            }
                        ],
                        "edge_cases": [],
                    },
                ],
                "correctness_properties": [],
                "execution_paths": [],
                "error_handling": [],
            },
            indent=2,
        )
    )
    # test_spec only covers REQ-1, leaving REQ-2 uncovered
    (spec_dir / "test_spec.json").write_text(
        json.dumps(
            {
                "spec_id": "test-05-prop",
                "spec_name": "test_fixture",
                "schema_version": 1,
                "test_cases": [
                    {
                        "id": "TS-TEST-1",
                        "title": "Test for REQ-1 only",
                        "requirement_ref": "TEST-REQ-1.1",
                        "type": "unit",
                        "preconditions": ["test"],
                        "input": "test input",
                        "expected": "test output",
                        "assertion_pseudocode": "assert True",
                    }
                ],
                "property_tests": [],
                "edge_case_tests": [],
                "smoke_tests": [],
                "coverage": {
                    "requirements_covered": ["TEST-REQ-1.1"],
                    "properties_covered": [],
                    "paths_covered": [],
                    "gaps": ["TEST-REQ-2.1"],
                },
            },
            indent=2,
        )
    )
    (spec_dir / "tasks.json").write_text(_make_tasks_json())
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"]})
    )


def _write_spec_with_io_errors(spec_dir: Path) -> None:
    """Populate a directory with a spec that has IO errors (missing artifact files)."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    # Declare artifacts in session but don't write them to disk
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json"]})
    )


def _get_spec_package_dir() -> Path:
    """Return the directory of the spec package."""
    import spec

    return Path(spec.__file__).parent


def _get_cli_source() -> str:
    """Read the source text of spec/cli.py."""
    import spec.cli

    return Path(spec.cli.__file__).read_text()


@pytest.fixture
def runner() -> CliRunner:
    """Provide a Click CliRunner."""
    return CliRunner()


# ===========================================================================
# TS-05-22: Agent-mode purity -- all output is JSON
# ===========================================================================


class TestAgentModePurity:
    """TS-05-22: All output is JSON envelopes when AF_AGENT=1.

    Requirement: 05-REQ-5.4
    """

    @pytest.mark.xfail(reason="Agent mode JSON purity not yet implemented")
    @pytest.mark.parametrize(
        "cmd_args",
        [
            pytest.param(["render", "01", "--combined"], id="render-combined"),
            pytest.param(["validate", "01"], id="validate"),
            pytest.param(["status", "01"], id="status"),
        ],
    )
    def test_agent_mode_json_output(
        self, runner: CliRunner, tmp_path: Path, cmd_args: list[str]
    ) -> None:
        """Every stdout line is valid JSON with AF_AGENT=1."""
        spec_root = tmp_path / "specs"
        spec_root.mkdir()
        _write_valid_spec(spec_root / "01_test")
        from spec.cli import main

        result = runner.invoke(
            main,
            ["-d", str(spec_root), *cmd_args],
            env={"AF_AGENT": "1"},
            catch_exceptions=True,
        )
        stdout = result.output.strip()
        if stdout:
            for line in stdout.split("\n"):
                line = line.strip()
                if line:
                    json.loads(line)  # Must not raise


# ===========================================================================
# TS-05-23: Full test suite regression guard
# ===========================================================================


class TestFullSuiteRegression:
    """TS-05-23: All pre-existing tests pass after migration.

    Requirement: 05-REQ-6.1

    Runs pre-existing spec tests in a subprocess to verify no regressions
    from the agentic CLI migration.
    """

    @pytest.mark.smoke
    def test_spec_cli_importable(self) -> None:
        """Core spec CLI module is importable after migration."""
        from spec.cli import main

        assert callable(main)

    @pytest.mark.smoke
    def test_preexisting_spec_tests_pass(self) -> None:
        """Pre-existing spec tests pass after migration (TS-05-23).

        Runs pytest in a subprocess against the spec test directory,
        excluding spec 05 tests (to avoid recursion) and any other
        recursive full-suite-runner tests. Verifies that the migration
        did not break any pre-existing test cases.
        """
        project_root = Path(__file__).resolve().parents[4]
        result = subprocess.run(
            [
                "uv",
                "run",
                "pytest",
                "-q",
                "--tb=line",
                "packages/agentfox/tests/spec/",
                "--ignore=packages/agentfox/tests/spec/test_05_cli_wiring.py",
                "--ignore=packages/agentfox/tests/spec/test_05_migration_static.py",
                "--ignore=packages/agentfox/tests/spec/test_05_properties.py",
                # Exclude recursive suite-runner tests that invoke pytest
                # in a subprocess (they cascade failures from unrelated areas)
                "-k",
                "not test_full_test_suite_passes",
            ],
            capture_output=True,
            text=True,
            timeout=300,
            cwd=str(project_root),
        )
        assert result.returncode == 0, (
            f"Pre-existing spec tests failed (return code {result.returncode}).\n"
            f"Output:\n{result.stdout[-1000:]}\n"
            f"Errors:\n{result.stderr[-500:]}"
        )


# ===========================================================================
# TS-05-P1: Render --json envelope always has required fields
# ===========================================================================


class TestRenderEnvelopeCompleteness:
    """TS-05-P1: render --json envelope always has required top-level keys.

    Property: 05-PROP-1
    Validates: 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.3
    """

    @pytest.mark.property
    @pytest.mark.xfail(reason="render --json not yet implemented")
    @settings(max_examples=5, deadline=None)
    @given(
        include_tasks=st.booleans(),
        combined=st.booleans(),
    )
    def test_render_json_required_keys(
        self,
        tmp_path_factory: pytest.TempPathFactory,
        include_tasks: bool,
        combined: bool,
    ) -> None:
        """render --json always has 'ok' and content/artifacts keys."""
        from spec.cli import main

        runner = CliRunner()
        spec_root = tmp_path_factory.mktemp("prop_render")
        _write_valid_spec(spec_root / "01_test", include_tasks=include_tasks)

        args = ["-d", str(spec_root), "render", "01", "--json"]
        if combined:
            args.append("--combined")

        result = runner.invoke(main, args, catch_exceptions=True)
        if result.exit_code != 0:
            # Error envelopes must still have 'ok'
            parsed = json.loads(result.output)
            assert "ok" in parsed
            return

        parsed = json.loads(result.output)
        assert "ok" in parsed
        if combined:
            assert "content" in parsed
            assert "sections" in parsed
        else:
            assert "artifacts" in parsed


# ===========================================================================
# TS-05-P2: Structured errors always include category field
# ===========================================================================


class TestStructuredErrorCategory:
    """TS-05-P2: Every error object has a valid category field.

    Property: 05-PROP-2
    Validates: 05-REQ-3.1, 05-REQ-3.2, 05-REQ-3.E1
    """

    VALID_CATEGORIES = {"schema", "integrity", "io"}

    @pytest.mark.property
    @pytest.mark.xfail(reason="Structured errors not yet implemented")
    @settings(max_examples=10, deadline=None)
    @given(
        error_type=st.sampled_from(["valid", "schema", "integrity", "io"]),
    )
    def test_error_category_always_valid(
        self,
        tmp_path_factory: pytest.TempPathFactory,
        error_type: str,
    ) -> None:
        """Every error in validate output has a valid category.

        Generates specs with varying combinations of error types:
        - valid: no errors
        - schema: empty requirement ID triggers schema validation failure
        - integrity: uncovered requirements trigger integrity check failure
        - io: missing artifact files trigger IO errors
        """
        from spec.cli import main

        runner = CliRunner()
        spec_root = tmp_path_factory.mktemp("prop_validate")

        if error_type == "schema":
            _write_spec_with_schema_errors(spec_root / "01_test")
        elif error_type == "integrity":
            _write_spec_with_integrity_errors(spec_root / "01_test")
        elif error_type == "io":
            _write_spec_with_io_errors(spec_root / "01_test")
        else:
            _write_valid_spec(spec_root / "01_test")

        result = runner.invoke(
            main,
            ["-d", str(spec_root), "validate", "01"],
            catch_exceptions=True,
        )
        parsed = json.loads(result.output)
        for err in parsed.get("errors", []):
            assert "category" in err, f"Error missing 'category' field: {err}"
            assert err["category"] in self.VALID_CATEGORIES, (
                f"Invalid category '{err['category']}'; must be one of {self.VALID_CATEGORIES}"
            )
            assert len(err["category"]) > 0


# ===========================================================================
# TS-05-P3: No mixed output in agent mode
# ===========================================================================


class TestNoMixedOutputAgentMode:
    """TS-05-P3: AF_AGENT=1 produces only JSON on stdout.

    Property: 05-PROP-3
    Validates: 05-REQ-1.4, 05-REQ-5.4
    """

    @pytest.mark.property
    @pytest.mark.xfail(reason="Agent mode JSON purity not yet implemented")
    @settings(max_examples=10, deadline=None)
    @given(
        subcommand=st.sampled_from(["render", "validate", "status"]),
        combined=st.booleans(),
        use_json_flag=st.booleans(),
        use_af_agent=st.booleans(),
        use_invalid_spec=st.booleans(),
    )
    def test_agent_mode_pure_json(
        self,
        tmp_path_factory: pytest.TempPathFactory,
        subcommand: str,
        combined: bool,
        use_json_flag: bool,
        use_af_agent: bool,
        use_invalid_spec: bool,
    ) -> None:
        """All stdout output is valid JSON with AF_AGENT=1 or --json.

        Generates arbitrary spec invocations covering:
        - Subcommands: render, validate, status
        - Flags: AF_AGENT=1 and/or --json
        - Valid and invalid spec inputs

        At least one of AF_AGENT=1 or --json must be active for the
        JSON-purity invariant to apply.
        """
        # At least one agent/json mode must be active for this property
        if not use_af_agent and not use_json_flag:
            return  # Property only applies when JSON output is requested

        from spec.cli import main

        runner = CliRunner()
        spec_root = tmp_path_factory.mktemp("prop_agent")

        if use_invalid_spec:
            _write_spec_with_schema_errors(spec_root / "01_test")
        else:
            _write_valid_spec(spec_root / "01_test")

        args = ["-d", str(spec_root), subcommand, "01"]
        if subcommand == "render" and combined:
            args.append("--combined")
        if subcommand == "render" and use_json_flag:
            args.append("--json")

        env: dict[str, str] = {}
        if use_af_agent:
            env["AF_AGENT"] = "1"

        result = runner.invoke(
            main,
            args,
            env=env if env else None,
            catch_exceptions=True,
        )
        stdout_text = result.output.strip()
        if stdout_text:
            for line in stdout_text.split("\n"):
                line = line.strip()
                if line:
                    json.loads(line)  # Must not raise ParseError


# ===========================================================================
# TS-05-P4: No legacy inline patterns in spec/cli.py
# ===========================================================================


class TestNoLegacyPatterns:
    """TS-05-P4: spec/cli.py has zero legacy inline patterns.

    Property: 05-PROP-4
    Validates: 05-REQ-5.1, 05-REQ-5.2
    """

    @pytest.mark.xfail(reason="Legacy patterns not yet removed")
    def test_no_json_error_exit_in_source(self) -> None:
        """spec/cli.py does not contain _json_error_exit."""
        source = _get_cli_source()
        assert "_json_error_exit" not in source

    @pytest.mark.xfail(reason="Legacy patterns not yet removed")
    def test_no_click_echo_json_dumps_in_source(self) -> None:
        """spec/cli.py does not contain click.echo(json.dumps."""
        source = _get_cli_source()
        assert "click.echo(json.dumps" not in source


# ===========================================================================
# TS-05-P5: No spec.ui imports in entire spec package
# ===========================================================================


class TestNoSpecUiImportsAnywhere:
    """TS-05-P5: No file in spec/ has from spec.ui import or import spec.ui.

    Property: 05-PROP-5
    Validates: 05-REQ-4.1, 05-REQ-4.2, 05-REQ-4.3
    """

    @pytest.mark.xfail(reason="spec.ui imports not yet migrated")
    def test_no_spec_ui_imports_in_package(self) -> None:
        """No Python file in spec/ contains spec.ui import statements."""
        spec_dir = _get_spec_package_dir()
        for py_file in spec_dir.rglob("*.py"):
            source = py_file.read_text()
            assert "from spec.ui import" not in source, (
                f"{py_file} contains 'from spec.ui import'"
            )
            assert "import spec.ui" not in source, (
                f"{py_file} contains 'import spec.ui'"
            )
