"""NodeSessionRunner model wiring tests.

Verifies that session_lifecycle.py delegates model resolution to
resolve_model_tier() / resolve_model() and contains no inline variant logic.

Test Spec: TS-14-44 (updated for #764: variant dimension removed)
Requirements: 14-REQ-12.2
"""

from __future__ import annotations

from pathlib import Path

import pytest

# NodeSessionRunner import chain pulls in ui.progress → rich.
# Runtime tests that instantiate NodeSessionRunner are skipped when rich is
# unavailable; source-inspection tests that only read the .py file work fine.
try:
    import rich  # noqa: F401

    _has_rich = True
except ModuleNotFoundError:
    _has_rich = False

_skip_no_rich = pytest.mark.skipif(not _has_rich, reason="rich not installed; NodeSessionRunner import chain fails")


# ---------------------------------------------------------------------------
# TS-14-44 (updated): session_lifecycle.py uses resolve_model() and
#                     resolve_model_tier() and contains no variant logic
# Requirement: 14-REQ-12.2
# ---------------------------------------------------------------------------


class TestNodeSessionRunnerSourceInspection:
    """Verify session_lifecycle.py delegates model resolution correctly and has no variant logic."""

    def test_source_contains_resolve_model(self) -> None:
        """TS-14-44: The source code calls resolve_model for model ID resolution."""
        source_path = Path(__file__).resolve().parents[2] / "agentfox" / "engine" / "session_lifecycle.py"
        source = source_path.read_text(encoding="utf-8")
        assert "resolve_model" in source, "session_lifecycle.py must contain a call to resolve_model"

    def test_source_contains_resolve_model_tier(self) -> None:
        """TS-14-44 corollary: The source code calls resolve_model_tier for tier resolution."""
        source_path = Path(__file__).resolve().parents[2] / "agentfox" / "engine" / "session_lifecycle.py"
        source = source_path.read_text(encoding="utf-8")
        assert "resolve_model_tier" in source, "session_lifecycle.py must contain a call to resolve_model_tier"

    def test_no_inline_model_registry_scanning(self) -> None:
        """TS-14-44 corollary: NodeSessionRunner does not embed inline variant-resolution logic.

        After #764 the variant dimension was removed entirely; session_lifecycle.py
        must delegate model selection only through resolve_model_tier() and resolve_model().
        """
        source_path = Path(__file__).resolve().parents[2] / "agentfox" / "engine" / "session_lifecycle.py"
        source = source_path.read_text(encoding="utf-8")
        # The old variant-order constant must not appear anywhere in the lifecycle module.
        assert "VARIANT" not in source, "session_lifecycle.py must not reference variant ordering directly"
