"""Backing module for spec linting.

Provides a function to validate specification files that can be called
from code without the CLI framework.

Requirements: 59-REQ-3.1, 59-REQ-3.2, 59-REQ-3.3, 59-REQ-3.E1,
              135-REQ-1.1, 135-REQ-1.2, 135-REQ-1.3, 135-REQ-1.E1,
              135-REQ-2.1, 135-REQ-2.2, 135-REQ-2.E1
"""

from __future__ import annotations

import logging
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path

import afspec

from agent_fox.core.errors import PlanError
from agent_fox.core.models import resolve_model
from agent_fox.spec.discovery import SpecFormat, SpecInfo, discover_specs
from agent_fox.spec.types import Finding, compute_exit_code, sort_findings
from agent_fox.spec.validators import validate_specs

logger = logging.getLogger(__name__)


@dataclass(frozen=True)
class LintResult:
    """Result of a spec lint run.

    Attributes:
        findings: List of validation findings.
        exit_code: 0 for clean, 1 for error-severity findings.
    """

    findings: list[Finding] = field(default_factory=list)
    exit_code: int = 0


_KNOWN_SEVERITIES = {"error", "warning", "hint"}


def _map_afspec_findings(
    spec_name: str,
    errors: list[afspec.ValidationError],
) -> list[Finding]:
    """Map afspec ValidationError instances to Finding instances.

    Since afspec.ValidationError has no ``severity`` or ``line`` fields,
    severity defaults to ``"error"`` and line defaults to ``None``.

    Requirements: 135-REQ-2.1, 135-REQ-2.2
    See errata: 135_validation_error_fields.md
    """
    findings: list[Finding] = []
    for ve in errors:
        # 135-REQ-2.2: default to "error" for unknown/missing severity.
        # afspec.ValidationError has no severity field, so we always
        # default. If a severity attribute is ever added, map known values
        # and default unknown ones to "error".
        raw_severity = getattr(ve, "severity", None)
        severity = raw_severity if raw_severity in _KNOWN_SEVERITIES else "error"

        findings.append(
            Finding(
                spec_name=spec_name,
                file=ve.file,
                rule=ve.rule,
                severity=severity,
                message=ve.message,
                line=getattr(ve, "line", None),
            )
        )
    return findings


def _validate_v12_spec(spec: SpecInfo) -> list[Finding]:
    """Validate a v1.2 spec using afspec.validate().

    Loads the spec via ``afspec.load_spec()`` then runs
    ``afspec.validate()`` on the loaded object. Maps the resulting
    ``ValidationError`` instances to ``Finding`` instances.

    If any exception occurs (load or validation), emits a single
    error-severity Finding with rule ``afspec-error``.

    Requirements: 135-REQ-1.1, 135-REQ-1.E1
    """
    try:
        loaded = afspec.load_spec(spec.path)
        errors = afspec.validate(loaded)
        return _map_afspec_findings(spec.name, errors)
    except Exception as exc:
        return [
            Finding(
                spec_name=spec.name,
                file=str(spec.path),
                rule="afspec-error",
                severity="error",
                message=str(exc),
                line=None,
            )
        ]


def _is_spec_implemented(spec: SpecInfo) -> bool:
    """Check whether a spec is fully implemented based on its tasks.

    For v1.2 specs, checks tasks.json via afspec.load_spec().
    For v1 specs, checks tasks.md via the markdown parser.

    Requirements: 135-REQ-3.2
    """
    # 135-REQ-3.2: v1.2 specs use tasks.json
    if spec.format == SpecFormat.V1_2_JSON:
        try:
            loaded = afspec.load_spec(spec.path)
            if not loaded.tasks or not loaded.tasks.task_groups:
                return False
            return all(
                all(
                    st.state == afspec.SubtaskState.DONE
                    for st in g.subtasks
                )
                for g in loaded.tasks.task_groups
            )
        except Exception:
            return False

    # v1 markdown specs use tasks.md
    tasks_path = spec.path / "tasks.md"
    if not tasks_path.is_file():
        return False

    from agent_fox.spec.parser import parse_tasks

    try:
        groups = parse_tasks(tasks_path)
    except Exception:
        return False

    if not groups:
        return False

    return all(g.completed for g in groups)


