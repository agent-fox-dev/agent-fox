"""Tests for spec landscape awareness -- load_spec_landscape and intent extraction.

Covers:
- TS-01-1 through TS-01-6: unit tests for load_spec_landscape discovery logic
- TS-01-7, TS-01-8: unit tests for intent extraction heuristic
- TS-01-E1 through TS-01-E4: edge case tests
- TS-01-P1, TS-01-P2, TS-01-P4, TS-01-P5: property tests
"""

from __future__ import annotations

from pathlib import Path
from unittest.mock import patch

import pytest

from afspec.discovery import load_spec_landscape

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------


def _make_prd(
    spec_id: str,
    spec_name: str,
    title: str,
    status: str = "draft",
    *,
    intent_section: str | None = "Test intent for this specification.",
    body_before_intent: str = "",
) -> str:
    """Build a minimal prd.md string with YAML frontmatter and optional ## Intent."""
    frontmatter = (
        f"---\n"
        f'spec_id: "{spec_id}"\n'
        f'spec_name: "{spec_name}"\n'
        f'title: "{title}"\n'
        f'status: "{status}"\n'
        f'created_at: "2026-01-01T00:00:00Z"\n'
        f'updated_at: "2026-01-01T00:00:00Z"\n'
        f'owner: "test"\n'
        f'source: "test"\n'
        f"supersedes: []\n"
        f"tags: []\n"
        f"intent_hash: null\n"
        f"schema_version: 1\n"
        f"---\n"
    )
    body = "# Test Spec\n\n"
    if body_before_intent:
        body += body_before_intent + "\n\n"
    if intent_section is not None:
        body += f"## Intent\n\n{intent_section}\n\n## Background\n\nSome background.\n"
    return frontmatter + body


def _setup_active_spec(
    root: Path,
    dir_name: str,
    spec_id: str,
    spec_name: str,
    title: str,
    status: str = "draft",
    *,
    intent_section: str | None = "Test intent for this specification.",
    body_before_intent: str = "",
) -> Path:
    """Create an active spec directory under *root* with prd.md."""
    folder = root / dir_name
    folder.mkdir(parents=True, exist_ok=True)
    prd_text = _make_prd(
        spec_id,
        spec_name,
        title,
        status,
        intent_section=intent_section,
        body_before_intent=body_before_intent,
    )
    (folder / "prd.md").write_text(prd_text)
    return folder


def _setup_archived_spec(
    root: Path,
    dir_name: str,
    spec_id: str,
    spec_name: str,
    title: str,
    status: str = "archived",
) -> Path:
    """Create an archived spec directory under *root*/archive/ with prd.md."""
    archive_dir = root / "archive"
    archive_dir.mkdir(parents=True, exist_ok=True)
    folder = archive_dir / dir_name
    folder.mkdir(parents=True, exist_ok=True)
    prd_text = _make_prd(spec_id, spec_name, title, status, intent_section="Archived intent.")
    (folder / "prd.md").write_text(prd_text)
    return folder


# ===========================================================================
# TS-01-1: load_spec_landscape calls discover_specs for active specs and
# scans archive/ for archived specs matching _SPEC_DIR_RE, returning one
# entry per discovered spec.  (01-REQ-1.1)
# ===========================================================================


class TestLoadSpecLandscapeDiscovery:
    """TS-01-1: load_spec_landscape returns one entry per active + archived spec."""

    def test_returns_active_and_archived_entries(self, tmp_path: Path) -> None:
        _setup_active_spec(tmp_path, "01_core_foundation", "01", "core_foundation", "Core Foundation")
        _setup_active_spec(tmp_path, "02_backend_protocol", "02", "backend_protocol", "Backend Protocol")
        _setup_archived_spec(tmp_path, "08_old_spec", "08", "old_spec", "Old Spec")

        result = load_spec_landscape(tmp_path, include_archive=True)

        assert len(result) == 3
        assert any(e["spec_id"] == "01" and e["archived"] is False for e in result)
        assert any(e["spec_id"] == "02" and e["archived"] is False for e in result)
        assert any(e["spec_id"] == "08" and e["archived"] is True for e in result)


# ===========================================================================
# TS-01-2: Each active spec entry contains all six required keys with
# non-null string values.  (01-REQ-1.2)
# ===========================================================================


