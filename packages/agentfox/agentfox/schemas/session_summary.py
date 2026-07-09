"""Pydantic model for the session-summary.json artifact.

Defines the canonical schema for the structured session summary that
coder agents write to ``.agent-fox/session-summary.json``.  This model
is the **single source of truth** shared between the prompt template
(``_templates/profiles/coder.md``) and every consumer that reads or
validates the artifact.

The field names and types match the inline JSON example in the coder
profile template.  Adding or renaming a field here is the only change
needed -- the prompt example and validation code both derive from this
model.
"""

from __future__ import annotations

from pydantic import BaseModel


class RejectedApproach(BaseModel):
    """A rejected approach with the reason it was discarded."""

    approach: str
    reason: str


class TestEntry(BaseModel):
    """A test file that was added or modified during the session."""

    path: str
    description: str


class SessionSummary(BaseModel):
    """Canonical schema for ``.agent-fox/session-summary.json``.

    All list fields default to empty so agents can omit them when
    nothing applies.  The ``summary`` field is required -- a session
    summary without a narrative is not meaningful.

    ``rejected_approaches`` accepts both structured
    ``RejectedApproach`` dicts and bare strings for backward
    compatibility — agents may produce either form.
    """

    summary: str
    rejected_approaches: list[RejectedApproach | str] = []
    gotchas: list[str] = []
    assumptions: list[str] = []
    tests_added_or_modified: list[TestEntry] = []
