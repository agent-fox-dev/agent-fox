"""File I/O for loading and saving spec packages.

Handles PRD frontmatter parsing, JSON artifact deserialization,
deterministic JSON serialization, and atomic file writes.
"""

from __future__ import annotations

import json
import os
import re
import tempfile
from datetime import datetime, timezone
from enum import Enum
from pathlib import Path
from typing import Any, Union

import yaml

from afspec.exceptions import LifecycleError, LoadError, SaveError
from afspec.models import (
    PRDDocument,
    PRDFrontmatter,
    Requirements,
    Spec,
    Status,
    Tasks,
    TestSpec,
    _ImmutableSnapshot,
)

# ---------------------------------------------------------------------------
# Artifact file names
# ---------------------------------------------------------------------------

_ARTIFACT_FILES = ("prd.md", "requirements.json", "test_spec.json", "tasks.json")

# ---------------------------------------------------------------------------
# PRD Parsing
# ---------------------------------------------------------------------------

_FRONTMATTER_RE = re.compile(r"\A---\r?\n(.*?)^---\r?\n", re.DOTALL | re.MULTILINE)


def _parse_prd(text: str) -> PRDDocument:
    """Parse a prd.md file into frontmatter + body.

    The frontmatter is the YAML content between the first pair of ``---``
    delimiters.  The body is everything after the closing ``---``.

    Raises ``LoadError`` if the frontmatter is missing or invalid.
    """
    m = _FRONTMATTER_RE.match(text)
    if m is None:
        raise LoadError("prd.md: missing or invalid YAML frontmatter (no '---' delimiters found)")

    yaml_text = m.group(1)
    body = text[m.end() :]

    try:
        data = yaml.safe_load(yaml_text)
    except yaml.YAMLError as exc:
        raise LoadError(f"prd.md: invalid YAML frontmatter: {exc}") from exc

    if not isinstance(data, dict):
        raise LoadError("prd.md: frontmatter is not a mapping")

    try:
        frontmatter = PRDFrontmatter.model_validate(data)
    except Exception as exc:
        raise LoadError(f"prd.md: invalid frontmatter fields: {exc}") from exc

    return PRDDocument(frontmatter=frontmatter, body=body)


# ---------------------------------------------------------------------------
# JSON Artifact Loading
# ---------------------------------------------------------------------------


def _load_json_artifact(path: Path, model_cls: Any) -> Any:
    """Load a JSON artifact file and validate it into a Pydantic model.

    Raises ``LoadError`` on malformed JSON or validation failure.
    """
    text = path.read_text(encoding="utf-8")
    try:
        data = json.loads(text)
    except json.JSONDecodeError as exc:
        raise LoadError(f"{path.name}: malformed JSON: {exc}") from exc

    try:
        return model_cls.model_validate(data)
    except Exception as exc:
        raise LoadError(f"{path.name}: validation error: {exc}") from exc


# ---------------------------------------------------------------------------
# load_spec
# ---------------------------------------------------------------------------


def load_spec(dir: Union[str, Path]) -> Spec:
    """Load a spec from a directory containing all four artifact files.

    Reads ``prd.md``, ``requirements.json``, ``test_spec.json``, and
    ``tasks.json`` from *dir* and returns a populated ``Spec``.

    Raises ``LoadError`` if any files are missing, contain malformed
    JSON, or have invalid YAML frontmatter.
    """
    dir_path = Path(dir)

    # Check all four files exist
    missing = [name for name in _ARTIFACT_FILES if not (dir_path / name).is_file()]
    if missing:
        raise LoadError(f"Missing artifact file(s) in {dir_path}: {', '.join(missing)}")

    # Parse PRD
    prd_text = (dir_path / "prd.md").read_text(encoding="utf-8")
    prd = _parse_prd(prd_text)

    # Load JSON artifacts
    requirements = _load_json_artifact(dir_path / "requirements.json", Requirements)
    test_spec = _load_json_artifact(dir_path / "test_spec.json", TestSpec)
    tasks = _load_json_artifact(dir_path / "tasks.json", Tasks)

    # Load optional architecture.md (read as bytes to avoid universal-newline
    # translation that would convert \r to \n, breaking round-trip fidelity)
    arch_path = dir_path / "architecture.md"
    architecture = arch_path.read_bytes().decode("utf-8") if arch_path.is_file() else None

    # Assemble Spec
    spec = Spec(
        prd=prd,
        requirements=requirements,
        test_spec=test_spec,
        tasks=tasks,
        architecture=architecture,
    )

    # Capture immutable snapshot for mutation guard
    spec._loaded = _ImmutableSnapshot(
        spec_id=prd.frontmatter.spec_id,
        spec_name=prd.frontmatter.spec_name,
        created_at=prd.frontmatter.created_at,
    )

    return spec


