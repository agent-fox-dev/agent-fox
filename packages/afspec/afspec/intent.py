"""Intent hash computation and verification.

Extracts the ``## Intent`` section from a PRD body, normalizes it
(LF line endings, collapsed blank lines, lower-case, trimmed), and
computes a SHA-256 hex digest.
"""

from __future__ import annotations

import hashlib
import re

from afspec.exceptions import IntentError

# Matches "## Intent" heading and captures everything up to the next
# "##" heading or end-of-string.
_INTENT_RE = re.compile(
    r"^## Intent[ \t]*\r?\n(.*?)(?=^## |\Z)",
    re.DOTALL | re.MULTILINE,
)


def compute_intent_hash(body: str) -> str:
    """Compute SHA-256 intent hash from a PRD body string.

    Extracts the text between ``## Intent`` and the next ``##`` heading
    (or end of document), normalizes it, and returns the lowercase hex
    digest.

    Normalization steps:
    1. Normalize line endings to LF
    2. Collapse multiple consecutive blank lines into one
    3. Lower-case the text
    4. Trim leading and trailing whitespace

    Raises ``IntentError`` if the ``## Intent`` section is missing.
    Returns the SHA-256 of empty bytes if the section is whitespace-only.
    """
    m = _INTENT_RE.search(body)
    if m is None:
        raise IntentError("PRD body does not contain a '## Intent' section")

    content = m.group(1)

    # Normalize line endings to LF
    content = content.replace("\r\n", "\n").replace("\r", "\n")

    # Lower-case
    content = content.lower()

    # Collapse multiple consecutive blank lines into one
    content = re.sub(r"\n{3,}", "\n\n", content)

    # Trim leading and trailing whitespace
    content = content.strip()

    # Compute SHA-256
    digest = hashlib.sha256(content.encode("utf-8")).hexdigest()
    return digest