class TestActiveSpecEntryKeys:
    """TS-01-2: Active entries have spec_id, spec_name, title, status, intent, archived."""

    def test_active_entry_has_all_six_keys(self, tmp_path: Path) -> None:
        _setup_active_spec(
            tmp_path,
            "01_core_foundation",
            "01",
            "core_foundation",
            "Core Foundation",
            intent_section="Establish the base layer for the project.",
        )

        result = load_spec_landscape(tmp_path, include_archive=False)

        assert len(result) == 1
        entry = result[0]
        for key in ("spec_id", "spec_name", "title", "status", "intent"):
            assert key in entry, f"Missing key: {key}"
            assert isinstance(entry[key], str), f"Key {key} is not a string"
        assert entry["archived"] is False


# ===========================================================================
# TS-01-3: Each archived spec entry contains exactly spec_id, spec_name,
# title, status, and archived=True, with no intent key.  (01-REQ-1.3)
# ===========================================================================


class TestArchivedSpecEntryKeys:
    """TS-01-3: Archived entries have five keys; no 'intent' key."""

    def test_archived_entry_has_no_intent_key(self, tmp_path: Path) -> None:
        _setup_archived_spec(tmp_path, "08_old_spec", "08", "old_spec", "Old Spec")

        result = load_spec_landscape(tmp_path, include_archive=True)

        archived_entries = [e for e in result if e["archived"] is True]
        assert len(archived_entries) == 1
        entry = archived_entries[0]
        for key in ("spec_id", "spec_name", "title", "status"):
            assert key in entry, f"Missing key: {key}"
        assert entry["archived"] is True
        assert "intent" not in entry


# ===========================================================================
# TS-01-4: load_spec_landscape excludes the spec whose spec_id matches
# current_spec_id.  (01-REQ-1.4)
# ===========================================================================


class TestSelfExclusion:
    """TS-01-4: current_spec_id is excluded from results."""

    def test_excludes_current_spec_id(self, tmp_path: Path) -> None:
        _setup_active_spec(tmp_path, "01_core_foundation", "01", "core_foundation", "Core Foundation")
        _setup_active_spec(tmp_path, "02_backend_protocol", "02", "backend_protocol", "Backend Protocol")

        result = load_spec_landscape(tmp_path, include_archive=False, current_spec_id="01")

        assert all(e["spec_id"] != "01" for e in result)
        assert any(e["spec_id"] == "02" for e in result)


# ===========================================================================
# TS-01-5: load_spec_landscape returns [] without re-raising when any
# exception is raised during discovery or file I/O.  (01-REQ-1.5)
# ===========================================================================


class TestGracefulDegradation:
    """TS-01-5: Returns [] on any internal exception."""

    def test_returns_empty_list_on_discover_specs_oserror(self, tmp_path: Path) -> None:
        with patch("afspec.discovery.discover_specs", side_effect=OSError("disk failure")):
            result = load_spec_landscape(tmp_path)
            assert result == []


# ===========================================================================
# TS-01-6: When called with include_archive=False, load_spec_landscape
# skips archive and returns only active entries.  (01-REQ-1.6)
# ===========================================================================


class TestIncludeArchiveFalse:
    """TS-01-6: include_archive=False returns only active entries."""

    def test_skips_archive_when_disabled(self, tmp_path: Path) -> None:
        _setup_active_spec(tmp_path, "01_core_foundation", "01", "core_foundation", "Core Foundation")
        _setup_archived_spec(tmp_path, "08_old_spec", "08", "old_spec", "Old Spec")

        result = load_spec_landscape(tmp_path, include_archive=False)

        assert all(e["archived"] is False for e in result)
        assert len(result) == 1
        assert result[0]["spec_id"] == "01"


# ===========================================================================
# TS-01-7: Intent extraction reads the ## Intent section from prd.md body
# and returns its text truncated to 300 characters.  (01-REQ-2.1)
# ===========================================================================


