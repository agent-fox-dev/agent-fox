"""CLI entry point for the spec tool.

All commands produce JSON on stdout (except ``render`` which outputs
markdown).  Progress and errors go to stderr.  This makes the CLI
easy to drive from agent skills and scripts.
"""

from __future__ import annotations

import asyncio
import json
import re
import sys
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

import click
import jsonschema
import yaml
from agentfox.io import AgentFoxGroup, StatusSpinner, emit, emit_ok
from agentspec.errors import AgentError
from agentspec.session import SessionState, SpecSession

_SPEC_DIR_RE = re.compile(r"^(\d{2})_(.+)$")
_SPEC_NAME_RE = re.compile(r"^[a-z][a-z0-9_]*$")
_DEFAULT_SPEC_DIR = ".agent-fox/specs"


class _SpecGroup(AgentFoxGroup):
    """Extends AgentFoxGroup to suppress the banner for JSON-producing commands.

    Subcommands like ``render --json`` require stdout to be pure JSON.
    ``AgentFoxGroup`` already suppresses the banner in agent mode
    (``AF_AGENT=1``) and when ``--quiet`` is passed.  This subclass
    additionally sets *quiet* when the remaining (subcommand) args
    contain ``--json`` or when the subcommand always produces JSON
    output (``validate``, ``status``), so the banner never
    contaminates JSON output.
    """

    # Subcommands whose output is always JSON, even without ``--json``.
    _JSON_SUBCOMMANDS = frozenset({"validate", "status"})

    def invoke(self, ctx: click.Context) -> None:
        # Peek at unconsumed args.  ``_protected_args`` holds the
        # subcommand name; ``args`` holds the remaining tokens
        # (subcommand arguments).  Both must be checked for ``--json``.
        protected: list[str] = getattr(ctx, "_protected_args", [])
        remaining: list[str] = getattr(ctx, "args", [])
        subcommand = protected[0] if protected else None
        if "--json" in protected + remaining or subcommand in self._JSON_SUBCOMMANDS:
            ctx.params["quiet"] = True
        super().invoke(ctx)


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _resolve_spec(spec_dir: Path, spec_arg: str) -> Path:
    """Resolve a spec argument to a spec directory path.

    Matches by full directory name first, then by zero-padded prefix.
    """
    candidates: list[tuple[int, Path]] = []
    if not spec_dir.exists():
        raise click.ClickException(f"Spec directory does not exist: {spec_dir}")

    for entry in spec_dir.iterdir():
        if not entry.is_dir():
            continue
        match = _SPEC_DIR_RE.match(entry.name)
        if match:
            candidates.append((int(match.group(1)), entry))

    candidates.sort(key=lambda x: x[0])

    for _, path in candidates:
        if path.name == spec_arg:
            return path

    padded = spec_arg.zfill(2)
    for _, path in candidates:
        match = _SPEC_DIR_RE.match(path.name)
        if match and match.group(1) == padded:
            return path

    if candidates:
        available = "\n".join(f"  {p.name}" for _, p in candidates)
        raise click.ClickException(f"Spec '{spec_arg}' not found. Available:\n{available}")
    raise click.ClickException(f"Spec '{spec_arg}' not found. No specs in {spec_dir}")


def _next_prefix(spec_dir: Path) -> int:
    """Compute the next numeric prefix for a new spec."""
    max_prefix = 0
    if spec_dir.exists():
        for entry in spec_dir.iterdir():
            if not entry.is_dir():
                continue
            match = _SPEC_DIR_RE.match(entry.name)
            if match:
                max_prefix = max(max_prefix, int(match.group(1)))
    return max_prefix + 1


def _derive_spec_name(filename: str) -> str:
    """Derive a snake_case spec name from a PRD filename."""
    stem = Path(filename).stem
    return re.sub(r"[^a-z0-9]+", "_", stem.lower()).strip("_")


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------


