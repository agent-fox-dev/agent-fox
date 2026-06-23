"""Spec 05: End-to-end smoke tests for agentic CLI migration.

Test Spec: TS-05-SMOKE-1, TS-05-SMOKE-2, TS-05-SMOKE-3, TS-05-SMOKE-4,
           TS-05-SMOKE-5
Requirements: 05-REQ-1.1, 05-REQ-1.5, 05-REQ-2.1, 05-REQ-2.3, 05-REQ-2.4,
              05-REQ-2.5, 05-REQ-3.4, 05-REQ-3.5
Execution Paths: 05-PATH-1, 05-PATH-2, 05-PATH-3, 05-PATH-4, 05-PATH-5
"""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any
from unittest.mock import patch

import pytest
from click.testing import CliRunner

# ---------------------------------------------------------------------------
# Valid spec fixture content (shared across smoke tests)
# ---------------------------------------------------------------------------

PRD_MD = """\
---
spec_id: "smoke-05"
spec_name: "smoke_fixture"
title: "Smoke Test Spec"
status: "draft"
created_at: "2024-01-01T00:00:00Z"
updated_at: "2024-01-01T00:00:00Z"
owner: "test"
source: "test"
schema_version: 1
---
# Smoke Test PRD

Smoke test PRD content.
"""

