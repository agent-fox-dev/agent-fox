"""Spec 05: CLI wiring, render --json, and validate structured output tests.

Test Spec: TS-05-1 through TS-05-15, TS-05-E1 through TS-05-E5
Requirements: 05-REQ-1.1, 05-REQ-1.2, 05-REQ-1.3, 05-REQ-1.4, 05-REQ-1.5,
              05-REQ-1.E1, 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.3, 05-REQ-2.4,
              05-REQ-2.5, 05-REQ-2.E1, 05-REQ-2.E2, 05-REQ-3.1, 05-REQ-3.2,
              05-REQ-3.3, 05-REQ-3.4, 05-REQ-3.5, 05-REQ-3.E1, 05-REQ-3.E2
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from unittest.mock import patch

import pytest
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# Valid spec fixture content for render/validate tests
# ---------------------------------------------------------------------------

PRD_MD = """\
---
spec_id: "test-05"
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

REQUIREMENTS_JSON = json.dumps(
    {
        "spec_id": "test-05",
        "spec_name": "test_fixture",
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

TEST_SPEC_JSON = json.dumps(
    {
        "spec_id": "test-05",
        "spec_name": "test_fixture",
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
            },
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

TASKS_JSON = json.dumps(
    {
        "spec_id": "test-05",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": [],
        "traceability": [],
    },
    indent=2,
)

# Schema-invalid requirements (empty requirement ID triggers validation error)
REQUIREMENTS_JSON_SCHEMA_ERROR = json.dumps(
    {
        "spec_id": "test-05",
        "spec_name": "test_fixture",
        "schema_version": 1,
        "introduction": "Test requirements",
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

# Top-level schema error: missing required root-level field 'spec_id'.
# This produces a validation error with an empty/root path (not field-specific),
# which is needed for TS-05-E5 to verify that path/value keys are omitted.
REQUIREMENTS_JSON_TOP_LEVEL_ERROR = json.dumps(
    {
        # 'spec_id' is intentionally omitted — it's a required top-level field
        "spec_name": "test_fixture",
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

# Integrity error: requirement with no test case coverage
REQUIREMENTS_JSON_INTEGRITY_ERROR = json.dumps(
    {
        "spec_id": "test-05",
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

# Test spec that only covers REQ-1 (not REQ-2)
TEST_SPEC_JSON_PARTIAL_COVERAGE = json.dumps(
    {
        "spec_id": "test-05",
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


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


def _write_valid_spec(spec_dir: Path) -> None:
    """Populate a directory with valid spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON)
    (spec_dir / "tasks.json").write_text(TASKS_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"]})
    )


def _write_spec_no_tasks(spec_dir: Path) -> None:
    """Populate a directory with valid spec artifacts but no tasks."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json"]})
    )


def _write_spec_schema_error(spec_dir: Path) -> None:
    """Populate a directory with a spec containing a schema error."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_SCHEMA_ERROR)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON)
    (spec_dir / "tasks.json").write_text(TASKS_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"]})
    )


def _write_spec_top_level_error(spec_dir: Path) -> None:
    """Populate a directory with a spec containing a top-level schema error.

    Uses requirements JSON missing the required root-level 'spec_id' field,
    producing a validation error with an empty/root path (not field-specific).
    """
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_TOP_LEVEL_ERROR)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON)
    (spec_dir / "tasks.json").write_text(TASKS_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"]})
    )


def _write_spec_integrity_error(spec_dir: Path) -> None:
    """Populate a directory with a spec containing an integrity error."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_INTEGRITY_ERROR)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_PARTIAL_COVERAGE)
    (spec_dir / "tasks.json").write_text(TASKS_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json", "test_spec.json", "tasks.json"]})
    )


@pytest.fixture
def runner() -> CliRunner:
    """Provide a Click CliRunner."""
    return CliRunner()


@pytest.fixture
def valid_spec_root(tmp_path: Path) -> Path:
    """A specs root with a valid spec for render/validate tests."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_valid_spec(root / "01_test_spec")
    return root


@pytest.fixture
def no_tasks_spec_root(tmp_path: Path) -> Path:
    """A specs root with a spec missing the tasks artifact."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec_no_tasks(root / "01_no_tasks_spec")
    return root


@pytest.fixture
def schema_error_spec_root(tmp_path: Path) -> Path:
    """A specs root with a spec containing a schema error."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec_schema_error(root / "01_schema_error")
    return root


@pytest.fixture
def top_level_error_spec_root(tmp_path: Path) -> Path:
    """A specs root with a spec containing a top-level schema error (missing spec_id)."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec_top_level_error(root / "01_top_level_error")
    return root


@pytest.fixture
def integrity_error_spec_root(tmp_path: Path) -> Path:
    """A specs root with a spec containing an integrity error."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec_integrity_error(root / "01_integrity_error")
    return root


@pytest.fixture
def missing_artifact_spec_root(tmp_path: Path) -> Path:
    """A specs root with a spec missing an artifact file entirely."""
    root = tmp_path / "specs"
    root.mkdir()
    spec_dir = root / "01_missing"
    spec_dir.mkdir()
    (spec_dir / "prd.md").write_text(PRD_MD)
    # Missing requirements.json, test_spec.json, tasks.json
    (spec_dir / "_session.json").write_text(
        json.dumps({"state": "generated", "generated_artifacts": ["requirements.json"]})
    )
    return root


def _get_cli_source() -> str:
    """Read the source text of spec/cli.py."""
    import spec.cli

    return Path(spec.cli.__file__).read_text()


def _invoke_spec(
    runner: CliRunner,
    args: list[str],
    env: dict[str, str] | None = None,
) -> Any:
    """Invoke the spec CLI via CliRunner."""
    from spec.cli import main

    return runner.invoke(main, args, env=env, catch_exceptions=False)


def _invoke_spec_catching(
    runner: CliRunner,
    args: list[str],
    env: dict[str, str] | None = None,
) -> Any:
    """Invoke the spec CLI via CliRunner, catching exceptions."""
    from spec.cli import main

    return runner.invoke(main, args, env=env, catch_exceptions=True)


# ===========================================================================
# TS-05-1: Root group uses AgentFoxGroup
# ===========================================================================


class TestRootGroupAgentFoxGroup:
    """TS-05-1: Verify spec/cli.py root group uses cls=AgentFoxGroup.

    Requirement: 05-REQ-1.1
    """

    @pytest.mark.xfail(reason="AgentFoxGroup migration not yet implemented")
    def test_root_group_is_agentfoxgroup(self) -> None:
        """The root CLI group object is an instance of AgentFoxGroup."""
        from agentfox.io import AgentFoxGroup
        from spec.cli import cli

        assert isinstance(cli, AgentFoxGroup)


# ===========================================================================
# TS-05-2: No manual banner rendering or config loading
# ===========================================================================


class TestNoBannerOrConfigLoad:
    """TS-05-2: Verify absence of manual banner/config-load patterns.

    Requirement: 05-REQ-1.2
    """

    @pytest.mark.xfail(reason="Banner/config migration not yet implemented")
    def test_no_render_banner_call(self) -> None:
        """spec/cli.py does not call render_banner()."""
        source = _get_cli_source()
        assert "render_banner" not in source

    @pytest.mark.xfail(reason="Banner/config migration not yet implemented")
    def test_no_manual_load_config(self) -> None:
        """spec/cli.py does not manually call load_config()."""
        source = _get_cli_source()
        assert "load_config" not in source


# ===========================================================================
# TS-05-3: _json_error_exit and _error_type removed
# ===========================================================================


class TestLegacyHelpersRemoved:
    """TS-05-3: Verify _json_error_exit and _error_type are absent.

    Requirement: 05-REQ-1.3
    """

    @pytest.mark.xfail(reason="Legacy helpers not yet removed")
    def test_json_error_exit_absent(self) -> None:
        """_json_error_exit is not in spec/cli.py source."""
        source = _get_cli_source()
        assert "_json_error_exit" not in source

    @pytest.mark.xfail(reason="Legacy helpers not yet removed")
    def test_error_type_absent(self) -> None:
        """_error_type is not in spec/cli.py source."""
        source = _get_cli_source()
        assert "_error_type" not in source


# ===========================================================================
# TS-05-E1: Legacy + AgentFoxGroup coexistence prevented
# ===========================================================================


class TestLegacyAgentFoxGroupCoexistence:
    """TS-05-E1: _json_error_exit absent AND AgentFoxGroup present in source.

    Requirement: 05-REQ-1.E1
    """

    @pytest.mark.xfail(reason="AgentFoxGroup migration not yet implemented")
    def test_no_legacy_with_agentfoxgroup(self) -> None:
        """_json_error_exit is absent and AgentFoxGroup is present."""
        source = _get_cli_source()
        assert "_json_error_exit" not in source
        assert "AgentFoxGroup" in source


# ===========================================================================
# TS-05-4: AF_AGENT=1 suppresses banner
# ===========================================================================


class TestAgentModeBannerSuppression:
    """TS-05-4: AF_AGENT=1 suppresses banner output.

    Requirement: 05-REQ-1.4
    """

    @pytest.mark.xfail(reason="Agent mode banner suppression not yet implemented")
    def test_no_banner_in_agent_mode(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """No banner/fox-art text in stdout when AF_AGENT=1."""
        # Use a real subcommand (not --help which bypasses group callback)
        result = _invoke_spec_catching(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--combined"],
            env={"AF_AGENT": "1"},
        )
        output = result.output or ""
        # The fox ASCII art banner should be suppressed in agent mode
        assert "/\\_/\\" not in output, "Fox ASCII art banner should be suppressed in agent mode"
        # No agent-fox version line should appear
        assert "agent-fox v" not in output, "Version banner should be suppressed in agent mode"


# ===========================================================================
# TS-05-5: Unhandled exception -> JSON error envelope
# ===========================================================================


class TestUnhandledExceptionRouting:
    """TS-05-5: Unhandled exceptions produce JSON error envelope.

    Requirement: 05-REQ-1.5
    """

    @pytest.mark.xfail(reason="AgentFoxGroup error routing not yet implemented")
    def test_unhandled_exception_json_envelope(self, runner: CliRunner) -> None:
        """Unhandled RuntimeError produces JSON error with ok=false."""
        with patch(
            "agentspec.session.SpecSession.resume",
            side_effect=RuntimeError("test crash"),
        ):
            result = _invoke_spec_catching(
                runner,
                ["-d", "/tmp/nonexistent", "render", "01"],
                env={"AF_AGENT": "1"},
            )
        assert result.exit_code != 0
        parsed = json.loads(result.output)
        assert parsed["ok"] is False


# ===========================================================================
# TS-05-6: render --json --combined envelope
# ===========================================================================


class TestRenderJsonCombined:
    """TS-05-6/7: render --json --combined returns combined envelope.

    Requirements: 05-REQ-2.1, 05-REQ-2.2
    """

    @pytest.mark.xfail(reason="render --json not yet implemented")
    def test_combined_json_envelope_fields(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """JSON envelope has ok, format, content, sections."""
        result = _invoke_spec(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--json", "--combined"],
        )
        assert result.exit_code == 0
        parsed = json.loads(result.output)
        assert parsed["ok"] is True
        assert parsed["format"] == "markdown"
        assert "content" in parsed
        assert isinstance(parsed["content"], str)
        assert "sections" in parsed
        assert isinstance(parsed["sections"], list)

    @pytest.mark.xfail(reason="render --json not yet implemented")
    def test_combined_content_is_merged_string(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """Combined mode returns single content string, not artifacts map."""
        result = _invoke_spec(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--json", "--combined"],
        )
        parsed = json.loads(result.output)
        assert "content" in parsed
        assert "artifacts" not in parsed
        assert isinstance(parsed["content"], str)

    @pytest.mark.xfail(reason="render --json not yet implemented")
    def test_combined_sections_list(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """Combined mode includes sections array reflecting included artifacts."""
        result = _invoke_spec(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--json", "--combined"],
        )
        parsed = json.loads(result.output)
        sections = parsed["sections"]
        assert "requirements" in sections
        assert "test_spec" in sections
        assert "tasks" in sections


# ===========================================================================
# TS-05-8: render --json per-artifact mode
# ===========================================================================


class TestRenderJsonPerArtifact:
    """TS-05-8: render --json without --combined returns per-artifact map.

    Requirement: 05-REQ-2.3
    """

    @pytest.mark.xfail(reason="render --json not yet implemented")
    def test_per_artifact_envelope(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """JSON envelope has ok=true and artifacts map, no content key."""
        result = _invoke_spec(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--json"],
        )
        assert result.exit_code == 0
        parsed = json.loads(result.output)
        assert parsed["ok"] is True
        assert "artifacts" in parsed
        assert "content" not in parsed
        assert "requirements" in parsed["artifacts"]


# ===========================================================================
# TS-05-9: render without --json outputs raw markdown
# ===========================================================================


class TestRenderNoJson:
    """TS-05-9: render without --json outputs raw markdown.

    Requirement: 05-REQ-2.4
    """

    def test_raw_markdown_output(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """Without --json, stdout is raw markdown, not JSON."""
        result = _invoke_spec_catching(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--combined"],
        )
        assert result.exit_code == 0
        # Output should not be valid JSON
        with pytest.raises(json.JSONDecodeError):
            json.loads(result.output)


# ===========================================================================
# TS-05-10: render uses emit_ok from agentfox.io
# ===========================================================================


class TestRenderUsesEmitOk:
    """TS-05-10: render handler uses emit_ok, not click.echo(json.dumps).

    Requirement: 05-REQ-2.5
    """

    @pytest.mark.xfail(reason="emit_ok migration not yet implemented")
    def test_emit_ok_in_source(self) -> None:
        """spec/cli.py source uses emit_ok for render JSON output."""
        source = _get_cli_source()
        assert "emit_ok" in source

    def test_no_json_dumps_in_render(self) -> None:
        """Render handler does not use click.echo(json.dumps(...))."""
        source = _get_cli_source()
        # Find the render command section
        render_start = source.find("def render_cmd")
        if render_start == -1:
            render_start = source.find("def render")
        assert render_start != -1, "render command not found in source"
        # Find next command definition or end of file
        next_cmd = source.find("\ndef ", render_start + 10)
        render_section = source[render_start:next_cmd] if next_cmd != -1 else source[render_start:]
        assert "json.dumps" not in render_section


# ===========================================================================
# TS-05-E2: render --json with non-existent spec
# ===========================================================================


class TestRenderJsonNonExistentSpec:
    """TS-05-E2: render --json emits error envelope for missing spec.

    Requirement: 05-REQ-2.E1
    """

    @pytest.mark.xfail(reason="render --json error handling not yet implemented")
    def test_nonexistent_spec_error_envelope(
        self, runner: CliRunner, tmp_path: Path
    ) -> None:
        """Non-existent spec produces ok=false JSON envelope."""
        empty_spec_dir = tmp_path / "empty_specs"
        empty_spec_dir.mkdir()
        result = _invoke_spec_catching(
            runner,
            ["-d", str(empty_spec_dir), "render", "nonexistent", "--json"],
        )
        parsed = json.loads(result.output)
        assert parsed["ok"] is False
        assert "error" in parsed
        assert result.exit_code != 0


# ===========================================================================
# TS-05-E3: render --json omits missing artifacts with warnings
# ===========================================================================


class TestRenderJsonMissingArtifactWarnings:
    """TS-05-E3: render --json omits missing artifacts, includes warnings.

    Requirement: 05-REQ-2.E2
    """

    @pytest.mark.xfail(reason="render --json missing artifact warnings not yet implemented")
    def test_missing_tasks_artifact_warning(
        self, runner: CliRunner, no_tasks_spec_root: Path
    ) -> None:
        """Missing tasks artifact is omitted with a warning."""
        result = _invoke_spec(
            runner,
            ["-d", str(no_tasks_spec_root), "render", "01", "--json"],
        )
        parsed = json.loads(result.output)
        assert parsed["ok"] is True
        assert "tasks" not in parsed.get("artifacts", {})
        assert "warnings" in parsed
        assert any("tasks" in w for w in parsed["warnings"])


# ===========================================================================
# TS-05-11: validate structured schema errors
# ===========================================================================


class TestValidateStructuredSchemaErrors:
    """TS-05-11: validate emits structured errors with category='schema'.

    Requirement: 05-REQ-3.1
    """

    @pytest.mark.xfail(reason="Structured validation errors not yet implemented")
    def test_schema_error_structure(
        self, runner: CliRunner, schema_error_spec_root: Path
    ) -> None:
        """Schema errors have category, artifact, path, message, value fields."""
        result = _invoke_spec_catching(
            runner,
            ["-d", str(schema_error_spec_root), "validate", "01"],
        )
        parsed = json.loads(result.output)
        assert parsed["valid"] is False
        schema_errs = [e for e in parsed["errors"] if e["category"] == "schema"]
        assert len(schema_errs) > 0
        err = schema_errs[0]
        assert "artifact" in err
        assert "path" in err
        assert "message" in err
        assert "value" in err


# ===========================================================================
# TS-05-12: validate structured integrity errors
# ===========================================================================


class TestValidateStructuredIntegrityErrors:
    """TS-05-12: validate emits structured errors with category='integrity'.

    Requirement: 05-REQ-3.2
    """

    @pytest.mark.xfail(reason="Structured integrity errors not yet implemented")
    def test_integrity_error_structure(
        self, runner: CliRunner, integrity_error_spec_root: Path
    ) -> None:
        """Integrity errors have category, check, message, requirement_id."""
        result = _invoke_spec_catching(
            runner,
            ["-d", str(integrity_error_spec_root), "validate", "01"],
        )
        parsed = json.loads(result.output)
        integrity_errs = [e for e in parsed["errors"] if e["category"] == "integrity"]
        assert len(integrity_errs) > 0
        err = integrity_errs[0]
        assert "check" in err
        assert "message" in err
        assert "requirement_id" in err


# ===========================================================================
# TS-05-13: validate returns valid=true with empty errors on success
# ===========================================================================


class TestValidateSuccess:
    """TS-05-13: validate returns {valid: true, errors: []} on success.

    Requirement: 05-REQ-3.3
    """

    @pytest.mark.xfail(reason="Structured validation output not yet implemented")
    def test_valid_spec_result(
        self, runner: CliRunner, valid_spec_root: Path
    ) -> None:
        """Valid spec returns valid=true, errors=[] and exit code 0."""
        result = _invoke_spec(
            runner,
            ["-d", str(valid_spec_root), "validate", "01"],
        )
        parsed = json.loads(result.output)
        assert parsed["valid"] is True
        assert parsed["errors"] == []
        assert result.exit_code == 0


# ===========================================================================
# TS-05-14: validate returns valid=false with non-empty errors
# ===========================================================================


class TestValidateFailure:
    """TS-05-14: validate returns valid=false with errors and exit code 1.

    Requirement: 05-REQ-3.4
    """

    @pytest.mark.xfail(reason="Structured validation output not yet implemented")
    def test_invalid_spec_result(
        self, runner: CliRunner, schema_error_spec_root: Path
    ) -> None:
        """Invalid spec returns valid=false, non-empty errors, exit code 1."""
        result = _invoke_spec_catching(
            runner,
            ["-d", str(schema_error_spec_root), "validate", "01"],
        )
        parsed = json.loads(result.output)
        assert parsed["valid"] is False
        assert len(parsed["errors"]) > 0
        assert result.exit_code == 1


# ===========================================================================
# TS-05-15: validate uses emit/emit_ok from agentfox.io
# ===========================================================================


class TestValidateUsesEmit:
    """TS-05-15: validate handler uses emit/emit_ok, not click.echo(json.dumps).

    Requirement: 05-REQ-3.5
    """

    @pytest.mark.xfail(reason="emit/emit_ok migration not yet implemented")
    def test_emit_in_validate_source(self) -> None:
        """Validate handler uses emit or emit_ok from agentfox.io."""
        source = _get_cli_source()
        validate_start = source.find("def validate_cmd")
        if validate_start == -1:
            validate_start = source.find("def validate")
        assert validate_start != -1, "validate command not found in source"
        next_cmd = source.find("\ndef ", validate_start + 10)
        validate_section = source[validate_start:next_cmd] if next_cmd != -1 else source[validate_start:]
        assert "emit_ok" in validate_section or "emit" in validate_section
        assert "click.echo(json.dumps" not in validate_section


# ===========================================================================
# TS-05-E4: validate IO error for missing artifact file
# ===========================================================================


class TestValidateIOError:
    """TS-05-E4: validate emits category='io' error for missing file.

    Requirement: 05-REQ-3.E1
    """

    @pytest.mark.xfail(reason="Structured IO error not yet implemented")
    def test_missing_artifact_io_error(
        self, runner: CliRunner, missing_artifact_spec_root: Path
    ) -> None:
        """Missing artifact file produces category='io' error, exit code 1."""
        result = _invoke_spec_catching(
            runner,
            ["-d", str(missing_artifact_spec_root), "validate", "01"],
        )
        parsed = json.loads(result.output)
        assert parsed["valid"] is False
        io_errs = [e for e in parsed["errors"] if e["category"] == "io"]
        assert len(io_errs) > 0
        err = io_errs[0]
        assert "artifact" in err
        assert "message" in err
        assert result.exit_code == 1


# ===========================================================================
# TS-05-E5: top-level errors omit path and value
# ===========================================================================


class TestValidateTopLevelErrorOmitsPathValue:
    """TS-05-E5: Top-level errors omit path and value (not null, absent).

    Requirement: 05-REQ-3.E2
    """

    @pytest.mark.xfail(reason="Structured error field omission not yet implemented")
    def test_top_level_error_no_path_no_value(
        self, runner: CliRunner, top_level_error_spec_root: Path
    ) -> None:
        """Top-level error object has no path or value keys.

        Uses a fixture with a missing required root-level field (spec_id),
        which produces a genuinely top-level schema error (empty path).
        The implementation must omit 'path' and 'value' keys entirely
        rather than including them as null.
        """
        result = _invoke_spec_catching(
            runner,
            ["-d", str(top_level_error_spec_root), "validate", "01"],
        )
        parsed = json.loads(result.output)
        assert parsed["valid"] is False, "Expected validation to fail for missing spec_id"

        schema_errs = [e for e in parsed["errors"] if e.get("category") == "schema"]
        assert len(schema_errs) > 0, "Expected at least one schema error"

        # At least one error must be top-level (no 'path' key at all)
        top_level_errs = [e for e in schema_errs if "path" not in e]
        assert len(top_level_errs) > 0, (
            "Expected at least one top-level error without 'path' key, "
            f"but all schema errors have paths: {schema_errs}"
        )
        # Verify top-level errors omit both 'path' and 'value' (not null, absent)
        for err in top_level_errs:
            assert "path" not in err, f"Top-level error should not have 'path' key: {err}"
            assert "value" not in err, f"Top-level error should not have 'value' key: {err}"