# ---------------------------------------------------------------------------
# JSON Serialization
# ---------------------------------------------------------------------------


def _serialize_value(obj: Any) -> Any:
    """Recursively prepare a value for JSON serialization.

    - Pydantic models → dict via _serialize_model (handles Criterion omitempty)
    - Enums → their string value
    - dicts → sorted keys
    - lists → preserve order
    - Everything else → as-is
    """
    if hasattr(obj, "model_dump"):
        return _serialize_model(obj)
    if isinstance(obj, dict):
        return {k: _serialize_value(v) for k, v in sorted(obj.items())}
    if isinstance(obj, list):
        return [_serialize_value(item) for item in obj]
    if isinstance(obj, Enum):
        return obj.value
    return obj


def _serialize_model(model: Any) -> dict[str, Any]:
    """Serialize a Pydantic model to a dict suitable for JSON output.

    Walks model fields in declaration order, recursively serializing
    nested models via ``_serialize_value``.  This ensures nested
    Criterion objects have their None pattern-specific fields stripped
    (matching Go's omitempty behavior) instead of being flattened to
    plain dicts by a single top-level ``model_dump()`` call.
    """
    from afspec.models import Criterion

    _PATTERN_FIELDS = {"trigger", "condition", "error_condition", "state", "feature"}
    result: dict[str, Any] = {}

    for field_name, field_info in type(model).model_fields.items():
        alias = field_info.alias
        output_key = alias if alias else field_name
        value = getattr(model, field_name)

        # For Criterion, skip None pattern-specific fields (omitempty)
        if isinstance(model, Criterion) and field_name in _PATTERN_FIELDS and value is None:
            continue

        result[output_key] = _serialize_value(value)

    return result


def marshal_json(model: object) -> str:
    """Serialize a Pydantic model to deterministic JSON.

    Output uses 2-space indentation, a trailing newline, model fields in
    declaration order, and dict keys sorted alphabetically.
    """
    serialized = _serialize_model(model)
    # Use json.dumps with sort_keys=False because we already control key order
    # in model fields (declaration order) and sort dict values separately
    output = json.dumps(serialized, indent=2, ensure_ascii=False)
    return output + "\n"


# ---------------------------------------------------------------------------
# Atomic File Writes
# ---------------------------------------------------------------------------


