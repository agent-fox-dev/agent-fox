"""Tests for afaudit.constants module — AUDIT_DIR definition.

TS-01-32: AUDIT_DIR defined in afaudit.constants and re-exported from afaudit
"""

from __future__ import annotations

from pathlib import Path


class TestAuditDirConstant:
    """TS-01-32: AUDIT_DIR is Path('.agent-fox/audit') from both import paths.

    Requirement: 01-REQ-9.1
    """

    def test_audit_dir_from_constants_module(self) -> None:
        """AUDIT_DIR from afaudit.constants must equal Path('.agent-fox/audit')."""
        from afaudit.constants import AUDIT_DIR

        assert AUDIT_DIR == Path(".agent-fox/audit")

    def test_audit_dir_from_top_level(self) -> None:
        """AUDIT_DIR from afaudit must equal Path('.agent-fox/audit')."""
        from afaudit import AUDIT_DIR

        assert AUDIT_DIR == Path(".agent-fox/audit")

    def test_audit_dir_is_same_object(self) -> None:
        """Both import paths must resolve to the same object."""
        from afaudit import AUDIT_DIR as top_level
        from afaudit.constants import AUDIT_DIR as from_constants

        assert top_level is from_constants

    def test_audit_dir_is_path_instance(self) -> None:
        """AUDIT_DIR must be a pathlib.Path instance."""
        from afaudit.constants import AUDIT_DIR

        assert isinstance(AUDIT_DIR, Path)
