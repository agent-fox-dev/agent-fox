"""VARIANT_ORDER constant tests.

Test Spec: TS-14-8, TS-14-9, TS-14-10
Requirements: 14-REQ-3.1, 14-REQ-3.2, 14-REQ-3.3
"""

from __future__ import annotations

import subprocess
import sys

# ---------------------------------------------------------------------------
# TS-14-8: VARIANT_ORDER maps canonical labels to correct ordinal values
# Requirement: 14-REQ-3.1
# ---------------------------------------------------------------------------


class TestVariantOrderValues:
    """Verify that VARIANT_ORDER maps fast to 0, standard to 1, and extended to 2."""

    def test_fast_maps_to_zero(self) -> None:
        """TS-14-8: VARIANT_ORDER['fast'] == 0."""
        from agentfox.core.models import VARIANT_ORDER

        assert VARIANT_ORDER["fast"] == 0

    def test_standard_maps_to_one(self) -> None:
        """TS-14-8: VARIANT_ORDER['standard'] == 1."""
        from agentfox.core.models import VARIANT_ORDER

        assert VARIANT_ORDER["standard"] == 1

    def test_extended_maps_to_two(self) -> None:
        """TS-14-8: VARIANT_ORDER['extended'] == 2."""
        from agentfox.core.models import VARIANT_ORDER

        assert VARIANT_ORDER["extended"] == 2

    def test_complete_mapping(self) -> None:
        """TS-14-8: VARIANT_ORDER equals the expected dict."""
        from agentfox.core.models import VARIANT_ORDER

        assert VARIANT_ORDER == {"fast": 0, "standard": 1, "extended": 2}


# ---------------------------------------------------------------------------
# TS-14-9: VARIANT_ORDER is importable without circular import errors
# Requirement: 14-REQ-3.2
# ---------------------------------------------------------------------------


class TestVariantOrderImportable:
    """Verify that VARIANT_ORDER is importable from models.py without circular imports."""

    def test_import_in_subprocess(self) -> None:
        """TS-14-9: Import VARIANT_ORDER in a fresh subprocess without ImportError."""
        result = subprocess.run(
            [
                sys.executable,
                "-c",
                "from agentfox.core.models import VARIANT_ORDER; assert VARIANT_ORDER is not None",
            ],
            capture_output=True,
            text=True,
            timeout=10,
        )
        assert result.returncode == 0, f"Import failed: {result.stderr}"


# ---------------------------------------------------------------------------
# TS-14-10: None is not a key in VARIANT_ORDER
# Requirement: 14-REQ-3.3
# ---------------------------------------------------------------------------


class TestVariantOrderNoNoneKey:
    """Verify that None is absent from VARIANT_ORDER keys."""

    def test_none_not_in_variant_order(self) -> None:
        """TS-14-10: None is not a key in VARIANT_ORDER."""
        from agentfox.core.models import VARIANT_ORDER

        assert None not in VARIANT_ORDER
