"""Shared fixtures for property tests."""

from __future__ import annotations

import pytest


@pytest.fixture(autouse=True)
def _patch_coverage_measurement(monkeypatch: pytest.MonkeyPatch) -> None:
    """Disable coverage measurement to avoid subprocess hangs in xdist."""
    monkeypatch.setattr(
        "agentfox.engine.result_handler.measure_coverage",
        lambda *a, **kw: None,
    )
