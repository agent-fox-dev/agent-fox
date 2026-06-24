"""CLI entry point for the standalone night-shift daemon -- stub.

This module will be implemented in task group 4. It currently provides
a placeholder ``main`` so that Group 1 tests can be collected and will
fail at assertion time (not import time).
"""

from __future__ import annotations

import click


@click.command("night-shift")
def main() -> None:
    """Stub -- not yet implemented."""
    raise NotImplementedError("nightshift.app:main not yet implemented -- Group 4")
