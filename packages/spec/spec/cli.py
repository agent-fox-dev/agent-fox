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
import yaml
from agentfox.core.config import ThemeConfig, load_config
from agentfox.ui.display import create_theme, render_banner
from agentspec.errors import AgentError, AgentSpecError, SessionError
from agentspec.session import SessionState, SpecSession

from spec.ui import StatusSpinner

_SPEC_DIR_RE = re.compile(r"^(\d{2})_(.+)$")
_SPEC_NAME_RE = re.compile(r"^[a-z][a-z0-9_]*$")
_DEFAULT_SPEC_DIR = ".agent-fox/specs"


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


def _assessment_to_json(assessment: Any) -> dict[str, Any]:
    """Serialise an Assessment to a JSON-friendly dict."""
    questions = []
    for q in getattr(assessment, "questions", []):
        questions.append(
            {
                "id": q.id,
                "text": q.text,
                "context": q.context,
                "options": q.options,
                "required": q.required,
            }
        )
    return {
        "quality": assessment.quality,
        "summary": assessment.summary,
        "gaps": list(assessment.gaps),
        "questions": questions,
    }


def _error_type(exc: Exception) -> str:
    """Map exception to a short error type string."""
    if hasattr(exc, "category"):
        return f"{exc.category}_error"
    return "internal_error"


def _json_error_exit(
    exc: Exception,
    *,
    code: int = 1,
    state: str | None = None,
) -> None:
    """Emit structured JSON error to stdout and exit."""
    error_body: dict[str, Any] = {
        "type": _error_type(exc),
        "message": str(exc),
    }

    if isinstance(exc, AgentError):
        error_body["retryable"] = exc.retryable
        if exc.http_status is not None:
            error_body["http_status"] = exc.http_status
    else:
        error_body["retryable"] = False

    if state is not None:
        error_body["state"] = state

    if exc.__cause__ is not None:
        error_body["cause"] = str(exc.__cause__)

    click.echo(json.dumps({"ok": False, "error": error_body}))
    sys.exit(code)


# ---------------------------------------------------------------------------
# CLI entry point
# ---------------------------------------------------------------------------


@click.group(invoke_without_command=True)
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

    config = load_config(Path(".agent-fox/config.toml"))
    theme_config = config.theme if config else ThemeConfig()
    theme = create_theme(theme_config)
    render_banner(theme, quiet=quiet)

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
    try:
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

        click.echo(json.dumps({"spec_dir": dir_name, "state": "init"}))
    except click.ClickException:
        raise
    except (AgentSpecError, SessionError) as exc:
        _json_error_exit(exc)
    except Exception as exc:
        _json_error_exit(exc, code=2)


# ---------------------------------------------------------------------------
# refine
# ---------------------------------------------------------------------------


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
    session: SpecSession | None = None
    try:
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
                result = _assessment_to_json(assessment)
                result["type"] = "assessment"
                click.echo(json.dumps(result, indent=2))
                return

            questions = session.pending_questions()
            output: dict[str, Any] = {
                "type": "questions",
                "questions": questions,
                "answers": {q["id"]: "" for q in questions},
            }
            click.echo(json.dumps(output, indent=2))
            return

        if not session._assessment_history:
            with StatusSpinner("Assessing PRD...", quiet=quiet):
                asyncio.run(session.assess())

        if answers == "-":
            answers_text = sys.stdin.read()
        else:
            answers_path = Path(answers)
            if not answers_path.exists():
                _json_error_exit(
                    AgentError(f"Answers file not found: {answers}", category="input"),
                    state=session.state.value,
                )
            answers_text = answers_path.read_text()

        try:
            answers_data = json.loads(answers_text)
        except json.JSONDecodeError as exc:
            _json_error_exit(
                AgentError(f"Invalid JSON in answers: {exc}", category="input"),
                state=session.state.value,
            )

        if not isinstance(answers_data, dict):
            _json_error_exit(
                AgentError(
                    "Answers file must be a JSON object mapping question IDs to answers.",
                    category="input",
                ),
                state=session.state.value,
            )

        if "answers" in answers_data and isinstance(answers_data["answers"], dict):
            answers_data = answers_data["answers"]

        with StatusSpinner("Refining PRD...", quiet=quiet):
            assessment = asyncio.run(session.refine(answers_data))

        result = _assessment_to_json(assessment)
        result["type"] = "assessment"
        click.echo(json.dumps(result, indent=2))
    except click.ClickException:
        raise
    except (AgentSpecError, SessionError) as exc:
        _json_error_exit(exc, state=session.state.value if session else None)
    except Exception as exc:
        _json_error_exit(exc, code=2, state=session.state.value if session else None)