@click.group(cls=_SpecGroup, invoke_without_command=True)
@click.option(
    "--spec-dir",
    "-d",
    type=click.Path(),
    default=_DEFAULT_SPEC_DIR,
    envvar="SPEC_DIR",
    help=f"Spec directory (default: {_DEFAULT_SPEC_DIR})",
)
@click.option("--quiet", "-q", is_flag=True, default=False, help="Suppress progress output")
@click.version_option(package_name="spec")
@click.pass_context
def main(ctx: click.Context, spec_dir: str, quiet: bool) -> None:
    """spec: AI-powered spec creation tool."""
    ctx.ensure_object(dict)
    ctx.obj["spec_dir"] = Path(spec_dir)
    ctx.obj["quiet"] = quiet
    # Propagate agent_mode if set by AgentFoxGroup
    ctx.obj.setdefault("agent_mode", False)

    if ctx.invoked_subcommand is None:
        click.echo(ctx.get_help())


# ---------------------------------------------------------------------------
# new
# ---------------------------------------------------------------------------


@main.command("new")
@click.argument("prd_file", type=click.Path(exists=True))
@click.option("--name", default=None, help="Spec name (default: derived from filename)")
@click.pass_context
def new_cmd(ctx: click.Context, prd_file: str, name: str | None) -> None:
    """Create a new spec from a PRD file."""
    spec_dir: Path = ctx.obj["spec_dir"]
    prd_path = Path(prd_file)
    prd_content = prd_path.read_text()

    if name is None:
        name = _derive_spec_name(prd_path.name)

    if not _SPEC_NAME_RE.match(name):
        raise click.ClickException(
            f"Invalid spec name {name!r}: must match [a-z][a-z0-9_]* "
            "(start with lowercase letter, only lowercase letters, digits, underscores)"
        )

    spec_dir.mkdir(parents=True, exist_ok=True)

    prefix = _next_prefix(spec_dir)
    spec_id = f"{prefix:02d}"
    dir_name = f"{spec_id}_{name}"
    target = spec_dir / dir_name
    target.mkdir()

    now = datetime.now(UTC).isoformat()
    frontmatter = {
        "spec_id": spec_id,
        "spec_name": name,
        "title": name.replace("_", " ").title(),
        "status": "draft",
        "created_at": now,
        "updated_at": now,
        "owner": "",
        "source": "interactive",
        "schema_version": 1,
    }
    frontmatter_yaml = yaml.dump(frontmatter, default_flow_style=False, sort_keys=False)
    (target / "prd.md").write_text(f"---\n{frontmatter_yaml}---\n{prd_content}\n")

    SpecSession._create(target)

    emit_ok(spec_dir=dir_name, state="init")


# ---------------------------------------------------------------------------
# refine
# ---------------------------------------------------------------------------


def _serialize_assessment(assessment: Any) -> dict[str, Any]:
    """Serialise an Assessment to a JSON-friendly dict.

    Converts assessment attributes and nested question objects into
    plain Python dicts suitable for ``emit()`` / ``emit_ok()``.
    """
    questions = [
        {
            "id": q.id,
            "text": q.text,
            "context": q.context,
            "options": q.options,
            "required": q.required,
        }
        for q in getattr(assessment, "questions", [])
    ]
    return {
        "quality": assessment.quality,
        "summary": assessment.summary,
        "gaps": list(assessment.gaps),
        "questions": questions,
    }