class TestIntentExtraction:
    """TS-01-7: Intent from ## Intent section, truncated to 300 chars."""

    def test_intent_from_section_truncated_to_300(self, tmp_path: Path) -> None:
        # Create an intent text longer than 300 characters
        long_intent = "This spec establishes the core foundation " + "x" * 310
        assert len(long_intent) > 300

        _setup_active_spec(
            tmp_path,
            "01_core",
            "01",
            "core",
            "Core",
            intent_section=long_intent,
        )

        result = load_spec_landscape(tmp_path, include_archive=False)
        entry = result[0]

        assert len(entry["intent"]) <= 300
        assert "core foundation" in entry["intent"].lower()
        # Should NOT contain content from ## Background section
        assert "Background" not in entry["intent"]


# ===========================================================================
# TS-01-8: When no ## Intent section exists, the intent falls back to the
# first non-empty paragraph of the body, truncated to 300 chars. (01-REQ-2.2)
# ===========================================================================


class TestIntentFallback:
    """TS-01-8: Without ## Intent, falls back to first non-empty paragraph."""

    def test_fallback_to_first_paragraph(self, tmp_path: Path) -> None:
        _setup_active_spec(
            tmp_path,
            "01_core",
            "01",
            "core",
            "Core",
            intent_section=None,  # No ## Intent section
            body_before_intent="This is the fallback paragraph describing the spec purpose.",
        )

        result = load_spec_landscape(tmp_path, include_archive=False)
        entry = result[0]

        assert "fallback paragraph" in entry["intent"]
        assert len(entry["intent"]) <= 300


# ===========================================================================
# Edge Case Tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-01-E1: load_spec_landscape treats a missing archive/ directory as an
# empty archive.  (01-REQ-1.E1)
# ---------------------------------------------------------------------------


class TestMissingArchiveDir:
    """TS-01-E1: Missing archive/ treated as empty; no exception."""

    def test_missing_archive_returns_only_active(self, tmp_path: Path) -> None:
        _setup_active_spec(tmp_path, "01_core_foundation", "01", "core_foundation", "Core Foundation")
        # Do NOT create spec_root/archive/ directory

        result = load_spec_landscape(tmp_path, include_archive=True)

        assert isinstance(result, list)
        assert len(result) == 1
        assert result[0]["archived"] is False


# ---------------------------------------------------------------------------
# TS-01-E2: load_spec_landscape returns [] when no specs exist.
# (01-REQ-1.E2)
# ---------------------------------------------------------------------------


class TestEmptySpecRoot:
    """TS-01-E2: Empty spec_root returns []."""

    def test_empty_root_returns_empty_list(self, tmp_path: Path) -> None:
        # spec_root exists but has no spec directories; no archive/ either
        result = load_spec_landscape(tmp_path, include_archive=True)
        assert result == []


# ---------------------------------------------------------------------------
# TS-01-E3: load_spec_landscape returns [] and does not call sys.exit when
# an internal error raises an exception.  (01-REQ-1.E3)
# ---------------------------------------------------------------------------


class TestNoProcessExit:
    """TS-01-E3: Returns [] on RuntimeError; no process exit."""

    def test_runtime_error_returns_empty_list(self, tmp_path: Path) -> None:
        with patch(
            "afspec.discovery.discover_specs",
            side_effect=RuntimeError("unexpected"),
        ):
            try:
                result = load_spec_landscape(tmp_path)
                assert result == []
            except Exception as exc:
                pytest.fail(f"Exception should not propagate: {exc}")


# ---------------------------------------------------------------------------
# TS-01-E4: When prd.md is missing or unreadable for an active spec,
# intent is '' and the spec entry is still included.  (01-REQ-2.E1)
# ---------------------------------------------------------------------------


class TestMissingPrdIntent:
    """TS-01-E4: Missing/empty prd.md body => intent='', entry still present."""

    def test_missing_prd_body_has_empty_intent(self, tmp_path: Path) -> None:
        # Create a valid spec that discover_specs will find, but with a
        # prd.md that has no body content (no Intent section, no paragraphs)
        # so intent extraction returns ''.
        folder = _setup_active_spec(
            tmp_path,
            "01_core_foundation",
            "01",
            "core_foundation",
            "Core Foundation",
        )
        # Overwrite prd.md with frontmatter-only (no body)
        minimal_prd = (
            "---\n"
            'spec_id: "01"\n'
            'spec_name: "core_foundation"\n'
            'title: "Core Foundation"\n'
            'status: "draft"\n'
            'created_at: "2026-01-01T00:00:00Z"\n'
            'updated_at: "2026-01-01T00:00:00Z"\n'
            'owner: "test"\n'
            'source: "test"\n'
            "supersedes: []\n"
            "tags: []\n"
            "intent_hash: null\n"
            "schema_version: 1\n"
            "---\n"
        )
        (folder / "prd.md").write_text(minimal_prd)

        result = load_spec_landscape(tmp_path, include_archive=False)

        assert len(result) == 1
        assert result[0]["intent"] == ""
        assert result[0]["spec_id"] == "01"
        assert "title" in result[0]


