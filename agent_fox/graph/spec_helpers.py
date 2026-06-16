"""Spec analysis helpers shared by the graph builder and injection modules.

Extracted to break the circular dependency between builder.py and injection.py.

Requirements: 46-REQ-3.1, 46-REQ-3.2, 46-REQ-4.4,
              134-REQ-3.1, 134-REQ-3.2, 134-REQ-3.3, 134-REQ-3.E1
"""

from __future__ import annotations

import logging
import re
from pathlib import Path

logger = logging.getLogger(__name__)

# ---------------------------------------------------------------------------
# Test-writing group detection (46-REQ-3.1, 46-REQ-3.2)
# ---------------------------------------------------------------------------

_TEST_GROUP_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"write failing spec tests", re.IGNORECASE),
    re.compile(r"write failing tests", re.IGNORECASE),
    re.compile(r"create unit test", re.IGNORECASE),
    re.compile(r"create test file", re.IGNORECASE),
    re.compile(r"spec tests", re.IGNORECASE),
]


def is_test_writing_group(title: str) -> bool:
    """Return True if the group title matches a test-writing pattern.

    Requirements: 46-REQ-3.1, 46-REQ-3.2, 46-REQ-3.E1, 46-REQ-3.E2
    """
    return any(p.search(title) for p in _TEST_GROUP_PATTERNS)


def count_ts_entries(spec_dir: Path) -> int:
    """Count test entries in a spec's test specification.

    For v1.2 specs (containing ``test_spec.json``), loads the spec via
    afspec and returns the total count of test cases, property tests,
    edge case tests, and smoke tests.

    Returns 0 if ``test_spec.json`` is missing or loading fails.

    Returns 0 if neither file exists or if loading fails.

    Requirements: 46-REQ-4.4, 134-REQ-3.1, 134-REQ-3.2, 134-REQ-3.E1
    """
    # v1.2 branch: count from afspec models (134-REQ-3.1)
    test_spec_json = spec_dir / "test_spec.json"
    if test_spec_json.is_file():
        try:
            import afspec

            spec = afspec.load_spec(spec_dir)
            ts = spec.test_spec
            return (
                len(ts.test_cases)
                + len(ts.property_tests)
                + len(ts.edge_case_tests)
                + len(ts.smoke_tests)
            )
        except Exception:
            # 134-REQ-3.E1: return 0 and log warning on load failure
            logger.warning("Failed to load test_spec.json in %s", spec_dir)
            return 0

    return 0


# ---------------------------------------------------------------------------
# Oracle gating: skip when spec targets only new code
# ---------------------------------------------------------------------------

# Matches file paths in backtick-bold markdown like **`agent_fox/foo.py`** (modified)
_DESIGN_FILE_REF = re.compile(
    r"\*\*`([a-zA-Z0-9_/.\-]+\.\w+)`\*\*\s*\(modified\)",
)


def spec_has_existing_code(spec_path: Path) -> bool:
    """Check whether a spec's design document references files that already exist.

    For v1.2 specs (containing ``requirements.json``), reads
    ``architecture.md``.

    Extracts paths marked ``(modified)`` and returns True if at least one
    of those paths exists on disk.  Returns True (safe default) when the
    target file is missing or unreadable so the oracle is not accidentally
    suppressed.

    Requirements: 134-REQ-3.3
    """
    target = spec_path / "architecture.md"

    try:
        content = target.read_text(encoding="utf-8")
    except OSError:
        # No architecture.md or unreadable — assume code exists (safe default)
        return True

    refs = _DESIGN_FILE_REF.findall(content)
    if not refs:
        # No (modified) references found — nothing for oracle to validate
        return False

    for ref in refs:
        if Path(ref).exists():
            return True

    return False