@main.command("refine")
@click.argument("spec")
@click.option(
    "--answers",
    required=False,
    default=None,
    help="JSON file with answers, or '-' to read from stdin.",
)
@click.option("--force", is_flag=True, help="Discard previous assessments and start a fresh refine cycle")
@click.pass_context
def refine_cmd(ctx: click.Context, spec: str, answers: str | None, force: bool) -> None:
    """Assess PRD, submit answers, and refine.

    Without --answers: runs the initial assessment (if needed) and
    outputs the pending questions as JSON.

    With --answers: submits answers, updates the PRD, and outputs
    the new assessment as JSON.

    Loop until quality is "ready", then run generate.
    """
    spec_dir: Path = ctx.obj["spec_dir"]
    quiet: bool = ctx.obj["quiet"]
    target = _resolve_spec(spec_dir, spec)
    session = SpecSession.resume(target)

    if force:
        for name in ("requirements.json", "test_spec.json", "tasks.json"):
            artifact_path = target / name
            if artifact_path.exists():
                artifact_path.unlink()
        session._state = SessionState.INIT
        session._assessment_history = []
        session._qa_exchanges = []
        session._generated_artifacts = []
        session._persist()

    if answers is None:
        if not session._assessment_history:
            with StatusSpinner("Assessing PRD...", quiet=quiet):
                assessment = asyncio.run(session.assess())
            result = _serialize_assessment(assessment)
            result["type"] = "assessment"
            emit(result)
            return

        questions = session.pending_questions()
        output: dict[str, Any] = {
            "type": "questions",
            "questions": questions,
            "answers": {q["id"]: "" for q in questions},
        }
        emit(output)
        return

    if not session._assessment_history:
        with StatusSpinner("Assessing PRD...", quiet=quiet):
            asyncio.run(session.assess())

    if answers == "-":
        answers_text = sys.stdin.read()
    else:
        answers_path = Path(answers)
        if not answers_path.exists():
            raise AgentError(f"Answers file not found: {answers}", category="input")
        answers_text = answers_path.read_text()

    try:
        answers_data = json.loads(answers_text)
    except json.JSONDecodeError as exc:
        raise AgentError(f"Invalid JSON in answers: {exc}", category="input") from exc

    if not isinstance(answers_data, dict):
        raise AgentError(
            "Answers file must be a JSON object mapping question IDs to answers.",
            category="input",
        )

    if "answers" in answers_data and isinstance(answers_data["answers"], dict):
        answers_data = answers_data["answers"]

    with StatusSpinner("Refining PRD...", quiet=quiet):
        assessment = asyncio.run(session.refine(answers_data))

    result = _serialize_assessment(assessment)
    result["type"] = "assessment"
    emit(result)


# ---------------------------------------------------------------------------
# generate
# ---------------------------------------------------------------------------


@main.command("generate")
@click.argument("spec")
@click.option("--force", is_flag=True, help="Delete existing artifacts and regenerate from scratch")
@click.pass_context
def generate_cmd(ctx: click.Context, spec: str, force: bool) -> None:
    """Generate JSON artifacts from accepted PRD."""
    spec_dir: Path = ctx.obj["spec_dir"]
    quiet: bool = ctx.obj["quiet"]
    target = _resolve_spec(spec_dir, spec)
    session = SpecSession.resume(target)

    if force and session.state in (SessionState.GENERATED, SessionState.GENERATING):
        for name in ("requirements.json", "test_spec.json", "tasks.json"):
            artifact_path = target / name
            if artifact_path.exists():
                artifact_path.unlink()
        session._state = SessionState.PRD_ACCEPTED
        session._generated_artifacts = []
        session._persist()

    if session.state in (SessionState.ASSESSING, SessionState.REFINING):
        session.accept_prd()

    with StatusSpinner("Generating artifacts...", quiet=quiet) as spinner:
        result = asyncio.run(session.generate())
        artifacts = result.artifacts if hasattr(result, "artifacts") else result.get("artifacts", [])
        for artifact in artifacts:
            spinner.log(f"  {artifact}")

    emit_ok(artifacts=list(artifacts))


# ---------------------------------------------------------------------------
# render
# ---------------------------------------------------------------------------


_RENDER_ARTIFACT_FILES: dict[str, str] = {
    "requirements": "requirements.json",
    "test_spec": "test_spec.json",
    "tasks": "tasks.json",
}