def _atomic_write(path: Path, content: str) -> str:
    """Write *content* to *path* atomically via temp-then-rename.

    Returns the temp file path (for cleanup tracking).
    Raises ``OSError`` on failure.
    """
    dir_path = path.parent
    fd, tmp_path = tempfile.mkstemp(dir=str(dir_path), suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            f.write(content)
        os.rename(tmp_path, str(path))
    except Exception:
        # Clean up temp file on failure
        try:
            os.unlink(tmp_path)
        except OSError:
            pass
        raise
    return tmp_path


# ---------------------------------------------------------------------------
# PRD Serialization
# ---------------------------------------------------------------------------

# Fields in fixed order for YAML frontmatter serialization
_FRONTMATTER_FIELDS = [
    "spec_id",
    "spec_name",
    "title",
    "status",
    "created_at",
    "updated_at",
    "owner",
    "source",
    "supersedes",
    "tags",
    "intent_hash",
    "schema_version",
]


def _render_yaml_value(value: Any) -> str:
    """Render a single YAML value."""
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "true" if value else "false"
    if isinstance(value, int):
        return str(value)
    if isinstance(value, list):
        if not value:
            return "[]"
        items = ", ".join(f'"{v}"' for v in value)
        return f"[{items}]"
    if isinstance(value, str):
        return f'"{value}"'
    return str(value)


def _render_prd(prd: PRDDocument) -> str:
    """Render a PRDDocument to the prd.md file format."""
    fm = prd.frontmatter
    lines = ["---"]
    for field in _FRONTMATTER_FIELDS:
        value = getattr(fm, field)
        # Convert enum to string value
        if hasattr(value, "value"):
            value = value.value
        lines.append(f"{field}: {_render_yaml_value(value)}")
    lines.append("---")
    return "\n".join(lines) + "\n" + prd.body


# ---------------------------------------------------------------------------
# Save
# ---------------------------------------------------------------------------


def save(spec: Spec, dir: Union[str, Path]) -> None:
    """Save a spec to disk atomically.

    Writes all four artifact files (``prd.md``, ``requirements.json``,
    ``test_spec.json``, ``tasks.json``) to *dir* using atomic writes.

    Automatically sets ``updated_at`` and computes ``coverage`` before
    writing.

    Raises ``SaveError`` if *dir* does not exist or any write fails.
    Raises ``LifecycleError`` if the spec is in a terminal state
    (sealed, superseded, archived) or if immutable fields have changed
    in active state.
    """
    dir_path = Path(dir)

    # Check directory exists
    if not dir_path.is_dir():
        raise SaveError(f"Target directory does not exist: {dir_path}")

    # Mutation guard: reject saves for terminal states
    status = spec.prd.frontmatter.status
    if status in (Status.SEALED, Status.SUPERSEDED, Status.ARCHIVED):
        raise LifecycleError(f"Cannot save spec in {status.value} state: mutations are forbidden")

    # Mutation guard: check immutable fields and intent hash for active specs
    if status == Status.ACTIVE and spec._loaded is not None:
        _check_active_mutations(spec)

    # Compute coverage
    from afspec.coverage import compute_coverage

    coverage = compute_coverage(spec.test_spec, spec.requirements)
    spec = spec.model_copy(
        update={
            "test_spec": spec.test_spec.model_copy(update={"coverage": coverage}),
        }
    )

    # Update updated_at
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    spec = spec.model_copy(
        update={
            "prd": spec.prd.model_copy(
                update={"frontmatter": spec.prd.frontmatter.model_copy(update={"updated_at": now})}
            )
        }
    )

    # Serialize all artifacts
    prd_content = _render_prd(spec.prd)
    req_content = marshal_json(spec.requirements)
    ts_content = marshal_json(spec.test_spec)
    tasks_content = marshal_json(spec.tasks)

    # Write atomically
    try:
        _atomic_write(dir_path / "prd.md", prd_content)
        _atomic_write(dir_path / "requirements.json", req_content)
        _atomic_write(dir_path / "test_spec.json", ts_content)
        _atomic_write(dir_path / "tasks.json", tasks_content)
        if spec.architecture is not None:
            _atomic_write(dir_path / "architecture.md", spec.architecture)
    except Exception as exc:
        # Clean up any remaining temp files
        for tmp in dir_path.glob("*.tmp*"):
            try:
                tmp.unlink()
            except OSError:
                pass
        raise SaveError(f"Failed to write spec to {dir_path}: {exc}") from exc


def _check_active_mutations(spec: Spec) -> None:
    """Verify immutable fields haven't changed for active specs."""
    snapshot = spec._loaded
    if snapshot is None:
        return

    fm = spec.prd.frontmatter

    # Check immutable frontmatter fields
    if fm.spec_id != snapshot.spec_id:
        raise LifecycleError(f"Cannot modify spec_id in active state: was {snapshot.spec_id!r}, now {fm.spec_id!r}")
    if fm.spec_name != snapshot.spec_name:
        raise LifecycleError(
            f"Cannot modify spec_name in active state: was {snapshot.spec_name!r}, now {fm.spec_name!r}"
        )
    if fm.created_at != snapshot.created_at:
        raise LifecycleError(
            f"Cannot modify created_at in active state: was {snapshot.created_at!r}, now {fm.created_at!r}"
        )
    # Check intent hash
    if fm.intent_hash is not None:
        from afspec.intent import compute_intent_hash

        current_hash = compute_intent_hash(spec.prd.body)
        if current_hash != fm.intent_hash:
            raise LifecycleError(
                "Cannot save: intent section has been modified in active state "
                f"(expected hash {fm.intent_hash!r}, got {current_hash!r})"
            )


def _save_internal(spec: Spec, dir: Union[str, Path]) -> None:
    """Internal save that bypasses mutation guards.

    Used by lifecycle functions (transition, supersede) to persist
    state changes.
    """
    dir_path = Path(dir)

    if not dir_path.is_dir():
        raise SaveError(f"Target directory does not exist: {dir_path}")

    # Compute coverage
    from afspec.coverage import compute_coverage

    coverage = compute_coverage(spec.test_spec, spec.requirements)
    spec = spec.model_copy(
        update={
            "test_spec": spec.test_spec.model_copy(update={"coverage": coverage}),
        }
    )

    # Update updated_at
    now = datetime.now(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    spec = spec.model_copy(
        update={
            "prd": spec.prd.model_copy(
                update={"frontmatter": spec.prd.frontmatter.model_copy(update={"updated_at": now})}
            )
        }
    )

    # Serialize all artifacts
    prd_content = _render_prd(spec.prd)
    req_content = marshal_json(spec.requirements)
    ts_content = marshal_json(spec.test_spec)
    tasks_content = marshal_json(spec.tasks)

    # Write atomically
    try:
        _atomic_write(dir_path / "prd.md", prd_content)
        _atomic_write(dir_path / "requirements.json", req_content)
        _atomic_write(dir_path / "test_spec.json", ts_content)
        _atomic_write(dir_path / "tasks.json", tasks_content)
        if spec.architecture is not None:
            _atomic_write(dir_path / "architecture.md", spec.architecture)
    except Exception as exc:
        # Clean up any remaining temp files
        for tmp in dir_path.glob("*.tmp*"):
            try:
                tmp.unlink()
            except OSError:
                pass
        raise SaveError(f"Failed to write spec to {dir_path}: {exc}") from exc