# ===========================================================================
# Property Tests
# ===========================================================================


# ---------------------------------------------------------------------------
# TS-01-P1: Self-exclusion invariant -- for any current_spec_id, no returned
# entry has spec_id == current_spec_id.  (01-PROP-1)
# ---------------------------------------------------------------------------


class TestSelfExclusionProperty:
    """TS-01-P1: Self-exclusion invariant holds for various spec sets."""

    @pytest.mark.parametrize(
        "current_id",
        ["01", "02", "03", "05", "10"],
    )
    def test_self_exclusion_for_various_ids(self, tmp_path: Path, current_id: str) -> None:
        # Create a set of specs, one of which matches current_id
        for i in range(1, 6):
            sid = f"{i:02d}"
            _setup_active_spec(
                tmp_path,
                f"{sid}_spec_{sid}",
                sid,
                f"spec_{sid}",
                f"Title {sid}",
            )

        result = load_spec_landscape(tmp_path, current_spec_id=current_id)

        for entry in result:
            assert entry["spec_id"] != current_id


# ---------------------------------------------------------------------------
# TS-01-P2: Graceful degradation -- for OSError, ValueError, RuntimeError,
# PermissionError the function returns [].  (01-PROP-2)
# ---------------------------------------------------------------------------


class TestGracefulDegradationProperty:
    """TS-01-P2: Returns [] for any exception type during discovery."""

    @pytest.mark.parametrize(
        "exc_type",
        [OSError, ValueError, RuntimeError, PermissionError],
    )
    def test_returns_empty_on_various_exceptions(self, tmp_path: Path, exc_type: type) -> None:
        with patch("afspec.discovery.discover_specs", side_effect=exc_type("test error")):
            result = load_spec_landscape(tmp_path)
            assert result == []


# ---------------------------------------------------------------------------
# TS-01-P4: Intent truncation invariant -- for active entries, intent
# length is at most 300 characters.  (01-PROP-4)
# ---------------------------------------------------------------------------


class TestIntentTruncationProperty:
    """TS-01-P4: All active entries have len(intent) <= 300."""

    @pytest.mark.parametrize(
        "intent_len",
        [0, 50, 299, 300, 301, 500, 1000],
    )
    def test_intent_length_capped_at_300(self, tmp_path: Path, intent_len: int) -> None:
        intent_text = "A" * intent_len if intent_len > 0 else ""
        _setup_active_spec(
            tmp_path,
            "01_core",
            "01",
            "core",
            "Core",
            intent_section=intent_text if intent_len > 0 else None,
            body_before_intent=intent_text if intent_len == 0 else "",
        )

        result = load_spec_landscape(tmp_path, include_archive=False)

        for entry in result:
            if entry["archived"] is False:
                assert len(entry["intent"]) <= 300


# ---------------------------------------------------------------------------
# TS-01-P5: Archived spec entries omit 'intent' key.  (01-PROP-5)
# ---------------------------------------------------------------------------


class TestArchivedOmitsIntentProperty:
    """TS-01-P5: All archived entries have no 'intent' key."""

    @pytest.mark.parametrize("num_archived", [1, 3, 5])
    def test_archived_entries_never_have_intent(self, tmp_path: Path, num_archived: int) -> None:
        for i in range(num_archived):
            sid = f"{i + 10:02d}"
            _setup_archived_spec(
                tmp_path,
                f"{sid}_archived_{sid}",
                sid,
                f"archived_{sid}",
                f"Archived {sid}",
            )

        result = load_spec_landscape(tmp_path, include_archive=True)

        for entry in result:
            if entry["archived"] is True:
                assert "intent" not in entry