def _render_available_artifacts(target: Path) -> tuple[dict[str, str], list[str]]:
    """Render whichever artifacts exist, returning (artifacts, warnings).

    Loads each available JSON artifact individually and renders it to
    markdown.  Returns a mapping of artifact name to rendered markdown
    and a list of warning strings for missing artifacts.
    """
    import afspec  # type: ignore[import-untyped]
    from afspec import Requirements, Tasks, TestSpec  # type: ignore[import-untyped]

    artifacts: dict[str, str] = {}
    warnings: list[str] = []

    _loaders: dict[str, tuple[type, Any]] = {
        "requirements": (Requirements, afspec.render_requirements),
        "test_spec": (TestSpec, afspec.render_test_spec),
        "tasks": (Tasks, afspec.render_tasks),
    }

    for name, filename in _RENDER_ARTIFACT_FILES.items():
        fpath = target / filename
        if not fpath.exists():
            warnings.append(f"{name} artifact not found")
            continue
        model_cls, render_fn = _loaders[name]
        model = model_cls.model_validate_json(fpath.read_text())
        artifacts[name] = render_fn(model)

    return artifacts, warnings


@main.command("render")
@click.argument("spec")
@click.option("--combined", is_flag=True, help="Render as single combined document")
@click.option("--json", "output_json", is_flag=True, default=False, help="Output JSON envelope")
@click.pass_context
def render_cmd(ctx: click.Context, spec: str, combined: bool, output_json: bool) -> None:
    """Render spec as markdown."""
    spec_dir: Path = ctx.obj["spec_dir"]

    # Auto-enable JSON output in agent mode (AF_AGENT=1) so that
    # agent consumers always receive structured envelopes without
    # having to pass --json explicitly.
    if ctx.obj.get("agent_mode"):
        output_json = True

    if not output_json:
        # Original behaviour: raw markdown output
        target = _resolve_spec(spec_dir, spec)
        session = SpecSession.resume(target)
        result = session.render(combined=combined)
        if isinstance(result, str):
            click.echo(result)
        else:
            for artifact_name, content in result.items():
                click.echo(f"--- {artifact_name} ---")
                click.echo(content)
                click.echo()
        return

    # --- JSON output mode ---
    try:
        target = _resolve_spec(spec_dir, spec)
        session = SpecSession.resume(target)
    except (click.ClickException, Exception) as exc:
        msg = exc.format_message() if isinstance(exc, click.ClickException) else str(exc)
        emit({"ok": False, "error": msg})
        ctx.exit(1)
        return

    if combined:
        # --json --combined: single merged content string + sections list
        # Try full render first; fall back to partial if artifacts missing
        try:
            merged = session.render(combined=True)
            assert isinstance(merged, str)
            sections = [n for n, f in _RENDER_ARTIFACT_FILES.items() if (target / f).exists()]
            emit_ok(format="markdown", content=merged, sections=sections)
        except Exception:
            # Partial render: merge what we can
            arts, warnings = _render_available_artifacts(target)
            prd_path = target / "prd.md"
            parts: list[str] = []
            if prd_path.exists():
                parts.append(prd_path.read_text().rstrip())
            for art_md in arts.values():
                parts.append("")
                parts.append("---")
                parts.append("")
                parts.append(art_md.rstrip())
            parts.append("")
            merged_partial = "\n".join(parts)
            sections = list(arts.keys())
            payload: dict[str, Any] = {
                "format": "markdown",
                "content": merged_partial,
                "sections": sections,
            }
            if warnings:
                payload["warnings"] = warnings
            emit_ok(**payload)
    else:
        # --json (per-artifact): artifacts map + optional warnings
        # Check which artifact files exist to decide strategy
        missing = [n for n, f in _RENDER_ARTIFACT_FILES.items() if not (target / f).exists()]
        if not missing:
            # All artifacts present – use the full session render
            result = session.render(combined=False)
            assert isinstance(result, dict)
            # Keep only the three standard artifact keys
            arts_map = {k: v for k, v in result.items() if k in _RENDER_ARTIFACT_FILES}
            emit_ok(artifacts=arts_map)
        else:
            # Some artifacts missing – render available ones, emit warnings
            arts_map, warnings = _render_available_artifacts(target)
            emit_ok(artifacts=arts_map, warnings=warnings)


