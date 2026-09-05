"""Tests for auditor-related configuration models.

Test Spec: TS-46-3, TS-46-4
Requirements: 46-REQ-2.1, 46-REQ-2.2, 46-REQ-2.E1
"""

from __future__ import annotations

# ---------------------------------------------------------------------------
# TS-46-3: Config Auditor Field Default
# Requirements: 46-REQ-2.1, 46-REQ-2.E1
# ---------------------------------------------------------------------------


class TestAuditorDefaultTrue:
    """Verify ArchetypesConfig defaults reviewer to True."""

    def test_auditor_default_true(self) -> None:
        from agentfox.core.config import ArchetypesConfig

        config = ArchetypesConfig()
        assert config.reviewer is True