REQUIREMENTS_JSON = json.dumps(
    {
        "spec_id": "smoke-05",
        "spec_name": "smoke_fixture",
        "schema_version": 1,
        "introduction": "Smoke test requirements",
        "glossary": {},
        "requirements": [
            {
                "id": "SMOKE-REQ-1",
                "title": "Smoke test requirement",
                "user_story": {"role": "tester", "goal": "test", "benefit": "verify"},
                "acceptance_criteria": [
                    {
                        "id": "SMOKE-REQ-1.1",
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
        "spec_id": "smoke-05",
        "spec_name": "smoke_fixture",
        "schema_version": 1,
        "test_cases": [
            {
                "id": "TS-SMOKE-1",
                "description": "Smoke test case",
                "requirement_id": "SMOKE-REQ-1.1",
                "kind": "unit",
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
            "requirements_covered": ["SMOKE-REQ-1.1"],
            "properties_covered": [],
            "paths_covered": [],
            "gaps": [],
        },
    },
    indent=2,
)

TASKS_JSON = json.dumps(
    {
        "spec_id": "smoke-05",
        "spec_name": "smoke_fixture",
        "schema_version": 1,
        "test_commands": {"spec_tests": "", "all_tests": "", "linter": ""},
        "dependencies": [],
        "task_groups": [],
        "traceability": [],
    },
    indent=2,
)

# Requirements with an integrity error (REQ-2 has no test coverage)
REQUIREMENTS_JSON_INTEGRITY_ERROR = json.dumps(
    {
        "spec_id": "smoke-05",
        "spec_name": "smoke_fixture",
        "schema_version": 1,
        "introduction": "Smoke test requirements",
        "glossary": {},
        "requirements": [
            {
                "id": "SMOKE-REQ-1",
                "title": "Covered requirement",
                "user_story": {"role": "tester", "goal": "test", "benefit": "verify"},
                "acceptance_criteria": [
                    {
                        "id": "SMOKE-REQ-1.1",
                        "ears_pattern": "ubiquitous",
                        "system": "the system",
                        "action": "SHALL do something",
                    }
                ],
                "edge_cases": [],
            },
            {
                "id": "SMOKE-REQ-2",
                "title": "Uncovered requirement",
                "user_story": {"role": "tester", "goal": "coverage", "benefit": "verify"},
                "acceptance_criteria": [
                    {
                        "id": "SMOKE-REQ-2.1",
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
        "spec_id": "smoke-05",
        "spec_name": "smoke_fixture",
        "schema_version": 1,
        "test_cases": [
            {
                "id": "TS-SMOKE-1",
                "description": "Test for REQ-1 only",
                "requirement_id": "SMOKE-REQ-1.1",
                "kind": "unit",
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
            "requirements_covered": ["SMOKE-REQ-1.1"],
            "properties_covered": [],
            "paths_covered": [],
            "gaps": ["SMOKE-REQ-2.1"],
        },
    },
    indent=2,
)


# ---------------------------------------------------------------------------
# Fixture helpers
# ---------------------------------------------------------------------------


def _write_valid_spec(spec_dir: Path) -> None:
    """Populate a directory with valid spec artifacts."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON)
    (spec_dir / "tasks.json").write_text(TASKS_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps(
            {
                "state": "generated",
                "generated_artifacts": [
                    "requirements.json",
                    "test_spec.json",
                    "tasks.json",
                ],
            }
        )
    )


def _write_spec_with_integrity_errors(spec_dir: Path) -> None:
    """Populate a directory with a spec that has integrity errors."""
    spec_dir.mkdir(parents=True, exist_ok=True)
    (spec_dir / "prd.md").write_text(PRD_MD)
    (spec_dir / "requirements.json").write_text(REQUIREMENTS_JSON_INTEGRITY_ERROR)
    (spec_dir / "test_spec.json").write_text(TEST_SPEC_JSON_PARTIAL_COVERAGE)
    (spec_dir / "tasks.json").write_text(TASKS_JSON)
    (spec_dir / "_session.json").write_text(
        json.dumps(
            {
                "state": "generated",
                "generated_artifacts": [
                    "requirements.json",
                    "test_spec.json",
                    "tasks.json",
                ],
            }
        )
    )


def _invoke(
    runner: CliRunner,
    args: list[str],
    env: dict[str, str] | None = None,
    catch: bool = True,
) -> Any:
    """Invoke the spec CLI via CliRunner."""
    from spec.cli import main

    return runner.invoke(main, args, env=env, catch_exceptions=catch)


# ---------------------------------------------------------------------------
# Fixtures
# ---------------------------------------------------------------------------


@pytest.fixture
def runner() -> CliRunner:
    """Provide a Click CliRunner."""
    return CliRunner()


@pytest.fixture
def valid_spec_root(tmp_path: Path) -> Path:
    """A specs root with a valid spec for smoke tests."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_valid_spec(root / "01_smoke_spec")
    return root


@pytest.fixture
def error_spec_root(tmp_path: Path) -> Path:
    """A specs root with a spec containing integrity errors."""
    root = tmp_path / "specs"
    root.mkdir()
    _write_spec_with_integrity_errors(root / "01_error_spec")
    return root


# ===========================================================================
# TS-05-SMOKE-1: render --json --combined (combined mode)
# ===========================================================================


class TestSMOKERenderJsonCombined:
    """TS-05-SMOKE-1: End-to-end smoke test for spec render --json --combined.

    Execution Path: 05-PATH-1
    Requirements: 05-REQ-2.1, 05-REQ-2.2, 05-REQ-2.5
    Real components: spec/cli.py (_SpecGroup root), render command handler,
                     agentfox/io/ emit_ok function, agentspec rendering logic
    """

    def test_SMOKE_render_json_combined_envelope(
        self,
        runner: CliRunner,
        valid_spec_root: Path,
    ) -> None:
        """Agent calls spec render --json --combined and receives a valid
        render envelope with merged markdown content.

        Verifies:
        - stdout is a single valid JSON object
        - JSON contains ok=true, format='markdown', non-empty content string,
          and a sections array with at least one entry
        - Process exits with code 0
        - No banner or plain-text appears before or after the JSON object
        """
        result = _invoke(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--json", "--combined"],
        )
        assert result.exit_code == 0, f"Expected exit code 0, got {result.exit_code}"

        # stdout must be valid JSON
        parsed = json.loads(result.output)

        # Required fields
        assert parsed["ok"] is True
        assert parsed["format"] == "markdown"
        assert isinstance(parsed["content"], str)
        assert len(parsed["content"]) > 0, "content must be non-empty"
        assert isinstance(parsed["sections"], list)
        assert len(parsed["sections"]) > 0, "sections must have at least one entry"

        # No banner text should appear
        assert "/\\_/\\" not in result.output, "Fox ASCII art should not appear"
        assert "agent-fox v" not in result.output, "Version banner should not appear"


# ===========================================================================
# TS-05-SMOKE-2: render --json per-artifact mode
# ===========================================================================


class TestSMOKERenderJsonPerArtifact:
    """TS-05-SMOKE-2: End-to-end smoke test for spec render --json (per-artifact).

    Execution Path: 05-PATH-2
    Requirements: 05-REQ-2.3, 05-REQ-2.5
    Real components: spec/cli.py (_SpecGroup root), render command handler,
                     agentfox/io/ emit_ok function, agentspec rendering logic
    """

    def test_SMOKE_render_json_per_artifact_envelope(
        self,
        runner: CliRunner,
        valid_spec_root: Path,
    ) -> None:
        """Agent calls spec render --json (no --combined) and receives an
        artifacts map envelope.

        Verifies:
        - stdout is a single valid JSON object
        - JSON contains ok=true and an 'artifacts' map keyed by artifact name
        - No 'content' top-level key is present
        - Process exits with code 0
        """
        result = _invoke(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--json"],
        )
        assert result.exit_code == 0, f"Expected exit code 0, got {result.exit_code}"

        parsed = json.loads(result.output)
        assert parsed["ok"] is True
        assert "artifacts" in parsed
        assert "content" not in parsed
        assert isinstance(parsed["artifacts"], dict)
        assert len(parsed["artifacts"]) > 0


# ===========================================================================
# TS-05-SMOKE-3: validate with known errors
# ===========================================================================


class TestSMOKEValidateStructuredErrors:
    """TS-05-SMOKE-3: End-to-end smoke test for spec validate with errors.

    Execution Path: 05-PATH-3
    Requirements: 05-REQ-3.4, 05-REQ-3.5
    Real components: spec/cli.py (_SpecGroup root), validate command handler,
                     agentspec validation logic, agentfox/io/ emit function
    """

    def test_SMOKE_validate_structured_errors(
        self,
        runner: CliRunner,
        error_spec_root: Path,
    ) -> None:
        """CI pipeline calls spec validate on a spec with known errors and
        receives structured error objects.

        Verifies:
        - stdout is a valid JSON object with valid=false
        - errors array contains error objects with category fields
        - At least one integrity error is present (uncovered requirement)
        - Process exits with code 1
        """
        result = _invoke(
            runner,
            ["-d", str(error_spec_root), "validate", "01"],
        )
        assert result.exit_code == 1, f"Expected exit code 1, got {result.exit_code}"

        parsed = json.loads(result.output)
        assert parsed["valid"] is False
        assert isinstance(parsed["errors"], list)
        assert len(parsed["errors"]) > 0

        # Verify all errors have a valid category
        valid_categories = {"schema", "integrity", "io"}
        for err in parsed["errors"]:
            assert "category" in err, f"Error missing 'category' field: {err}"
            assert err["category"] in valid_categories, f"Invalid category '{err['category']}'"

        # Expect at least one integrity error (uncovered requirement)
        integrity_errs = [e for e in parsed["errors"] if e["category"] == "integrity"]
        assert len(integrity_errs) > 0, "Expected at least one integrity error for uncovered requirement"
        for ie in integrity_errs:
            assert "check" in ie
            assert "message" in ie


# ===========================================================================
# TS-05-SMOKE-4: render without --json (raw markdown)
# ===========================================================================


class TestSMOKERenderRawMarkdown:
    """TS-05-SMOKE-4: End-to-end smoke test for spec render (no --json).

    Execution Path: 05-PATH-4
    Requirements: 05-REQ-2.4
    Real components: spec/cli.py (_SpecGroup root), render command handler,
                     agentspec rendering logic
    """

    def test_SMOKE_render_raw_markdown_output(
        self,
        runner: CliRunner,
        valid_spec_root: Path,
    ) -> None:
        """Developer runs spec render without --json and receives raw markdown.

        Verifies:
        - stdout contains raw markdown text (not a JSON object)
        - Process exits with code 0
        - No JSON envelope wraps the output
        """
        result = _invoke(
            runner,
            ["-d", str(valid_spec_root), "render", "01", "--combined"],
        )
        assert result.exit_code == 0, f"Expected exit code 0, got {result.exit_code}"

        # Output should NOT be valid JSON
        with pytest.raises(json.JSONDecodeError):
            json.loads(result.output)

        # Output should contain some text (markdown)
        assert len(result.output.strip()) > 0, "Expected non-empty output"


# ===========================================================================
# TS-05-SMOKE-5: Unhandled exception -> JSON error envelope
# ===========================================================================


class TestSMOKEUnhandledExceptionErrorEnvelope:
    """TS-05-SMOKE-5: End-to-end smoke test for unhandled exception routing.

    Execution Path: 05-PATH-5
    Requirements: 05-REQ-1.5
    Real components: spec/cli.py (_SpecGroup root), agentfox/io/errors.py
                     error formatter
    """

    def test_SMOKE_unhandled_exception_json_error_envelope(
        self,
        runner: CliRunner,
        valid_spec_root: Path,
    ) -> None:
        """An unhandled RuntimeError in a spec subcommand is routed through
        AgentFoxGroup and emitted as a JSON error envelope.

        Patches SpecSession.resume to raise RuntimeError (simulating an
        unhandled crash). In agent mode (AF_AGENT=1), the error should be
        caught by AgentFoxGroup and emitted as a JSON envelope.

        Verifies:
        - stdout contains a JSON object with ok=false or an error field
        - No raw Python traceback is written to stdout
        - Process exits with a non-zero exit code
        - The JSON error envelope is produced by agentfox/io/errors.py
          via AgentFoxGroup
        """
        with patch(
            "agentspec.session.SpecSession.resume",
            side_effect=RuntimeError("smoke test crash"),
        ):
            result = _invoke(
                runner,
                ["-d", str(valid_spec_root), "status", "01"],
                env={"AF_AGENT": "1"},
            )

        assert result.exit_code != 0, "Expected non-zero exit code for unhandled exception"

        # stdout must be valid JSON
        parsed = json.loads(result.output)

        # The error envelope must indicate failure
        assert parsed.get("ok") is False, "Expected ok=false in error envelope"

        # No raw Python traceback should appear in stdout
        assert "Traceback" not in result.output, "Raw traceback should not appear in stdout"


# ===========================================================================
# Cross-spec entry point verification (subtask 8.5)
# ===========================================================================


class TestCrossSpecEntryPoints:
    """Verify that cross-spec entry points from spec 03 are called from
    production code.

    Subtask: 8.5
    Requirements: 05-REQ-1.1, 05-REQ-2.5, 05-REQ-3.5, 05-REQ-4.2
    """

    def test_root_cli_is_agentfoxgroup(self) -> None:
        """spec/cli.py root group is an instance of AgentFoxGroup."""
        from agentfox.io import AgentFoxGroup
        from spec.cli import cli

        assert isinstance(cli, AgentFoxGroup)

    def test_agentfox_io_module_available(self) -> None:
        """agentfox/io/ module is importable and has required symbols."""
        from agentfox.io import AgentFoxGroup, StatusSpinner, emit, emit_ok

        assert callable(emit)
        assert callable(emit_ok)
        assert AgentFoxGroup is not None
        assert StatusSpinner is not None

    def test_emit_ok_used_in_render_handler(self) -> None:
        """spec/cli.py render handler calls emit_ok (not json.dumps)."""
        import spec.cli

        source = Path(spec.cli.__file__).read_text()
        render_start = source.find("def render_cmd")
        assert render_start != -1
        next_cmd = source.find("\ndef ", render_start + 10)
        render_section = source[render_start:next_cmd] if next_cmd != -1 else source[render_start:]
        assert "emit_ok" in render_section, "render handler must use emit_ok"
        assert "json.dumps" not in render_section, "render handler must not use json.dumps"

    def test_emit_used_in_validate_handler(self) -> None:
        """spec/cli.py validate handler calls emit or emit_ok."""
        import spec.cli

        source = Path(spec.cli.__file__).read_text()
        validate_start = source.find("def validate_cmd")
        assert validate_start != -1
        next_cmd = source.find("\ndef ", validate_start + 10)
        validate_section = source[validate_start:next_cmd] if next_cmd != -1 else source[validate_start:]
        has_emit = "emit(" in validate_section or "emit_ok(" in validate_section
        assert has_emit, "validate handler must use emit or emit_ok"

    def test_statusspinner_from_agentfox_io(self) -> None:
        """spec/cli.py imports StatusSpinner from agentfox.io."""
        import spec.cli

        source = Path(spec.cli.__file__).read_text()
        assert "from agentfox.io" in source
        assert "StatusSpinner" in source


# ===========================================================================
# Stub and dead-code audit (subtask 8.4)
# ===========================================================================


class TestNoStubsOrDeadCode:
    """Audit for stub and dead-code markers across the spec package.

    Subtask: 8.4
    Requirements: 05-REQ-5.1, 05-REQ-5.2, 05-REQ-5.3
    """

    def test_no_not_implemented_error(self) -> None:
        """spec/ contains no raise NotImplementedError (migration stubs)."""
        import spec

        spec_dir = Path(spec.__file__).parent
        for py_file in spec_dir.rglob("*.py"):
            source = py_file.read_text()
            assert "raise NotImplementedError" not in source, f"{py_file} contains 'raise NotImplementedError'"

    def test_no_legacy_patterns(self) -> None:
        """spec/cli.py has no legacy inline patterns after migration."""
        import spec.cli

        source = Path(spec.cli.__file__).read_text()
        assert "_json_error_exit" not in source
        assert "_assessment_to_json" not in source
        assert "click.echo(json.dumps" not in source