# ---------------------------------------------------------------------------
# validate
# ---------------------------------------------------------------------------

_VALIDATE_ARTIFACT_FILES: dict[str, str] = {
    "requirements": "requirements.json",
    "test_spec": "test_spec.json",
    "tasks": "tasks.json",
}

_VALIDATE_SCHEMA_MAP: dict[str, str] = {
    "requirements.json": "requirements.v1.json",
    "test_spec.json": "test_spec.v1.json",
    "tasks.json": "tasks.v1.json",
}


def _get_value_at_path(data: Any, path: str) -> Any:
    """Navigate a dotted path in a nested dict/list structure.

    Returns the value at the path, or ``None`` when the path is
    invalid or points nowhere.
    """
    if not path:
        return None
    parts = path.split(".")
    current = data
    for part in parts:
        if isinstance(current, dict):
            if part in current:
                current = current[part]
            else:
                return None
        elif isinstance(current, list):
            try:
                idx = int(part)
                current = current[idx]
            except (ValueError, IndexError):
                return None
        else:
            return None
    return current


def _extract_requirement_id(message: str) -> str | None:
    """Extract a requirement/criterion ID from a validation error message.

    Looks for the first identifier in single quotes that matches a
    typical requirement ID pattern (e.g., ``TEST-REQ-1.1``).
    """
    match = re.search(r"'([A-Z][\w.-]+)'", message)
    return match.group(1) if match else None


def _build_schema_errors(
    target: Path,
    artifact_data: dict[str, dict[str, Any]],
) -> list[dict[str, Any]]:
    """Validate raw JSON artifact data against JSON schemas.

    Runs ``jsonschema`` validation directly on the raw JSON dicts
    to capture both missing-required-field errors (which produce
    top-level empty-path errors) and field-constraint errors (which
    carry the offending value).

    Returns a list of structured error dicts with
    ``category="schema"``.
    """
    import afspec as _afspec

    all_schemas = _afspec.schemas()
    errors: list[dict[str, Any]] = []

    for filename, schema_name in _VALIDATE_SCHEMA_MAP.items():
        if filename not in artifact_data:
            continue  # IO errors handled separately
        raw_data = artifact_data[filename]
        schema = json.loads(all_schemas[schema_name])
        validator = jsonschema.Draft202012Validator(schema)
        for err in validator.iter_errors(raw_data):
            path = ".".join(str(p) for p in err.absolute_path) if err.absolute_path else ""
            error_dict: dict[str, Any] = {
                "category": "schema",
                "artifact": filename,
                "message": err.message,
            }
            if path:
                error_dict["path"] = path
                error_dict["value"] = err.instance
            # Top-level errors (empty path) omit path and value per 05-REQ-3.E2
            errors.append(error_dict)

    return errors


def _build_model_schema_errors(spec_obj: Any) -> list[dict[str, Any]]:
    """Run model-level schema validation (EARS constraints, task group rules).

    These checks require a loaded ``Spec`` object and complement
    the raw JSON schema validation.
    """
    from afspec.validation import _validate_ears_constraints, _validate_task_group_structure

    errors: list[dict[str, Any]] = []
    for err in _validate_ears_constraints(spec_obj):
        error_dict: dict[str, Any] = {
            "category": "schema",
            "artifact": err.file,
            "message": err.message,
        }
        if err.path:
            error_dict["path"] = err.path
        errors.append(error_dict)

    for err in _validate_task_group_structure(spec_obj):
        error_dict = {
            "category": "schema",
            "artifact": err.file,
            "message": err.message,
        }
        if err.path:
            error_dict["path"] = err.path
        errors.append(error_dict)

    return errors


