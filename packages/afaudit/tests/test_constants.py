"""Tests for afaudit.constants module — AUDIT_DIR definition and uniqueness.

TS-01-32: AUDIT_DIR defined in afaudit.constants and re-exported from afaudit
TS-01-33: AUDIT_DIR not importable from agentfox.core.node_id after migration
"""

from __future__ import annotations

from pathlib import Path

import pytest


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


@pytest.mark.integration
class TestAuditDirRemovedFromAgentfox:
    """TS-01-33: AUDIT_DIR not importable from agentfox.core.node_id after migration.

    Requirement: 01-REQ-9.2
    """

    def test_audit_dir_not_in_agentfox_core_node_id(self) -> None:
        """Importing AUDIT_DIR from agentfox.core.node_id must fail.

        After migration, AUDIT_DIR is defined exclusively in
        afaudit.constants. The old definition in agentfox.core.node_id
        must be removed.
        """
        try:
            from agentfox.core.node_id import AUDIT_DIR  # noqa: F811

            # If we get here, the import succeeded — which means
            # the migration hasn't removed it yet.
            pytest.fail(
                f"AUDIT_DIR is still importable from agentfox.core.node_id "
                f"(value={AUDIT_DIR!r}). It must be removed per 01-REQ-9.2."
            )
        except (ImportError, AttributeError):
            pass  # Expected — AUDIT_DIR has been removed