# ---------------------------------------------------------------------------
# generate
# ---------------------------------------------------------------------------


@main.command("generate")
@click.argument("spec")
@click.option("--force", is_flag=True, help="Delete existing artifacts and regenerate from scratch")
@click.pass_context
def generate_cmd(ctx: click.Context, spec: str, force: bool) -> None:
    """Generate JSON artifacts from accepted PRD."""
    session: SpecSession | None = None
    try:
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

        click.echo(json.dumps({"artifacts": list(artifacts)}))
    except click.ClickException:
        raise
    except (AgentSpecError, SessionError) as exc:
        _json_error_exit(exc, state=session.state.value if session else None)
    except Exception as exc:
        _json_error_exit(exc, code=2, state=session.state.value if session else None)


# ---------------------------------------------------------------------------
# render
# ---------------------------------------------------------------------------


@main.command("render")
@click.argument("spec")
@click.option("--combined", is_flag=True, help="Render as single combined document")
@click.pass_context
def render_cmd(ctx: click.Context, spec: str, combined: bool) -> None:
    """Render spec as markdown."""
    try:
        spec_dir: Path = ctx.obj["spec_dir"]
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
    except click.ClickException:
        raise
    except (AgentSpecError, SessionError) as exc:
        _json_error_exit(exc)
    except Exception as exc:
        _json_error_exit(exc, code=2)


# ---------------------------------------------------------------------------
# validate
# ---------------------------------------------------------------------------


@main.command("validate")
@click.argument("spec")
@click.pass_context
def validate_cmd(ctx: click.Context, spec: str) -> None:
    """Run schema and cross-file checks."""
    try:
        spec_dir: Path = ctx.obj["spec_dir"]
        target = _resolve_spec(spec_dir, spec)
        session = SpecSession.resume(target)
        validation = session.validate()

        if validation.valid:
            click.echo(json.dumps({"valid": True}))
            return

        errors: list[dict[str, str]] = []
        for err in validation.schema_errors:
            errors.append({"message": str(err)} if not isinstance(err, dict) else err)
        for err in validation.integrity_errors:
            errors.append({"message": str(err)} if not isinstance(err, dict) else err)

        click.echo(json.dumps({"valid": False, "errors": errors}, indent=2))
        sys.exit(1)
    except click.ClickException:
        raise
    except (AgentSpecError, SessionError) as exc:
        _json_error_exit(exc)
    except Exception as exc:
        _json_error_exit(exc, code=2)


# ---------------------------------------------------------------------------
# status
# ---------------------------------------------------------------------------


@main.command("status")
@click.argument("spec")
@click.pass_context
def status_cmd(ctx: click.Context, spec: str) -> None:
    """Query session state (read-only)."""
    try:
        spec_dir: Path = ctx.obj["spec_dir"]
        target = _resolve_spec(spec_dir, spec)
        session = SpecSession.resume(target)

        session_data = json.loads((target / "_session.json").read_text())

        output: dict[str, Any] = {
            "state": session.state.value,
            "has_assessment": bool(session._assessment_history),
            "generated_artifacts": list(session._generated_artifacts),
        }

        last_error = session_data.get("last_error")
        if last_error is not None:
            output["last_error"] = last_error

        assessment = session.assessment
        if assessment is not None:
            output["quality"] = assessment.quality

        click.echo(json.dumps(output))
    except click.ClickException:
        raise
    except (AgentSpecError, SessionError) as exc:
        _json_error_exit(exc)
    except Exception as exc:
        _json_error_exit(exc, code=2)


cli = main
