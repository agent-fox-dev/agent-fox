"""Shared session-summary extraction from response text.

Provides ``extract_session_summary`` which parses structured summary
fields from a session response string.  Used by both the engine
(``session_lifecycle.py``) and Night Shift (``fix_pipeline.py``) to
extract institutional-memory fields from agent responses.

The function looks for a JSON object (optionally inside a markdown
fenced code block) containing the session-summary schema fields:
``summary``, ``rejected_approaches``, ``gotchas``, ``assumptions``.

Validation is performed via the ``SessionSummary`` Pydantic model
(``agentfox.schemas.session_summary``) which serves as the single
source of truth for the artifact shape.

Requirements: 05-REQ-3.1, 05-REQ-3.2, 05-REQ-3.3, 05-REQ-3.4,
              05-REQ-3.5, 05-REQ-4.1
"""

import json
import logging
import re

from pydantic import ValidationError

from agentfox.schemas.session_summary import RejectedApproach, SessionSummary

logger = logging.getLogger(__name__)

# Regex for markdown code fences (```json ... ``` or ``` ... ```)
_FENCE_RE = re.compile(r"```(?:json)?\s*\n(.*?)\n\s*```", re.DOTALL)

_EMPTY_RESULT: tuple[None, list, list, list] = (None, [], [], [])


def extract_session_summary(
    response: str,
) -> tuple[str | None, list, list, list]:
    """Extract structured session-summary fields from a response string.

    Parses a JSON object from the *response* text — either inside a
    markdown fenced code block (``\\`\\`\\`json ... \\`\\`\\```) or as
    a bare JSON object — and extracts the session-summary fields.

    Returns a 4-tuple ``(summary_text, rejected_approaches, gotchas,
    assumptions)`` where *summary_text* is ``str | None`` and the
    remaining three elements are lists.

    Returns ``(None, [], [], [])`` when:

    - *response* is empty or contains no structured fields.
    - The JSON is malformed or cannot be parsed.
    - The parsed JSON is not a dict or lacks a valid ``summary`` field.

    This function is **synchronous** — callers invoke it directly
    without ``await``.

    Requirements: 05-REQ-3.1, 05-REQ-3.2, 05-REQ-3.3, 05-REQ-3.4,
                  05-REQ-3.5, 05-REQ-4.1
    """
    if not response or not response.strip():
        return _EMPTY_RESULT

    try:
        data = _find_summary_object(response)
    except Exception:
        # 05-REQ-3.4: Never raise — degrade gracefully.
        return _EMPTY_RESULT

    if data is None:
        return _EMPTY_RESULT

    return _extract_fields(data)


def _find_summary_object(text: str) -> dict | None:
    """Locate a JSON object containing session-summary fields in *text*.

    Strategy 1: Look inside markdown fenced code blocks.
    Strategy 2: Try ``json.loads`` on the full stripped text.
    Strategy 3: Scan for the first ``{`` and attempt ``raw_decode``.

    Returns the parsed dict or ``None``.
    """
    # Strategy 1: markdown fences
    for match in _FENCE_RE.finditer(text):
        content = match.group(1).strip()
        obj = _try_parse_dict(content)
        if obj is not None:
            return obj

    # Strategy 2: direct parse of stripped text
    obj = _try_parse_dict(text.strip())
    if obj is not None:
        return obj

    # Strategy 3: raw_decode scan for first object
    idx = text.find("{")
    if idx != -1:
        try:
            parsed, _ = json.JSONDecoder().raw_decode(text, idx)
            if isinstance(parsed, dict):
                return parsed
        except (json.JSONDecodeError, ValueError):
            pass

    return None


def _try_parse_dict(text: str) -> dict | None:
    """Attempt to parse *text* as a JSON dict. Returns None on failure."""
    try:
        parsed = json.loads(text)
        if isinstance(parsed, dict):
            return parsed
    except (json.JSONDecodeError, TypeError, ValueError):
        pass
    return None


def _extract_fields(data: dict) -> tuple[str | None, list, list, list]:
    """Extract session-summary fields from a parsed JSON dict.

    Validates the data against the ``SessionSummary`` Pydantic model.
    On validation failure, emits a WARNING-level log message naming the
    offending field(s) and the received value, then returns the empty
    result.  This replaces the previous silent degradation so operators
    can detect agent drift.
    """
    try:
        model = SessionSummary.model_validate(data)
    except ValidationError as exc:
        # Emit diagnostic warning for each field that failed validation.
        for error in exc.errors():
            field_path = ".".join(str(loc) for loc in error["loc"])
            logger.warning(
                "Session summary validation failed — field %r: %s (received value: %r)",
                field_path,
                error["msg"],
                data.get(str(error["loc"][0])) if error["loc"] else data,
            )
        return _EMPTY_RESULT

    if not model.summary:
        # Empty string — no summary available.
        return _EMPTY_RESULT

    # Convert RejectedApproach models back to dicts for backward
    # compatibility with downstream consumers that expect list[dict].
    # Bare strings (accepted for backward compat) pass through as-is.
    rejected = [ra.model_dump() if isinstance(ra, RejectedApproach) else ra for ra in model.rejected_approaches]
    return (model.summary, rejected, model.gotchas, model.assumptions)