def _build_integrity_errors(spec_obj: Any) -> list[dict[str, Any]]:
    """Run cross-file integrity validation and produce structured errors."""
    import afspec as _afspec

    errors: list[dict[str, Any]] = []
    for err in _afspec.validate_cross_file(spec_obj):
        error_dict: dict[str, Any] = {
            "category": "integrity",
            "check": err.rule,
            "message": err.message,
        }
        req_id = _extract_requirement_id(err.message)
        if req_id:
            error_dict["requirement_id"] = req_id
        errors.append(error_dict)
    return errors


@main.command("validate")
@click.argument("spec")
@click.pass_context
def validate_cmd(ctx: click.Context, spec: str) -> None:
    """Run schema and cross-file checks."""
    import afspec as _afspec

    spec_dir: Path = ctx.obj["spec_dir"]
    target = _resolve_spec(spec_dir, spec)

    # Check for missing artifact files (IO errors) ----------------------------
    required_files = ["prd.md", "requirements.json", "test_spec.json", "tasks.json"]
    io_errors: list[dict[str, Any]] = []
    for filename in required_files:
        fpath = target / filename
        if not fpath.exists():
            io_errors.append(
                {
                    "category": "io",
                    "artifact": filename,
                    "message": f"Artifact file not found: {filename}",
                }
            )

    if io_errors:
        emit({"valid": False, "errors": io_errors})
        ctx.exit(1)
        return

    # Load raw JSON for schema validation -------------------------------------
    artifact_data: dict[str, dict[str, Any]] = {}
    for filename in ["requirements.json", "test_spec.json", "tasks.json"]:
        fpath = target / filename
        try:
            artifact_data[filename] = json.loads(fpath.read_text())
        except (OSError, json.JSONDecodeError) as exc:
            io_errors.append(
                {
                    "category": "io",
                    "artifact": filename,
                    "message": f"Cannot read artifact: {exc}",
                }
            )

    if io_errors:
        emit({"valid": False, "errors": io_errors})
        ctx.exit(1)
        return

    # Schema validation (raw JSON against JSON schemas) -----------------------
    schema_errors = _build_schema_errors(target, artifact_data)

    # Load spec object for model-level and cross-file validation --------------
    try:
        spec_obj = _afspec.load_spec(target)
    except Exception:
        session = SpecSession.resume(target)
        spec_obj = session._load_spec_from_artifacts()

    # Model-level schema checks (EARS constraints, task group structure)
    schema_errors.extend(_build_model_schema_errors(spec_obj))

    # Cross-file integrity validation -----------------------------------------
    integrity_errors = _build_integrity_errors(spec_obj)

    # Emit results ------------------------------------------------------------
    all_errors = schema_errors + integrity_errors
    if not all_errors:
        emit_ok(valid=True, errors=[])
        return

    emit({"valid": False, "errors": all_errors})
    ctx.exit(1)


# ---------------------------------------------------------------------------
# status
# ---------------------------------------------------------------------------


@main.command("status")
@click.argument("spec")
@click.pass_context
def status_cmd(ctx: click.Context, spec: str) -> None:
    """Query session state (read-only)."""
    spec_dir: Path = ctx.obj["spec_dir"]
    target = _resolve_spec(spec_dir, spec)
    session = SpecSession.resume(target)

    output: dict[str, Any] = {
        "state": session.state.value,
        "has_assessment": bool(session._assessment_history),
        "generated_artifacts": list(session._generated_artifacts),
    }

    if session._last_error is not None:
        output["last_error"] = session._last_error

    assessment = session.assessment
    if assessment is not None:
        output["quality"] = assessment.quality

    emit(output)


cli = main