def run_lint_specs(
    specs_dir: Path,
    *,
    ai: bool = False,
    lint_all: bool = False,
    progress_callback: Callable[[str], None] | None = None,
) -> LintResult:
    """Run spec linting and return structured results.

    Args:
        specs_dir: Path to the specifications directory.
        ai: Enable AI-powered semantic analysis.
        lint_all: Include fully-implemented specs.
        progress_callback: Optional callable receiving phase-level status
            messages. Called at each major phase: discovery, validation,
            and (when ``ai=True``) AI analysis.

    Returns:
        LintResult with findings and exit code.

    Raises:
        PlanError: If specs_dir does not exist.

    Requirements: 59-REQ-3.1, 59-REQ-3.2, 59-REQ-3.3, 59-REQ-3.E1,
                  127-REQ-4.2, 127-REQ-4.3, 127-REQ-4.E1
    """
    if not specs_dir.exists():
        raise PlanError(f"Specs directory not found: {specs_dir}")

    # Discover specs
    if progress_callback is not None:
        progress_callback("Discovering specs...")
    try:
        discovered: list[SpecInfo] = discover_specs(specs_dir)
    except PlanError:
        # No specs found — return error finding
        no_spec_finding = Finding(
            spec_name="(none)",
            file=str(specs_dir),
            rule="no-specs",
            severity="error",
            message=f"No specifications found in {specs_dir} directory",
            line=None,
        )
        return LintResult(findings=[no_spec_finding], exit_code=1)

    # Filter out fully-implemented specs unless lint_all is set
    if not lint_all:
        filtered = [s for s in discovered if not _is_spec_implemented(s)]
        skipped = len(discovered) - len(filtered)
        if skipped > 0:
            logger.info(
                "Skipping %d fully-implemented spec(s) (use --all to include)",
                skipped,
            )
        if not filtered:
            return LintResult(findings=[], exit_code=0)
        discovered = filtered

    # 135-REQ-1.1, 135-REQ-1.2, 135-REQ-1.3: Partition by format and
    # route to the appropriate validator.
    if progress_callback is not None:
        progress_callback(f"Validating {len(discovered)} spec(s)...")

    v1_specs = [s for s in discovered if s.format == SpecFormat.V1_MARKDOWN]
    v12_specs = [s for s in discovered if s.format == SpecFormat.V1_2_JSON]

    findings: list[Finding] = []

    # Validate v1 markdown specs with custom validators
    if v1_specs:
        findings.extend(validate_specs(specs_dir, v1_specs))

    # Validate v1.2 JSON specs with afspec
    for spec in v12_specs:
        findings.extend(_validate_v12_spec(spec))

    findings = sort_findings(findings)

    # Optionally run AI validation
    if ai:
        if progress_callback is not None:
            progress_callback("Running AI analysis...")
        findings = _merge_ai_findings(findings, discovered, specs_dir)

    exit_code = compute_exit_code(findings)
    return LintResult(
        findings=findings,
        exit_code=exit_code,
    )


def _merge_ai_findings(
    findings: list[Finding],
    discovered: list[SpecInfo],
    specs_dir: Path,
) -> list[Finding]:
    """Run AI validation and merge results into existing findings."""
    import asyncio

    try:
        from agent_fox.spec.ai_validation import run_ai_validation

        standard_model = resolve_model("STANDARD").model_id
        ai_findings = asyncio.run(run_ai_validation(discovered, standard_model, specs_dir=specs_dir))
        return sort_findings(findings + ai_findings)
    except Exception as exc:
        logger.warning("AI validation failed: %s", exc)
        return findings
